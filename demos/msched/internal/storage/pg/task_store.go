package pg

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/cockroachdb/cockroach-go/v2/crdb"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// TaskStore 实现 scheduler.TaskStore（PostgreSQL/CockroachDB，DESIGN §3/§7/§8）。
//
// CAS 乐观锁 + group_stats 同事务实时维护（§3）。CockroachDB 默认 SERIALIZABLE 隔离，
// crdb.ExecuteTx 自动重试整个闭包：并发 CAS 时重试后读到新状态、UPDATE 影响行=0，正确返回 false。
//
// 非事务查询用 GORM Raw（? 占位符）；事务用 crdb.ExecuteTx(*sql.Tx，$N 占位符)。
type TaskStore struct {
	db    *gorm.DB
	sqlDB *sql.DB // 供 crdb.ExecuteTx 事务重试
}

// NewTaskStore 构造 TaskStore。提取底层 *sql.DB 供 crdb.ExecuteTx 事务重试用。
func NewTaskStore(db *gorm.DB) (*TaskStore, error) {
	sqldb, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying *sql.DB: %w", err)
	}
	return &TaskStore{db: db, sqlDB: sqldb}, nil
}

// 编译期断言：实现 scheduler.TaskStore。
var _ scheduler.TaskStore = (*TaskStore)(nil)

// groupStatsRow group_stats 表行（DESIGN §3）。Selectors 用 pq.StringArray 映射 TEXT[]。
type groupStatsRow struct {
	ID           int64          `gorm:"column:id;primaryKey"`
	GroupUID     string         `gorm:"column:group_uid;uniqueIndex"`
	SelectorHash string         `gorm:"column:selector_hash;uniqueIndex"`
	Selectors    pq.StringArray `gorm:"column:selectors;type:text[]"`
	State        string         `gorm:"column:state;uniqueIndex"`
	Count        int64          `gorm:"column:count"`
	CreatedAt    time.Time      `gorm:"column:created_at"`
	UpdatedAt    time.Time      `gorm:"column:updated_at"`
}

func (groupStatsRow) TableName() string { return "group_stats" }

// --- scheduler.TaskStore ---

// Create 创建任务（DESIGN §3）。幂等：相同 group+args 产生相同 UnitID，tasks.unit_id
// UNIQUE 冲突 no-op 并回读已有行返回。补算 unit_id / selector_hash / priority（<=0 取
// created_at 微秒，DESIGN §3），初始 state=PENDING，同事务 group_stats PENDING count+1。
//
// crdb.ExecuteTx 事务内 INSERT ... ON CONFLICT (unit_id) DO NOTHING：仅首次插入（影响行=1）
// 才调 adjustGroupStats +1 PENDING，避免幂等创建重复计数。worker_selector 直接传 in.WorkerSelector
// （驱动调 Value() 序列化 JSONB，selector_codec.go），max_exec_duration 显式转 int64(ns)，
// state 显式转 string（自定义类型，驱动需基础类型）。args nil 兜底 []byte{}（NOT NULL）。
func (s *TaskStore) Create(ctx context.Context, in model.Task) (model.Task, error) {
	if in.UnitID == "" {
		in.UnitID = model.ComputeUnitID(in.GroupUID, in.Args)
	}
	if in.SelectorHash == "" {
		in.SelectorHash = model.ComputeSelectorHash(in.WorkerSelector)
	}
	if in.Priority <= 0 {
		in.Priority = time.Now().UnixMicro()
	}
	in.State = model.StatePending
	args := in.Args
	if args == nil {
		args = []byte{} // tasks.args BYTES NOT NULL
	}

	var created model.Task
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (unit_id, group_uid, priority, state, worker_selector, selector_hash,
			                    max_exec_duration, args, dispatch_count, fail_count, lease_owner)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, 0, '')
			 ON CONFLICT (unit_id) DO NOTHING`,
			in.UnitID, in.GroupUID, in.Priority, string(in.State), in.WorkerSelector, in.SelectorHash,
			int64(in.MaxExecDuration), args,
		)
		if err != nil {
			return fmt.Errorf("insert task %s: %w", in.UnitID, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		// 首次插入才 +1 PENDING（避免幂等创建重复计数，DESIGN §3）。
		if affected == 1 {
			selectors := model.ComputeSelectorPairs(in.WorkerSelector)
			if err := adjustGroupStats(ctx, tx, in.GroupUID, in.SelectorHash, model.StatePending, +1, selectors); err != nil {
				return fmt.Errorf("group_stats inc pending: %w", err)
			}
		}
		created = in
		return nil
	})
	if err != nil {
		return model.Task{}, fmt.Errorf("create task: %w", err)
	}
	return created, nil
}

// ListByGroup 按 group list 任务（DESIGN §3）。states 为空不过滤；按 priority, unit_id
// 升序（与撮合序一致）；limit/offset 分页。GORM Where("state IN ?") 自动展开状态切片，
// 不读 args/result 大字段（Select 列出所需列）。
func (s *TaskStore) ListByGroup(ctx context.Context, groupUID string, states []model.State, limit, offset int) ([]model.Task, error) {
	if limit <= 0 {
		limit = 50
	}
	q := s.db.WithContext(ctx).
		Model(&model.Task{}).
		Select("unit_id, group_uid, priority, state, selector_hash, max_exec_duration, next_retry_time, fail_count, dispatch_count").
		Where("group_uid = ?", groupUID).
		Order("priority, unit_id").
		Limit(limit).Offset(offset)
	if len(states) > 0 {
		q = q.Where("state IN ?", states)
	}
	var tasks []model.Task
	if err := q.Find(&tasks).Error; err != nil {
		return nil, fmt.Errorf("list tasks by group %s: %w", groupUID, err)
	}
	return tasks, nil
}

// ListActiveGroups 返回有 PENDING/到期 BACKOFF 任务的活跃 group 列表（DESIGN §9.5）。
func (s *TaskStore) ListActiveGroups(ctx context.Context) ([]string, error) {
	var groups []string
	err := s.db.WithContext(ctx).
		Model(&model.Task{}).
		Distinct("group_uid").
		Where("state = 'PENDING' OR (state = 'BACKOFF' AND next_retry_time <= now())").
		Pluck("group_uid", &groups).Error
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}
	sort.Strings(groups)
	return groups, nil
}

// ListPendByGroup 按 group 游标取一批候选（DESIGN §9.1/§7）。
// 不读 args/result 等大字段（撮合不需要）。GORM Select 列出所需列。
func (s *TaskStore) ListPendByGroup(ctx context.Context, group string, n int) ([]model.Task, error) {
	var tasks []model.Task
	err := s.db.WithContext(ctx).
		Model(&model.Task{}).
		Select("unit_id, group_uid, priority, state, worker_selector, selector_hash, max_exec_duration, next_retry_time, fail_count").
		Where("group_uid = ? AND (state = 'PENDING' OR (state = 'BACKOFF' AND next_retry_time <= now()))", group).
		Order("priority, unit_id").
		Limit(n).
		Find(&tasks).Error
	if err != nil {
		return nil, fmt.Errorf("list pend by group %s: %w", group, err)
	}
	return tasks, nil
}

// CASSchedule 撮合 CAS：PENDING/BACKOFF -> SCHEDULED（DESIGN §8.2）。
// 影响行=1 才算抢到。同事务维护 group_stats：old_state count-1、SCHEDULED count+1。
func (s *TaskStore) CASSchedule(ctx context.Context, unitID string, owner string) (bool, error) {
	var scheduled bool
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		// 1. CAS 前读取 old_state + group_stats 维护字段。
		var groupUID, selectorHash, state string
		var workerSelector []byte
		err := tx.QueryRowContext(ctx,
			`SELECT group_uid, selector_hash, state, worker_selector FROM tasks
			 WHERE unit_id = $1 AND state IN ('PENDING','BACKOFF')`, unitID,
		).Scan(&groupUID, &selectorHash, &state, &workerSelector)
		if err == sql.ErrNoRows {
			scheduled = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("select task for cas: %w", err)
		}
		oldState := model.State(state)

		// 2. CAS UPDATE（影响行=1 才算抢到）。
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks
			   SET state = 'SCHEDULED', lease_owner = $1, dispatch_time = now(),
			       dispatch_count = dispatch_count + 1, updated_at = now()
			 WHERE unit_id = $2 AND state IN ('PENDING','BACKOFF')`, owner, unitID,
		)
		if err != nil {
			return fmt.Errorf("cas update: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		if affected != 1 {
			scheduled = false
			return nil
		}

		// 3. group_stats 维护（§3）。worker_selector 复用 codec Scan 解码。
		sel := model.WorkerSelector{}
		if err := sel.Scan(workerSelector); err != nil {
			return fmt.Errorf("scan worker_selector for stats: %w", err)
		}
		selectors := model.ComputeSelectorPairs(sel)
		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, oldState, -1, nil); err != nil {
			return fmt.Errorf("group_stats dec %s: %w", oldState, err)
		}
		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, model.StateScheduled, +1, selectors); err != nil {
			return fmt.Errorf("group_stats inc scheduled: %w", err)
		}
		scheduled = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("cas schedule task %s: %w", unitID, err)
	}
	return scheduled, nil
}

// SetBackoff 无匹配 worker 退避：PENDING/BACKOFF -> BACKOFF（DESIGN §8.6）。
// fail_count++；状态已变（他节点 CAS 抢走）时 no-op。
// group_stats：仅 PENDING->BACKOFF 调整；BACKOFF->BACKOFF（重退避）old=new 不变。
func (s *TaskStore) SetBackoff(ctx context.Context, unitID string, nextRetry time.Time) error {
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		var groupUID, selectorHash, state string
		err := tx.QueryRowContext(ctx,
			`SELECT group_uid, selector_hash, state FROM tasks
			 WHERE unit_id = $1 AND state IN ('PENDING','BACKOFF')`, unitID,
		).Scan(&groupUID, &selectorHash, &state)
		if err == sql.ErrNoRows {
			return nil // 不存在或状态已变，no-op
		}
		if err != nil {
			return fmt.Errorf("select task for backoff: %w", err)
		}
		oldState := model.State(state)

		res, err := tx.ExecContext(ctx,
			`UPDATE tasks
			   SET state = 'BACKOFF', next_retry_time = $1, fail_count = fail_count + 1, updated_at = now()
			 WHERE unit_id = $2 AND state IN ('PENDING','BACKOFF')`, nextRetry, unitID,
		)
		if err != nil {
			return fmt.Errorf("backoff update: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		if affected != 1 {
			return nil // 状态已变，no-op
		}
		if oldState != model.StateBackoff {
			if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, oldState, -1, nil); err != nil {
				return fmt.Errorf("group_stats dec %s: %w", oldState, err)
			}
			if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, model.StateBackoff, +1, nil); err != nil {
				return fmt.Errorf("group_stats inc backoff: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("set backoff task %s: %w", unitID, err)
	}
	return nil
}

// BatchSetRunning 批量 SCHEDULED -> RUNNING（DESIGN §8.3），带 lease_owner 所有权校验。
// 只转属于 wuid 的 SCHEDULED 任务，防回收重派他处后被误标 RUNNING（防双发 §8.4/§8.7）。
// 设 pull_time + timeout。返回实际转为 RUNNING 的 unitID 集合。同事务维护 group_stats。
//
// 用 crdb.ExecuteTx（*sql.Tx）保证 CockroachDB SERIALIZABLE 自动重试。两步：
// 1) UPDATE（Exec） 2) SELECT 转换行（先收集 + 显式关闭 *sql.Rows，再做 group_stats 维护）。
// 必须先关闭 rows 再执行后续 UPDATE：crdb.ExecuteTx 重试时，未关闭的 *sql.Rows 会使
// 连接进入坏状态（bad connection），这是 CockroachDB 事务重试与 *sql.Rows 生命周期的已知约束。
func (s *TaskStore) BatchSetRunning(ctx context.Context, wuid string, unitIDs []string) ([]string, error) {
	if len(unitIDs) == 0 {
		return nil, nil
	}
	running := make([]string, 0, len(unitIDs))
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		// 1. 批量 UPDATE（所有权校验：lease_owner=wuid AND state=SCHEDULED）。
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks
			   SET state = 'RUNNING', pull_time = now(),
			       timeout = now() + (max_exec_duration::text || ' microseconds')::interval, updated_at = now()
			 WHERE unit_id = ANY($1::text[]) AND lease_owner = $2 AND state = 'SCHEDULED'`,
			pq.Array(unitIDs), wuid,
		); err != nil {
			return fmt.Errorf("batch set running update: %w", err)
		}

		// 2. 查刚转 RUNNING 的行（同事务内可见），收集后立即关闭 rows。
		type runningRow struct {
			uid, groupUID, selectorHash string
			workerSelector              []byte
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT unit_id, group_uid, selector_hash, worker_selector FROM tasks
			 WHERE unit_id = ANY($1::text[]) AND lease_owner = $2 AND state = 'RUNNING'`,
			pq.Array(unitIDs), wuid,
		)
		if err != nil {
			return fmt.Errorf("select running rows: %w", err)
		}
		collected := make([]runningRow, 0)
		for rows.Next() {
			var r runningRow
			if err := rows.Scan(&r.uid, &r.groupUID, &r.selectorHash, &r.workerSelector); err != nil {
				rows.Close()
				return fmt.Errorf("scan running row: %w", err)
			}
			collected = append(collected, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("rows iter: %w", err)
		}
		rows.Close() // 关键：先关闭 rows，再做后续 group_stats UPDATE（否则重试时坏连接）

		// 3. group_stats 维护（rows 已关闭，安全）。
		for _, r := range collected {
			running = append(running, r.uid)
			sel := model.WorkerSelector{}
			if err := sel.Scan(r.workerSelector); err != nil {
				return fmt.Errorf("scan worker_selector for stats: %w", err)
			}
			selectors := model.ComputeSelectorPairs(sel)
			if err := adjustGroupStats(ctx, tx, r.groupUID, r.selectorHash, model.StateScheduled, -1, nil); err != nil {
				return fmt.Errorf("group_stats dec scheduled: %w", err)
			}
			if err := adjustGroupStats(ctx, tx, r.groupUID, r.selectorHash, model.StateRunning, +1, selectors); err != nil {
				return fmt.Errorf("group_stats inc running: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("batch set running: %w", err)
	}
	return running, nil
}

// ListByUnitIDs 按 UnitID 批量读任务详情（worker Pull 返回 []Task，DESIGN §10）。读全列。
func (s *TaskStore) ListByUnitIDs(ctx context.Context, unitIDs []string) ([]model.Task, error) {
	if len(unitIDs) == 0 {
		return nil, nil
	}
	var tasks []model.Task
	err := s.db.WithContext(ctx).
		Model(&model.Task{}).
		Where("unit_id IN ?", unitIDs).
		Find(&tasks).Error
	if err != nil {
		return nil, fmt.Errorf("list by unit ids: %w", err)
	}
	return tasks, nil
}

// Complete 执行成功回传：CAS RUNNING -> COMPLETED，写 Result（DESIGN §8.7）。
// 所有权校验 lease_owner==wuid：UPDATE 带 state='RUNNING' AND lease_owner=wuid，
// 影响行=1 才命中（true）。状态已变/所有权不符返回 false（Report 忽略，§8.4/§8.7）。
// 同事务维护 group_stats：RUNNING count-1、COMPLETED count+1。result 为 nil 写 NULL。
func (s *TaskStore) Complete(ctx context.Context, unitID, wuid string, result []byte) (bool, error) {
	var completed bool
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		var groupUID, selectorHash, state, leaseOwner string
		err := tx.QueryRowContext(ctx,
			`SELECT group_uid, selector_hash, state, lease_owner FROM tasks
			 WHERE unit_id = $1 AND state = 'RUNNING'`, unitID,
		).Scan(&groupUID, &selectorHash, &state, &leaseOwner)
		if err == sql.ErrNoRows {
			completed = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("select task for complete: %w", err)
		}

		// CAS UPDATE（所有权校验 lease_owner=wuid），影响行=1 才命中。
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET state = 'COMPLETED', result = $1, lease_owner = '', updated_at = now()
			 WHERE unit_id = $2 AND lease_owner = $3 AND state = 'RUNNING'`,
			result, unitID, wuid,
		)
		if err != nil {
			return fmt.Errorf("complete update: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		if affected != 1 {
			completed = false // 状态已变/所有权不符，忽略
			return nil
		}

		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, model.StateRunning, -1, nil); err != nil {
			return fmt.Errorf("group_stats dec running: %w", err)
		}
		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, model.StateCompleted, +1, nil); err != nil {
			return fmt.Errorf("group_stats inc completed: %w", err)
		}
		completed = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("complete task %s: %w", unitID, err)
	}
	return completed, nil
}

// ReportFail 执行失败回传：CAS 所有权校验后按 dispatch_count 兜底（DESIGN §8.5/§8.6）。
// 仅 state='RUNNING' AND lease_owner=wuid 命中（影响行=1）。
//
//	dispatch_count >= maxAttempt -> DISCARDED（放弃）
//	否则 -> PENDING（reschedule，重新可撮合；不入 BACKOFF，§8.6）
//
// 返回 reported（命中）+ discarded（命中且转 DISCARDED）。dispatch_count 在 CAS PENDING->SCHEDULED
// 时已累加（§8.2），此处不重复 +1。回 PENDING 时清 lease_owner（回到无主）。同事务维护 group_stats。
func (s *TaskStore) ReportFail(ctx context.Context, unitID, wuid string, maxAttempt int) (reported bool, discarded bool, err error) {
	err = crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		var groupUID, selectorHash, state, leaseOwner string
		var dispatchCount int64
		err := tx.QueryRowContext(ctx,
			`SELECT group_uid, selector_hash, state, lease_owner, dispatch_count FROM tasks
			 WHERE unit_id = $1 AND state = 'RUNNING'`, unitID,
		).Scan(&groupUID, &selectorHash, &state, &leaseOwner, &dispatchCount)
		if err == sql.ErrNoRows {
			return nil // 不在 RUNNING，忽略
		}
		if err != nil {
			return fmt.Errorf("select task for report fail: %w", err)
		}

		var target model.State
		if int(dispatchCount) >= maxAttempt {
			target = model.StateDiscarded
		} else {
			target = model.StatePending
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET state = $1, lease_owner = '', updated_at = now()
			 WHERE unit_id = $2 AND lease_owner = $3 AND state = 'RUNNING'`,
			string(target), unitID, wuid,
		)
		if err != nil {
			return fmt.Errorf("report fail update: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		if affected != 1 {
			return nil // 状态已变/所有权不符，忽略
		}
		reported = true
		discarded = target == model.StateDiscarded

		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, model.StateRunning, -1, nil); err != nil {
			return fmt.Errorf("group_stats dec running: %w", err)
		}
		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, target, +1, nil); err != nil {
			return fmt.Errorf("group_stats inc %s: %w", target, err)
		}
		return nil
	})
	if err != nil {
		return false, false, fmt.Errorf("report fail task %s: %w", unitID, err)
	}
	return reported, discarded, nil
}

// ListGroupStats 返回某 group 下各 (selector_hash, state) 计数（DESIGN §3）。
// 仅返回 count>0 的行，按 (selector_hash, state) 升序稳定排列。
func (s *TaskStore) ListGroupStats(ctx context.Context, groupUID string) ([]model.GroupStats, error) {
	var rows []groupStatsRow
	err := s.db.WithContext(ctx).
		Where("group_uid = ? AND count > 0", groupUID).
		Order("selector_hash, state").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list group stats %s: %w", groupUID, err)
	}
	out := make([]model.GroupStats, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.GroupStats{
			GroupUID:     r.GroupUID,
			SelectorHash: r.SelectorHash,
			Selectors:    []string(r.Selectors),
			State:        model.State(r.State),
			Count:        r.Count,
		})
	}
	return out, nil
}

// Delete 物理删除任务（scheduler.TaskStore.Delete）。
//
// unitIDs 为空：删 groupUID 下全部任务 + group_stats 该 group 所有行（group 不再存在）。
// unitIDs 非空：删属于 groupUID 且 unit_id 在列表中的任务，group_stats 按 (selector_hash,
// state) 聚合减 count（行保留，count 可降至 0）。
//
// 事务内三步（删指定）：SELECT 聚合 -> DELETE tasks -> batch 减 group_stats。
// crdb.ExecuteTx 重试时闭包整体重跑（SELECT 重读、DELETE 幂等），安全。
// 用 pq.Array + ANY($N) 批量定位，避免 IN 列表手工展开。
func (s *TaskStore) Delete(ctx context.Context, groupUID string, unitIDs []string) (int64, error) {
	var deleted int64
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		gtx := gtxFromTx(s.db, ctx, tx)
		if len(unitIDs) == 0 {
			// 删整个 group：先删 tasks，再删 group_stats 该 group 全部行。
			res := gtx.Where("group_uid = ?", groupUID).Delete(&model.Task{})
			if res.Error != nil {
				return fmt.Errorf("delete tasks by group %s: %w", groupUID, res.Error)
			}
			deleted = res.RowsAffected
			if err := gtx.Where("group_uid = ?", groupUID).Delete(&groupStatsRow{}).Error; err != nil {
				return fmt.Errorf("delete group_stats by group %s: %w", groupUID, err)
			}
			return nil
		}

		// 删指定 unitIDs（仅属于 groupUID）。先关闭 rows 再做后续 UPDATE（crdb 重试约束，
		// 同 BatchSetRunning）。
		rows, err := tx.QueryContext(ctx,
			`SELECT selector_hash, state FROM tasks WHERE group_uid = $1 AND unit_id = ANY($2)`,
			groupUID, pq.Array(unitIDs),
		)
		if err != nil {
			return fmt.Errorf("select for delete: %w", err)
		}
		type hashState struct{ hash, state string }
		agg := map[hashState]int64{}
		for rows.Next() {
			var h, st string
			if err := rows.Scan(&h, &st); err != nil {
				rows.Close()
				return fmt.Errorf("scan for delete: %w", err)
			}
			agg[hashState{h, st}]++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("rows iter: %w", err)
		}
		rows.Close()

		res := gtx.Where("group_uid = ? AND unit_id IN ?", groupUID, unitIDs).Delete(&model.Task{})
		if res.Error != nil {
			return fmt.Errorf("delete tasks by unit_ids: %w", res.Error)
		}
		deleted = res.RowsAffected

		// group_stats 按 (hash, state) 聚合减 delta（batch 减，非逐个 ±1）。
		for k, delta := range agg {
			if _, err := tx.ExecContext(ctx,
				`UPDATE group_stats SET count = count - $4, updated_at = now()
				 WHERE group_uid = $1 AND selector_hash = $2 AND state = $3`,
				groupUID, k.hash, k.state, delta,
			); err != nil {
				return fmt.Errorf("group_stats dec %s/%s: %w", k.state, k.hash, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("delete tasks group %s: %w", groupUID, err)
	}
	return deleted, nil
}

// adjustGroupStats 增减 group_stats 行 count（DESIGN §3 实时维护）。
//
//	delta=+1: UPSERT（首次 INSERT count=1 + selectors，冲突 count+1）
//	delta=-1: UPDATE count-1（行应已存在；不存在则 0 行，数据由创建侧负责初始化）
//
// selectors 仅 +1 首次 INSERT 时写入（可读伴随，§3），传 nil 则空数组。
// $3::text[] 把 pq.Array 的字符串字面量转为 TEXT[]。
func adjustGroupStats(ctx context.Context, tx *sql.Tx, groupUID, selectorHash string, state model.State, delta int64, selectors []string) error {
	if delta > 0 {
		if selectors == nil {
			selectors = []string{}
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO group_stats (group_uid, selector_hash, selectors, state, count)
			 VALUES ($1, $2, $3::text[], $4, 1)
			 ON CONFLICT (group_uid, selector_hash, state)
			 DO UPDATE SET count = group_stats.count + 1, updated_at = now()`,
			groupUID, selectorHash, pq.Array(selectors), string(state),
		)
		if err != nil {
			return fmt.Errorf("group_stats upsert %s: %w", state, err)
		}
		return nil
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE group_stats SET count = count - 1, updated_at = now()
		 WHERE group_uid = $1 AND selector_hash = $2 AND state = $3`,
		groupUID, selectorHash, string(state),
	)
	if err != nil {
		return fmt.Errorf("group_stats dec %s: %w", state, err)
	}
	return nil
}

// RecoverByWorker 回收某 worker 名下全部 RUNNING + SCHEDULED -> PENDING（DESIGN §8.4）。
// worker 心跳 zset 过期（worker 离线）时由扫描器调用。SCHEDULED 残留的 Redis 派发桶
// 清理由调用方另做。crdb.ExecuteTx + SERIALIZABLE：并发他节点同回收时事务冲突重试，
// 重跑 QueryContext 看到 state 已变 -> 不重复回收（CAS 天然防双回收）。同事务维护 group_stats。
func (s *TaskStore) RecoverByWorker(ctx context.Context, wuid string) ([]string, error) {
	var recovered []string
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT unit_id, group_uid, selector_hash, state FROM tasks
			 WHERE lease_owner = $1 AND state IN ('RUNNING','SCHEDULED')`,
			wuid,
		)
		if err != nil {
			return fmt.Errorf("select recover by worker: %w", err)
		}
		type recRow struct{ uid, groupUID, selectorHash, state string }
		collected := make([]recRow, 0)
		for rows.Next() {
			var r recRow
			if err := rows.Scan(&r.uid, &r.groupUID, &r.selectorHash, &r.state); err != nil {
				rows.Close()
				return fmt.Errorf("scan recover by worker: %w", err)
			}
			collected = append(collected, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("rows iter recover by worker: %w", err)
		}
		rows.Close() // crdb 重试约束：先关 rows 再 UPDATE

		gtx := gtxFromTx(s.db, ctx, tx)
		for _, r := range collected {
			oldState := model.State(r.state)
			if err := gtx.Model(&model.Task{}).
				Where("unit_id = ? AND lease_owner = ? AND state = ?", r.uid, wuid, oldState).
				Updates(map[string]any{
					"state":       model.StatePending,
					"lease_owner": "",
					"updated_at":  gorm.Expr("now()"),
				}).Error; err != nil {
				return fmt.Errorf("recover by worker update %s: %w", r.uid, err)
			}
			if err := adjustGroupStats(ctx, tx, r.groupUID, r.selectorHash, oldState, -1, nil); err != nil {
				return fmt.Errorf("group_stats dec %s: %w", oldState, err)
			}
			if err := adjustGroupStats(ctx, tx, r.groupUID, r.selectorHash, model.StatePending, +1, nil); err != nil {
				return fmt.Errorf("group_stats inc pending: %w", err)
			}
			recovered = append(recovered, r.uid)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("recover by worker %s: %w", wuid, err)
	}
	return recovered, nil
}

// RecoverTask 单 task 执行超时回收：CAS RUNNING -> PENDING（DESIGN §8.4）。
// task 心跳 zset 过期（worker 崩溃不续期 / 硬截止 timeout 到）时由扫描器调用。
// 不校验 lease_owner（zset member 只存 unitID）。影响行=1 才算回收，group_stats 一致维护。
func (s *TaskStore) RecoverTask(ctx context.Context, unitID string) (bool, error) {
	var recovered bool
	err := crdb.ExecuteTx(ctx, s.sqlDB, nil, func(tx *sql.Tx) error {
		var groupUID, selectorHash string
		err := tx.QueryRowContext(ctx,
			`SELECT group_uid, selector_hash FROM tasks
			 WHERE unit_id = $1 AND state = 'RUNNING'`, unitID,
		).Scan(&groupUID, &selectorHash)
		if err == sql.ErrNoRows {
			recovered = false // 不在 RUNNING，忽略
			return nil
		}
		if err != nil {
			return fmt.Errorf("select task for recover: %w", err)
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET state = 'PENDING', lease_owner = '', updated_at = now()
			 WHERE unit_id = $1 AND state = 'RUNNING'`,
			unitID,
		)
		if err != nil {
			return fmt.Errorf("recover task update: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		if affected != 1 {
			recovered = false // 状态已变，忽略
			return nil
		}
		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, model.StateRunning, -1, nil); err != nil {
			return fmt.Errorf("group_stats dec running: %w", err)
		}
		if err := adjustGroupStats(ctx, tx, groupUID, selectorHash, model.StatePending, +1, nil); err != nil {
			return fmt.Errorf("group_stats inc pending: %w", err)
		}
		recovered = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("recover task %s: %w", unitID, err)
	}
	return recovered, nil
}

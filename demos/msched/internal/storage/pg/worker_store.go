package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/cockroachdb/cockroach-go/v2/crdb"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WorkerStore 实现 scheduler.WorkerStore（DESIGN §10 Register/Heartbeat + token 校验）。
//
// Register/Heartbeat 直接写 PostgreSQL workers 表；WorkerRegistry（读视图缓存，§4.1）
// 下次刷新时可见。Heartbeat 的任务级回收（recovered）走 crdb.ExecuteTx 事务，与
// group_stats 同事务维护。GetByUnitID 供 RPC 入口 token 校验。
type WorkerStore struct {
	db    *gorm.DB
	sqlDB *sql.DB // 供 crdb.ExecuteTx 事务重试
}

// NewWorkerStore 构造 WorkerStore。提取底层 *sql.DB 供 crdb.ExecuteTx 事务重试用。
func NewWorkerStore(db *gorm.DB) (*WorkerStore, error) {
	sqldb, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying *sql.DB: %w", err)
	}
	return &WorkerStore{db: db, sqlDB: sqldb}, nil
}

// 编译期断言：实现 scheduler.WorkerStore。
var _ scheduler.WorkerStore = (*WorkerStore)(nil)

// Register 上线注册（DESIGN §10）。upsert workers 表：labels/capacity/lease_expire_time/
// token/state。相同 unit_id 重复注册覆盖刷新（labels 变更/lease 续期）。lease_expire_time
// = now + leaseTTL。state 置 'ONLINE'（lease 有效期内即在线，§4.1）。返回含算好 lease_expire_time。
//
// GORM clause.OnConflict 实现 upsert：冲突 unit_id 时覆盖指定列（updated_at 用 now()）。
// labels 直接传 in.Labels（WorkerSelector），驱动调 Value() 序列化为 JSONB（selector_codec.go），
// nil 输出 "{}" 满足 NOT NULL 约束，无需手动 json.Marshal。
func (w *WorkerStore) Register(ctx context.Context, in model.Worker, leaseTTL time.Duration) (model.Worker, error) {
	state := in.State
	if state == "" {
		state = "ONLINE"
	}
	expire := time.Now().Add(leaseTTL)

	row := model.Worker{
		UnitID:          in.UnitID,
		Labels:          in.Labels,
		Capacity:        in.Capacity,
		LeaseExpireTime: expire,
		State:           state,
		Token:           in.Token,
	}
	err := w.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "unit_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"labels":            gorm.Expr("EXCLUDED.labels"),
			"capacity":          gorm.Expr("EXCLUDED.capacity"),
			"lease_expire_time": gorm.Expr("EXCLUDED.lease_expire_time"),
			"state":             gorm.Expr("EXCLUDED.state"),
			"token":             gorm.Expr("EXCLUDED.token"),
			"updated_at":        gorm.Expr("now()"),
		}),
	}).Create(&row).Error
	if err != nil {
		return model.Worker{}, fmt.Errorf("upsert worker %s: %w", in.UnitID, err)
	}
	return model.Worker{
		UnitID:          in.UnitID,
		Labels:          in.Labels,
		Capacity:        in.Capacity,
		LeaseExpireTime: expire,
		State:           state,
		Token:           in.Token,
	}, nil
}

// Heartbeat 刷新 lease + 任务级活性心跳回收（DESIGN §10/§8.4 补充）。
//
//  1. UPDATE workers.lease_expire_time = now + leaseTTL（worker 不存在则影响行=0，幂等不报错）
//  2. 回收：lease_owner=unitID AND state='RUNNING' AND unit_id NOT IN runningUnitIDs 的任务
//     批量转 PENDING（清 lease_owner），返回 recovered。runningUnitIDs 为空时回收该 worker
//     全部 RUNNING（持空集即全丢）。
//
// 回收走 crdb.ExecuteTx 事务，与 group_stats 同事务维护（RUNNING-1/PENDING+1）。
// 先关闭 *sql.Rows 再做 UPDATE（crdb 重试约束，同 BatchSetRunning）。
func (w *WorkerStore) Heartbeat(ctx context.Context, unitID string, runningUnitIDs []string, leaseTTL time.Duration) (time.Time, []string, error) {
	expire := time.Now().Add(leaseTTL)

	// 1. refresh lease（非事务，单 UPDATE；worker 不存在则 no-op，不报错）。
	res := w.db.WithContext(ctx).
		Model(&model.Worker{}).
		Where("unit_id = ?", unitID).
		Updates(map[string]any{
			"lease_expire_time": expire,
			"updated_at":        gorm.Expr("now()"),
		})
	if res.Error != nil {
		return time.Time{}, nil, fmt.Errorf("refresh worker lease %s: %w", unitID, res.Error)
	}
	_ = res.RowsAffected // worker 不存在则 0，幂等不报错

	// 2. 任务级回收（事务 + group_stats 维护）。
	// runningUnitIDs 为空时回收该 worker 全部 RUNNING（持空集即全丢）。
	// 注意：`unit_id <> ALL('{}')` 在 PostgreSQL 返回 NULL（非 true），空数组不能走 NOT IN，
	// 故空集与空集分别拼条件（非空才加 unit_id <> ALL($2)）。
	var recovered []string
	err := crdb.ExecuteTx(ctx, w.sqlDB, nil, func(tx *sql.Tx) error {
		gtx := gtxFromTx(w.db, ctx, tx)
		var (
			rows *sql.Rows
			err  error
		)
		if len(runningUnitIDs) > 0 {
			rows, err = tx.QueryContext(ctx,
				`SELECT unit_id, group_uid, selector_hash FROM tasks
				 WHERE lease_owner = $1 AND state = 'RUNNING' AND unit_id <> ALL($2::text[])`,
				unitID, pq.Array(runningUnitIDs),
			)
		} else {
			rows, err = tx.QueryContext(ctx,
				`SELECT unit_id, group_uid, selector_hash FROM tasks
				 WHERE lease_owner = $1 AND state = 'RUNNING'`,
				unitID,
			)
		}
		if err != nil {
			return fmt.Errorf("select running for recover: %w", err)
		}
		type recRow struct{ uid, groupUID, selectorHash string }
		collected := make([]recRow, 0)
		for rows.Next() {
			var r recRow
			if err := rows.Scan(&r.uid, &r.groupUID, &r.selectorHash); err != nil {
				rows.Close()
				return fmt.Errorf("scan running for recover: %w", err)
			}
			collected = append(collected, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("rows iter: %w", err)
		}
		rows.Close()

		for _, r := range collected {
			if err := gtx.Model(&model.Task{}).
				Where("unit_id = ? AND lease_owner = ? AND state = ?", r.uid, unitID, model.StateRunning).
				Updates(map[string]any{
					"state":       model.StatePending,
					"lease_owner": "",
					"updated_at":  gorm.Expr("now()"),
				}).Error; err != nil {
				return fmt.Errorf("recover update %s: %w", r.uid, err)
			}
			if err := adjustGroupStats(ctx, tx, r.groupUID, r.selectorHash, model.StateRunning, -1, nil); err != nil {
				return fmt.Errorf("group_stats dec running: %w", err)
			}
			if err := adjustGroupStats(ctx, tx, r.groupUID, r.selectorHash, model.StatePending, +1, nil); err != nil {
				return fmt.Errorf("group_stats inc pending: %w", err)
			}
			recovered = append(recovered, r.uid)
		}
		return nil
	})
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("heartbeat recover %s: %w", unitID, err)
	}
	return expire, recovered, nil
}

// GetByUnitID 按 unit_id 查 worker（token 校验用，DESIGN §10）。未找到返回错误。
func (w *WorkerStore) GetByUnitID(ctx context.Context, unitID string) (model.Worker, error) {
	var wk model.Worker
	err := w.db.WithContext(ctx).Where("unit_id = ?", unitID).First(&wk).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Worker{}, fmt.Errorf("worker %s not found", unitID)
		}
		return model.Worker{}, fmt.Errorf("query worker %s: %w", unitID, err)
	}
	return wk, nil
}

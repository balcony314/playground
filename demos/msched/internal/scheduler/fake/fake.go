// Package fake 提供 scheduler 存储接口的 in-memory 实现，仅用于测试。
//
// 语义忠实模拟 DESIGN：PostgreSQL CAS 乐观锁、Redis 派发队列 ZSET（按 score 升序）、
// worker 视图进程内遍历匹配（SelectorSubset，开放 k/v selector 亦成立）。不涉及真实 PostgreSQL/Redis。
package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
)

// bucketEntry 派发队列成员，模拟 ZSET (member=unitID, score=dispatch_time)。
type bucketEntry struct {
	unitID string
	score  int64
}

// dispatchBucket 模拟 dispatch:{wuid}:{group} ZSET，按 score 升序，同 score 按 unitID 升序。
type dispatchBucket struct {
	entries []bucketEntry
}

func (b *dispatchBucket) push(unitID string, score int64) {
	i := sort.Search(len(b.entries), func(i int) bool {
		if b.entries[i].score != score {
			return b.entries[i].score > score
		}
		return b.entries[i].unitID >= unitID
	})
	b.entries = append(b.entries, bucketEntry{})
	copy(b.entries[i+1:], b.entries[i:])
	b.entries[i] = bucketEntry{unitID: unitID, score: score}
}

func (b *dispatchBucket) popMin() (string, bool) {
	if len(b.entries) == 0 {
		return "", false
	}
	e := b.entries[0]
	b.entries = b.entries[1:]
	return e.unitID, true
}

// groupStatsKey group_stats 行式主键三元组（DESIGN §3）。
type groupStatsKey struct {
	GroupUID     string
	SelectorHash string
	State        model.State
}

// Store 同时实现 scheduler.TaskStore / WorkerRegistry / DispatchQueue，
// 供撮合器与派发器测试装配。所有方法线程安全。
type Store struct {
	mu               sync.Mutex
	tasks            map[string]*model.Task
	workers          map[string]*model.Worker
	dispatch         map[string]map[string]*dispatchBucket // wuid -> group -> bucket
	groupStats       map[groupStatsKey]int64               // (group, selector_hash, state) -> count
	hashPairs        map[string][]string                   // selector_hash -> 可读 pairs（GroupStats.Selectors)
	getByUnitIDCount int                                   // GetByUnitID 调用次数（测试鉴权缓存用）
}

// New 构造空 Store。
func New() *Store {
	return &Store{
		tasks:      map[string]*model.Task{},
		workers:    map[string]*model.Worker{},
		dispatch:   map[string]map[string]*dispatchBucket{},
		groupStats: map[groupStatsKey]int64{},
		hashPairs:  map[string][]string{},
	}
}

// --- 测试辅助方法 ---

// adjustCount 增减 group_stats 计数（UPSERT 语义，DESIGN §3 实时维护）。
// 调用方持 s.mu。count 减到 0 保留行（查询直观）；负值不防御（状态转换正确则不应出现）。
func (s *Store) adjustCount(groupUID, selectorHash string, state model.State, delta int64) {
	key := groupStatsKey{GroupUID: groupUID, SelectorHash: selectorHash, State: state}
	s.groupStats[key] += delta
}

// AddTask 添加一个任务（拷贝）。若 SelectorHash 未填则按 WorkerSelector 计算。
// 实时维护 group_stats：创建即 PENDING，PENDING count+1（DESIGN §3）。
// 顺带记录 selector_hash 的可读 pairs（首次见到该 hash 时），供 ListGroupStats 填 Selectors。
func (s *Store) AddTask(t model.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.SelectorHash == "" {
		t.SelectorHash = model.ComputeSelectorHash(t.WorkerSelector)
	}
	if _, ok := s.hashPairs[t.SelectorHash]; !ok {
		s.hashPairs[t.SelectorHash] = model.ComputeSelectorPairs(t.WorkerSelector)
	}
	s.tasks[t.UnitID] = &t
	s.adjustCount(t.GroupUID, t.SelectorHash, model.StatePending, +1)
}

// AddWorker 添加一个 worker（拷贝）。
func (s *Store) AddWorker(w model.Worker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[w.UnitID] = &w
}

// Task 返回某任务的快照。
func (s *Store) Task(unitID string) (model.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[unitID]
	if !ok {
		return model.Task{}, false
	}
	return *t, true
}

// BucketLen 返回某 worker 某 group 派发桶当前长度（积压水位，测试断言用）。
func (s *Store) BucketLen(wuid, group string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatch[wuid] == nil || s.dispatch[wuid][group] == nil {
		return 0
	}
	return len(s.dispatch[wuid][group].entries)
}

// GroupStat 返回某 (group, selector_hash, state) 当前计数（测试断言用）。
// selector 为 nil 时用空 selector 的 hash（通配维度）。
func (s *Store) GroupStat(groupUID string, selector model.WorkerSelector, state model.State) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := groupStatsKey{
		GroupUID:     groupUID,
		SelectorHash: model.ComputeSelectorHash(selector),
		State:        state,
	}
	return s.groupStats[key]
}

// active 判断任务是否参与撮合查询（PENDING 或到期 BACKOFF，DESIGN §8.6）。
func active(t *model.Task, now time.Time) bool {
	if t.State == model.StatePending {
		return true
	}
	if t.State == model.StateBackoff && t.NextRetryTime != nil && !t.NextRetryTime.After(now) {
		return true
	}
	return false
}

// --- scheduler.TaskStore ---

// Create 创建任务（DESIGN §3）。幂等：相同 group+args 产生相同 UnitID，已存在则返回
// 旧值不重复计数。补算 unit_id / selector_hash / priority（<=0 取 created_at 微秒），
// 初始 state=PENDING，group_stats PENDING count+1（首次插入才计数）。
func (s *Store) Create(_ context.Context, in model.Task) (model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	in.FailCount = 0
	in.DispatchCount = 0
	if _, ok := s.hashPairs[in.SelectorHash]; !ok {
		s.hashPairs[in.SelectorHash] = model.ComputeSelectorPairs(in.WorkerSelector)
	}
	// 幂等：已存在则返回旧值，不重复 +1（DESIGN §3）。
	if existing, ok := s.tasks[in.UnitID]; ok {
		return *existing, nil
	}
	s.tasks[in.UnitID] = &in
	s.adjustCount(in.GroupUID, in.SelectorHash, model.StatePending, +1)
	return in, nil
}

// ListByGroup 按 group list 任务（DESIGN §3）。states 为空不过滤；按 priority, unit_id
// 升序；limit/offset 分页。不返回 args/result（保持与 pg 实现一致，list 不拉大字段）。
func (s *Store) ListByGroup(_ context.Context, groupUID string, states []model.State, limit, offset int) ([]model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[model.State]bool{}
	for _, st := range states {
		want[st] = true
	}
	matched := make([]model.Task, 0)
	for _, t := range s.tasks {
		if t.GroupUID != groupUID {
			continue
		}
		if len(states) > 0 && !want[t.State] {
			continue
		}
		matched = append(matched, *t)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Priority != matched[j].Priority {
			return matched[i].Priority < matched[j].Priority
		}
		return matched[i].UnitID < matched[j].UnitID
	})
	if offset > 0 && offset < len(matched) {
		matched = matched[offset:]
	} else if offset >= len(matched) {
		return []model.Task{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// ListActiveGroups 返回有可撮合任务的 group 列表（排序稳定，DESIGN §9.5）。
func (s *Store) ListActiveGroups(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	seen := map[string]bool{}
	for _, t := range s.tasks {
		if active(t, now) {
			seen[t.GroupUID] = true
		}
	}
	groups := make([]string, 0, len(seen))
	for g := range seen {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return groups, nil
}

// ListPendByGroup 按 group 取可撮合候选，按 Priority 升序取前 n（DESIGN §7/§9.1）。
// Priority 相同按 UnitID 升序兜底稳定序。
func (s *Store) ListPendByGroup(_ context.Context, group string, n int) ([]model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	matched := make([]model.Task, 0)
	for _, t := range s.tasks {
		if t.GroupUID == group && active(t, now) {
			matched = append(matched, *t)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Priority != matched[j].Priority {
			return matched[i].Priority < matched[j].Priority
		}
		return matched[i].UnitID < matched[j].UnitID
	})
	if n >= 0 && len(matched) > n {
		matched = matched[:n]
	}
	return matched, nil
}

// CASSchedule PENDING/BACKOFF → SCHEDULED（DESIGN §8.2），影响行=1 才算抢到。
func (s *Store) CASSchedule(_ context.Context, unitID string, owner string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[unitID]
	if !ok {
		return false, nil
	}
	if t.State != model.StatePending && t.State != model.StateBackoff {
		return false, nil
	}
	oldState := t.State
	t.State = model.StateScheduled
	t.DispatchTime = time.Now()
	t.DispatchCount++
	t.LeaseOwner = owner
	// 实时维护 group_stats：old_state count-1、SCHEDULED count+1（DESIGN §3）。
	s.adjustCount(t.GroupUID, t.SelectorHash, oldState, -1)
	s.adjustCount(t.GroupUID, t.SelectorHash, model.StateScheduled, +1)
	return true, nil
}

// SetBackoff 无匹配 worker 退避：PENDING/BACKOFF -> BACKOFF（DESIGN §8.6）。
// store 内 fail_count++；状态已变（他节点 CAS 抢走）时 no-op。
func (s *Store) SetBackoff(_ context.Context, unitID string, nextRetry time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[unitID]
	if !ok {
		return nil
	}
	if t.State != model.StatePending && t.State != model.StateBackoff {
		return nil // 已被 CAS 抢走或终态，no-op
	}
	oldState := t.State
	t.State = model.StateBackoff
	t.NextRetryTime = &nextRetry
	t.FailCount++
	// 实时维护 group_stats：old_state count-1、BACKOFF count+1（DESIGN §3）。
	// PENDING->BACKOFF 才计数变化；BACKOFF->BACKOFF（重退避）old=new 不调整也正确。
	if oldState != model.StateBackoff {
		s.adjustCount(t.GroupUID, t.SelectorHash, oldState, -1)
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StateBackoff, +1)
	}
	return nil
}

// BatchSetRunning 批量 SCHEDULED → RUNNING（DESIGN §8.3），带 lease_owner 所有权校验。
// 只转属于 wuid 的 SCHEDULED 任务，防止回收重派他处后被误标 RUNNING（防双发 §8.4/§8.7）。
// 返回实际转为 RUNNING 的 unitID 集合。
func (s *Store) BatchSetRunning(_ context.Context, wuid string, unitIDs []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	running := make([]string, 0, len(unitIDs))
	for _, uid := range unitIDs {
		t, ok := s.tasks[uid]
		if !ok {
			continue
		}
		if t.State != model.StateScheduled || t.LeaseOwner != wuid {
			continue
		}
		t.State = model.StateRunning
		t.PullTime = now
		t.Timeout = now.Add(t.MaxExecDuration) // 执行硬截止（DESIGN §8.3）
		// 实时维护 group_stats：SCHEDULED count-1、RUNNING count+1（DESIGN §3）。
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StateScheduled, -1)
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StateRunning, +1)
		running = append(running, uid)
	}
	return running, nil
}

// ListByUnitIDs 按 UnitID 批量读任务快照（DESIGN §10）。
func (s *Store) ListByUnitIDs(_ context.Context, unitIDs []string) ([]model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := make([]model.Task, 0, len(unitIDs))
	for _, uid := range unitIDs {
		t, ok := s.tasks[uid]
		if !ok {
			continue
		}
		tasks = append(tasks, *t)
	}
	return tasks, nil
}

// Complete 执行成功回传：CAS RUNNING -> COMPLETED，写 Result（DESIGN §8.7）。
// 所有权校验 lease_owner==wuid：不符或状态已变返回 false（Report 忽略）。
// 实时维护 group_stats：RUNNING count-1、COMPLETED count+1。
func (s *Store) Complete(_ context.Context, unitID, wuid string, result []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[unitID]
	if !ok || t.State != model.StateRunning || t.LeaseOwner != wuid {
		return false, nil // 不存在/状态已变/所有权不符，忽略
	}
	t.State = model.StateCompleted
	t.Result = result
	t.LeaseOwner = ""
	s.adjustCount(t.GroupUID, t.SelectorHash, model.StateRunning, -1)
	s.adjustCount(t.GroupUID, t.SelectorHash, model.StateCompleted, +1)
	return true, nil
}

// ReportFail 执行失败回传：CAS 所有权校验后按 dispatch_count 兜底（DESIGN §8.5/§8.6）。
// 仅 RUNNING AND lease_owner==wuid 命中。dispatch_count >= maxAttempt -> DISCARDED，否则 PENDING。
// 回 PENDING 不入 BACKOFF（§8.6）。返回 reported（命中）+ discarded（命中且转 DISCARDED）。
func (s *Store) ReportFail(_ context.Context, unitID, wuid string, maxAttempt int) (reported bool, discarded bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[unitID]
	if !ok || t.State != model.StateRunning || t.LeaseOwner != wuid {
		return false, false, nil // 忽略
	}
	if t.DispatchCount >= maxAttempt {
		t.State = model.StateDiscarded
		discarded = true
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StateRunning, -1)
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StateDiscarded, +1)
	} else {
		t.State = model.StatePending
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StateRunning, -1)
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StatePending, +1)
	}
	t.LeaseOwner = ""
	reported = true
	return reported, discarded, nil
}

// ListGroupStats 返回某 group 下各 (selector_hash, state) 计数（DESIGN §3）。
// 仅返回 count>0 的行，按 (selector_hash, state) 升序稳定排列。
func (s *Store) ListGroupStats(_ context.Context, groupUID string) ([]model.GroupStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.GroupStats, 0)
	for key, count := range s.groupStats {
		if key.GroupUID != groupUID || count <= 0 {
			continue
		}
		out = append(out, model.GroupStats{
			GroupUID:     key.GroupUID,
			SelectorHash: key.SelectorHash,
			Selectors:    s.hashPairs[key.SelectorHash],
			State:        key.State,
			Count:        count,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelectorHash != out[j].SelectorHash {
			return out[i].SelectorHash < out[j].SelectorHash
		}
		return out[i].State < out[j].State
	})
	return out, nil
}

// Delete 物理删除任务（scheduler.TaskStore.Delete）。
//
// unitIDs 为空：删 groupUID 下全部任务 + group_stats 该 group 所有行。
// unitIDs 非空：删属于 groupUID 且在列表中的任务，group_stats 按 (selector_hash, state)
// 减 count（行保留，count 可降至 0，与实时维护一致）。
func (s *Store) Delete(_ context.Context, groupUID string, unitIDs []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(unitIDs) == 0 {
		// 删整个 group：逐个 task 减 stats 后删 task，最后删该 group 全部 stats 行。
		var n int64
		for uid, t := range s.tasks {
			if t.GroupUID != groupUID {
				continue
			}
			s.adjustCount(groupUID, t.SelectorHash, t.State, -1)
			delete(s.tasks, uid)
			n++
		}
		for key := range s.groupStats {
			if key.GroupUID == groupUID {
				delete(s.groupStats, key)
			}
		}
		return n, nil
	}
	// 删指定
	want := make(map[string]struct{}, len(unitIDs))
	for _, u := range unitIDs {
		want[u] = struct{}{}
	}
	var n int64
	for uid, t := range s.tasks {
		if t.GroupUID != groupUID {
			continue
		}
		if _, ok := want[uid]; !ok {
			continue
		}
		s.adjustCount(groupUID, t.SelectorHash, t.State, -1)
		delete(s.tasks, uid)
		n++
	}
	return n, nil
}

// RecoverByWorker 回收某 worker 名下全部 RUNNING + SCHEDULED -> PENDING（DESIGN §8.4）。
// 返回实际回收的 unitID 列表（排序稳定）。group_stats 同步维护。
func (s *Store) RecoverByWorker(_ context.Context, wuid string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recovered := make([]string, 0)
	for uid, t := range s.tasks {
		if t.LeaseOwner != wuid {
			continue
		}
		if t.State != model.StateRunning && t.State != model.StateScheduled {
			continue
		}
		oldState := t.State
		t.State = model.StatePending
		t.LeaseOwner = ""
		s.adjustCount(t.GroupUID, t.SelectorHash, oldState, -1)
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StatePending, +1)
		recovered = append(recovered, uid)
	}
	sort.Strings(recovered)
	return recovered, nil
}

// RecoverTask 单 task 执行超时回收：RUNNING -> PENDING（DESIGN §8.4）。
// 不校验 lease_owner（zset member 只存 unitID）。返回是否命中。
func (s *Store) RecoverTask(_ context.Context, unitID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[unitID]
	if !ok || t.State != model.StateRunning {
		return false, nil
	}
	t.State = model.StatePending
	t.LeaseOwner = ""
	s.adjustCount(t.GroupUID, t.SelectorHash, model.StateRunning, -1)
	s.adjustCount(t.GroupUID, t.SelectorHash, model.StatePending, +1)
	return true, nil
}

// --- scheduler.WorkerRegistry ---

// Match 遍历在线 worker，返回 labels 包含 selector 的 worker ID 列表（DESIGN §4，S_t ⊆ L_w）。
func (s *Store) Match(_ context.Context, selector model.WorkerSelector) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	wuids := make([]string, 0)
	for _, w := range s.workers {
		online := w.LeaseExpireTime.IsZero() || w.LeaseExpireTime.After(now)
		if !online {
			continue
		}
		if model.SelectorSubset(selector, w.Labels) {
			wuids = append(wuids, w.UnitID)
		}
	}
	sort.Strings(wuids)
	return wuids, nil
}

// --- scheduler.DispatchQueue ---

// Push 入派发队列（ZADD），score=dispatch_time（DESIGN §8.2）。
func (s *Store) Push(_ context.Context, wuid, group string, unitID string, score int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatch[wuid] == nil {
		s.dispatch[wuid] = map[string]*dispatchBucket{}
	}
	if s.dispatch[wuid][group] == nil {
		s.dispatch[wuid][group] = &dispatchBucket{}
	}
	s.dispatch[wuid][group].push(unitID, score)
	return nil
}

// PullRoundRobin 派发端 group 轮询：逐桶 ZPOPMIN 1 各取一个，凑满 batch 或所有桶空（DESIGN §9.2）。
// 非阻塞，空批返回空切片。
func (s *Store) PullRoundRobin(_ context.Context, wuid string, batch int) ([]scheduler.ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wd := s.dispatch[wuid]
	groups := make([]string, 0, len(wd))
	for g, b := range wd {
		if len(b.entries) > 0 {
			groups = append(groups, g)
		}
	}
	sort.Strings(groups)

	result := make([]scheduler.ScheduledTask, 0, batch)
	for len(result) < batch {
		progressed := false
		for _, g := range groups {
			if len(result) >= batch {
				break
			}
			b := wd[g]
			if len(b.entries) == 0 {
				continue
			}
			tid, _ := b.popMin()
			result = append(result, scheduler.ScheduledTask{UnitID: tid, Group: g, Wuid: wuid})
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return result, nil
}

// --- scheduler.WorkerStore ---

// Register 上线注册（DESIGN §10）。upsert workers 表：相同 unit_id 覆盖刷新。
// lease_expire_time = now + leaseTTL。返回含算好 lease_expire_time。
func (s *Store) Register(_ context.Context, in model.Worker, leaseTTL time.Duration) (model.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := in
	w.LeaseExpireTime = time.Now().Add(leaseTTL)
	if w.State == "" {
		w.State = "ONLINE"
	}
	s.workers[w.UnitID] = &w
	return w, nil
}

// Heartbeat 刷新 lease + 任务级活性心跳回收（DESIGN §10/§8.4 补充）。
// 回收 lease_owner=unitID AND state=RUNNING AND unit_id NOT IN runningUnitIDs 的任务转 PENDING，
// 返回 recovered。runningUnitIDs 为空时回收该 worker 全部 RUNNING（持空集即全丢）。
func (s *Store) Heartbeat(_ context.Context, unitID string, runningUnitIDs []string, leaseTTL time.Duration) (time.Time, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	expire := time.Now().Add(leaseTTL)
	if w, ok := s.workers[unitID]; ok {
		w.LeaseExpireTime = expire
	}

	hold := make(map[string]struct{}, len(runningUnitIDs))
	for _, uid := range runningUnitIDs {
		hold[uid] = struct{}{}
	}

	recovered := make([]string, 0)
	for uid, t := range s.tasks {
		if t.LeaseOwner != unitID || t.State != model.StateRunning {
			continue
		}
		if _, ok := hold[uid]; ok {
			continue // 仍在上报集合内，保留
		}
		// 回收：RUNNING -> PENDING
		t.State = model.StatePending
		t.LeaseOwner = ""
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StateRunning, -1)
		s.adjustCount(t.GroupUID, t.SelectorHash, model.StatePending, +1)
		recovered = append(recovered, uid)
	}
	sort.Strings(recovered)
	return expire, recovered, nil
}

// GetByUnitID 按 unit_id 查 worker（token 校验用，DESIGN §10）。未找到返回错误。
func (s *Store) GetByUnitID(_ context.Context, unitID string) (model.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getByUnitIDCount++
	w, ok := s.workers[unitID]
	if !ok {
		return model.Worker{}, fmt.Errorf("worker %s not found", unitID)
	}
	return *w, nil
}

// GetByUnitIDCount 返回 GetByUnitID 调用次数（测试鉴权缓存命中率用）。
func (s *Store) GetByUnitIDCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getByUnitIDCount
}

// 编译期断言：Store 实现四个接口。
var (
	_ scheduler.TaskStore      = (*Store)(nil)
	_ scheduler.WorkerRegistry = (*Store)(nil)
	_ scheduler.DispatchQueue  = (*Store)(nil)
	_ scheduler.WorkerStore    = (*Store)(nil)
)

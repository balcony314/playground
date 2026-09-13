// dispatcher.go 派发器：派发队列 → Worker Pull（DESIGN §9.2/§8.3/§10）。
//
// 多租户公平（派发端，§9.2）：dispatch:{wuid}:{group} 分桶，Worker Pull 时
// PullRoundRobin 逐桶 ZPOPMIN 各取一个，大户 group 无法独占 worker 拉取额度。
// 热路径零撮合零 CAS：Pull 只拉现成队列 + 批量 SCHEDULED→RUNNING。

package scheduler

import (
	"context"
	"fmt"

	"github.com/balcony314/msched/internal/model"
)

// Dispatcher 派发器（无状态多实例，DESIGN §5）。
type Dispatcher struct {
	store TaskStore     // BatchSetRunning + ListByUnitIDs
	queue DispatchQueue // PullRoundRobin
}

// NewDispatcher 构造派发器。
func NewDispatcher(store TaskStore, queue DispatchQueue) *Dispatcher {
	return &Dispatcher{store: store, queue: queue}
}

// Pull 处理 worker 拉取（DESIGN §9.2/§8.3/§10）：
//  1. PullRoundRobin 逐 group 桶各取一个，凑满 batch 或扫完（非阻塞，空批返回空）
//  2. 批量 SCHEDULED → RUNNING（带 lease_owner=wuid 所有权校验，只转本 worker 的；
//     回收重派他处的任务不命中，防双发 §8.4/§8.7）
//  3. 只返回实际转 RUNNING 的 task 详情
func (d *Dispatcher) Pull(ctx context.Context, wuid string, batch int) ([]model.Task, error) {
	pulled, err := d.queue.PullRoundRobin(ctx, wuid, batch)
	if err != nil {
		return nil, fmt.Errorf("pull round robin wuid %s: %w", wuid, err)
	}
	if len(pulled) == 0 {
		return nil, nil
	}

	unitIDs := make([]string, len(pulled))
	for i, p := range pulled {
		unitIDs[i] = p.UnitID
	}

	runningIDs, err := d.store.BatchSetRunning(ctx, wuid, unitIDs)
	if err != nil {
		return nil, fmt.Errorf("batch set running: %w", err)
	}
	if len(runningIDs) == 0 {
		return nil, nil
	}

	tasks, err := d.store.ListByUnitIDs(ctx, runningIDs)
	if err != nil {
		return nil, fmt.Errorf("list by unit ids: %w", err)
	}
	return tasks, nil
}

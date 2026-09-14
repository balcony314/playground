// Package queue 实现带去重、延迟与指数退避重试的工作队列，
// 语义对齐 k8s.io/client-go 的 workqueue.RateLimitingInterface，
// 但零外部依赖，元素类型须可比较
package queue

import (
	"math/rand"
	"sync"
	"time"
)

type Queue[T comparable] struct {
	mu           sync.Mutex
	cond         *sync.Cond
	items        []T            // 待处理队列（FIFO）
	dirty        map[T]struct{} // 已入队待处理（去重用）
	processing   map[T]struct{} // 正在处理（Done 后从 processing 移除，若在 dirty 中则重新入队）
	failures     map[T]int      // 失败次数（指数退避与 NumRequeues 用）
	shuttingDown bool

	baseDelay time.Duration
	maxDelay  time.Duration
}

// New 创建队列。baseDelay 为重试退避基数，maxDelay 为退避上限
func New[T comparable](baseDelay, maxDelay time.Duration) *Queue[T] {
	q := &Queue[T]{
		dirty:      map[T]struct{}{},
		processing: map[T]struct{}{},
		failures:   map[T]int{},
		baseDelay:  baseDelay,
		maxDelay:   maxDelay,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Add 立即入队；已在队列中或正在处理的重复元素会被忽略（处理完成时若仍在 dirty 则重新入队）
func (q *Queue[T]) Add(item T) { q.AddAfter(item, 0) }

// AddAfter 延迟 d 后入队
func (q *Queue[T]) AddAfter(item T, d time.Duration) {
	if d <= 0 {
		q.enqueue(item)
		return
	}
	time.AfterFunc(d, func() { q.enqueue(item) })
}

func (q *Queue[T]) enqueue(item T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.shuttingDown {
		return
	}
	// 去重：已在待处理集合或队列中则忽略
	if _, ok := q.dirty[item]; ok {
		return
	}
	q.dirty[item] = struct{}{}
	// 正在处理的元素只记 dirty 不入队，Done 时由 dirty 判断重新入队（对齐 k8s workqueue 去重不变式）
	if _, ok := q.processing[item]; ok {
		return
	}
	q.items = append(q.items, item)
	q.cond.Signal()
}

// AddRateLimited 按失败次数做指数退避后入队（含随机抖动，避免惊群）
func (q *Queue[T]) AddRateLimited(item T) {
	q.mu.Lock()
	q.failures[item]++
	n := q.failures[item]
	q.mu.Unlock()

	backoff := q.baseDelay << (n - 1) // base * 2^(n-1)
	if backoff > q.maxDelay || backoff <= 0 {
		backoff = q.maxDelay
	}
	// 抖动：加法式 [1.0, 1.5) 倍（对齐 k8s wait.Jitter，延迟只增不减）
	jitter := backoff + time.Duration(rand.Float64()*float64(backoff)*0.5)
	q.AddAfter(item, jitter)
}

// Get 阻塞获取队头元素；队列关闭且无剩余元素后返回 shutdown=true
func (q *Queue[T]) Get() (T, bool) {
	var zero T
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.shuttingDown {
		q.cond.Wait()
	}
	if len(q.items) == 0 && q.shuttingDown {
		return zero, true
	}
	item := q.items[0]
	q.items = q.items[1:]
	delete(q.dirty, item)
	q.processing[item] = struct{}{}
	return item, false
}

// TryGet 非阻塞获取（测试用）；队列为空返回 open=false。
// 注意不能在持锁状态下调用 Get（会再次 Lock 导致死锁），出队逻辑须独立实现
func (q *Queue[T]) TryGet() (T, bool) {
	var zero T
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return zero, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	delete(q.dirty, item)
	q.processing[item] = struct{}{}
	return item, true
}

// Done 标记处理完成。若处理期间元素被再次 Add（在 dirty 中），重新入队
func (q *Queue[T]) Done(item T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.processing, item)
	if _, ok := q.dirty[item]; ok {
		q.items = append(q.items, item)
		q.cond.Signal()
	}
}

// Forget 清除元素的重试计数（处理成功后调用）
func (q *Queue[T]) Forget(item T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.failures, item)
}

// NumRequeues 返回元素当前的重试计数
func (q *Queue[T]) NumRequeues(item T) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.failures[item]
}

// ShutDown 关闭队列；阻塞中的 Get 会立即返回 shutdown
func (q *Queue[T]) ShutDown() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.shuttingDown = true
	q.cond.Broadcast()
}

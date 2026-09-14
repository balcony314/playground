package queue

import (
	"testing"
	"time"
)

func TestBasicGetAddDone(t *testing.T) {
	q := New[int](time.Millisecond, 10*time.Millisecond)
	q.Add(1)
	got, shutdown := q.Get()
	if got != 1 || shutdown {
		t.Fatalf("got %d shutdown %v", got, shutdown)
	}
	q.Done(1)
	q.ShutDown()
	if _, shutdown := q.Get(); !shutdown {
		t.Error("Get after ShutDown must return shutdown=true")
	}
}

func TestDedupWhileQueued(t *testing.T) {
	q := New[int](time.Millisecond, time.Millisecond)
	q.Add(1)
	q.Add(1) // 在队列中，应被去重
	q.Add(2)
	first, _ := q.Get()
	q.Done(first)
	second, _ := q.Get()
	q.Done(second)
	if _, open := q.TryGet(); open {
		t.Error("queue should be empty after both items consumed")
	}
}

func TestDoneThenRequeue(t *testing.T) {
	q := New[int](time.Millisecond, time.Millisecond)
	q.Add(1)
	item, _ := q.Get()
	q.Add(1) // 处理中再次 Add：记为 dirty，Done 后重新入队
	q.Done(item)
	requeued, _ := q.Get()
	if requeued != 1 {
		t.Error("item must be requeued when added during processing")
	}
	q.Done(requeued)
	// 重入队恰好一次：消费完后队列必须为空
	if _, open := q.TryGet(); open {
		t.Error("item must be requeued exactly once")
	}
}

func TestRateLimitedBackoff(t *testing.T) {
	base := 20 * time.Millisecond
	q := New[int](base, time.Second)
	q.Add(1)
	item, _ := q.Get()

	start := time.Now()
	q.AddRateLimited(1) // 第 1 次失败：约 base 延迟
	q.Done(item)
	got, _ := q.Get()
	elapsed := time.Since(start)
	if got != 1 {
		t.Fatalf("got %d", got)
	}
	if elapsed < base {
		t.Errorf("AddRateLimited should delay >= base, elapsed %v", elapsed)
	}

	// 退避倍增：第 2 次失败延迟 >= 2*base（安全断言：2*base*[1.0,1.5) >= 2*base）
	q.Done(got)
	q.AddRateLimited(1)
	start2 := time.Now()
	got2, _ := q.Get()
	elapsed2 := time.Since(start2)
	if got2 != 1 {
		t.Fatalf("got2 %d", got2)
	}
	q.Done(got2)
	if elapsed2 < 2*base {
		t.Errorf("second backoff should delay >= 2*base, elapsed %v", elapsed2)
	}
	if n := q.NumRequeues(1); n != 2 {
		t.Errorf("NumRequeues = %d, want 2", n)
	}
	q.Forget(1)
	if n := q.NumRequeues(1); n != 0 {
		t.Errorf("after Forget NumRequeues = %d, want 0", n)
	}
}

func TestAddAfter(t *testing.T) {
	q := New[int](time.Millisecond, time.Millisecond)
	q.AddAfter(7, 30*time.Millisecond)
	if _, open := q.TryGet(); open {
		t.Fatal("item should not be ready yet")
	}
	time.Sleep(50 * time.Millisecond)
	got, open := q.TryGet()
	if !open || got != 7 {
		t.Fatalf("TryGet = %d %v", got, open)
	}
}

func TestConcurrentSafety(t *testing.T) {
	q := New[int](time.Millisecond, time.Millisecond)
	done := make(chan struct{})
	go func() {
		for {
			item, shutdown := q.Get()
			if shutdown {
				close(done)
				return
			}
			q.Done(item)
		}
	}()
	for i := 0; i < 1000; i++ {
		q.Add(i % 50)
	}
	q.ShutDown()
	<-done
}

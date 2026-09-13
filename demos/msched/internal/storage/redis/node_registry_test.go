package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// newMiniNodeRegistry 启动 miniredis 并返回连它的 NodeRegistry + ring + miniredis。
// nodeID 默认 "self"，refresh/leaseTTL 用短周期便于测试。
func newMiniNodeRegistry(t *testing.T, nodeID string) (*NodeRegistry, *scheduler.Ring, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ring := scheduler.NewRing(64)
	nr := NewNodeRegistry(client, nodeID, ring, 50*time.Millisecond, 200*time.Millisecond)
	return nr, ring, mr
}

// TestNodeRegistryStartRegisters Start 后 msched:nodes 含自己，score 在未来。
func TestNodeRegistryStartRegisters(t *testing.T) {
	nr, _, mr := newMiniNodeRegistry(t, "self")
	ctx := context.Background()

	if err := nr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = nr.Stop(ctx2)
	}()

	score, err := mr.ZScore(nodesKey, "self")
	if err != nil {
		t.Fatalf("msched:nodes 应含 self: %v", err)
	}
	if score <= float64(time.Now().UnixMilli()) {
		t.Errorf("self lease score %v 应在未来", score)
	}
}

// TestNodeRegistryRingBuilds 预置其他活跃成员，Start 后 ring.Members 含全部。
func TestNodeRegistryRingBuilds(t *testing.T) {
	nr, ring, mr := newMiniNodeRegistry(t, "self")
	ctx := context.Background()

	// 预置另外两个活跃成员（score 在未来）。
	future := float64(time.Now().Add(10 * time.Second).UnixMilli())
	if _, err := mr.ZAdd(nodesKey, future, "n1"); err != nil {
		t.Fatalf("zadd n1: %v", err)
	}
	if _, err := mr.ZAdd(nodesKey, future, "n2"); err != nil {
		t.Fatalf("zadd n2: %v", err)
	}

	if err := nr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = nr.Stop(ctx2)
	}()

	got := ring.Members()
	want := map[string]bool{"self": true, "n1": true, "n2": true}
	if len(got) != 3 {
		t.Fatalf("ring.Members = %v, want 3 个成员", got)
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("意外成员 %q", m)
		}
	}
}

// TestNodeRegistryLeaseExpiry 过期成员不应进环（ZREMRANGEBYSCORE 清掉）。
func TestNodeRegistryLeaseExpiry(t *testing.T) {
	nr, ring, mr := newMiniNodeRegistry(t, "self")
	ctx := context.Background()

	// 预置一个过期成员 + 一个活跃成员。
	if _, err := mr.ZAdd(nodesKey, float64(time.Now().Add(-1*time.Second).UnixMilli()), "expired"); err != nil {
		t.Fatalf("zadd expired: %v", err)
	}
	if _, err := mr.ZAdd(nodesKey, float64(time.Now().Add(10*time.Second).UnixMilli()), "alive"); err != nil {
		t.Fatalf("zadd alive: %v", err)
	}

	if err := nr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = nr.Stop(ctx2)
	}()

	got := ring.Members()
	for _, m := range got {
		if m == "expired" {
			t.Errorf("过期成员 expired 不应进环: %v", got)
		}
	}
	found := false
	for _, m := range got {
		if m == "alive" {
			found = true
		}
	}
	if !found {
		t.Errorf("活跃成员 alive 应进环: %v", got)
	}
}

// TestNodeRegistryIncrementalUpdate Start 后成员增删，手动 refreshOnce 后环增删生效。
func TestNodeRegistryIncrementalUpdate(t *testing.T) {
	nr, ring, mr := newMiniNodeRegistry(t, "self")
	ctx := context.Background()

	// 初始只有自己。
	if err := nr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := ring.Members(); len(got) != 1 || got[0] != "self" {
		t.Fatalf("初始 ring = %v, want [self]", got)
	}

	// 追加 n1 + n2。
	future := float64(time.Now().Add(10 * time.Second).UnixMilli())
	if _, err := mr.ZAdd(nodesKey, future, "n1"); err != nil {
		t.Fatalf("zadd n1: %v", err)
	}
	if _, err := mr.ZAdd(nodesKey, future, "n2"); err != nil {
		t.Fatalf("zadd n2: %v", err)
	}
	if err := nr.refreshOnce(ctx); err != nil {
		t.Fatalf("refresh after add: %v", err)
	}
	if got := ring.Members(); len(got) != 3 {
		t.Errorf("加 n1,n2 后 ring = %v, want 3 个", got)
	}

	// 删除 n1。
	if _, err := mr.ZRem(nodesKey, "n1"); err != nil {
		t.Fatalf("zrem n1: %v", err)
	}
	if err := nr.refreshOnce(ctx); err != nil {
		t.Fatalf("refresh after remove: %v", err)
	}
	got := ring.Members()
	if len(got) != 2 {
		t.Fatalf("删 n1 后 ring = %v, want 2 个", got)
	}
	for _, m := range got {
		if m == "n1" {
			t.Errorf("n1 应已移除: %v", got)
		}
	}

	// 清理（Stop 会 ZREM self）。
	ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = nr.Stop(ctx2)
}

// TestNodeRegistryStopRemoves Stop 后 msched:nodes 不含自己。
func TestNodeRegistryStopRemoves(t *testing.T) {
	nr, _, mr := newMiniNodeRegistry(t, "self")
	ctx := context.Background()

	if err := nr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := mr.ZScore(nodesKey, "self"); err != nil {
		t.Fatalf("start 后应有 self: %v", err)
	}

	ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := nr.Stop(ctx2); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := mr.ZScore(nodesKey, "self"); err == nil {
		t.Error("stop 后应无 self")
	}
}

// TestNodeRegistryDoubleStartNoOp 双重 Start 不 panic、不重复 spawn loop（回归 worker_registry 缺陷1范式）。
func TestNodeRegistryDoubleStartNoOp(t *testing.T) {
	nr, _, mr := newMiniNodeRegistry(t, "self")
	ctx := context.Background()

	if err := nr.Start(ctx); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := nr.Start(ctx); err != nil { // 二次 Start 应 no-op
		t.Fatalf("second start: %v", err)
	}
	ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := nr.Stop(ctx2); err != nil { // Stop 不 panic
		t.Fatalf("stop: %v", err)
	}
	_ = mr
}

// TestNodeRegistryStopBeforeStartNoBlock 未 Start 直接 Stop 不阻塞（回归 worker_registry 缺陷2范式）。
func TestNodeRegistryStopBeforeStartNoBlock(t *testing.T) {
	nr, _, _ := newMiniNodeRegistry(t, "self")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- nr.Stop(ctx) // 未 Start 直接 Stop
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stop before start 返回错误: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop before start 阻塞超时")
	}
}

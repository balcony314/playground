package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newMiniWorkerHB 启动 miniredis 并返回连它的 WorkerHeartbeat。
func newMiniWorkerHB(t *testing.T) (*WorkerHeartbeat, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewWorkerHeartbeat(client), mr
}

// TestWorkerHeartbeatActiveExpired 续期后 Active 只含未过期、Expired 含过期、CleanExpired 清过期。
func TestWorkerHeartbeatActiveExpired(t *testing.T) {
	hb, _ := newMiniWorkerHB(t)
	ctx := context.Background()
	now := time.Now()

	if err := hb.Renew(ctx, "w1", now.Add(10*time.Second)); err != nil {
		t.Fatalf("renew w1: %v", err)
	}
	if err := hb.Renew(ctx, "w2", now.Add(-1*time.Second)); err != nil {
		t.Fatalf("renew w2: %v", err)
	}

	active, err := hb.Active(ctx)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if _, ok := active["w1"]; !ok {
		t.Errorf("active missing w1: %v", active)
	}
	if _, ok := active["w2"]; ok {
		t.Errorf("active should not contain expired w2: %v", active)
	}

	expired, err := hb.Expired(ctx)
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	if len(expired) != 1 || expired[0] != "w2" {
		t.Errorf("expired = %v, want [w2]", expired)
	}

	if err := hb.CleanExpired(ctx); err != nil {
		t.Fatalf("clean expired: %v", err)
	}
	expired2, _ := hb.Expired(ctx)
	if len(expired2) != 0 {
		t.Errorf("after clean, expired = %v, want empty", expired2)
	}
	active2, _ := hb.Active(ctx)
	if _, ok := active2["w1"]; !ok {
		t.Errorf("after clean, w1 should still be active: %v", active2)
	}
}

// TestWorkerHeartbeatRemove Remove 后不再 Active/Expired。
func TestWorkerHeartbeatRemove(t *testing.T) {
	hb, _ := newMiniWorkerHB(t)
	ctx := context.Background()
	if err := hb.Renew(ctx, "w1", time.Now().Add(10*time.Second)); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := hb.Remove(ctx, "w1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	active, _ := hb.Active(ctx)
	if len(active) != 0 {
		t.Errorf("after remove, active = %v, want empty", active)
	}
}

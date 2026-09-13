package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// newMiniTaskHB 启动 miniredis 并返回连它的 TaskHeartbeat。
func newMiniTaskHB(t *testing.T) (*TaskHeartbeat, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewTaskHeartbeat(client), mr
}

// TestTaskHeartbeatAddRenewRemove Add/RenewMany/Expired/RemoveMany 全流程。
func TestTaskHeartbeatAddRenewRemove(t *testing.T) {
	hb, _ := newMiniTaskHB(t)
	ctx := context.Background()
	now := time.Now()

	if err := hb.Add(ctx, "t1", now.Add(10*time.Second)); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	items := []scheduler.TaskHeartbeatItem{
		{UnitID: "t1", Expire: now.Add(20 * time.Second)}, // 续期
		{UnitID: "t2", Expire: now.Add(-1 * time.Second)}, // 已过期
	}
	if err := hb.RenewMany(ctx, items); err != nil {
		t.Fatalf("renew many: %v", err)
	}
	expired, err := hb.Expired(ctx)
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	if len(expired) != 1 || expired[0] != "t2" {
		t.Errorf("expired = %v, want [t2]", expired)
	}
	// RemoveMany（含不存在的 t3，幂等）
	if err := hb.RemoveMany(ctx, []string{"t1", "t2", "t3"}); err != nil {
		t.Fatalf("remove many: %v", err)
	}
	expired2, _ := hb.Expired(ctx)
	if len(expired2) != 0 {
		t.Errorf("after remove, expired = %v, want empty", expired2)
	}
}

// TestTaskHeartbeatRenewManyEmpty 空切片 no-op。
func TestTaskHeartbeatRenewManyEmpty(t *testing.T) {
	hb, _ := newMiniTaskHB(t)
	ctx := context.Background()
	if err := hb.RenewMany(ctx, nil); err != nil {
		t.Errorf("renew many nil: %v", err)
	}
	if err := hb.RemoveMany(ctx, nil); err != nil {
		t.Errorf("remove many nil: %v", err)
	}
}

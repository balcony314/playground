package rpc

import (
	"testing"
	"time"

	"github.com/balcony314/msched/internal/model"
)

// TestRenewExpireCapTimeout 续期 expire = min(now+ttl, 硬截止 timeout)，不超硬截止。
func TestRenewExpireCapTimeout(t *testing.T) {
	s := &workerServiceServer{taskHeartbeatTTL: 5 * time.Second}
	now := time.Now()

	// timeout 在 now+ttl 之前 -> cap 到 timeout（硬截止不可续期）。
	t1 := model.Task{Timeout: now.Add(2 * time.Second)}
	if got := s.renewExpire(t1); !got.Equal(t1.Timeout) {
		t.Errorf("cap to timeout: got %v, want %v", got, t1.Timeout)
	}

	// timeout 在 now+ttl 之后 -> 用 now+ttl（续期，不超硬截止）。
	t2 := model.Task{Timeout: now.Add(100 * time.Second)}
	got2 := s.renewExpire(t2)
	if !got2.Before(t2.Timeout) {
		t.Errorf("no cap: got %v should be before timeout %v", got2, t2.Timeout)
	}
	// 续期点约 now+5s（>now，<timeout）。
	if got2.Before(now) {
		t.Errorf("renew expire %v should be after now %v", got2, now)
	}

	// timeout 零值（理论不会，RUNNING 必设）-> 退化 now+ttl。
	t3 := model.Task{}
	got3 := s.renewExpire(t3)
	if !got3.After(now) {
		t.Errorf("zero timeout: got %v should be after now", got3)
	}
}

package scheduler

import (
	"testing"
	"time"
)

// TestNextBackoff 验证退避序列（DESIGN §8.6）：30s,1m,2m,5m,10m,30m,1h，封顶 1h。
func TestNextBackoff(t *testing.T) {
	want := []time.Duration{
		30 * time.Second,
		time.Minute,
		2 * time.Minute,
		5 * time.Minute,
		10 * time.Minute,
		30 * time.Minute,
		time.Hour,
	}
	for i, w := range want {
		if got := NextBackoff(i); got != w {
			t.Errorf("NextBackoff(%d) = %v, want %v", i, got, w)
		}
	}
	// 封顶：超过序列长度恒为 1h（永不放弃）。
	if got := NextBackoff(len(want) + 5); got != time.Hour {
		t.Errorf("NextBackoff(%d) = %v, want 1h (封顶)", len(want)+5, got)
	}
	// 负值兜底为 0 -> 30s。
	if got := NextBackoff(-1); got != 30*time.Second {
		t.Errorf("NextBackoff(-1) = %v, want 30s", got)
	}
}

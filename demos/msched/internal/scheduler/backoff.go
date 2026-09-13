// backoff.go 无匹配 worker 的退避序列（DESIGN §8.6）。
//
// task 撮合时 selector 无在线 worker 匹配 -> 转 BACKOFF，next_retry_time = now + NextBackoff(failCount)。
// 序列指数 + 抖动封顶 1h，永不放弃（worker 恢复即恢复下发）。

package scheduler

import "time"

// backoffSteps 退避序列（DESIGN §8.6）：30s, 1m, 2m, 5m, 10m, 30m, 1h，封顶 1h。
//
// 索引语义：用 failCount（递增前的当前值）取下标。第 1 次无匹配（failCount=0）
// 退避 30s，第 2 次 1m，依此类推；超过序列长度后恒为 1h。
var backoffSteps = []time.Duration{
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

// NextBackoff 按 failCount 返回退避时长，封顶 1h（DESIGN §8.6，永不放弃）。
// failCount 为递增前的当前值：0 -> 30s，1 -> 1m，...，>=7 -> 1h。
func NextBackoff(failCount int) time.Duration {
	if failCount < 0 {
		failCount = 0
	}
	if failCount >= len(backoffSteps) {
		return backoffSteps[len(backoffSteps)-1]
	}
	return backoffSteps[failCount]
}

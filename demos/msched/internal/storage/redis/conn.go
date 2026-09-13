// Package redis 实现 scheduler.DispatchQueue 的 Redis 版本（派发队列，DESIGN §6/§9.2）。
//
// 详见 docs/REDIS.md。队列按 dispatch:{wuid}:{group} 分桶，{wuid} hash tag 集中同 worker
// 各 group 桶到同 slot，便于 SCAN。非阻塞：空批立即返回。

package redis

import (
	"github.com/balcony314/msched/internal/storage"
	"github.com/redis/go-redis/v9"
)

// NewRedis 建立 Redis 客户端。
func NewRedis(cfg storage.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}

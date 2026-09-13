package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// authCache 缓存 Pull/Heartbeat/Report 鉴权结果，避免热路径每次 GetByUnitID 查 PostgreSQL
// （DESIGN §9.2 Pull 热路径零撮合零 CAS，鉴权 DB 查询会成瓶颈）。
//
// 缓存 key 为 (unitID, token) 的 hash（不存 token 明文），value 为通过/不通过 + 过期时间。
// TTL=authCacheTTL（1s，与 WorkerRegistry 刷新周期对齐）：worker Register 改 token 后
// 最长 1s 内旧 token 仍可能通过（可接受，worker 自己重新注册才改 token）。
// 仅缓存命中通过的结果；不缓存"worker 不存在"等否定结果（避免新注册 worker 延迟可见）。
//
// 实现：sync.Map 存 entry，惰性过期（读时检查，过期当 miss）。写少读多场景无锁竞争。
type authCache struct {
	ttl   time.Duration
	store sync.Map // map[string]*authEntry
}

// authEntry 缓存条目。
type authEntry struct {
	ok     bool
	expiry time.Time
}

const authCacheTTL = time.Second

// newAuthCache 构造鉴权缓存。
func newAuthCache() *authCache {
	return &authCache{ttl: authCacheTTL}
}

// authCacheKey (unitID, token) 的指纹 hash（SHA-256 hex，防 token 明文落内存）。
func authCacheKey(unitID, token string) string {
	h := sha256.New()
	h.Write([]byte(unitID))
	h.Write([]byte{0}) // 分隔，防拼接歧义
	h.Write([]byte(token))
	return hex.EncodeToString(h.Sum(nil))
}

// get 命中且未过期返回 (result, true)；否则 (false, false)。
func (c *authCache) get(unitID, token string) (bool, bool) {
	v, ok := c.store.Load(authCacheKey(unitID, token))
	if !ok {
		return false, false
	}
	e := v.(*authEntry)
	if time.Now().After(e.expiry) {
		c.store.Delete(authCacheKey(unitID, token)) // 惰性清过期
		return false, false
	}
	return e.ok, true
}

// set 写入鉴权结果（仅通过的结果才缓存，见类型注释）。
func (c *authCache) set(unitID, token string, ok bool) {
	if !ok {
		return // 否定结果不缓存（避免新注册 worker 延迟可见）
	}
	c.store.Store(authCacheKey(unitID, token), &authEntry{
		ok:     ok,
		expiry: time.Now().Add(c.ttl),
	})
}

// ctxCacheKey 用于 context 注入 authCache（parseInterceptor 设置，authWorker 读取）。
type ctxCacheKey struct{}

// authCacheFromCtx 从 ctx 取 parseInterceptor 注入的缓存；无则返回 nil（测试/未启用）。
func authCacheFromCtx(ctx context.Context) *authCache {
	v, _ := ctx.Value(ctxCacheKey{}).(*authCache)
	return v
}

// withAuthCache 把缓存注入 ctx（parseInterceptor 用）。
func withAuthCache(ctx context.Context, c *authCache) context.Context {
	return context.WithValue(ctx, ctxCacheKey{}, c)
}

// ring.go 一致性哈希环，软分片决定 group 归属（DESIGN §5）。
//
// 横向扩展模式：N 个 Scheduler 节点对等，按 group_uid hash 顺时针归属环上首个节点，
// 各节点只撮合自己认领的 group，消除多实例对同一活跃 group 的 CAS 竞争（DESIGN §12.8）。
// 加删节点只影响相邻段（虚节点均摊负载）。CAS 兜底保留：分片只为减竞争，N 变更瞬间
// group 可能被多节点认领，靠 §8.2 CAS 天然防双发。
//
// 成员来源（哪些 nodeID 在环上）由存储/协调层供给（节点注册表，待实现），
// 本环只负责归属计算逻辑。

package scheduler

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// Ring 一致性哈希环。线程安全：读（Owner/Owns）并发，写（Add/Remove）互斥。
type Ring struct {
	mu      sync.RWMutex
	vnodes  int
	hashFn  func(string) uint64
	points  []ringPoint // 按 hash 升序
	members map[string]bool
}

type ringPoint struct {
	hash   uint64
	nodeID string
}

// NewRing 构造空环。vnodes 为每节点虚节点数，<=0 取默认 128。
func NewRing(vnodes int) *Ring {
	if vnodes <= 0 {
		vnodes = 128
	}
	return &Ring{
		vnodes:  vnodes,
		hashFn:  shaHash,
		members: map[string]bool{},
	}
}

// Add 加入节点（已存在则跳过），重排虚节点。
func (r *Ring) Add(nodeIDs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range nodeIDs {
		if r.members[id] {
			continue
		}
		r.members[id] = true
		for i := 0; i < r.vnodes; i++ {
			r.points = append(r.points, ringPoint{
				hash:   r.hashFn(fmt.Sprintf("%s#%d", id, i)),
				nodeID: id,
			})
		}
	}
	sort.Slice(r.points, func(i, j int) bool { return r.points[i].hash < r.points[j].hash })
}

// Remove 移除节点（不存在则跳过）。
func (r *Ring) Remove(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.members[nodeID] {
		return
	}
	delete(r.members, nodeID)
	kept := r.points[:0]
	for _, p := range r.points {
		if p.nodeID != nodeID {
			kept = append(kept, p)
		}
	}
	r.points = kept
}

// Owner 返回 key 顺时针归属的节点 ID。空环返回 ""。
func (r *Ring) Owner(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.points) == 0 {
		return ""
	}
	h := r.hashFn(key)
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i].hash >= h })
	if i == len(r.points) {
		i = 0 // 环绕到首节点
	}
	return r.points[i].nodeID
}

// Owns 判断 key 是否归属 myNodeID。
func (r *Ring) Owns(myNodeID, key string) bool {
	return r.Owner(key) == myNodeID
}

// Members 返回当前环上节点 ID 的有序副本（用于快照/调试）。
func (r *Ring) Members() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.members))
	for id := range r.members {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// shaHash 用 SHA-256 取前 8 字节作 64 位哈希。FNV 对短输入（nodeID#i）雪崩不足，
// 导致环上区间分布严重倾斜；SHA-256 雪崩良好，虚节点均摊更均匀。
func shaHash(s string) uint64 {
	h := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(h[:8])
}

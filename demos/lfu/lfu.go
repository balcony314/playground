// Package lfu 实现了 LFU (Least Frequently Used) 缓存淘汰算法。
// 使用双哈希表 + 双向链表实现 O(1) 时间复杂度的 Get/Set 操作。
package lfu

// LFU 缓存的公开接口
type LFU interface {
	// Set 设置缓存键值对，若键已存在则更新值并提升访问频率
	Set(k string, v interface{})
	// Get 获取缓存值，若存在则提升访问频率并返回值和 true，否则返回 nil 和 false
	Get(k string) (v interface{}, ok bool)
}

// listNode 双向链表节点，存储缓存的键值对及访问频率
type listNode struct {
	key        string
	value      interface{}
	prev, next *listNode
	count      int // 访问频率
}

// twoWayList 双向链表，用于维护相同频率的节点
type twoWayList struct {
	head, tail *listNode
	len        int
}

// addToHead 将节点添加到链表头部
func (t *twoWayList) addToHead(cur *listNode) {
	switch {
	case t.len == 0:
		t.head = cur
		t.tail = cur
		t.len = 1
	default:
		t.head.prev = cur
		cur.next = t.head
		t.head = cur
		t.len++
	}
}

// delNode 从链表中删除指定节点
func (t *twoWayList) delNode(cur *listNode) {
	switch {
	case t.len == 0:
	case t.len == 1:
		t.head = nil
		t.tail = nil
		t.len = 0
		cur.next = nil
		cur.prev = nil
	case cur == t.head:
		t.head = t.head.next
		t.head.prev = nil
		t.len--
		cur.next = nil
		cur.prev = nil
	case cur == t.tail:
		t.tail = t.tail.prev
		t.tail.next = nil
		t.len--
		cur.next = nil
		cur.prev = nil
	default:
		cur.next.prev = cur.prev
		cur.prev.next = cur.next
		t.len--
		cur.next = nil
		cur.prev = nil
	}
}

// delTailNode 删除并返回链表尾部节点（最低频中最久未使用的）
func (t *twoWayList) delTailNode() (node *listNode) {
	node = t.tail
	switch {
	case t.len == 0:
	case t.len == 1:
		t.head = nil
		t.tail = nil
		t.len = 0
		node.prev = nil
		node.next = nil
	default:
		t.tail = t.tail.prev
		t.tail.next = nil
		t.len--
		node.prev = nil
		node.next = nil
	}
	return
}

// ifEmpty 判断链表是否为空
func (t *twoWayList) ifEmpty() bool {
	return t.len <= 0
}

// lfuCache LFU 缓存的核心数据结构
type lfuCache struct {
	cap, len int
	hash1    map[string]*listNode    // key -> 节点映射，O(1) 查找
	hash2    map[int]*twoWayList     // 频率 -> 同频节点链表，用于淘汰
	minCount int                     // 当前最小频率，淘汰时直接定位候选链表
}

// New 创建指定容量的 LFU 缓存实例
func New(cap int) LFU {
	if cap < 0 {
		cap = 0
	}

	return &lfuCache{
		cap:      cap,
		len:      0,
		minCount: 0,
		hash1:    make(map[string]*listNode, cap),
		hash2:    make(map[int]*twoWayList, cap),
	}
}

// updateNode 更新节点的访问频率
func (l *lfuCache) updateNode(cur *listNode) {
	// 从当前频率链表中移除
	li, _ := l.hash2[cur.count]
	li.delNode(cur)

	// 若当前频率链表为空，更新最小频率
	if li.ifEmpty() {
		delete(l.hash2, cur.count)
		if l.minCount == cur.count {
			l.minCount++
		}
	}

	// 频率 +1，添加到新频率链表
	cur.count++
	li, ok := l.hash2[cur.count]
	if !ok {
		li = &twoWayList{}
		l.hash2[cur.count] = li
	}
	li.addToHead(cur)
}

// eliminateNode 淘汰最低频中最久未使用的节点
func (l *lfuCache) eliminateNode() {
	li, _ := l.hash2[l.minCount]
	v := li.delTailNode()
	delete(l.hash1, v.key)
	if li.ifEmpty() {
		delete(l.hash2, v.count)
	}
	l.len--
}

// addNewNode 添加新节点到缓存
func (l *lfuCache) addNewNode(cur *listNode) {
	cur.count = 1
	l.hash1[cur.key] = cur
	li, ok := l.hash2[cur.count]
	if !ok {
		li = &twoWayList{}
		l.hash2[cur.count] = li
	}
	li.addToHead(cur)
	l.minCount = cur.count
	l.len++
}

// Set 设置缓存键值对
func (l *lfuCache) Set(k string, v interface{}) {
	if l.cap <= 0 {
		return
	}

	// 键已存在，更新值并提升频率
	if node, ok := l.hash1[k]; ok {
		node.value = v
		l.updateNode(node)
		return
	}

	// 缓存已满，淘汰最不常用的节点
	if l.len == l.cap {
		l.eliminateNode()
	}

	// 添加新节点
	l.addNewNode(&listNode{
		key:   k,
		value: v,
	})
}

// Get 获取缓存值
func (l *lfuCache) Get(k string) (v interface{}, ok bool) {
	if node, ok := l.hash1[k]; ok {
		l.updateNode(node)
		return node.value, true
	}
	return nil, false
}

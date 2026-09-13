package skiplist

import (
	"math/rand"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	// SkipListMaxLevel 跳表最大层数
	SkipListMaxLevel = 32
	// SkipListP 层级晋升概率，每层约 25% 的节点会晋升到更高层
	SkipListP float64 = 0.25
)

// SkipLister 跳表公共接口
type SkipLister interface {
	Set(score uint64, val interface{})
	GetByScore(score uint64) interface{}
	GetByIndex(index int) interface{}
	Len() int
	DelByScore(score uint64)
}

// randomLevel 按几何分布生成随机层级，p=SkipListP
func randomLevel() int {
	level := 1
	for float64(rand.Int63n(0xFFFF)) < (SkipListP * float64(0xFFFF)) {
		level++
	}
	if level < SkipListMaxLevel {
		return level
	}
	return SkipListMaxLevel
}

type skipList struct {
	head, tail *skipListNode
	length     int
	level      int // 当前最大层数
}

type skipListNode struct {
	val     interface{}
	score   uint64          // 排序键
	forward []*skipListNode // 各层的前向指针
}

func createNode(level int, score uint64, val interface{}) *skipListNode {
	return &skipListNode{
		forward: make([]*skipListNode, level, SkipListMaxLevel),
		score:   score,
		val:     val,
	}
}

// Create 创建一个新的跳表实例
func Create() SkipLister {
	return &skipList{
		head:   createNode(SkipListMaxLevel, 0, nil),
		tail:   nil,
		length: 0,
		level:  0,
	}
}

// Set 插入或更新元素，score 相同时覆盖旧值
func (sk *skipList) Set(score uint64, val interface{}) {
	sk.set(score, val)
}

func (sk *skipList) set(score uint64, val interface{}) {
	update := make([]*skipListNode, SkipListMaxLevel, SkipListMaxLevel)
	x := sk.head

	// 从最高层向下搜索，记录每层的前驱节点
	for i := sk.level - 1; i >= 0; i-- {
		for x.forward[i] != nil && x.forward[i].score < score {
			x = x.forward[i]
		}
		update[i] = x
	}

	// score 已存在则原地更新值
	if x.forward[0] != nil && x.forward[0].score == score {
		x.forward[0].val = val
		return
	}

	level := randomLevel()

	// 新节点层数超过当前最大层数时，补充 update 指针
	if level > sk.level {
		for i := sk.level; i < level; i++ {
			update[i] = sk.head
		}
		sk.level = level
	}

	x = createNode(level, score, val)

	// 逐层插入新节点
	for i := 0; i < level; i++ {
		x.forward[i] = update[i].forward[i]
		update[i].forward[i] = x
	}

	sk.length++
}

// DelByScore 按 score 删除元素
func (sk *skipList) DelByScore(score uint64) {
	sk.delByScore(score)
}

func (sk *skipList) delByScore(score uint64) {
	update := make([]*skipListNode, SkipListMaxLevel, SkipListMaxLevel)
	x := sk.head

	for i := sk.level - 1; i >= 0; i-- {
		for x.forward[i] != nil && x.forward[i].score < score {
			x = x.forward[i]
		}
		update[i] = x
	}

	x = x.forward[0]

	if x != nil && score == x.score {
		sk.deleteNode(x, update)
	}
}

func (sk *skipList) deleteNode(x *skipListNode, update []*skipListNode) {
	for i := 0; i < sk.level; i++ {
		if update[i].forward[i] == x {
			update[i].forward[i] = x.forward[i]
		}
	}

	// 缩减空层
	for sk.level > 1 && sk.head.forward[sk.level-1] == nil {
		sk.level--
	}

	sk.length--
}

// GetByScore 按 score 查找，O(log n)
func (sk *skipList) GetByScore(score uint64) interface{} {
	x := sk.head

	for i := sk.level - 1; i >= 0; i-- {
		for x.forward[i] != nil && x.forward[i].score < score {
			x = x.forward[i]
		}
	}

	x = x.forward[0]

	if x != nil && x.score == score {
		return x.val
	}

	return nil
}

// GetByIndex 按排序位置查找（从 0 开始），O(n)
func (sk *skipList) GetByIndex(index int) interface{} {
	idx := 0
	for x := sk.head.forward[0]; x != nil; x = x.forward[0] {
		if idx == index {
			return x.val
		}
		idx++
	}
	return nil
}

// Len 返回跳表元素数量
func (sk *skipList) Len() int {
	return sk.length
}

package rbt

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test_RBT 测试红黑树的基本操作
func Test_RBT(t *testing.T) {
	a := GenRBT()

	// 测试空树的 Get 操作
	v, ok := a.Get(1)
	assert.False(t, ok)
	assert.Nil(t, v)

	// 测试空树的 Print 操作
	l1, l2 := a.Print()
	assert.Nil(t, l1)
	assert.Nil(t, l2)

	// 插入 101 个键值对 (0-100)
	for i := 0; i < 101; i++ {
		a.Set(i, fmt.Sprintf("%d", i))
	}
	l1, l2 = a.Print()
	assert.Equal(t, 101, len(l1))
	assert.Equal(t, 101, len(l2))

	// 测试查找存在的键
	v, ok = a.Get(55)
	assert.True(t, ok)
	assert.Equal(t, "55", v)

	// 测试查找不存在的键
	_, ok = a.Get(155)
	assert.False(t, ok)

	// 验证有序遍历的正确性
	l1, l2 = a.Print()
	assert.Equal(t, 55, l1[55])
	assert.Equal(t, "55", l2[55])

	// 测试删除操作
	a.Del(55)
	_, ok = a.Get(55)
	assert.False(t, ok)

	// 验证删除后有序遍历的正确性
	l1, l2 = a.Print()
	assert.Equal(t, 54, l1[54])
	assert.Equal(t, "54", l2[54])
	assert.Equal(t, 56, l1[55])
	assert.Equal(t, "56", l2[55])

	// 测试重新插入已删除的键
	a.Set(55, "55")
	l1, l2 = a.Print()
	assert.Equal(t, 55, l1[55])
	assert.Equal(t, "55", l2[55])
	assert.Equal(t, 101, len(l1))
	assert.Equal(t, 101, len(l2))

	// 测试更新已有键的值
	a.Set(55, "155")
	v, ok = a.Get(55)
	assert.Equal(t, "155", v)
	assert.True(t, ok)
}

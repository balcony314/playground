package lfu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLFU_1(t *testing.T) {
	cache := New(3)

	cache.Set("a", 1)
	cache.Set("b", 2)
	cache.Set("c", 3)
	cache.Set("d", 4)

	v, ok := cache.Get("a")
	assert.False(t, ok)
	assert.Nil(t, v)

	v, ok = cache.Get("d")
	assert.True(t, ok)
	assert.Equal(t, 4, v)

	v, ok = cache.Get("c")
	assert.True(t, ok)
	assert.Equal(t, 3, v)

	v, ok = cache.Get("b")
	assert.True(t, ok)
	assert.Equal(t, 2, v)

	cache.Get("b")
	cache.Get("b")
	cache.Get("c")
	cache.Get("d")
	cache.Get("d")

	cache.Set("e", 5)

	v, ok = cache.Get("c")
	assert.False(t, ok)
	assert.Nil(t, v)

	v, ok = cache.Get("e")
	assert.True(t, ok)
	assert.Equal(t, 5, v)

	v, ok = cache.Get("b")
	assert.True(t, ok)
	assert.Equal(t, 2, v)

	v, ok = cache.Get("d")
	assert.True(t, ok)
	assert.Equal(t, 4, v)
}

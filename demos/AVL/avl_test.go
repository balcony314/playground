package avl

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_avl(t *testing.T) {

	a := GenAVL()

	v, ok := a.Get(1)
	assert.False(t, ok)
	assert.Nil(t, v)

	l1, l2 := a.Print()
	assert.Nil(t, l1)
	assert.Nil(t, l2)

	for i := 0; i < 101; i++ {
		a.Set(i, fmt.Sprintf("%d", i))
	}
	l1, l2 = a.Print()
	assert.Equal(t, 101, len(l1))
	assert.Equal(t, 101, len(l2))

	v, ok = a.Get(55)
	assert.True(t, ok)
	assert.Equal(t, "55", v)

	_, ok = a.Get(155)
	assert.False(t, ok)

	l1, l2 = a.Print()
	assert.Equal(t, 55, l1[55])
	assert.Equal(t, "55", l2[55])

	a.Del(55)
	_, ok = a.Get(55)
	assert.False(t, ok)

	l1, l2 = a.Print()
	assert.Equal(t, 54, l1[54])
	assert.Equal(t, "54", l2[54])
	assert.Equal(t, 56, l1[55])
	assert.Equal(t, "56", l2[55])

	a.Set(55, "55")
	l1, l2 = a.Print()
	assert.Equal(t, 55, l1[55])
	assert.Equal(t, "55", l2[55])
	assert.Equal(t, 101, len(l1))
	assert.Equal(t, 101, len(l2))

	a.Set(55, "155")
	v, ok = a.Get(55)
	assert.Equal(t, "155", v)
	assert.True(t, ok)
}

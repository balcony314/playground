package model

import "testing"

// TestComputeSelectorHash_MapOrderInvariant 验证 map 遍历顺序不影响 hash：
// 相同 selector 无论 map 插入序如何，产出相同 hash。
func TestComputeSelectorHash_MapOrderInvariant(t *testing.T) {
	// Go map 遍历顺序随机，多次构造相同内容 selector，hash 应一致。
	prev := ComputeSelectorHash(WorkerSelector{"region": "us", "gpu": "v100", "arch": "x86"})
	for i := 0; i < 20; i++ {
		// 重新构造同内容 map（插入顺序可能不同），hash 应不变。
		s := WorkerSelector{"arch": "x86", "region": "us", "gpu": "v100"}
		got := ComputeSelectorHash(s)
		if got != prev {
			t.Fatalf("iter %d: 相同 selector 不同 map 构造产出不同 hash: %s != %s", i, got, prev)
		}
	}
}

// TestComputeSelectorHash_Distinct 验证不同 selector 产出不同 hash。
func TestComputeSelectorHash_Distinct(t *testing.T) {
	base := ComputeSelectorHash(WorkerSelector{"gpu": "v100"})
	tests := []struct {
		name     string
		selector WorkerSelector
	}{
		{"不同值", WorkerSelector{"gpu": "a100"}},
		{"不同 key", WorkerSelector{"cpu": "v100"}},
		{"多一项", WorkerSelector{"gpu": "v100", "region": "us"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeSelectorHash(tt.selector); got == base {
				t.Errorf("%s 应产出不同 hash，got 相同", tt.name)
			}
		})
	}
}

// TestComputeSelectorHash_NoConcatAmbiguity 验证长度前缀消除拼接歧义：
// {a:b, c:d} 不应等于 {a:bc, c:} 等。
func TestComputeSelectorHash_NoConcatAmbiguity(t *testing.T) {
	a := ComputeSelectorHash(WorkerSelector{"a": "b", "c": "d"})
	b := ComputeSelectorHash(WorkerSelector{"a": "bcd", "c": ""})
	if a == b {
		t.Error("拼接歧义：{a:b,c:d} 与 {a:bcd,c:} 不应产生相同 hash")
	}
}

// TestComputeSelectorHash_Empty 验证空 selector 产出稳定 hash（通配维度）。
func TestComputeSelectorHash_Empty(t *testing.T) {
	a := ComputeSelectorHash(WorkerSelector{})
	b := ComputeSelectorHash(nil)
	if a != b {
		t.Errorf("空 map 与 nil selector 应产出相同 hash: %s != %s", a, b)
	}
	if a == "" {
		t.Error("空 selector hash 不应为空字符串")
	}
}

func TestComputeSelectorHash_Length(t *testing.T) {
	h := ComputeSelectorHash(WorkerSelector{"gpu": "v100"})
	if len(h) != 64 {
		t.Errorf("selector hash 长度 = %d, want 64 (SHA-256 hex)", len(h))
	}
}

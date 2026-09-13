package model

import "testing"

func TestComputeUnitID_Deterministic(t *testing.T) {
	a := ComputeUnitID("g1", []byte("args"))
	b := ComputeUnitID("g1", []byte("args"))
	if a != b {
		t.Errorf("相同输入应产生相同 UnitID: %s != %s", a, b)
	}
}

func TestComputeUnitID_Distinct(t *testing.T) {
	base := ComputeUnitID("g1", []byte("args"))
	tests := []struct {
		name  string
		group string
		args  []byte
	}{
		{"不同 group", "g2", []byte("args")},
		{"不同 args", "g1", []byte("args2")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeUnitID(tt.group, tt.args); got == base {
				t.Errorf("%s 应产生不同 UnitID，got 相同: %s", tt.name, got)
			}
		})
	}
}

// TestComputeUnitID_NoConcatAmbiguity 验证长度前缀消除拼接歧义：
// group="a"+args="bc" 不应等于 group="ab"+args="c"。
func TestComputeUnitID_NoConcatAmbiguity(t *testing.T) {
	a := ComputeUnitID("a", []byte("bc"))
	b := ComputeUnitID("ab", []byte("c"))
	if a == b {
		t.Error("拼接歧义：a+bc 与 ab+c 不应产生相同 UnitID")
	}
}

func TestComputeUnitID_Length(t *testing.T) {
	id := ComputeUnitID("g1", []byte("args"))
	if len(id) != 64 {
		t.Errorf("UnitID 长度 = %d, want 64 (SHA-256 hex)", len(id))
	}
}

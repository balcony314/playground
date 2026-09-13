package scheduler

import (
	"testing"
)

// TestRingOwnerStable 验证同一 key 归属稳定（不随 Add 其他 key 变化）。
func TestRingOwnerStable(t *testing.T) {
	r := NewRing(64)
	r.Add("n1", "n2", "n3")
	o := r.Owner("g1")
	// 再 Add 新节点不应改变已有 key 的归属（一致性哈希特性，加节点只迁移相邻段）。
	r.Add("n4")
	if r.Owner("g1") != o {
		t.Errorf("Owner(g1) 在 Add 后变化: %s -> %s", o, r.Owner("g1"))
	}
}

// TestRingEmptyOwner 验证空环 Owner 返回空串。
func TestRingEmptyOwner(t *testing.T) {
	r := NewRing(0)
	if got := r.Owner("g1"); got != "" {
		t.Errorf("空环 Owner = %q, want empty", got)
	}
}

// TestRingOwns 验证 Owns 判定。
func TestRingOwns(t *testing.T) {
	r := NewRing(64)
	r.Add("n1", "n2")
	owner := r.Owner("g1")
	if !r.Owns(owner, "g1") {
		t.Errorf("Owns(%s, g1) = false, want true", owner)
	}
	other := "n1"
	if owner == "n1" {
		other = "n2"
	}
	if r.Owns(other, "g1") {
		t.Errorf("Owns(%s, g1) = true, want false (归属 %s)", other, owner)
	}
}

// TestRingRemove 验证移除节点后归属迁移到其余节点。
func TestRingRemove(t *testing.T) {
	r := NewRing(64)
	r.Add("n1", "n2")
	r.Remove("n1")
	if r.Owner("g1") != "n2" {
		t.Errorf("移除 n1 后 Owner = %q, want n2", r.Owner("g1"))
	}
}

// TestRingMembers 验证成员快照。
func TestRingMembers(t *testing.T) {
	r := NewRing(64)
	r.Add("n2", "n1", "n3")
	got := r.Members()
	want := []string{"n1", "n2", "n3"}
	if len(got) != len(want) {
		t.Fatalf("Members = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Members[%d] = %q, want %q (应有序)", i, got[i], w)
		}
	}
}

// TestRingDistribution 验证虚节点均摊：大量 key 下各节点占比接近 1/N。
func TestRingDistribution(t *testing.T) {
	r := NewRing(128)
	r.Add("n1", "n2", "n3", "n4")
	counts := map[string]int{}
	for i := 0; i < 4000; i++ {
		o := r.Owner(groupName(i))
		counts[o]++
	}
	// 每节点期望 1000，允许 ±40%（虚节点均摊，非严格）。
	for _, n := range r.Members() {
		c := counts[n]
		if c < 600 || c > 1400 {
			t.Errorf("节点 %s 承担 %d key，超出 [600,1400] 均摊区间", n, c)
		}
	}
}

func groupName(i int) string {
	return "g" + string(rune('a'+(i%26))) + string(rune('0'+(i/26)))
}

package entity

import (
	"encoding/json"
	"testing"
)

func TestMicroServiceCopyIsDeep(t *testing.T) {
	svc := &MicroService{Name: "a", ServiceVisibility: []string{"b"}}
	cp := svc.Copy()
	cp.Name = "a2"
	cp.ServiceVisibility[0] = "c"
	if svc.Name != "a" || svc.ServiceVisibility[0] != "b" {
		t.Error("Copy must be deep")
	}
}

func TestSettingsGet(t *testing.T) {
	svc := &MicroService{Settings: Settings{
		{Type: NearestType, Properties: json.RawMessage(`{"enable":true,"fallbackType":"ZONE"}`)},
	}}
	var n Nearest
	if err := svc.Settings.Get(NearestType, &n); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !n.Enable || n.FallBackType != Zone {
		t.Errorf("nearest = %+v", n)
	}
	if err := svc.Settings.Get(LbPolicyType, &n); err == nil {
		t.Error("missing setting must error")
	}
}

func TestInstanceFilterApply(t *testing.T) {
	healthy := true
	list := InstanceList{
		{IP: "1.1.1.1", Region: "r1", Zone: "z1", Healthy: &healthy},
		{IP: "2.2.2.2", Region: "r2", Zone: "z2", Healthy: &healthy},
	}
	got := list.ApplyFilter(&InstanceFilter{Region: "r1"})
	if len(got) != 1 || got[0].IP != "1.1.1.1" {
		t.Errorf("filter result = %v", got)
	}
}

func TestSelectorMatch(t *testing.T) {
	s := Selector{Key: "version", Value: "v1", Operator: SelectorOperatorEqual}
	if !s.Match(map[string]string{"version": "v1"}) || s.Match(map[string]string{"version": "v2"}) {
		t.Error("equal selector broken")
	}
	ne := Selector{Key: "version", Value: "v1", Operator: SelectorOperatorNotEqual}
	if !ne.Match(map[string]string{}) {
		t.Error("not-equal on missing key should match")
	}
}

func TestVisibilityNilMeansAll(t *testing.T) {
	svc := &MicroService{Name: "b", ServiceVisibility: nil}
	_ = svc
	// 语义在 internal/xds/utils 的可见性函数中测试，这里仅保证 nil 可正常序列化
	data, err := json.Marshal(svc)
	if err != nil || string(data) == "" {
		t.Fatalf("marshal: %v", err)
	}
}

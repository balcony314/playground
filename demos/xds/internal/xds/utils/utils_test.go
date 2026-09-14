package utils

import (
	"testing"

	"github.com/balcony314/xds/internal/entity"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	structpb "google.golang.org/protobuf/types/known/structpb"
)

// seedServices 注入 fake 服务列表，替代默认的 service.Storage() 读取路径（见 Step 3 的 SetServiceLister）
func seedServices(list []entity.MicroService) {
	SetServiceLister(func() ([]entity.MicroService, error) { return list, nil })
}

func TestClusterNaming(t *testing.T) {
	if got := GetOutboundTrafficClusterName("svc", 8080, ""); got != "outbound|8080||svc" {
		t.Errorf("outbound cluster name = %q", got)
	}
	if got := GetInboundTrafficClusterName("svc", 8080, "http"); got != "inbound|8080|http|svc" {
		t.Errorf("inbound cluster name = %q", got)
	}
	if got := GetRouteName("svc", 8080); got != "svc:8080" {
		t.Errorf("route name = %q", got)
	}
}

func TestServiceHashID(t *testing.T) {
	h := ServiceHash{}
	if got := h.ID(nil); got != "" {
		t.Errorf("nil node hash = %q", got)
	}
	meta, _ := structpb.NewStruct(map[string]any{"type": "proxy"})
	node := &core.Node{Id: "svc-a", Metadata: meta}
	if got := h.ID(node); got != "svc-a@proxy" {
		t.Errorf("proxy node hash = %q", got)
	}
	node2 := &core.Node{Id: "svc-a"} // 无 type → proxyless
	if got := h.ID(node2); got != "svc-a@proxyless" {
		t.Errorf("proxyless node hash = %q", got)
	}
}

func TestVisibilityWhitelist(t *testing.T) {
	seedServices([]entity.MicroService{
		{Name: "open", ServiceVisibility: nil},                   // 全可见
		{Name: "limited", ServiceVisibility: []string{"svc-a"}}, // 只对 svc-a 可见
		{Name: "closed", ServiceVisibility: []string{"other"}},  // 对 svc-a 不可见
	})
	visible, err := MeCanAccess("svc-a")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range visible {
		names[s.Name] = true
	}
	if !names["open"] || !names["limited"] || names["closed"] {
		t.Errorf("MeCanAccess(svc-a) = %v", names)
	}

	// CanAccessMe 语义见 Step 3：遍历全部服务，名字在我的白名单里的上游可访问我；
	// 我的白名单为空（nil）= 对全部服务开放
	callers, err := CanAccessMe([]string{"open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 1 || callers[0].Name != "open" {
		t.Errorf("CanAccessMe([open]) = %v, want only [open]", callers)
	}

	all, err := CanAccessMe(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("CanAccessMe(nil) = %v, want all 3 services (nil means fully open)", all)
	}
}

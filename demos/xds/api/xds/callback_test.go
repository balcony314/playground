package xds

import (
	"context"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/balcony314/xds/internal/entity"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// cla 构造一个只含单个locality分组的ClusterLoadAssignment测试辅助函数
func cla(name, region, zone string, priority uint32) *endpoint.ClusterLoadAssignment {
	return &endpoint.ClusterLoadAssignment{
		ClusterName: name,
		Endpoints: []*endpoint.LocalityLbEndpoints{
			{Locality: &corev3.Locality{Region: region, Zone: zone}, Priority: priority},
		},
	}
}

func TestSetPriorityByNearest(t *testing.T) {
	// 调用方在 r1/z1；endpoint 分布在三个层级，原始 priority 无意义
	load := cla("outbound|8080||svc", "r1", "z1", 5)
	load.Endpoints = append(load.Endpoints,
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r1", Zone: "z9"}, Priority: 5},
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r2", Zone: "z1"}, Priority: 5},
	)
	setPriorityByNearest(load, "r1", "z1")
	want := []uint32{0, 1, 2}
	for i, ep := range load.Endpoints {
		if ep.Priority != want[i] {
			t.Errorf("endpoint[%d] priority = %d, want %d", i, ep.Priority, want[i])
		}
	}
}

func TestSetPriorityCompress(t *testing.T) {
	// 只有同 region 跨 zone 与跨 region 两层时，priority 应压缩为 0/1（Envoy 要求从 0 连续）
	load := cla("outbound|8080||svc", "r1", "z1", 9)
	load.Endpoints = append(load.Endpoints,
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r2", Zone: "z1"}, Priority: 9},
	)
	setPriorityByNearest(load, "r1", "z1")
	if load.Endpoints[0].Priority != 0 || load.Endpoints[1].Priority != 1 {
		t.Errorf("priorities = %d/%d, want 0/1", load.Endpoints[0].Priority, load.Endpoints[1].Priority)
	}
}

func TestFilterEndpointsByRegion(t *testing.T) {
	load := cla("c", "r1", "z1", 0)
	load.Endpoints = append(load.Endpoints,
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r2", Zone: "z1"}, Priority: 0},
	)
	got := filterEndpointsByRegion(load.Endpoints, "r1")
	if len(got) != 1 || got[0].Locality.Region != "r1" {
		t.Errorf("filterByRegion = %v", got)
	}
	got = filterEndpointsByZone(load.Endpoints, "z1")
	if len(got) != 2 {
		t.Errorf("filterByZone should keep both z1 endpoints, got %d", len(got))
	}
}

func TestGetNearestByClusterName(t *testing.T) {
	// cluster 名第 4 段为服务名；服务未注册时返回零值 Nearest（不 panic）
	got := getNearestByClusterName("outbound|8080||unknown-svc")
	if got.Enable || got.FallBackType != entity.All {
		t.Errorf("unknown service nearest = %+v", got)
	}
}

// withNearest 注入带 nearest 配置的服务元数据读取替身，返回恢复函数。
// 覆盖 getNearestByClusterName 的"按服务配置决定改写规则"路径（生产走全局存储，单测注入）
func withNearest(nearest entity.Nearest) (restore func()) {
	old := serviceInfoReader
	serviceInfoReader = func(string) (*entity.MicroService, error) {
		svc := &entity.MicroService{Name: "svc"}
		setting := entity.MeshSetting{Type: entity.NearestType}
		setting.SetProperties(&nearest)
		svc.Settings = append(svc.Settings, setting)
		return svc, nil
	}
	return func() { serviceInfoReader = old }
}

// edsResponse 把单个 ClusterLoadAssignment 打包成 EDS DiscoveryResponse
func edsResponse(load *endpoint.ClusterLoadAssignment) *discovery.DiscoveryResponse {
	a, err := anypb.New(load)
	if err != nil {
		panic(err)
	}
	return &discovery.DiscoveryResponse{TypeUrl: resource.EndpointType, Resources: []*anypb.Any{a}}
}

// edsRequest 构造带调用方 locality 的 EDS 订阅请求
func edsRequest(region, zone string) *discovery.DiscoveryRequest {
	return &discovery.DiscoveryRequest{
		Node:    &corev3.Node{Id: "caller", Locality: &corev3.Locality{Region: region, Zone: zone}},
		TypeUrl: resource.EndpointType,
	}
}

// unmarshalCLA 从响应中取出第一个 ClusterLoadAssignment(指针传递,proto消息含锁不可按值拷贝)
func unmarshalCLA(t *testing.T, resp *discovery.DiscoveryResponse) *endpoint.ClusterLoadAssignment {
	t.Helper()
	var load endpoint.ClusterLoadAssignment
	if err := anypb.UnmarshalTo(resp.Resources[0], &load, proto.UnmarshalOptions{}); err != nil {
		t.Fatalf("unmarshal CLA: %v", err)
	}
	return &load
}

func TestMutateLocalityNearestAndZoneFallback(t *testing.T) {
	// 开启 nearest + zone 兜底：三个层级先按调用方位置(r1/z1)重排为0/1/2，
	// zone 过滤再裁掉跨zone实例。注意zone过滤只比zone不比region——r2/z1同样保留；
	// 且过滤发生在重排之后不再压缩priority，因此保留的两个为0/2（间隔是既有行为）
	defer withNearest(entity.Nearest{Enable: true, FallBackType: entity.Zone})()

	load := cla("outbound|8080||svc", "r1", "z1", 9)
	load.Endpoints = append(load.Endpoints,
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r1", Zone: "z9"}, Priority: 9},
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r2", Zone: "z1"}, Priority: 9},
	)
	resp := edsResponse(load)

	(&MatrixMeshCallback{}).OnStreamResponse(context.Background(), 1, edsRequest("r1", "z1"), resp)

	got := unmarshalCLA(t, resp)
	if len(got.Endpoints) != 2 {
		t.Fatalf("zone fallback should keep both z1 endpoints (r1/z1 and r2/z1), got %d", len(got.Endpoints))
	}
	for i, want := range []uint32{0, 2} {
		if got.Endpoints[i].Priority != want {
			t.Errorf("endpoint[%d] priority = %d, want %d", i, got.Endpoints[i].Priority, want)
		}
		if got.Endpoints[i].Locality.Zone != "z1" {
			t.Errorf("endpoint[%d] zone = %s, want z1", i, got.Endpoints[i].Locality.Zone)
		}
	}
}

func TestMutateLocalityRegionFallback(t *testing.T) {
	// 开启 nearest + region 兜底：同region的两个endpoint保留(priority 0/1)，跨region的被裁掉
	defer withNearest(entity.Nearest{Enable: true, FallBackType: entity.Region})()

	load := cla("outbound|8080||svc", "r1", "z1", 7)
	load.Endpoints = append(load.Endpoints,
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r1", Zone: "z9"}, Priority: 7},
		&endpoint.LocalityLbEndpoints{Locality: &corev3.Locality{Region: "r2", Zone: "z1"}, Priority: 7},
	)
	resp := edsResponse(load)

	(&MatrixMeshCallback{}).OnStreamResponse(context.Background(), 1, edsRequest("r1", "z1"), resp)

	got := unmarshalCLA(t, resp)
	if len(got.Endpoints) != 2 {
		t.Fatalf("region fallback should keep both r1 endpoints, got %d", len(got.Endpoints))
	}
	for i, want := range []uint32{0, 1} {
		if got.Endpoints[i].Priority != want {
			t.Errorf("endpoint[%d] priority = %d, want %d", i, got.Endpoints[i].Priority, want)
		}
	}
}

func TestMutateLocalityNoop(t *testing.T) {
	// 无 node 信息、或非 EDS 请求时响应必须原样透传（priority 不被改写）
	defer withNearest(entity.Nearest{Enable: true, FallBackType: entity.All})()

	load := cla("outbound|8080||svc", "r1", "z9", 9)

	// 1. 请求无 node
	resp := edsResponse(load)
	(&MatrixMeshCallback{}).OnStreamResponse(context.Background(), 1, &discovery.DiscoveryRequest{TypeUrl: resource.EndpointType}, resp)
	if got := unmarshalCLA(t, resp); got.Endpoints[0].Priority != 9 {
		t.Errorf("no-node request should not mutate, priority = %d, want 9", got.Endpoints[0].Priority)
	}

	// 2. 请求类型为 CDS（非 EDS）
	resp = edsResponse(load)
	req := edsRequest("r1", "z1")
	req.TypeUrl = resource.ClusterType
	(&MatrixMeshCallback{}).OnStreamResponse(context.Background(), 1, req, resp)
	if got := unmarshalCLA(t, resp); got.Endpoints[0].Priority != 9 {
		t.Errorf("non-EDS request should not mutate, priority = %d, want 9", got.Endpoints[0].Priority)
	}
}

func TestGetCurrentRegionAndZoneFallback(t *testing.T) {
	// node 未上报 locality 时降级用进程环境变量兜底（REGION/ZONE）
	t.Setenv("REGION", "env-region")
	t.Setenv("ZONE", "env-zone")
	req := &discovery.DiscoveryRequest{Node: &corev3.Node{Id: "n"}}
	region, zone := getCurrentRegionAndZone(req)
	if region != "env-region" || zone != "env-zone" {
		t.Errorf("env fallback = %s/%s, want env-region/env-zone", region, zone)
	}
}

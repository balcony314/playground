package xds

import (
	"testing"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/internal/service/storage"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
)

// fakeStorage 测试用存储服务：只实现CreateEndpoints依赖的ListInstances，
// 其余方法经嵌入的nil接口保持不可调用（本测试不会触达）
type fakeStorage struct {
	storage.Service
	instances map[string]entity.InstanceList
}

// ListInstances 返回预置的实例列表（忽略onlyAvailable/filter，生成侧会再过滤）
func (f *fakeStorage) ListInstances(serviceName string, _ bool, _ *entity.InstanceFilter) (entity.InstanceList, error) {
	return f.instances[serviceName], nil
}

// withFakeStorage 把全局存储服务替换为fake并注册恢复函数
func withFakeStorage(t *testing.T, instances map[string]entity.InstanceList) {
	t.Helper()
	orig := service.Services.Storage
	service.Services.Storage = &fakeStorage{instances: instances}
	t.Cleanup(func() { service.Services.Storage = orig })
}

// healthyInstance 构造一个健康、未隔离、带region/zone的测试实例
func healthyInstance(ip, region, zone string, port int) entity.Instance {
	return entity.Instance{
		IP:      ip,
		Port:    port,
		Region:  region,
		Zone:    zone,
		Source:  entity.InstanceSourceTypeCustom,
		Labels:  map[string]string{"version": "v1"},
		Healthy: ptr(true),
		Isolate: ptr(false),
	}
}

// 下游服务测试数据：两个http服务，各带一个实例组与两个跨zone实例
func testDownstream() []entity.MicroService {
	return []entity.MicroService{
		{
			Name:     "callee-a",
			Protocol: "http",
			Port:     8081,
			VIP:      "10.0.0.1",
			InstanceGroups: []entity.InstanceGroup{
				{Name: "gray", Selector: entity.InstanceGroupSelectorList{
					{Key: "version", Value: "v2", Operator: entity.SelectorOperatorEqual},
				}},
			},
		},
		{
			Name:     "callee-b",
			Protocol: "http",
			Port:     8082,
			VIP:      "10.0.0.2",
		},
	}
}

// testInstances 每个下游两个实例（同region跨zone），其中callee-a的v2实例命中gray组
func testInstances() map[string]entity.InstanceList {
	return map[string]entity.InstanceList{
		"callee-a": {
			healthyInstance("10.1.1.1", "r1", "z1", 8081),
			healthyInstance("10.1.1.2", "r1", "z2", 8081),
		},
		"callee-b": {
			healthyInstance("10.2.1.1", "r1", "z1", 8082),
			healthyInstance("10.2.1.2", "r2", "z1", 8082),
		},
	}
}

// TestSnapshotGenerationConsistent 端到端验证四类资源生成自洽（proxy模式）：
// 构造一个最小可用的服务与两个下游，生成CDS/RDS/LDS/EDS后用
// Snapshot.Consistent校验名字引用闭环（cluster→route→listener之间的
// 字符串引用必须互相命中）
func TestSnapshotGenerationConsistent(t *testing.T) {
	withFakeStorage(t, testInstances())

	me := &entity.MicroService{
		Name:     "caller",
		Protocol: "http",
		Port:     8080,
		Mode:     entity.ServiceModeProxy,
	}
	downstream := testDownstream()

	eds, err := CreateEndpoints(me, downstream)
	if err != nil {
		t.Fatalf("CreateEndpoints: %v", err)
	}
	cds := CreateCluster(me, downstream)
	rds := CreateRoute(me, downstream)
	lds, err := CreateListeners(me, downstream)
	if err != nil {
		t.Fatalf("CreateListeners: %v", err)
	}

	if len(cds) == 0 || len(rds) == 0 || len(lds) == 0 || len(eds) == 0 {
		t.Fatalf("empty resources: cds=%d rds=%d lds=%d eds=%d", len(cds), len(rds), len(lds), len(eds))
	}

	// 用 go-control-plane 的 Snapshot.Consistent 校验名字引用闭环
	// （cluster→route→listener 之间的字符串引用必须互相命中）
	snap, err := cache.NewSnapshot("v1", map[resource.Type][]types.Resource{
		resource.ClusterType:  cds,
		resource.RouteType:    rds,
		resource.ListenerType: lds,
		resource.EndpointType: eds,
	})
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if err := snap.Consistent(); err != nil {
		t.Fatalf("snapshot inconsistent: %v", err)
	}
}

// TestSnapshotGenerationConsistentProxyless proxyless模式下的同一校验：
// listener集合换为各下游独立ApiListener + 绑定业务端口的virtualInbound，
// 其余资源与proxy模式相同，引用闭环同样必须成立
func TestSnapshotGenerationConsistentProxyless(t *testing.T) {
	withFakeStorage(t, testInstances())

	me := &entity.MicroService{
		Name:     "caller",
		Protocol: "http",
		Port:     8080,
		Mode:     entity.ServiceModeProxyless,
	}
	downstream := testDownstream()

	eds, err := CreateEndpoints(me, downstream)
	if err != nil {
		t.Fatalf("CreateEndpoints: %v", err)
	}
	cds := CreateCluster(me, downstream)
	rds := CreateRoute(me, downstream)
	lds, err := CreateListeners(me, downstream)
	if err != nil {
		t.Fatalf("CreateListeners: %v", err)
	}

	if len(cds) == 0 || len(rds) == 0 || len(lds) == 0 || len(eds) == 0 {
		t.Fatalf("empty resources: cds=%d rds=%d lds=%d eds=%d", len(cds), len(rds), len(lds), len(eds))
	}

	snap, err := cache.NewSnapshot("v1", map[resource.Type][]types.Resource{
		resource.ClusterType:  cds,
		resource.RouteType:    rds,
		resource.ListenerType: lds,
		resource.EndpointType: eds,
	})
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if err := snap.Consistent(); err != nil {
		t.Fatalf("snapshot inconsistent: %v", err)
	}
}

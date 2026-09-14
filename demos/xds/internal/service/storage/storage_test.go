package storage

import (
	"sync"
	"testing"
	"time"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/errs"
)

// fakeRepo 内存实现 StorageRepository，仅覆盖缓存测试所需方法
type fakeRepo struct {
	services map[string]*entity.MicroService
}

func (f *fakeRepo) InitSubscribe(enqueue func(bool, ...string), syncInstance func(string) error, syncServiceInfo func(string) error) error {
	return nil
}
func (f *fakeRepo) RegisterInstance(string, *entity.Instance) error { return nil }
func (f *fakeRepo) DeregisterInstance(string, string, int) error    { return nil }
func (f *fakeRepo) UpdateInstance(string, *entity.Instance) error   { return nil }
func (f *fakeRepo) CleanInstances(string, bool) error               { return nil }
func (f *fakeRepo) ListInstances(string, bool, *entity.InstanceFilter) (entity.InstanceList, error) {
	return nil, nil
}
func (f *fakeRepo) ListInstancesFromWeb(string, bool, *entity.InstanceFilter) (entity.InstanceList, error) {
	return nil, nil
}
func (f *fakeRepo) GetMicroService(name string) (*entity.MicroService, error) {
	svc, ok := f.services[name]
	if !ok {
		return nil, errs.ErrServiceNotFound
	}
	return svc, nil
}
func (f *fakeRepo) DeleteMicroService(string) error            { return nil }
func (f *fakeRepo) SaveMicroService(*entity.MicroService) error { return nil }
func (f *fakeRepo) ListAllMicroServices() ([]entity.MicroService, error) {
	result := make([]entity.MicroService, 0, len(f.services))
	for _, v := range f.services {
		result = append(result, *v)
	}
	return result, nil
}

func newTestService(t *testing.T, services map[string]*entity.MicroService) *service {
	t.Helper()
	s, err := NewService(&fakeRepo{services: services})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func TestGetServiceInfoNotFound(t *testing.T) {
	s := newTestService(t, map[string]*entity.MicroService{})
	if _, err := s.GetServiceInfo("missing"); err == nil {
		t.Error("expect ErrServiceNotFound")
	}
}

func TestSyncServiceInfoCacheDelete(t *testing.T) {
	// brief 原始测试中 fake 始终持有 "gone"，GetMicroService 永远命中，
	// 无法覆盖"repo 已删除→缓存级联移除"分支；此处改为两段式构造该场景
	fake := &fakeRepo{services: map[string]*entity.MicroService{"gone": {Name: "gone"}}}
	s, err := NewService(fake)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// 先将服务同步进缓存
	if err := s.SyncServiceInfoCache("gone"); err != nil {
		t.Fatal(err)
	}
	// 模拟 repo 中已删除（fake 随后返回 NotFound）
	delete(fake.services, "gone")
	// 再次同步：缓存应随之移除
	if err := s.SyncServiceInfoCache("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetServiceInfo("gone"); err == nil {
		t.Error("deleted service must be removed from cache")
	}
}

func TestEnqueueBroadcastSemantics(t *testing.T) {
	s := newTestService(t, map[string]*entity.MicroService{})

	var mu sync.Mutex
	var processed []entity.ServiceEvent
	// Watch 注册回调并启动 worker（与生产路径一致，见 watch.go）
	if err := s.Watch(func(broadcast bool, service string) error {
		mu.Lock()
		defer mu.Unlock()
		processed = append(processed, entity.ServiceEvent{Service: service, Broadcast: broadcast})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer s.queue.ShutDown()

	s.Enqueue(true, "svc-a", "svc-b")

	// 队列异步消费：轮询等待两个事件都处理完（10ms × 200 = 至多 2 秒）
	for i := 0; i < 200; i++ {
		mu.Lock()
		n := len(processed)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(processed) != 2 {
		t.Fatalf("processed = %v, want 2 items", processed)
	}
	for _, e := range processed {
		if !e.Broadcast {
			t.Errorf("event %+v broadcast = false, want true", e)
		}
	}
}

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/service"
	microservice "github.com/balcony314/xds/internal/service/micro_service"
	storagesvc "github.com/balcony314/xds/internal/service/storage"
	"github.com/balcony314/xds/pkg/web"
	"github.com/gin-gonic/gin"
)

// errFake 测试用哨兵错误
var errFake = errors.New("fake error")

// deregisterCall 记录一次实例注销调用的参数
type deregisterCall struct {
	serviceName string
	ip          string
	port        int
}

// cleanCall 记录一次实例批量清理调用的参数
type cleanCall struct {
	serviceName    string
	includeHealthy bool
}

// fakeStorage storagesvc.Service 的测试替身，仅实现handler用到的读写方法，
// 其余方法（Init/Watch等生命周期方法）与handler测试无关，不会被调用
type fakeStorage struct {
	instances    entity.InstanceList
	services     map[string]*entity.MicroService
	registered   []*entity.Instance
	updated      []*entity.Instance
	deregisters  []deregisterCall
	cleans       []cleanCall
	fromWebCalls int
	sdkCalls     int
}

func (f *fakeStorage) RegisterInstance(serviceName string, instance *entity.Instance) error {
	f.registered = append(f.registered, instance)
	return nil
}

func (f *fakeStorage) DeregisterInstance(serviceName, ip string, port int) error {
	f.deregisters = append(f.deregisters, deregisterCall{serviceName, ip, port})
	return nil
}

func (f *fakeStorage) UpdateInstance(serviceName string, instance *entity.Instance) error {
	f.updated = append(f.updated, instance)
	return nil
}

func (f *fakeStorage) CleanInstances(serviceName string, includeHealthy bool) error {
	f.cleans = append(f.cleans, cleanCall{serviceName, includeHealthy})
	return nil
}

func (f *fakeStorage) ListInstances(serviceName string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error) {
	f.sdkCalls++
	return f.instances, nil
}

func (f *fakeStorage) ListInstancesFromWeb(service string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error) {
	f.fromWebCalls++
	return f.instances, nil
}

func (f *fakeStorage) GetServiceInfo(serviceName string) (*entity.MicroService, error) {
	if svc, ok := f.services[serviceName]; ok {
		return svc, nil
	}
	//未配置的服务返回空元数据，模拟缓存命中但无设置项
	return &entity.MicroService{Name: serviceName}, nil
}

func (f *fakeStorage) Init(callback entity.WatchCallback) error   { return nil }
func (f *fakeStorage) Watch(callback entity.WatchCallback) error  { return nil }
func (f *fakeStorage) Enqueue(broadcast bool, services ...string) {}
func (f *fakeStorage) ListAllMicroServices() ([]entity.MicroService, error) {
	return nil, nil
}

// 编译期保证fake实现了storagesvc.Service接口
var _ storagesvc.Service = (*fakeStorage)(nil)

// fakeMicroService microservice.Service 的测试替身，记录调用参数
type fakeMicroService struct {
	synced    []*entity.MicroService
	deleted   []string
	syncErr   error
	deleteErr error
}

func (f *fakeMicroService) SyncMicroService(body *entity.MicroService) error {
	if f.syncErr != nil {
		return f.syncErr
	}
	f.synced = append(f.synced, body)
	return nil
}

func (f *fakeMicroService) DeleteMicroService(serviceName string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, serviceName)
	return nil
}

// 编译期保证fake实现了microservice.Service接口
var _ microservice.Service = (*fakeMicroService)(nil)

// setup 注入fake service并构建业务router；测试结束后还原全局service管理器
func setup(t *testing.T, st *fakeStorage, ms *fakeMicroService) *gin.Engine {
	t.Helper()
	if st == nil {
		st = &fakeStorage{}
	}
	if ms == nil {
		ms = &fakeMicroService{}
	}
	oldStorage, oldMicro := service.Services.Storage, service.Services.MicroService
	service.Services.Storage = st
	service.Services.MicroService = ms
	t.Cleanup(func() {
		service.Services.Storage = oldStorage
		service.Services.MicroService = oldMicro
	})
	return newRouter()
}

// doRequest 以给定方法和body发起JSON请求，返回响应recorder
func doRequest(router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

// decodeResponse 解析统一响应结构
func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) web.Response {
	t.Helper()
	var resp web.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v, body: %s", err, rec.Body.String())
	}
	return resp
}

// assertOK 断言响应为200且业务码为0
func assertOK(t *testing.T, rec *httptest.ResponseRecorder) web.Response {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码200，实际%d，body: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Code != 0 {
		t.Fatalf("期望业务码0，实际%d，message: %s", resp.Code, resp.Message)
	}
	return resp
}

func TestInstanceListFromWeb(t *testing.T) {
	st := &fakeStorage{instances: entity.InstanceList{{IP: "10.0.0.1", Port: 8080}}}
	router := setup(t, st, nil)

	//不传fromSdk时默认走web端视图
	rec := doRequest(router, http.MethodPost, "/api/v1/instances/list", map[string]any{"service": "svc-a"})
	assertOK(t, rec)
	if st.fromWebCalls != 1 || st.sdkCalls != 0 {
		t.Fatalf("期望走web端视图1次，实际web=%d sdk=%d", st.fromWebCalls, st.sdkCalls)
	}
}

func TestInstanceListFromSdk(t *testing.T) {
	st := &fakeStorage{}
	router := setup(t, st, nil)

	rec := doRequest(router, http.MethodPost, "/api/v1/instances/list",
		map[string]any{"service": "svc-a", "fromSdk": true})
	assertOK(t, rec)
	if st.sdkCalls != 1 || st.fromWebCalls != 0 {
		t.Fatalf("期望走sdk视图1次，实际sdk=%d web=%d", st.sdkCalls, st.fromWebCalls)
	}
}

func TestInstanceListBadRequest(t *testing.T) {
	router := setup(t, nil, nil)

	//缺少必填的service字段
	rec := doRequest(router, http.MethodPost, "/api/v1/instances/list", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("期望状态码400，实际%d，body: %s", rec.Code, rec.Body.String())
	}
}

func TestInstanceRegister(t *testing.T) {
	st := &fakeStorage{}
	router := setup(t, st, nil)

	rec := doRequest(router, http.MethodPost, "/api/v1/instances",
		map[string]any{"service": "svc-a", "ip": "10.0.0.1", "port": 8080, "region": "r1", "zone": "z1"})
	assertOK(t, rec)

	if len(st.registered) != 1 {
		t.Fatalf("期望注册1个实例，实际%d", len(st.registered))
	}
	//不传source时默认为custom实例
	if st.registered[0].Source != entity.InstanceSourceTypeCustom {
		t.Fatalf("期望默认source=%s，实际%s", entity.InstanceSourceTypeCustom, st.registered[0].Source)
	}
}

func TestInstanceUpdate(t *testing.T) {
	st := &fakeStorage{}
	router := setup(t, st, nil)

	rec := doRequest(router, http.MethodPut, "/api/v1/instances",
		map[string]any{"service": "svc-a", "ip": "10.0.0.1", "port": 8080, "region": "r1", "zone": "z1"})
	assertOK(t, rec)
	if len(st.updated) != 1 {
		t.Fatalf("期望更新1个实例，实际%d", len(st.updated))
	}
}

func TestInstanceDeregister(t *testing.T) {
	st := &fakeStorage{}
	router := setup(t, st, nil)

	rec := doRequest(router, http.MethodDelete, "/api/v1/instances",
		map[string]any{"service": "svc-a", "ip": "10.0.0.1", "port": 8080})
	assertOK(t, rec)

	if len(st.deregisters) != 1 || st.deregisters[0].serviceName != "svc-a" ||
		st.deregisters[0].ip != "10.0.0.1" || st.deregisters[0].port != 8080 {
		t.Fatalf("注销参数不符合预期: %+v", st.deregisters)
	}
}

func TestInstanceClean(t *testing.T) {
	st := &fakeStorage{}
	router := setup(t, st, nil)

	rec := doRequest(router, http.MethodDelete, "/api/v1/instances/clean",
		map[string]any{"service": "svc-a", "includeHealthy": true})
	assertOK(t, rec)

	if len(st.cleans) != 1 || !st.cleans[0].includeHealthy {
		t.Fatalf("清理参数不符合预期: %+v", st.cleans)
	}
}

func TestMeshSync(t *testing.T) {
	ms := &fakeMicroService{}
	router := setup(t, nil, ms)

	rec := doRequest(router, http.MethodPost, "/api/v1/mesh",
		map[string]any{"name": "svc-a", "protocol": "http", "port": 8080})
	assertOK(t, rec)

	if len(ms.synced) != 1 || ms.synced[0].Name != "svc-a" {
		t.Fatalf("期望同步服务svc-a，实际%+v", ms.synced)
	}
}

func TestMeshSyncError(t *testing.T) {
	ms := &fakeMicroService{syncErr: errFake}
	router := setup(t, nil, ms)

	rec := doRequest(router, http.MethodPost, "/api/v1/mesh", map[string]any{"name": "svc-a"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("期望状态码500，实际%d，body: %s", rec.Code, rec.Body.String())
	}
}

func TestMeshDelete(t *testing.T) {
	ms := &fakeMicroService{}
	router := setup(t, nil, ms)

	rec := doRequest(router, http.MethodDelete, "/api/v1/mesh/svc-a", nil)
	assertOK(t, rec)
	if len(ms.deleted) != 1 || ms.deleted[0] != "svc-a" {
		t.Fatalf("期望删除服务svc-a，实际%+v", ms.deleted)
	}
}

func TestGetTTLDefault(t *testing.T) {
	//服务未配置健康检查设置时应返回默认TTL=10秒
	router := setup(t, &fakeStorage{}, nil)

	rec := doRequest(router, http.MethodGet, "/api/v1/service/svc-a/settings/healthy-check", nil)
	resp := assertOK(t, rec)

	var setting entity.HealthyCheckSetting
	data, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(data, &setting); err != nil {
		t.Fatalf("解析健康检查设置失败: %v", err)
	}
	if setting.TTL != 10 {
		t.Fatalf("期望默认TTL=10，实际%d", setting.TTL)
	}
}

func TestGetTTLConfigured(t *testing.T) {
	//服务已配置健康检查设置时返回配置值
	meshSetting := entity.MeshSetting{Type: entity.HealthyCheckType}
	meshSetting.SetProperties(entity.HealthyCheckSetting{TTL: 30})
	st := &fakeStorage{services: map[string]*entity.MicroService{
		"svc-a": {
			Name:     "svc-a",
			Settings: entity.Settings{meshSetting},
		},
	}}
	router := setup(t, st, nil)

	rec := doRequest(router, http.MethodGet, "/api/v1/service/svc-a/settings/healthy-check", nil)
	resp := assertOK(t, rec)

	var setting entity.HealthyCheckSetting
	data, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(data, &setting); err != nil {
		t.Fatalf("解析健康检查设置失败: %v", err)
	}
	if setting.TTL != 30 {
		t.Fatalf("期望TTL=30，实际%d", setting.TTL)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	router := setup(t, nil, nil)

	rec := doRequest(router, http.MethodGet, "/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码200，实际%d", rec.Code)
	}
}

func TestAdminRoutesNotOnBusinessRouter(t *testing.T) {
	//admin接口只应挂在127.0.0.1的admin server上，业务server不应暴露
	router := setup(t, &fakeStorage{}, nil)

	for _, path := range []string{"/-/ready", "/-/healthy", "/-/dump"} {
		rec := doRequest(router, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("业务server上%s期望404，实际%d", path, rec.Code)
		}
	}
}

func TestAdminServer(t *testing.T) {
	st := &fakeStorage{instances: entity.InstanceList{{IP: "10.0.0.1", Port: 8080}}}
	oldStorage, oldMicro := service.Services.Storage, service.Services.MicroService
	service.Services.Storage = st
	service.Services.MicroService = &fakeMicroService{}
	t.Cleanup(func() {
		service.Services.Storage = oldStorage
		service.Services.MicroService = oldMicro
	})

	srv := newAdminServer()
	handler := srv.Handler

	//探针接口存在即可响应（默认未就绪返回503，状态位由运行时各组件置位）
	rec := doRequest(handler, http.MethodGet, "/-/ready", nil)
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusOK {
		t.Fatalf("/-/ready期望503或200，实际%d", rec.Code)
	}

	//dump接口输出服务快照
	rec = doRequest(handler, http.MethodGet, "/-/dump?service=svc-a", nil)
	resp := assertOK(t, rec)
	data, _ := json.Marshal(resp.Data)
	var dump map[string]json.RawMessage
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatalf("解析dump响应失败: %v", err)
	}
	if _, ok := dump["service"]; !ok {
		t.Fatal("dump响应缺少service字段")
	}
	if _, ok := dump["instances"]; !ok {
		t.Fatal("dump响应缺少instances字段")
	}
}

func TestNewHTTPServer(t *testing.T) {
	s, err := NewHTTPServer()
	if err != nil {
		t.Fatalf("创建HTTPServer失败: %v", err)
	}
	if s.server == nil || s.server.Handler == nil {
		t.Fatal("HTTPServer内部server或Handler未初始化")
	}
}

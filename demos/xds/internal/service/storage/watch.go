package storage

import (
	"fmt"
	"time"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/pkg/logging"
)

const (
	// workers 并发消费队列的worker数量
	workers = 20
	// maxRetries 单个事件处理失败后的最大重试次数，超出后放弃
	maxRetries = 10
)

// runWorkers 启动一组worker消费变更队列。
// 语义对齐k8s wait.Until：worker因panic异常退出后延迟1秒重启；
// worker正常返回（队列已ShutDown）或stopCh关闭时goroutine退出
func (s *service) runWorkers() {
	s.stopCh = make(chan struct{})
	// 事件监听负责将服务装入队列，worker负责从队列提取并处理
	for i := 0; i < workers; i++ {
		go func() {
			for {
				panicked := s.runWorkerOnce()
				if !panicked {
					// worker正常退出即队列已关闭，无需重启
					return
				}
				// panic恢复后等待1秒再重启，期间收到退出信号则终止
				select {
				case <-s.stopCh:
					return
				case <-time.After(time.Second):
				}
			}
		}()
	}
}

// runWorkerOnce 执行一轮worker主循环，返回是否因panic中断
func (s *service) runWorkerOnce() (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			// worker异常退出（panic）后由外层循环自动重启
			logging.Errorf("worker panic: %v", r)
			panicked = true
		}
	}()
	s.worker()
	return false
}

// worker 单个消费者主循环，持续从队列取事件处理，直到队列关闭
func (s *service) worker() {
	for s.processNextWorkItem() {
	}
}

// processNextWorkItem 取出并处理单个事件：调用xDS层注册的watchCallback，
// 回调内部会重新生成配置并下发，处理结果交由handleErr决定重试或放弃
func (s *service) processNextWorkItem() bool {
	event, quit := s.queue.Get()
	if quit {
		return false
	}
	defer s.queue.Done(event)
	t1 := time.Now()
	if s.watchCallback == nil {
		logging.Warnf("watch callback haven't been registered")
	}
	logging.With("service", event.Service, "broadcast", event.Broadcast).Debugf("process item start")
	err := s.watchCallback(event.Broadcast, event.Service)
	s.handleErr(err, event)
	// 注意必须用闭包defer：defer参数会在defer语句处立即求值，直接写time.Since会得到错误的耗时
	defer func() {
		logging.With("service", event.Service, "broadcast", event.Broadcast, "time_taken", time.Since(t1).String()).Debugf("process item end")
	}()
	return true
}

// handleErr 处理错误，对出错的对象等待一定时间后重新入队，超出最大尝试次数后放弃此对象
func (s *service) handleErr(err error, event entity.ServiceEvent) {
	if err == nil {
		// 处理成功，清除该事件的重试计数
		s.queue.Forget(event)
		return
	}

	logging.With("count", s.queue.NumRequeues(event), "service", event.Service).Errorf("handle service error: %+v", err)
	if s.queue.NumRequeues(event) < maxRetries {
		logging.With("count", s.queue.NumRequeues(event), "service", event.Service).Infof("trying requeue service")
		// 失败后按限速策略延迟重新入队（指数退避），等待Nacos等下游收敛后重试
		s.queue.AddRateLimited(event)
		return
	}

	// 重试耗尽仍未成功，放弃该事件并丢弃重试计数，等待下次变更再触发
	logging.With("count", s.queue.NumRequeues(event), "service", event.Service).Warnf("Dropping service %s out of the queue for reaching max error retry", event.Service)
	s.queue.Forget(event)
}

// Watch 注册增量变更回调并启动worker消费队列，只处理订阅建立之后的变动
func (s *service) Watch(callback entity.WatchCallback) error {
	s.watchCallback = callback

	s.runWorkers()
	return nil
}

// Enqueue 将指定服务插入队列，便于后续重新同步
// 由自身变化导致的变动，broadcast=true
// 由其他服务变化导致的被动跟心，broadcast=false
func (s *service) Enqueue(broadcast bool, services ...string) {
	for _, svc := range services {
		s.queue.Add(entity.ServiceEvent{
			Service:   svc,
			Broadcast: broadcast,
		})
		logging.Debugf("enqueue %s, broadcast %v", svc, broadcast)
	}
}

// Init 全量初始化：一次性加载全部服务到本地缓存，并对每个服务执行一次callback建立配置基线；
// 与Watch的区别在于Init是全量驱动，Watch是后续增量的队列消费驱动
func (s *service) Init(callback entity.WatchCallback) error {
	services, err := s.storageRepo.ListAllMicroServices()
	if err != nil {
		return fmt.Errorf("list all micro services: %w", err)
	}

	// 初始化的时候需要全部加载一遍到缓存里
	for _, svc := range services {
		err := s.SyncServiceInfoCache(svc.Name)
		if err != nil {
			logging.Errorf("sync service info cache for service err: %s", err)
		}
		err = s.SyncInstanceCache(svc.Name)
		if err != nil {
			logging.Errorf("sync service instances cache for service err: %s", err)
		}
		logging.Infof("add service %s to cache", svc.Name)
	}

	//TODO: 遍历处理所有服务的初始化工作，后续可以考虑加入并发处理
	if callback == nil {
		return nil
	}
	for _, svc := range services {
		err = callback(false, svc.Name)
		if err != nil {
			return fmt.Errorf("init callback for %s: %w", svc.Name, err)
		}
	}
	return nil
}

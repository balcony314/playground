package proc

import "sync"

// listenerManager 退出监听器管理器：按注册顺序串行调用各监听器，
// 并通过WaitGroup支持等待某个监听器执行完毕
type listenerManager struct {
	lock      sync.Mutex
	waitGroup sync.WaitGroup
	listeners []func()
}

// addListener 注册监听器fn，返回的函数阻塞至fn被调用并执行完毕
func (lm *listenerManager) addListener(fn func()) (waitForCalled func()) {
	lm.waitGroup.Add(1)

	lm.lock.Lock()
	lm.listeners = append(lm.listeners, func() {
		defer lm.waitGroup.Done()
		fn()
	})
	lm.lock.Unlock()

	return func() {
		lm.waitGroup.Wait()
	}
}

// notifyListeners 依次同步调用全部已注册监听器
func (lm *listenerManager) notifyListeners() {
	lm.lock.Lock()
	defer lm.lock.Unlock()

	for _, listener := range lm.listeners {
		listener()
	}
}

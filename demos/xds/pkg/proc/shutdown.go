package proc

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/balcony314/xds/pkg/logging"
)

const timeFormat = "0102150405"

func init() {
	go func() {
		// https://golang.org/pkg/os/signal/#Notify
		signals := make(chan os.Signal, 1)
		// 同时监听SIGINT（Ctrl+C），使开发与裸机运行下也能触发优雅退出，而非硬断连接
		signal.Notify(signals, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGTERM, syscall.SIGINT)

		for {
			v := <-signals
			switch v {
			case syscall.SIGUSR1, syscall.SIGUSR2:
				dumpGoroutines()
			case syscall.SIGTERM, syscall.SIGINT:
				gracefulStop(signals)
			default:
				logging.Error("Got unregistered signal:", v)
			}
		}
	}()
}

const (
	wrapUpTime = time.Second
	// 为什么是5500毫秒：因为大部分队列是阻塞模式且超时为5秒
	waitTime = 5500 * time.Millisecond
)

var (
	wrapUpListeners          = new(listenerManager)
	shutdownListeners        = new(listenerManager)
	delayTimeBeforeForceQuit = waitTime
)

// AddShutdownListener 注册fn为优雅退出监听器，进程收到SIGTERM时会被依次调用。
// 返回的函数可用于等待fn被实际调用（阻塞至该监听器执行完毕）
func AddShutdownListener(fn func()) (waitForCalled func()) {
	return shutdownListeners.addListener(fn)
}

// AddWrapUpListener 注册fn为收尾阶段监听器，先于shutdown监听器被调用。
// 返回的函数可用于等待fn被实际调用
func AddWrapUpListener(fn func()) (waitForCalled func()) {
	return wrapUpListeners.addListener(fn)
}

// SetTimeToForceQuit 设置优雅退出阶段的最长等待时间，超时后强制杀死进程
func SetTimeToForceQuit(duration time.Duration) {
	delayTimeBeforeForceQuit = duration
}

// gracefulStop 优雅退出流程：先通知收尾监听器，等待wrapUpTime后
// 通知退出监听器，再等待delayTimeBeforeForceQuit后强制自杀
func gracefulStop(signals chan os.Signal) {
	signal.Stop(signals)

	logging.Info("Got signal SIGTERM/SIGINT, shutting down...")
	wrapUpListeners.notifyListeners()

	time.Sleep(wrapUpTime)
	shutdownListeners.notifyListeners()

	time.Sleep(delayTimeBeforeForceQuit - wrapUpTime)
	logging.Infof("Still alive after %v, going to force kill the process...", delayTimeBeforeForceQuit)
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
}

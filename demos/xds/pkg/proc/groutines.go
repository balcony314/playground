package proc

import (
	"fmt"
	"os"
	"path"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/balcony314/xds/pkg/logging"
)

const (
	goroutineProfile = "goroutine"
	debugLevel       = 2
)

// dumpGoroutines 将当前全部goroutine堆栈dump到临时目录下的文件，
// 由SIGUSR1/SIGUSR2信号触发，用于排查进程假死、goroutine泄漏等问题
func dumpGoroutines() {
	command := path.Base(os.Args[0])
	pid := syscall.Getpid()
	dumpFile := path.Join(os.TempDir(), fmt.Sprintf("%s-%d-goroutines-%s.dump",
		command, pid, time.Now().Format(timeFormat)))

	logging.Infof("Got dump goroutine signal, printing goroutine profile to %s", dumpFile)

	if f, err := os.Create(dumpFile); err != nil {
		logging.Errorf("Failed to dump goroutine profile, error: %v", err)
	} else {
		defer f.Close()
		pprof.Lookup(goroutineProfile).WriteTo(f, debugLevel)
	}
}

package exec

// ═══════════════════════════════════════════════════════════════
// process.go — 后台进程管理
// ═══════════════════════════════════════════════════════════════
//
// 管理后台执行的进程，支持：
//   - 注册/移除进程
//   - 查询进程状态
//   - 终止进程

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// ManagedProcess 被管理的后台进程
type ManagedProcess struct {
	PID       int           // 进程 ID
	Command   string        // 执行的命令
	StartTime time.Time     // 启动时间
	Done      chan struct{}  // 进程结束时关闭
	Result    *ExecResult   // 进程结束后填充
}

// ProcessManager 后台进程管理器
type ProcessManager struct {
	processes map[int]*ManagedProcess
	mu        sync.RWMutex
}

// NewProcessManager 创建进程管理器
func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		processes: make(map[int]*ManagedProcess),
	}
}

// Add 注册一个后台进程
func (pm *ProcessManager) Add(proc *ManagedProcess) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.processes[proc.PID] = proc
}

// Get 获取指定 PID 的进程信息
func (pm *ProcessManager) Get(pid int) (*ManagedProcess, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	proc, ok := pm.processes[pid]
	return proc, ok
}

// List 列出所有后台进程
func (pm *ProcessManager) List() []*ManagedProcess {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	procs := make([]*ManagedProcess, 0, len(pm.processes))
	for _, proc := range pm.processes {
		procs = append(procs, proc)
	}
	return procs
}

// Kill 终止指定 PID 的进程
func (pm *ProcessManager) Kill(pid int) error {
	pm.mu.Lock()
	proc, ok := pm.processes[pid]
	pm.mu.Unlock()

	if !ok {
		return fmt.Errorf("进程不存在: %d", pid)
	}

	// 查找系统进程
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("查找进程失败: %w", err)
	}

	// 先发送 SIGTERM
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// 进程可能已结束
		pm.Remove(pid)
		return fmt.Errorf("发送 SIGTERM 失败: %w", err)
	}

	// 等待 3 秒，如果还没结束则强制杀死
	go func() {
		select {
		case <-proc.Done:
			return
		case <-time.After(3 * time.Second):
			// 检查进程是否仍在运行
			pm.mu.RLock()
			_, stillExists := pm.processes[pid]
			pm.mu.RUnlock()
			if stillExists {
				process.Signal(syscall.SIGKILL)
			}
		}
	}()

	return nil
}

// Remove 移除已完成的进程记录
func (pm *ProcessManager) Remove(pid int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.processes, pid)
}

// Cleanup 清理已完成的进程
func (pm *ProcessManager) Cleanup() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for pid, proc := range pm.processes {
		select {
		case <-proc.Done:
			delete(pm.processes, pid)
		default:
			continue
		}
	}
}

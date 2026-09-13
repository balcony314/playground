package exec

import (
	"os/exec"
	"testing"
	"time"
)

func TestProcessManager_Add(t *testing.T) {
	pm := NewProcessManager()

	proc := &ManagedProcess{
		PID:       12345,
		Command:   "sleep 10",
		StartTime: time.Now(),
		Done:      make(chan struct{}),
	}

	pm.Add(proc)

	got, ok := pm.Get(12345)
	if !ok {
		t.Fatal("进程未找到")
	}
	if got.PID != 12345 {
		t.Errorf("PID = %d, 期望 12345", got.PID)
	}
	if got.Command != "sleep 10" {
		t.Errorf("Command = %q, 期望 %q", got.Command, "sleep 10")
	}
}

func TestProcessManager_Get_NotFound(t *testing.T) {
	pm := NewProcessManager()

	_, ok := pm.Get(99999)
	if ok {
		t.Error("不存在的进程应返回 false")
	}
}

func TestProcessManager_List(t *testing.T) {
	pm := NewProcessManager()

	// 添加多个进程
	for i := 0; i < 3; i++ {
		pm.Add(&ManagedProcess{
			PID:       i + 1,
			Command:   "test",
			StartTime: time.Now(),
			Done:      make(chan struct{}),
		})
	}

	procs := pm.List()
	if len(procs) != 3 {
		t.Errorf("进程数 = %d, 期望 3", len(procs))
	}
}

func TestProcessManager_Remove(t *testing.T) {
	pm := NewProcessManager()

	proc := &ManagedProcess{
		PID:       12345,
		Command:   "test",
		StartTime: time.Now(),
		Done:      make(chan struct{}),
	}

	pm.Add(proc)
	pm.Remove(12345)

	_, ok := pm.Get(12345)
	if ok {
		t.Error("进程应已被移除")
	}
}

func TestProcessManager_Cleanup(t *testing.T) {
	pm := NewProcessManager()

	// 添加已完成的进程
	doneCh := make(chan struct{})
	close(doneCh)
	pm.Add(&ManagedProcess{
		PID:       1,
		Command:   "completed",
		StartTime: time.Now(),
		Done:      doneCh,
	})

	// 添加未完成的进程
	pm.Add(&ManagedProcess{
		PID:       2,
		Command:   "running",
		StartTime: time.Now(),
		Done:      make(chan struct{}),
	})

	pm.Cleanup()

	// 已完成的进程应被清理
	_, ok1 := pm.Get(1)
	if ok1 {
		t.Error("已完成的进程应被清理")
	}

	// 未完成的进程应保留
	_, ok2 := pm.Get(2)
	if !ok2 {
		t.Error("未完成的进程应保留")
	}
}

func TestProcessManager_Kill(t *testing.T) {
	pm := NewProcessManager()

	// 启动一个真实的 sleep 进程
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动进程失败: %v", err)
	}

	doneCh := make(chan struct{})
	proc := &ManagedProcess{
		PID:       cmd.Process.Pid,
		Command:   "sleep 30",
		StartTime: time.Now(),
		Done:      doneCh,
	}
	pm.Add(proc)

	// 终止进程
	if err := pm.Kill(cmd.Process.Pid); err != nil {
		t.Fatalf("终止进程失败: %v", err)
	}

	// 等待进程结束
	select {
	case <-doneCh:
		// 进程已结束（不会走到这里，因为 doneCh 需要外部关闭）
	case <-time.After(5 * time.Second):
		// 预期超时，因为 doneCh 由测试外管理
	}

	// 验证进程已死亡
	err := cmd.Wait()
	if err == nil {
		t.Error("被杀死的进程应返回错误")
	}
}

func TestProcessManager_Kill_NotFound(t *testing.T) {
	pm := NewProcessManager()

	err := pm.Kill(99999)
	if err == nil {
		t.Error("不存在的进程应返回错误")
	}
}

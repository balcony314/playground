package exec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecutor_Execute_BasicCommand(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	result, err := executor.Execute(context.Background(), "echo hello", "", 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("退出码 = %d, 期望 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("输出 = %q, 期望包含 'hello'", result.Stdout)
	}
}

func TestExecutor_Execute_NonZeroExitCode(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	result, err := executor.Execute(context.Background(), "exit 1", "", 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if result.ExitCode != 1 {
		t.Errorf("退出码 = %d, 期望 1", result.ExitCode)
	}
}

func TestExecutor_Execute_Timeout(t *testing.T) {
	config := ExecConfig{
		Timeout:   1 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	// 使用 while 循环测试超时
	result, err := executor.Execute(context.Background(), "while true; do sleep 0.1; done", "", 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if !result.TimedOut {
		t.Error("应超时")
	}
}

func TestExecutor_Execute_CustomTimeout(t *testing.T) {
	config := ExecConfig{
		Timeout:   10 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	// 使用自定义超时
	result, err := executor.Execute(context.Background(), "while true; do sleep 0.1; done", "", 1*time.Second, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if !result.TimedOut {
		t.Error("应超时")
	}
}

func TestExecutor_Execute_OutputLimit(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 100, // 100 bytes
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	// 生成大输出（使用 yes 命令，避免命令替换）
	result, err := executor.Execute(context.Background(), "yes | head -1000", "", 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if len(result.Stdout) > 200 { // 允许一些余量
		t.Errorf("输出过大: %d bytes", len(result.Stdout))
	}
	if !strings.Contains(result.Stdout, "截断") {
		t.Error("输出应包含截断提示")
	}
}

func TestExecutor_Execute_BlockedCommand(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	_, err := executor.Execute(context.Background(), "rm -rf /", "", 0, false)
	if err == nil {
		t.Error("危险命令应被拒绝")
	}
}

func TestExecutor_Execute_SensitiveCommand(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	// 不确认时返回确认消息
	result, err := executor.Execute(context.Background(), "rm file.txt", "", 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if result.ExitCode != -1 {
		t.Errorf("未确认时应返回 -1: %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "确认") {
		t.Errorf("应包含确认提示: %s", result.Stdout)
	}

	// 确认后执行（文件不存在会失败，但不应被拒绝）
	result, err = executor.Execute(context.Background(), "rm nonexistent", "", 0, true)
	if err != nil {
		t.Fatalf("确认后执行不应被拒绝: %v", err)
	}
}

func TestExecutor_Execute_WorkDir(t *testing.T) {
	tmpDir := t.TempDir()
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   tmpDir,
	}
	executor := NewExecutor(config)

	result, err := executor.Execute(context.Background(), "pwd", "", 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if !strings.Contains(result.Stdout, tmpDir) {
		t.Errorf("工作目录 = %q, 期望包含 %q", result.Stdout, tmpDir)
	}
}

func TestExecutor_Execute_CustomWorkDir(t *testing.T) {
	tmpDir := t.TempDir()
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   "/tmp",
	}
	executor := NewExecutor(config)

	result, err := executor.Execute(context.Background(), "pwd", tmpDir, 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if !strings.Contains(result.Stdout, tmpDir) {
		t.Errorf("工作目录 = %q, 期望包含 %q", result.Stdout, tmpDir)
	}
}

func TestFormatResult_Success(t *testing.T) {
	result := &ExecResult{
		Stdout:   "hello world",
		ExitCode: 0,
		Duration: 100 * time.Millisecond,
	}

	output := FormatResult(result, "echo hello")
	if !strings.Contains(output, "hello world") {
		t.Errorf("应包含输出: %s", output)
	}
}

func TestFormatResult_Failure(t *testing.T) {
	result := &ExecResult{
		Stdout:   "",
		Stderr:   "command not found",
		ExitCode: 127,
		Duration: 10 * time.Millisecond,
	}

	output := FormatResult(result, "invalid_cmd")
	if !strings.Contains(output, "命令未找到") {
		t.Errorf("应包含错误分析: %s", output)
	}
}

func TestFormatResult_Timeout(t *testing.T) {
	result := &ExecResult{
		TimedOut:  true,
		Duration:  30 * time.Second,
		ExitCode:  -1,
	}

	output := FormatResult(result, "sleep 100")
	if !strings.Contains(output, "超时") {
		t.Errorf("应包含超时提示: %s", output)
	}
}

func TestAnalyzeError(t *testing.T) {
	tests := []struct {
		exitCode int
		stderr   string
		want     string
	}{
		{126, "", "权限不足"},
		{127, "", "命令未找到"},
		{137, "", "内存不足"},
		{139, "", "段错误"},
		{143, "", "SIGTERM"},
		{1, "permission denied", "权限不足"},
		{1, "no such file or directory", "不存在"},
		{1, "command not found", "命令未找到"},
		{1, "connection refused", "连接被拒绝"},
		{1, "timeout error", "超时"},
		{1, "unknown error", "执行失败"},
	}

	for _, tt := range tests {
		got := analyzeError(tt.exitCode, tt.stderr)
		if !strings.Contains(got, tt.want) {
			t.Errorf("analyzeError(%d, %q) = %q, 期望包含 %q", tt.exitCode, tt.stderr, got, tt.want)
		}
	}
}

func TestExecutor_GetProcessManager(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	pm := executor.GetProcessManager()
	if pm == nil {
		t.Error("ProcessManager 不应为 nil")
	}
}

func TestExecutor_Close(t *testing.T) {
	tmpDir := t.TempDir()
	config := ExecConfig{
		Timeout:      5 * time.Second,
		MaxOutput:    1024 * 1024,
		WorkDir:      tmpDir,
		AuditLogPath: tmpDir + "/audit.log",
	}
	executor := NewExecutor(config)

	// 执行一个命令生成审计日志
	_, err := executor.Execute(context.Background(), "echo test", "", 0, false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 关闭执行器
	if err := executor.Close(); err != nil {
		t.Errorf("关闭执行器失败: %v", err)
	}
}

func TestExecutor_ExecuteAsync(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	// 后台执行命令
	result, err := executor.ExecuteAsync(context.Background(), "sleep 0.5 && echo done", "", false)
	if err != nil {
		t.Fatalf("后台执行失败: %v", err)
	}

	// 应立即返回 PID
	if result.PID == 0 {
		t.Error("应返回有效的 PID")
	}
	if !strings.Contains(result.Stdout, "后台启动") {
		t.Errorf("应包含后台启动提示: %s", result.Stdout)
	}

	// 等待进程完成
	pm := executor.GetProcessManager()
	proc, ok := pm.Get(result.PID)
	if !ok {
		t.Fatal("进程应已注册")
	}

	select {
	case <-proc.Done:
		// 进程已完成
	case <-time.After(2 * time.Second):
		t.Fatal("等待进程完成超时")
	}

	// 检查结果
	if proc.Result == nil {
		t.Error("进程结果不应为 nil")
	} else if proc.Result.ExitCode != 0 {
		t.Errorf("退出码 = %d, 期望 0", proc.Result.ExitCode)
	}
}

func TestExecutor_ExecuteAsync_BlockedCommand(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	// 危险命令应被拒绝
	_, err := executor.ExecuteAsync(context.Background(), "rm -rf /", "", false)
	if err == nil {
		t.Error("危险命令应被拒绝")
	}
}

func TestExecutor_ExecuteAsync_SensitiveCommand(t *testing.T) {
	config := ExecConfig{
		Timeout:   5 * time.Second,
		MaxOutput: 1024 * 1024,
		WorkDir:   t.TempDir(),
	}
	executor := NewExecutor(config)

	// 不确认时返回确认消息
	result, err := executor.ExecuteAsync(context.Background(), "rm file.txt", "", false)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if result.PID != 0 {
		t.Error("未确认时不应返回 PID")
	}

	// 确认后执行
	result, err = executor.ExecuteAsync(context.Background(), "echo ok", "", true)
	if err != nil {
		t.Fatalf("确认后执行失败: %v", err)
	}
	if result.PID == 0 {
		t.Error("确认后应返回 PID")
	}
}

func TestFormatResult_WithStderr(t *testing.T) {
	result := &ExecResult{
		Stdout:   "output",
		Stderr:   "warning message",
		ExitCode: 0,
		Duration: 100 * time.Millisecond,
	}

	output := FormatResult(result, "echo test")
	if !strings.Contains(output, "警告") {
		t.Errorf("应包含警告标记: %s", output)
	}
	if !strings.Contains(output, "warning message") {
		t.Errorf("应包含 stderr 内容: %s", output)
	}
}

func TestFormatResult_ExitCode139(t *testing.T) {
	result := &ExecResult{
		Stderr:   "segmentation fault",
		ExitCode: 139,
		Duration: 10 * time.Millisecond,
	}

	output := FormatResult(result, "crash")
	if !strings.Contains(output, "段错误") {
		t.Errorf("应包含段错误分析: %s", output)
	}
}

func TestFormatResult_ExitCode143(t *testing.T) {
	result := &ExecResult{
		Stderr:   "terminated",
		ExitCode: 143,
		Duration: 10 * time.Millisecond,
	}

	output := FormatResult(result, "killed")
	if !strings.Contains(output, "SIGTERM") {
		t.Errorf("应包含 SIGTERM 分析: %s", output)
	}
}

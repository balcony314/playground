package exec

// ═══════════════════════════════════════════════════════════════
// executor.go — 命令执行核心
// ═══════════════════════════════════════════════════════════════
//
// 核心功能：
//   - 同步执行命令（带超时）
//   - 后台执行命令
//   - stdout/stderr 分离捕获
//   - 输出大小限制
//   - 进度反馈

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ExecResult 命令执行结果
type ExecResult struct {
	Stdout   string        // 标准输出
	Stderr   string        // 标准错误
	ExitCode int           // 进程退出码
	Duration time.Duration // 执行耗时
	TimedOut bool          // 是否因超时被终止
	PID      int           // 进程 ID（后台执行时使用）
}

// limitedWriter 限制写入大小的 Writer
type limitedWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedWriter) Write(p []byte) (n int, err error) {
	if w.buf.Len()+len(p) > w.limit {
		remaining := w.limit - w.buf.Len()
		if remaining > 0 {
			n, _ = w.buf.Write(p[:remaining])
		}
		w.truncated = true
		return n, nil
	}
	return w.buf.Write(p)
}

func (w *limitedWriter) String() string {
	s := w.buf.String()
	if w.truncated {
		s += "\n... [输出被截断，超过大小限制]"
	}
	return s
}

// Executor 命令执行器
type Executor struct {
	config   ExecConfig
	security *SecurityChecker
	auditor  *AuditLogger
	pm       *ProcessManager
	mu       sync.Mutex
}

// NewExecutor 创建执行器
func NewExecutor(config ExecConfig) *Executor {
	return &Executor{
		config:   config,
		security: NewSecurityChecker(config.WorkDir),
		auditor:  NewAuditLogger(config.AuditLogPath),
		pm:       NewProcessManager(),
	}
}

// GetProcessManager 获取进程管理器
func (e *Executor) GetProcessManager() *ProcessManager {
	return e.pm
}

// Close 关闭执行器，释放资源
func (e *Executor) Close() error {
	if e.auditor != nil {
		return e.auditor.Close()
	}
	return nil
}

// Execute 同步执行命令
func (e *Executor) Execute(ctx context.Context, command string, workDir string, timeout time.Duration, confirm bool) (*ExecResult, error) {
	// 安全检查
	result := e.security.Validate(command)
	if !result.Allowed {
		return nil, fmt.Errorf("命令被拒绝: %s", result.Reason)
	}
	if result.NeedsConfirm && !confirm {
		return &ExecResult{
			Stdout:   result.ConfirmMessage,
			ExitCode: -1,
		}, nil
	}

	// 确定工作目录
	dir := workDir
	if dir == "" {
		dir = e.config.WorkDir
	}

	// 确定超时时间
	if timeout <= 0 {
		timeout = e.config.Timeout
	}

	// 创建带超时的 context
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 创建命令
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdin = nil // 禁止交互式输入

	// 设置输出捕获
	stdoutWriter := &limitedWriter{limit: e.config.MaxOutput}
	stderrWriter := &limitedWriter{limit: e.config.MaxOutput}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	// 记录开始时间
	start := time.Now()

	// 启动进度反馈
	done := make(chan struct{})
	go e.reportProgress(done, start)
	defer close(done)

	// 执行命令
	err := cmd.Run()
	duration := time.Since(start)

	// 构建结果
	execResult := &ExecResult{
		Stdout:   stdoutWriter.String(),
		Stderr:   stderrWriter.String(),
		Duration: duration,
	}

	// 处理退出码和超时
	if ctx.Err() == context.DeadlineExceeded {
		execResult.TimedOut = true
		execResult.ExitCode = -1
		execResult.Stderr += "\n命令执行超时"
	} else if err != nil {
		execResult.ExitCode = extractExitCode(err)
		if execResult.ExitCode == -1 {
			execResult.Stderr += "\n" + err.Error()
		}
	}

	// 记录审计日志
	e.auditLog(command, dir, execResult)

	return execResult, nil
}

// ExecuteAsync 后台执行命令
func (e *Executor) ExecuteAsync(ctx context.Context, command string, workDir string, confirm bool) (*ExecResult, error) {
	// 安全检查
	result := e.security.Validate(command)
	if !result.Allowed {
		return nil, fmt.Errorf("命令被拒绝: %s", result.Reason)
	}
	if result.NeedsConfirm && !confirm {
		return &ExecResult{
			Stdout:   result.ConfirmMessage,
			ExitCode: -1,
		}, nil
	}

	// 确定工作目录
	dir := workDir
	if dir == "" {
		dir = e.config.WorkDir
	}

	// 创建命令
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdin = nil

	// 设置输出捕获
	stdoutWriter := &limitedWriter{limit: e.config.MaxOutput}
	stderrWriter := &limitedWriter{limit: e.config.MaxOutput}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	// 启动命令
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动命令失败: %w", err)
	}

	// 注册到进程管理器
	proc := &ManagedProcess{
		PID:       cmd.Process.Pid,
		Command:   command,
		StartTime: time.Now(),
		Done:      make(chan struct{}),
	}
	e.pm.Add(proc)

	// 后台等待完成
	go func() {
		err := cmd.Wait()
		duration := time.Since(proc.StartTime)

		execResult := &ExecResult{
			Stdout:   stdoutWriter.String(),
			Stderr:   stderrWriter.String(),
			Duration: duration,
			PID:      proc.PID,
		}

		if err != nil {
			execResult.ExitCode = extractExitCode(err)
			if execResult.ExitCode == -1 {
				execResult.Stderr += "\n" + err.Error()
			}
		}

		proc.Result = execResult
		close(proc.Done)

		// 记录审计日志
		e.auditLog(command, dir, execResult)
	}()

	return &ExecResult{
		Stdout: fmt.Sprintf("命令已在后台启动\nPID: %d\n命令: %s", cmd.Process.Pid, command),
		PID:    cmd.Process.Pid,
	}, nil
}

// reportProgress 定期报告执行进度
func (e *Executor) reportProgress(done chan struct{}, start time.Time) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(start).Round(time.Second)
			fmt.Printf("  ⏱️ 命令已执行 %v...\n", elapsed)
		case <-done:
			return
		}
	}
}

// extractExitCode 从 cmd.Run 错误中提取退出码，非 ExitError 返回 -1
func extractExitCode(err error) int {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// auditLog 记录审计日志
func (e *Executor) auditLog(command string, workDir string, result *ExecResult) {
	entry := AuditEntry{
		Timestamp: time.Now(),
		Command:   command,
		WorkDir:   workDir,
		ExitCode:  result.ExitCode,
		Duration:  result.Duration,
		TimedOut:  result.TimedOut,
	}

	if result.ExitCode != 0 {
		entry.Error = result.Stderr
	}

	e.auditor.Log(entry)
}

// FormatResult 格式化执行结果为人类可读的字符串
func FormatResult(result *ExecResult, command string) string {
	var sb strings.Builder

	if result.TimedOut {
		sb.WriteString("⏰ 命令执行超时\n")
		sb.WriteString(fmt.Sprintf("命令: %s\n", command))
		sb.WriteString(fmt.Sprintf("耗时: %v\n", result.Duration.Round(time.Millisecond)))
		return sb.String()
	}

	if result.ExitCode == 0 {
		if result.Stdout != "" {
			sb.WriteString(result.Stdout)
		}
		if result.Stderr != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("⚠️ 警告:\n")
			sb.WriteString(result.Stderr)
		}
	} else {
		sb.WriteString(fmt.Sprintf("❌ 命令执行失败 (退出码: %d)\n", result.ExitCode))
		sb.WriteString(fmt.Sprintf("命令: %s\n", command))

		if result.Stdout != "" {
			sb.WriteString("\n📤 标准输出:\n")
			sb.WriteString(result.Stdout)
		}
		if result.Stderr != "" {
			sb.WriteString("\n📥 错误输出:\n")
			sb.WriteString(result.Stderr)
		}

		// 错误分析
		sb.WriteString("\n💡 " + analyzeError(result.ExitCode, result.Stderr))
	}

	sb.WriteString(fmt.Sprintf("\n⏱️ 耗时: %v", result.Duration.Round(time.Millisecond)))

	return sb.String()
}

// analyzeError 分析错误原因并提供建议
func analyzeError(exitCode int, stderr string) string {
	switch exitCode {
	case 126:
		return "权限不足: 命令无法执行，请检查文件权限"
	case 127:
		return "命令未找到: 请检查命令是否正确安装并在 PATH 中"
	case 137:
		return "进程被杀死: 可能是内存不足 (OOM) 或被 SIGKILL 终止"
	case 139:
		return "段错误: 程序发生内存访问违规"
	case 143:
		return "进程被终止: 收到 SIGTERM 信号"
	}

	lowerStderr := strings.ToLower(stderr)
	switch {
	case strings.Contains(lowerStderr, "permission denied"):
		return "权限不足: 请检查文件或目录的权限设置"
	case strings.Contains(lowerStderr, "no such file or directory"):
		return "文件或目录不存在: 请检查路径是否正确"
	case strings.Contains(lowerStderr, "command not found"):
		return "命令未找到: 请检查命令是否正确安装"
	case strings.Contains(lowerStderr, "connection refused"):
		return "连接被拒绝: 请检查目标服务是否正在运行"
	case strings.Contains(lowerStderr, "timeout"):
		return "操作超时: 请检查网络连接或增加超时时间"
	}

	return fmt.Sprintf("执行失败 (退出码: %d)，请检查命令和参数", exitCode)
}

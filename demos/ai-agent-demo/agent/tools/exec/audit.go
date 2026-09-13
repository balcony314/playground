package exec

// ═══════════════════════════════════════════════════════════════
// audit.go — 审计日志模块
// ═══════════════════════════════════════════════════════════════
//
// 记录所有命令执行的审计日志，用于安全审计和问题追踪。
// 日志格式：JSON Lines（每行一个 JSON 对象）
// 优点：
//   - 人类可读
//   - 机器可解析
//   - 支持 grep 和流式处理
//   - 无需复杂的日志轮转

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// AuditEntry 审计日志条目
type AuditEntry struct {
	Timestamp time.Time     `json:"timestamp"` // 执行时间
	Command   string        `json:"command"`   // 执行的命令
	WorkDir   string        `json:"work_dir"`  // 工作目录
	ExitCode  int           `json:"exit_code"` // 退出码
	Duration  time.Duration `json:"duration"`  // 执行耗时（毫秒）
	TimedOut  bool          `json:"timed_out"` // 是否超时
	Error     string        `json:"error"`     // 错误信息（如有）
}

// AuditLogger 审计日志记录器
type AuditLogger struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// NewAuditLogger 创建审计日志记录器
// path 为日志文件路径，空字符串表示不记录
func NewAuditLogger(path string) *AuditLogger {
	if path == "" {
		return nil
	}
	return &AuditLogger{
		path: path,
	}
}

// Log 记录一条审计日志
func (l *AuditLogger) Log(entry AuditEntry) error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// 懒打开文件
	if l.file == nil {
		file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		l.file = file
	}

	// 序列化为 JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	// 写入一行
	data = append(data, '\n')
	_, err = l.file.Write(data)
	return err
}

// Close 关闭日志文件
func (l *AuditLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	err := l.file.Close()
	l.file = nil
	return err
}

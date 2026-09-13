package exec

// ═══════════════════════════════════════════════════════════════
// config.go — 执行配置管理
// ═══════════════════════════════════════════════════════════════
//
// 通过环境变量配置命令执行的各项参数，支持默认值。
// 配置项：
//   - EXEC_TIMEOUT: 命令超时时间（秒），默认 30
//   - EXEC_MAX_OUTPUT: 最大输出字节数，默认 1MB
//   - EXEC_AUDIT_LOG: 审计日志文件路径，默认为空（不记录）

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// DefaultTimeout 默认命令超时时间
	DefaultTimeout = 30 * time.Second

	// DefaultMaxOutput 默认最大输出字节数 (1MB)
	DefaultMaxOutput = 1024 * 1024

	// MinTimeout 最小超时时间
	MinTimeout = 1 * time.Second

	// MaxTimeout 最大超时时间 (10 分钟)
	MaxTimeout = 10 * time.Minute

	// MinMaxOutput 最小输出限制
	MinMaxOutput = 1024 // 1KB

	// MaxMaxOutput 最大输出限制
	MaxMaxOutput = 10 * 1024 * 1024 // 10MB
)

// ExecConfig 命令执行配置
type ExecConfig struct {
	Timeout      time.Duration // 命令超时时间
	MaxOutput    int           // 最大输出字节数
	WorkDir      string        // 默认工作目录
	AuditLogPath string        // 审计日志文件路径
}

// LoadConfig 从环境变量加载配置
func LoadConfig() ExecConfig {
	config := ExecConfig{
		Timeout:   DefaultTimeout,
		MaxOutput: DefaultMaxOutput,
	}

	if v := os.Getenv("EXEC_TIMEOUT"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil {
			config.Timeout = clampDuration(time.Duration(seconds)*time.Second, MinTimeout, MaxTimeout)
		}
	}

	if v := os.Getenv("EXEC_MAX_OUTPUT"); v != "" {
		if bytes, err := strconv.Atoi(v); err == nil {
			config.MaxOutput = clampInt(bytes, MinMaxOutput, MaxMaxOutput)
		}
	}

	if logPath := os.Getenv("EXEC_AUDIT_LOG"); logPath != "" {
		config.AuditLogPath = filepath.Clean(logPath)
	}

	return config
}

// clampDuration 将 duration 钳位到 [min, max] 范围
func clampDuration(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

// clampInt 将值钳位到 [min, max] 范围
func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// WithWorkDir 返回带工作目录的配置副本
func (c ExecConfig) WithWorkDir(dir string) ExecConfig {
	c.WorkDir = dir
	return c
}

// WithTimeout 返回带超时的配置副本
func (c ExecConfig) WithTimeout(timeout time.Duration) ExecConfig {
	c.Timeout = clampDuration(timeout, MinTimeout, MaxTimeout)
	return c
}

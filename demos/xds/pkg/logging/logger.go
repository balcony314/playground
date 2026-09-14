// Package logging 提供基于 log/slog 的结构化日志门面，
// 调用形式与原项目 pkg/logger 保持一致（With 链式字段 + Infof 等格式化方法）
package logging

import "os"

// Logger 日志接口。方法集刻意与原 pkg/logger 一致以降低迁移成本；
// 其中 Debugf/Infof/Warnf/Errorf 同时满足 go-control-plane 的 log.Logger 约束
type Logger interface {
	With(kvs ...any) Logger
	Info(args ...any)
	Warn(args ...any)
	Error(args ...any)
	Debug(args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Debugf(format string, args ...any)
	Fatalf(format string, args ...any)
}

var std Logger = newSlogLogger(os.Stdout, "info", "console")

// SetLogger 替换全局日志实现（测试注入用）
func SetLogger(l Logger) { std = l }

// GetLogger 返回全局日志实现
func GetLogger() Logger { return std }

func With(kvs ...any) Logger    { return std.With(kvs...) }
func Info(args ...any)          { std.Info(args...) }
func Warn(args ...any)          { std.Warn(args...) }
func Error(args ...any)         { std.Error(args...) }
func Debug(args ...any)         { std.Debug(args...) }
func Infof(f string, a ...any)  { std.Infof(f, a...) }
func Warnf(f string, a ...any)  { std.Warnf(f, a...) }
func Errorf(f string, a ...any) { std.Errorf(f, a...) }
func Debugf(f string, a ...any) { std.Debugf(f, a...) }
func Fatalf(f string, a ...any) { std.Fatalf(f, a...) }

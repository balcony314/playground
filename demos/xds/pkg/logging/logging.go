package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Init 按配置初始化全局日志。dir 为空时输出到 stdout
func Init(level, format, dir string, maxSizeMB, maxBackups, maxAgeDays int) {
	var w io.Writer = os.Stdout
	if dir != "" {
		// 生产环境写入文件并按大小轮转
		w = &lumberjack.Logger{
			Filename:   dir + "/xds-server.log",
			MaxSize:    maxSizeMB,
			MaxBackups: maxBackups,
			MaxAge:     maxAgeDays,
			Compress:   true,
		}
	}
	SetLogger(newSlogLogger(w, level, format))
}

// newSlogLogger 构造 slog 实现的 Logger
func newSlogLogger(w io.Writer, level, format string) Logger {
	lv := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return &slogLogger{l: slog.New(h)}
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type slogLogger struct{ l *slog.Logger }

func (s *slogLogger) With(kvs ...any) Logger    { return &slogLogger{l: s.l.With(kvs...)} }
func (s *slogLogger) Info(args ...any)          { s.l.Info(fmt.Sprint(args...)) }
func (s *slogLogger) Warn(args ...any)          { s.l.Warn(fmt.Sprint(args...)) }
func (s *slogLogger) Error(args ...any)         { s.l.Error(fmt.Sprint(args...)) }
func (s *slogLogger) Debug(args ...any)         { s.l.Debug(fmt.Sprint(args...)) }
func (s *slogLogger) Infof(f string, a ...any)  { s.l.Info(fmt.Sprintf(f, a...)) }
func (s *slogLogger) Warnf(f string, a ...any)  { s.l.Warn(fmt.Sprintf(f, a...)) }
func (s *slogLogger) Errorf(f string, a ...any) { s.l.Error(fmt.Sprintf(f, a...)) }
func (s *slogLogger) Debugf(f string, a ...any) { s.l.Debug(fmt.Sprintf(f, a...)) }
func (s *slogLogger) Fatalf(f string, a ...any) {
	s.l.Error(fmt.Sprintf(f, a...))
	os.Exit(1)
}

package exec

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAuditLogger_Nil(t *testing.T) {
	// nil logger 不应 panic
	var logger *AuditLogger
	err := logger.Log(AuditEntry{
		Timestamp: time.Now(),
		Command:   "test",
	})
	if err != nil {
		t.Errorf("nil logger 应返回 nil error: %v", err)
	}
}

func TestAuditLogger_EmptyPath(t *testing.T) {
	logger := NewAuditLogger("")
	if logger != nil {
		t.Error("空路径应返回 nil logger")
	}
}

func TestAuditLogger_Log(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger := NewAuditLogger(logPath)
	defer logger.Close()

	entry := AuditEntry{
		Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		Command:   "echo hello",
		WorkDir:   "/tmp",
		ExitCode:  0,
		Duration:  100 * time.Millisecond,
		TimedOut:  false,
		Error:     "",
	}

	if err := logger.Log(entry); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}

	// 读取并验证
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("打开日志文件失败: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("日志文件为空")
	}

	var got AuditEntry
	if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
		t.Fatalf("解析日志失败: %v", err)
	}

	if got.Command != entry.Command {
		t.Errorf("命令 = %q, 期望 %q", got.Command, entry.Command)
	}
	if got.ExitCode != entry.ExitCode {
		t.Errorf("退出码 = %d, 期望 %d", got.ExitCode, entry.ExitCode)
	}
	if got.TimedOut != entry.TimedOut {
		t.Errorf("超时 = %v, 期望 %v", got.TimedOut, entry.TimedOut)
	}
}

func TestAuditLogger_MultipleEntries(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger := NewAuditLogger(logPath)
	defer logger.Close()

	entries := []AuditEntry{
		{Timestamp: time.Now(), Command: "ls", ExitCode: 0},
		{Timestamp: time.Now(), Command: "cat file.txt", ExitCode: 1, Error: "file not found"},
		{Timestamp: time.Now(), Command: "echo test", ExitCode: 0},
	}

	for _, entry := range entries {
		if err := logger.Log(entry); err != nil {
			t.Fatalf("写入日志失败: %v", err)
		}
	}

	// 读取并验证行数
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("打开日志文件失败: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}

	if count != len(entries) {
		t.Errorf("日志行数 = %d, 期望 %d", count, len(entries))
	}
}

func TestAuditLogger_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger := NewAuditLogger(logPath)
	defer logger.Close()

	var wg sync.WaitGroup
	goroutines := 10
	entriesPerGoroutine := 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < entriesPerGoroutine; j++ {
				entry := AuditEntry{
					Timestamp: time.Now(),
					Command:   "test",
					ExitCode:  0,
				}
				logger.Log(entry)
			}
		}()
	}

	wg.Wait()

	// 验证总行数
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("打开日志文件失败: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}

	expected := goroutines * entriesPerGoroutine
	if count != expected {
		t.Errorf("日志行数 = %d, 期望 %d", count, expected)
	}
}

func TestAuditLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger := NewAuditLogger(logPath)

	// 写入一条日志
	if err := logger.Log(AuditEntry{Command: "test"}); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}

	// 关闭
	if err := logger.Close(); err != nil {
		t.Fatalf("关闭日志失败: %v", err)
	}

	// 再次关闭不应 panic
	if err := logger.Close(); err != nil {
		t.Errorf("重复关闭应返回 nil: %v", err)
	}
}

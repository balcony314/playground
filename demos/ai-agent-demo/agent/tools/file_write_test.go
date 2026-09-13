package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── write_file 测试 ─────────────────────────────────────────

func TestWriteFileTool_Success(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("write_file")

	result, err := tool.Execute(json.RawMessage(`{"path":"output.txt","content":"Hello World"}`))
	if err != nil {
		t.Fatalf("write_file 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}

	// 验证文件已创建
	content, err := os.ReadFile(filepath.Join(workDir, "output.txt"))
	if err != nil {
		t.Fatalf("读取写入的文件失败: %v", err)
	}
	if string(content) != "Hello World" {
		t.Errorf("文件内容 = %q, 期望 %q", string(content), "Hello World")
	}
}

func TestWriteFileTool_CreateDirs(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("write_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"subdir/nested/output.txt","content":"Hello"}`))
	if err != nil {
		t.Fatalf("write_file 执行失败: %v", err)
	}

	// 验证文件已创建
	if _, err := os.Stat(filepath.Join(workDir, "subdir", "nested", "output.txt")); err != nil {
		t.Fatalf("文件未创建: %v", err)
	}
}

func TestWriteFileTool_NonTextFile(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("write_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"binary.bin","content":"data"}`))
	if err == nil {
		t.Error("写入非文本文件应返回错误")
	}
}

// ─── edit_file 测试 ──────────────────────────────────────────

func TestEditFileTool_Replace(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Hello World\nHello Go"), 0644); err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	result, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"replace","find":"Hello","replace_with":"Hi"}`))
	if err != nil {
		t.Fatalf("edit_file 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}

	// 验证文件内容
	content, _ := os.ReadFile(testFile)
	expected := "Hi World\nHi Go"
	if string(content) != expected {
		t.Errorf("文件内容 = %q, 期望 %q", string(content), expected)
	}
}

func TestEditFileTool_Append(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件（以换行符结尾）
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Hello\n"), 0644); err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"append","content":"World"}`))
	if err != nil {
		t.Fatalf("edit_file 执行失败: %v", err)
	}

	// 验证文件内容
	content, _ := os.ReadFile(testFile)
	expected := "Hello\nWorld"
	if string(content) != expected {
		t.Errorf("文件内容 = %q, 期望 %q", string(content), expected)
	}
}

func TestEditFileTool_Insert(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Line 1\nLine 3"), 0644); err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"insert","line":2,"content":"Line 2"}`))
	if err != nil {
		t.Fatalf("edit_file 执行失败: %v", err)
	}

	// 验证文件内容
	content, _ := os.ReadFile(testFile)
	expected := "Line 1\nLine 2\nLine 3"
	if string(content) != expected {
		t.Errorf("文件内容 = %q, 期望 %q", string(content), expected)
	}
}

func TestEditFileTool_Delete(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Line 1\nLine 2\nLine 3"), 0644); err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"delete","line":2}`))
	if err != nil {
		t.Fatalf("edit_file 执行失败: %v", err)
	}

	// 验证文件内容
	content, _ := os.ReadFile(testFile)
	expected := "Line 1\nLine 3"
	if string(content) != expected {
		t.Errorf("文件内容 = %q, 期望 %q", string(content), expected)
	}
}

func TestEditFileTool_NotFound(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"nonexistent.txt","action":"replace","find":"a","replace_with":"b"}`))
	if err == nil {
		t.Error("编辑不存在的文件应返回错误")
	}
}

func TestEditFileTool_InvalidLineInsert(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	testFile := filepath.Join(workDir, "test.txt")
	os.WriteFile(testFile, []byte("Line 1"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"insert","line":10,"content":"test"}`))
	if err == nil {
		t.Error("行号超出范围应返回错误")
	}
}

func TestEditFileTool_InvalidLineDelete(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	testFile := filepath.Join(workDir, "test.txt")
	os.WriteFile(testFile, []byte("Line 1"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"delete","line":0}`))
	if err == nil {
		t.Error("行号小于 1 应返回错误")
	}
}

func TestEditFileTool_MissingParams(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	testFile := filepath.Join(workDir, "test.txt")
	os.WriteFile(testFile, []byte("content"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	// replace 缺少 find 参数
	_, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"replace"}`))
	if err == nil {
		t.Error("replace 操作缺少 find 参数应返回错误")
	}

	// append 缺少 content 参数
	_, err = tool.Execute(json.RawMessage(`{"path":"test.txt","action":"append"}`))
	if err == nil {
		t.Error("append 操作缺少 content 参数应返回错误")
	}

	// insert 缺少 content 参数
	_, err = tool.Execute(json.RawMessage(`{"path":"test.txt","action":"insert","line":1}`))
	if err == nil {
		t.Error("insert 操作缺少 content 参数应返回错误")
	}

	// 未知操作
	_, err = tool.Execute(json.RawMessage(`{"path":"test.txt","action":"unknown"}`))
	if err == nil {
		t.Error("未知操作应返回错误")
	}
}

func TestEditFileTool_DeleteRange(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	testFile := filepath.Join(workDir, "test.txt")
	os.WriteFile(testFile, []byte("Line 1\nLine 2\nLine 3\nLine 4"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"test.txt","action":"delete","line":2,"end_line":3}`))
	if err != nil {
		t.Fatalf("edit_file 执行失败: %v", err)
	}

	content, _ := os.ReadFile(testFile)
	expected := "Line 1\nLine 4"
	if string(content) != expected {
		t.Errorf("文件内容 = %q, 期望 %q", string(content), expected)
	}
}

// ─── delete_file 测试 ────────────────────────────────────────

func TestDeleteFileTool_File(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.txt")
	os.WriteFile(testFile, []byte("test"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("delete_file")

	result, err := tool.Execute(json.RawMessage(`{"path":"test.txt"}`))
	if err != nil {
		t.Fatalf("delete_file 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}

	// 验证文件已删除
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("文件应已被删除")
	}
}

func TestDeleteFileTool_Directory(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试目录
	testDir := filepath.Join(workDir, "testdir")
	os.MkdirAll(testDir, 0755)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("delete_file")

	// 尝试删除非空目录（不设置 recursive）
	os.WriteFile(filepath.Join(testDir, "file.txt"), []byte("test"), 0644)
	_, err := tool.Execute(json.RawMessage(`{"path":"testdir"}`))
	if err == nil {
		t.Error("删除非空目录应返回错误（未设置 recursive）")
	}

	// 设置 recursive 删除
	result, err := tool.Execute(json.RawMessage(`{"path":"testdir","recursive":true}`))
	if err != nil {
		t.Fatalf("delete_file 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestDeleteFileTool_EmptyDirectory(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建空目录
	testDir := filepath.Join(workDir, "emptydir")
	os.MkdirAll(testDir, 0755)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("delete_file")

	result, err := tool.Execute(json.RawMessage(`{"path":"emptydir"}`))
	if err != nil {
		t.Fatalf("delete_file 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestDeleteFileTool_NotExists(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("delete_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"nonexistent.txt"}`))
	if err == nil {
		t.Error("删除不存在的文件应返回错误")
	}
}

func TestWriteFileTool_InvalidJSON(t *testing.T) {
	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("write_file")

	_, err := tool.Execute(json.RawMessage(`{invalid`))
	if err == nil {
		t.Error("无效 JSON 应返回错误")
	}
}

func TestWriteFileTool_EmptyPath(t *testing.T) {
	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("write_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"","content":"test"}`))
	if err == nil {
		t.Error("空路径应返回错误")
	}
}

func TestDeleteFileTool_InvalidJSON(t *testing.T) {
	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("delete_file")

	_, err := tool.Execute(json.RawMessage(`{invalid`))
	if err == nil {
		t.Error("无效 JSON 应返回错误")
	}
}

func TestDeleteFileTool_EmptyPath(t *testing.T) {
	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("delete_file")

	_, err := tool.Execute(json.RawMessage(`{"path":""}`))
	if err == nil {
		t.Error("空路径应返回错误")
	}
}

func TestEditFileTool_InvalidJSON(t *testing.T) {
	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{invalid`))
	if err == nil {
		t.Error("无效 JSON 应返回错误")
	}
}

func TestEditFileTool_EmptyPath(t *testing.T) {
	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("edit_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"","action":"replace"}`))
	if err == nil {
		t.Error("空路径应返回错误")
	}
}

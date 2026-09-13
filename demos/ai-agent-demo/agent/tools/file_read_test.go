package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── read_file 测试 ──────────────────────────────────────────

func TestReadFileTool_Success(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.txt")
	content := "Hello\nWorld\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("read_file")

	result, err := tool.Execute(json.RawMessage(`{"path":"test.txt"}`))
	if err != nil {
		t.Fatalf("read_file 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestReadFileTool_NotFound(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("read_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"nonexistent.txt"}`))
	if err == nil {
		t.Error("读取不存在的文件应返回错误")
	}
}

func TestReadFileTool_IsDirectory(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("read_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"."}`))
	if err == nil {
		t.Error("读取目录应返回错误")
	}
}

func TestReadFileTool_NonTextFile(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建二进制文件
	testFile := filepath.Join(workDir, "binary.bin")
	os.WriteFile(testFile, []byte{0x00, 0x01, 0x02}, 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("read_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"binary.bin"}`))
	if err == nil {
		t.Error("读取非文本文件应返回错误")
	}
}

func TestReadFileTool_FileTooLarge(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建大文件（超过 1MB）
	testFile := filepath.Join(workDir, "large.txt")
	data := make([]byte, 1024*1024+1)
	os.WriteFile(testFile, data, 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("read_file")

	_, err := tool.Execute(json.RawMessage(`{"path":"large.txt"}`))
	if err == nil {
		t.Error("读取超大文件应返回错误")
	}
}

// ─── list_dir 测试 ───────────────────────────────────────────

func TestListDirTool_Success(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件和目录
	os.MkdirAll(filepath.Join(workDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(workDir, "test.txt"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("list_dir")

	result, err := tool.Execute(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list_dir 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestListDirTool_WithPattern(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	os.WriteFile(filepath.Join(workDir, "test.txt"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("list_dir")

	result, err := tool.Execute(json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("list_dir 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestListDirTool_NotExists(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("list_dir")

	_, err := tool.Execute(json.RawMessage(`{"path":"nonexistent"}`))
	if err == nil {
		t.Error("列出不存在的目录应返回错误")
	}
}

func TestListDirTool_NotDirectory(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建文件（不是目录）
	os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("test"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("list_dir")

	_, err := tool.Execute(json.RawMessage(`{"path":"file.txt"}`))
	if err == nil {
		t.Error("对文件调用 list_dir 应返回错误")
	}
}

// ─── file_info 测试 ──────────────────────────────────────────

func TestFileInfoTool_Success(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.txt")
	os.WriteFile(testFile, []byte("Hello World"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("file_info")

	result, err := tool.Execute(json.RawMessage(`{"path":"test.txt"}`))
	if err != nil {
		t.Fatalf("file_info 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestFileInfoTool_Directory(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("file_info")

	result, err := tool.Execute(json.RawMessage(`{"path":"."}`))
	if err != nil {
		t.Fatalf("file_info 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestFileInfoTool_NotExists(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("file_info")

	_, err := tool.Execute(json.RawMessage(`{"path":"nonexistent.txt"}`))
	if err == nil {
		t.Error("获取不存在文件的信息应返回错误")
	}
}

// ─── search_files 测试 ───────────────────────────────────────

func TestSearchFilesTool_Success(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(workDir, "test.go"), []byte("package test"), 0644)
	os.WriteFile(filepath.Join(workDir, "readme.md"), []byte("# README"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_files")

	result, err := tool.Execute(json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("search_files 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestSearchFilesTool_NotExists(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_files")

	_, err := tool.Execute(json.RawMessage(`{"pattern":"*.go","path":"nonexistent"}`))
	if err == nil {
		t.Error("搜索不存在的目录应返回错误")
	}
}

func TestSearchFilesTool_NotDirectory(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建文件
	os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("test"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_files")

	_, err := tool.Execute(json.RawMessage(`{"pattern":"*.go","path":"file.txt"}`))
	if err == nil {
		t.Error("对文件调用 search_files 应返回错误")
	}
}

// ─── search_content 测试 ─────────────────────────────────────

func TestSearchContentTool_Success(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.go")
	os.WriteFile(testFile, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_content")

	result, err := tool.Execute(json.RawMessage(`{"query":"func main"}`))
	if err != nil {
		t.Fatalf("search_content 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestSearchContentTool_Regex(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	testFile := filepath.Join(workDir, "test.go")
	os.WriteFile(testFile, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_content")

	result, err := tool.Execute(json.RawMessage(`{"query":"func \\w+"}`))
	if err != nil {
		t.Fatalf("search_content 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestSearchContentTool_NotExists(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_content")

	_, err := tool.Execute(json.RawMessage(`{"query":"test","path":"nonexistent"}`))
	if err == nil {
		t.Error("搜索不存在的目录应返回错误")
	}
}

func TestSearchContentTool_NotDirectory(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建文件
	os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("test"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_content")

	_, err := tool.Execute(json.RawMessage(`{"query":"test","path":"file.txt"}`))
	if err == nil {
		t.Error("对文件调用 search_content 应返回错误")
	}
}

func TestSearchContentTool_InvalidRegex(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 创建测试文件
	os.WriteFile(filepath.Join(workDir, "test.txt"), []byte("test content"), 0644)

	reg := NewToolRegistry()
	RegisterFileTools(reg)
	tool, _ := reg.Get("search_content")

	// 无效正则会降级为字面量匹配，不应报错
	result, err := tool.Execute(json.RawMessage(`{"query":"[invalid"}`))
	if err != nil {
		t.Fatalf("search_content 执行失败: %v", err)
	}

	if result == "" {
		t.Error("结果不应为空")
	}
}

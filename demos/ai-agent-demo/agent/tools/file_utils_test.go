package tools

import (
	"testing"
)

// ─── 路径安全验证测试 ─────────────────────────────────────────

func TestValidatePath_Valid(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	tests := []struct {
		name string
		path string
	}{
		{"相对路径", "test.txt"},
		{"当前目录", "./test.txt"},
		{"子目录", "subdir/test.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validatePath(tt.path)
			if err != nil {
				t.Fatalf("validatePath(%q) 返回错误: %v", tt.path, err)
			}
			if result == "" {
				t.Error("返回路径不应为空")
			}
		})
	}
}

func TestValidatePath_EmptyPath(t *testing.T) {
	_, err := validatePath("")
	if err == nil {
		t.Error("空路径应返回错误")
	}
}

func TestValidatePath_DotDotTraversal(t *testing.T) {
	tests := []string{
		"../test.txt",
		"subdir/../../test.txt",
		"../outside.txt",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			_, err := validatePath(path)
			if err == nil {
				t.Errorf("路径 %q 应返回错误（包含 ..）", path)
			}
		})
	}
}

func TestValidatePath_AbsolutePathOutside(t *testing.T) {
	// 保存原始函数并恢复
	origGetWorkDir := getWorkDir
	defer func() { getWorkDir = origGetWorkDir }()

	workDir := t.TempDir()
	getWorkDir = func() string { return workDir }

	// 尝试访问工作目录外的绝对路径
	_, err := validatePath("/etc/passwd")
	if err == nil {
		t.Error("绝对路径应返回错误（超出工作目录范围）")
	}
}

// ─── 辅助函数测试 ─────────────────────────────────────────────

func TestIsTextFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"script.py", true},
		{"app.js", true},
		{"style.css", true},
		{"data.json", true},
		{"README.md", true},
		{"Makefile", true},
		{"Dockerfile", true},
		{"image.png", false},
		{"binary.exe", false},
		{"archive.zip", false},
		{"video.mp4", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isTextFile(tt.path)
			if got != tt.want {
				t.Errorf("isTextFile(%q) = %v, 期望 %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatSize(tt.size)
			if got != tt.want {
				t.Errorf("formatSize(%d) = %q, 期望 %q", tt.size, got, tt.want)
			}
		})
	}
}

// ─── 集成测试 ─────────────────────────────────────────────────

func TestRegisterFileTools(t *testing.T) {
	reg := NewToolRegistry()
	RegisterFileTools(reg)

	expected := []string{
		"read_file", "write_file", "edit_file", "list_dir",
		"file_info", "delete_file", "search_files", "search_content",
	}
	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("文件工具 %q 未注册", name)
		}
	}
}

func TestRegisterBuiltinTools_WithFileTools(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)

	// 验证原有工具
	originalTools := []string{"calculator", "current_time", "search", "text_transform"}
	for _, name := range originalTools {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("原有工具 %q 未注册", name)
		}
	}

	// 验证文件工具
	fileTools := []string{
		"read_file", "write_file", "edit_file", "list_dir",
		"file_info", "delete_file", "search_files", "search_content",
	}
	for _, name := range fileTools {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("文件工具 %q 未注册", name)
		}
	}
}

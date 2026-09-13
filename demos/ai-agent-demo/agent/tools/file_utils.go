package tools

// ═══════════════════════════════════════════════════════════════
// file_utils.go — 文件工具通用函数
// ═══════════════════════════════════════════════════════════════
//
// 提供文件操作工具的通用功能：
//   - 路径安全验证（防止路径遍历攻击）
//   - 文件类型判断
//   - 文件大小格式化
//   - 工具注册入口

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ─── 路径安全验证 ─────────────────────────────────────────────

// getWorkDir 获取当前工作目录（支持测试时模拟）
var getWorkDir = func() string {
	dir, _ := os.Getwd()
	return dir
}

// validatePath 验证并规范化路径，确保在工作目录内
func validatePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("路径不能为空")
	}

	if strings.Contains(path, "..") {
		return "", fmt.Errorf("路径不允许包含 '..': %s", path)
	}

	workDir := getWorkDir()
	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath = filepath.Clean(filepath.Join(workDir, path))
	}

	if !strings.HasPrefix(absPath, workDir) {
		return "", fmt.Errorf("路径超出工作目录范围: %s (工作目录: %s)", path, workDir)
	}

	return absPath, nil
}

// isTextFile 检查文件是否为文本文件（通过扩展名判断）
func isTextFile(path string) bool {
	textExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true,
		".java": true, ".c": true, ".cpp": true, ".h": true,
		".rs": true, ".rb": true, ".php": true, ".swift": true,
		".kt": true, ".scala": true, ".sh": true, ".bash": true,
		".zsh": true, ".fish": true, ".ps1": true, ".bat": true,
		".cmd": true, ".sql": true, ".html": true, ".css": true,
		".scss": true, ".less": true, ".xml": true, ".json": true,
		".yaml": true, ".yml": true, ".toml": true, ".ini": true,
		".cfg": true, ".conf": true, ".md": true, ".txt": true,
		".rst": true, ".csv": true, ".log": true,
		".gitignore": true, ".dockerignore": true, ".editorconfig": true,
		".prettierrc": true, ".eslintrc": true, ".babelrc": true,
		".vue": true, ".jsx": true, ".tsx": true, ".svelte": true,
		".astro": true, ".graphql": true, ".proto": true,
		".makefile": true, ".dockerfile": true,
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		base := strings.ToLower(filepath.Base(path))
		return base == "makefile" || base == "dockerfile" ||
			base == "license" || base == "readme" ||
			base == "changelog" || base == "authors" ||
			base == "contributing"
	}
	return textExts[ext]
}

// formatSize 格式化文件大小为人类可读格式
func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(size)/float64(div), []string{"KB", "MB", "GB", "TB", "PB", "EB"}[exp])
}

// ─── 工具注册 ─────────────────────────────────────────────────

// RegisterFileTools 注册所有文件操作工具
func RegisterFileTools(registry *ToolRegistry) {
	// 只读工具
	registry.Register(newReadFileTool())
	registry.Register(newListDirTool())
	registry.Register(newFileInfoTool())
	registry.Register(newSearchFilesTool())
	registry.Register(newSearchContentTool())

	// 写入工具
	registry.Register(newWriteFileTool())
	registry.Register(newEditFileTool())
	registry.Register(newDeleteFileTool())
}

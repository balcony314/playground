package tools

// ═══════════════════════════════════════════════════════════════
// file_read.go — 只读文件操作工具
// ═══════════════════════════════════════════════════════════════
//
// 提供 5 个只读文件操作工具：
//   - read_file: 读取文件内容
//   - list_dir: 列出目录内容
//   - file_info: 获取文件信息
//   - search_files: 按名称模式搜索文件
//   - search_content: 按内容搜索（支持正则）

import (
	"ai-agent-demo/agent/types"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// newReadFileTool 创建读取文件工具
func newReadFileTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "read_file",
				Description: "读取文件内容。仅支持文本文件，最大 1MB。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "文件路径（相对于工作目录）"
						}
					},
					"required": ["path"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			absPath, err := validatePath(params.Path)
			if err != nil {
				return "", err
			}

			info, err := os.Stat(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return "", fmt.Errorf("文件不存在: %s", params.Path)
				}
				return "", fmt.Errorf("获取文件信息失败: %w", err)
			}

			if info.IsDir() {
				return "", fmt.Errorf("路径是目录，不是文件: %s", params.Path)
			}

			const maxSize = 1024 * 1024
			if info.Size() > maxSize {
				return "", fmt.Errorf("文件过大 (%s)，最大支持 1MB", formatSize(info.Size()))
			}

			if !isTextFile(absPath) {
				return "", fmt.Errorf("不支持的文件类型，仅支持文本文件: %s", params.Path)
			}

			content, err := os.ReadFile(absPath)
			if err != nil {
				return "", fmt.Errorf("读取文件失败: %w", err)
			}

			lines := strings.Count(string(content), "\n") + 1
			return fmt.Sprintf("文件内容 (%s, %d 行):\n%s", params.Path, lines, string(content)), nil
		},
	}
}

// newListDirTool 创建列出目录工具
func newListDirTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "list_dir",
				Description: "列出目录内容。显示文件和子目录。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "目录路径（相对于工作目录，默认当前目录）"
						},
						"pattern": {
							"type": "string",
							"description": "文件名匹配模式，如 '*.go'（可选）"
						}
					},
					"required": []
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Path    string `json:"path"`
				Pattern string `json:"pattern"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			targetPath := params.Path
			if targetPath == "" {
				targetPath = "."
			}

			absPath, err := validatePath(targetPath)
			if err != nil {
				return "", err
			}

			info, err := os.Stat(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return "", fmt.Errorf("目录不存在: %s", targetPath)
				}
				return "", fmt.Errorf("获取目录信息失败: %w", err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("路径不是目录: %s", targetPath)
			}

			entries, err := os.ReadDir(absPath)
			if err != nil {
				return "", fmt.Errorf("读取目录失败: %w", err)
			}

			var result strings.Builder
			result.WriteString(fmt.Sprintf("目录内容: %s (%d 项)\n\n", targetPath, len(entries)))

			for _, entry := range entries {
				name := entry.Name()

				if params.Pattern != "" {
					matched, _ := filepath.Match(params.Pattern, name)
					if !matched {
						continue
					}
				}

				if entry.IsDir() {
					result.WriteString(fmt.Sprintf("📁 %s/\n", name))
				} else {
					info, _ := entry.Info()
					result.WriteString(fmt.Sprintf("📄 %s (%s, %s)\n", name, formatSize(info.Size()), info.ModTime().Format("2006-01-02 15:04")))
				}
			}

			return result.String(), nil
		},
	}
}

// newFileInfoTool 创建文件信息工具
func newFileInfoTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "file_info",
				Description: "获取文件或目录的详细信息：大小、权限、修改时间等。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "文件或目录路径（相对于工作目录）"
						}
					},
					"required": ["path"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			absPath, err := validatePath(params.Path)
			if err != nil {
				return "", err
			}

			info, err := os.Stat(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return "", fmt.Errorf("文件或目录不存在: %s", params.Path)
				}
				return "", fmt.Errorf("获取文件信息失败: %w", err)
			}

			var result strings.Builder
			result.WriteString(fmt.Sprintf("文件信息: %s\n", info.Name()))
			result.WriteString(fmt.Sprintf("路径: %s\n", absPath))

			if info.IsDir() {
				result.WriteString("类型: 目录\n")
			} else {
				result.WriteString(fmt.Sprintf("类型: 文件\n大小: %s (%d 字节)\n文本文件: %t\n",
					formatSize(info.Size()), info.Size(), isTextFile(absPath)))
			}

			result.WriteString(fmt.Sprintf("权限: %s (%04o)\n修改时间: %s",
				info.Mode(), info.Mode().Perm(), info.ModTime().Format("2006-01-02 15:04:05")))

			return result.String(), nil
		},
	}
}

// newSearchFilesTool 创建按名称搜索文件工具
func newSearchFilesTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "search_files",
				Description: "按文件名模式搜索文件。支持通配符（如 *.go、test_*）。最大返回 100 条结果。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"pattern": {
							"type": "string",
							"description": "文件名匹配模式，支持通配符（如 '*.go'、'test_*'）"
						},
						"path": {
							"type": "string",
							"description": "搜索起始目录（相对于工作目录，默认当前目录）"
						}
					},
					"required": ["pattern"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			targetPath := params.Path
			if targetPath == "" {
				targetPath = "."
			}

			absPath, err := validatePath(targetPath)
			if err != nil {
				return "", err
			}

			info, err := os.Stat(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return "", fmt.Errorf("目录不存在: %s", targetPath)
				}
				return "", fmt.Errorf("获取目录信息失败: %w", err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("路径不是目录: %s", targetPath)
			}

			const maxResults = 100
			var matches []string

			err = filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}

				if len(matches) >= maxResults {
					return filepath.SkipAll
				}

				if matched, _ := filepath.Match(params.Pattern, d.Name()); matched {
					relPath, _ := filepath.Rel(absPath, path)
					matches = append(matches, relPath)
				}

				return nil
			})
			if err != nil {
				return "", fmt.Errorf("搜索失败: %w", err)
			}

			var result strings.Builder
			result.WriteString(fmt.Sprintf("搜索结果: '%s' (找到 %d 个文件)\n\n", params.Pattern, len(matches)))

			for _, match := range matches {
				result.WriteString(match + "\n")
			}

			if len(matches) >= maxResults {
				result.WriteString(fmt.Sprintf("\n... 已达到最大结果数 (%d)，可能还有更多匹配", maxResults))
			}

			return result.String(), nil
		},
	}
}

// newSearchContentTool 创建按内容搜索工具
func newSearchContentTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "search_content",
				Description: "按文件内容搜索（支持正则表达式）。显示匹配行。最大返回 50 处匹配。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"query": {
							"type": "string",
							"description": "搜索内容（支持正则表达式）"
						},
						"path": {
							"type": "string",
							"description": "搜索目录（相对于工作目录，默认当前目录）"
						}
					},
					"required": ["query"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Query string `json:"query"`
				Path  string `json:"path"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			targetPath := params.Path
			if targetPath == "" {
				targetPath = "."
			}

			absPath, err := validatePath(targetPath)
			if err != nil {
				return "", err
			}

			info, err := os.Stat(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return "", fmt.Errorf("目录不存在: %s", targetPath)
				}
				return "", fmt.Errorf("获取目录信息失败: %w", err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("路径不是目录: %s", targetPath)
			}

			re, err := regexp.Compile(params.Query)
			if err != nil {
				re = regexp.MustCompile(regexp.QuoteMeta(params.Query))
			}

			const maxMatches = 50
			type match struct {
				File string
				Line int
				Text string
			}
			var matches []match

			err = filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || !isTextFile(path) || len(matches) >= maxMatches {
					return nil
				}

				content, err := os.ReadFile(path)
				if err != nil {
					return nil
				}

				for i, line := range strings.Split(string(content), "\n") {
					if len(matches) >= maxMatches {
						break
					}
					if re.MatchString(line) {
						relPath, _ := filepath.Rel(absPath, path)
						matches = append(matches, match{File: relPath, Line: i + 1, Text: line})
					}
				}

				return nil
			})
			if err != nil {
				return "", fmt.Errorf("搜索失败: %w", err)
			}

			var result strings.Builder
			result.WriteString(fmt.Sprintf("搜索结果: '%s' (找到 %d 处匹配)\n\n", params.Query, len(matches)))

			for _, m := range matches {
				result.WriteString(fmt.Sprintf("%s:%d\n  %s\n\n", m.File, m.Line, strings.TrimSpace(m.Text)))
			}

			if len(matches) >= maxMatches {
				result.WriteString(fmt.Sprintf("... 已达到最大匹配数 (%d)，可能还有更多匹配", maxMatches))
			}

			return result.String(), nil
		},
	}
}

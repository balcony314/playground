package tools

// ═══════════════════════════════════════════════════════════════
// file_write.go — 写入文件操作工具
// ═══════════════════════════════════════════════════════════════
//
// 提供 3 个写入文件操作工具：
//   - write_file: 写入文件（创建/覆盖）
//   - edit_file: 编辑文件（替换/追加/插入/删除）
//   - delete_file: 删除文件或目录

import (
	"ai-agent-demo/agent/types"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// newWriteFileTool 创建写入文件工具
func newWriteFileTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "write_file",
				Description: "写入文件（创建或覆盖）。自动创建父目录。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "文件路径（相对于工作目录）"
						},
						"content": {
							"type": "string",
							"description": "要写入的内容"
						}
					},
					"required": ["path", "content"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			absPath, err := validatePath(params.Path)
			if err != nil {
				return "", err
			}

			if !isTextFile(absPath) {
				return "", fmt.Errorf("不支持的文件类型，仅支持文本文件: %s", params.Path)
			}

			if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
				return "", fmt.Errorf("创建目录失败: %w", err)
			}

			if err := os.WriteFile(absPath, []byte(params.Content), 0644); err != nil {
				return "", fmt.Errorf("写入文件失败: %w", err)
			}

			return fmt.Sprintf("文件已写入: %s (%d 字节)", params.Path, len(params.Content)), nil
		},
	}
}

// newEditFileTool 创建编辑文件工具
func newEditFileTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "edit_file",
				Description: "编辑文件。支持 4 种操作：replace（查找替换）、append（追加）、insert（插入行）、delete（删除行）。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "文件路径（相对于工作目录）"
						},
						"action": {
							"type": "string",
							"enum": ["replace", "append", "insert", "delete"],
							"description": "编辑操作类型"
						},
						"find": {
							"type": "string",
							"description": "要查找的文本（action=replace 时必填）"
						},
						"replace_with": {
							"type": "string",
							"description": "替换后的文本（action=replace 时必填）"
						},
						"content": {
							"type": "string",
							"description": "要追加或插入的内容（action=append/insert 时必填）"
						},
						"line": {
							"type": "integer",
							"description": "行号，从 1 开始（action=insert/delete 时必填）"
						},
						"end_line": {
							"type": "integer",
							"description": "结束行号，包含该行（action=delete 时可选，默认等于 line）"
						}
					},
					"required": ["path", "action"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Path        string `json:"path"`
				Action      string `json:"action"`
				Find        string `json:"find"`
				ReplaceWith string `json:"replace_with"`
				Content     string `json:"content"`
				Line        int    `json:"line"`
				EndLine     int    `json:"end_line"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			absPath, err := validatePath(params.Path)
			if err != nil {
				return "", err
			}

			// 读取文件内容
			content, err := os.ReadFile(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return "", fmt.Errorf("文件不存在: %s", params.Path)
				}
				return "", fmt.Errorf("读取文件失败: %w", err)
			}

			lines := strings.Split(string(content), "\n")
			var result string

			switch params.Action {
			case "replace":
				if params.Find == "" {
					return "", fmt.Errorf("replace 操作需要 find 参数")
				}
				newContent := strings.ReplaceAll(string(content), params.Find, params.ReplaceWith)
				count := strings.Count(string(content), params.Find) - strings.Count(newContent, params.Find)
				if count < 0 {
					count = 0
				}
				lines = strings.Split(newContent, "\n")
				result = fmt.Sprintf("操作: replace\n替换了 %d 处匹配\n修改后: %d 行", count, len(lines))

			case "append":
				if params.Content == "" {
					return "", fmt.Errorf("append 操作需要 content 参数")
				}
				// 移除最后一行的空行（如果有）
				if len(lines) > 0 && lines[len(lines)-1] == "" {
					lines = lines[:len(lines)-1]
				}
				lines = append(lines, strings.Split(params.Content, "\n")...)
				result = fmt.Sprintf("操作: append\n追加了 %d 行\n修改后: %d 行", len(strings.Split(params.Content, "\n")), len(lines))

			case "insert":
				if params.Content == "" {
					return "", fmt.Errorf("insert 操作需要 content 参数")
				}
				if params.Line < 1 || params.Line > len(lines)+1 {
					return "", fmt.Errorf("行号超出范围: %d (文件共 %d 行)", params.Line, len(lines))
				}
				insertLines := strings.Split(params.Content, "\n")
				newLines := make([]string, 0, len(lines)+len(insertLines))
				newLines = append(newLines, lines[:params.Line-1]...)
				newLines = append(newLines, insertLines...)
				newLines = append(newLines, lines[params.Line-1:]...)
				lines = newLines
				result = fmt.Sprintf("操作: insert\n在第 %d 行插入了 %d 行\n修改后: %d 行", params.Line, len(insertLines), len(lines))

			case "delete":
				if params.Line < 1 || params.Line > len(lines) {
					return "", fmt.Errorf("行号超出范围: %d (文件共 %d 行)", params.Line, len(lines))
				}
				endLine := params.Line
				if params.EndLine > 0 {
					endLine = params.EndLine
				}
				if endLine < params.Line || endLine > len(lines) {
					return "", fmt.Errorf("结束行号超出范围: %d (文件共 %d 行)", endLine, len(lines))
				}
				deletedCount := endLine - params.Line + 1
				newLines := make([]string, 0, len(lines)-deletedCount)
				newLines = append(newLines, lines[:params.Line-1]...)
				newLines = append(newLines, lines[endLine:]...)
				lines = newLines
				result = fmt.Sprintf("操作: delete\n删除了第 %d-%d 行 (%d 行)\n修改后: %d 行", params.Line, endLine, deletedCount, len(lines))

			default:
				return "", fmt.Errorf("未知操作: %s", params.Action)
			}

			// 写回文件
			if err := os.WriteFile(absPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
				return "", fmt.Errorf("写回文件失败: %w", err)
			}

			return fmt.Sprintf("文件已编辑: %s\n%s", params.Path, result), nil
		},
	}
}

// newDeleteFileTool 创建删除文件工具
func newDeleteFileTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "delete_file",
				Description: "删除文件或目录。删除目录需要设置 recursive=true。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {
							"type": "string",
							"description": "文件或目录路径（相对于工作目录）"
						},
						"recursive": {
							"type": "boolean",
							"description": "是否递归删除目录（默认 false）"
						}
					},
					"required": ["path"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Path      string `json:"path"`
				Recursive bool   `json:"recursive"`
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

			if info.IsDir() {
				if !params.Recursive {
					entries, err := os.ReadDir(absPath)
					if err != nil {
						return "", fmt.Errorf("读取目录失败: %w", err)
					}
					if len(entries) > 0 {
						return "", fmt.Errorf("目录不为空 (%d 项)，需要设置 recursive=true", len(entries))
					}
				}
				if err := os.RemoveAll(absPath); err != nil {
					return "", fmt.Errorf("删除目录失败: %w", err)
				}
				return fmt.Sprintf("已删除: %s\n类型: 目录", params.Path), nil
			}

			if err := os.Remove(absPath); err != nil {
				return "", fmt.Errorf("删除文件失败: %w", err)
			}
			return fmt.Sprintf("已删除: %s\n类型: 文件", params.Path), nil
		},
	}
}

package tools

// ═══════════════════════════════════════════════════════════════
// exec.go — 命令执行工具集
// ═══════════════════════════════════════════════════════════════
//
// 提供 2 个命令执行工具：
//   - execute_command: 执行 shell 命令
//   - manage_process: 管理后台进程
//
// 安全防护：
//   - 命令黑名单
//   - 路径访问控制
//   - 敏感操作确认
//   - 审计日志

import (
	"ai-agent-demo/agent/tools/exec"
	"ai-agent-demo/agent/types"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ─── 单例 Executor ─────────────────────────────────────────────

var (
	sharedExecutor *exec.Executor
	executorOnce   sync.Once
	executorMu     sync.Mutex
)

// getSharedExecutor 获取共享的 Executor 实例（单例模式）
func getSharedExecutor() *exec.Executor {
	executorOnce.Do(func() {
		config := exec.LoadConfig().WithWorkDir(getWorkDir())
		sharedExecutor = exec.NewExecutor(config)
	})
	return sharedExecutor
}

// CloseExecutor 关闭共享的 Executor（释放资源）
func CloseExecutor() {
	executorMu.Lock()
	defer executorMu.Unlock()
	if sharedExecutor != nil {
		sharedExecutor.Close()
		sharedExecutor = nil
		executorOnce = sync.Once{}
	}
}

// ─── 工具注册 ─────────────────────────────────────────────────

// RegisterExecTools 注册所有命令执行工具
func RegisterExecTools(registry *ToolRegistry) {
	registry.Register(newExecuteCommandTool())
	registry.Register(newManageProcessTool())
}

// newExecuteCommandTool 创建执行命令工具
func newExecuteCommandTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name: "execute_command",
				Description: `执行 shell 命令。
特性：
- 支持所有 shell 命令（通过 sh -c 执行）
- 实时进度反馈
- 输出限制 1MB
- 安全防护（危险命令拦截、敏感操作确认）
- 支持后台执行

注意：
- 禁止交互式命令（如 vim、top）
- 危险命令（如 rm -rf /）会被阻止
- 敏感操作（如 git push、rm）需要确认`,
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"command": {
							"type": "string",
							"description": "要执行的 shell 命令"
						},
						"work_dir": {
							"type": "string",
							"description": "工作目录（相对于项目根目录），默认当前目录"
						},
						"timeout": {
							"type": "integer",
							"description": "超时时间（秒），默认 30 秒"
						},
						"background": {
							"type": "boolean",
							"description": "是否后台执行，默认 false"
						},
						"confirm": {
							"type": "boolean",
							"description": "确认执行敏感操作，默认 false"
						}
					},
					"required": ["command"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Command    string `json:"command"`
				WorkDir    string `json:"work_dir"`
				Timeout    int    `json:"timeout"`
				Background bool   `json:"background"`
				Confirm    bool   `json:"confirm"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			// 获取工作目录
			workDir := params.WorkDir
			if workDir != "" {
				absPath, err := validatePath(workDir)
				if err != nil {
					return "", fmt.Errorf("无效的工作目录: %w", err)
				}
				workDir = absPath
			} else {
				workDir = getWorkDir()
			}

			// 获取共享执行器
			executor := getSharedExecutor()

			// 设置超时
			var timeout time.Duration
			if params.Timeout > 0 {
				timeout = time.Duration(params.Timeout) * time.Second
			}

			// 执行命令
			ctx := context.Background()
			if params.Background {
				result, err := executor.ExecuteAsync(ctx, params.Command, workDir, params.Confirm)
				if err != nil {
					return "", err
				}
				return exec.FormatResult(result, params.Command), nil
			}

			result, err := executor.Execute(ctx, params.Command, workDir, timeout, params.Confirm)
			if err != nil {
				return "", err
			}
			return exec.FormatResult(result, params.Command), nil
		},
	}
}

// processStatus 返回进程的状态文本
func processStatus(proc *exec.ManagedProcess) string {
	select {
	case <-proc.Done:
		return "已完成"
	default:
		return "运行中"
	}
}

// newManageProcessTool 创建进程管理工具
func newManageProcessTool() types.Tool {
	return types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name: "manage_process",
				Description: `管理后台执行的进程。
操作：
- list: 列出所有后台进程
- status: 查看指定进程状态
- kill: 终止指定进程`,
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"action": {
							"type": "string",
							"enum": ["list", "status", "kill"],
							"description": "操作类型"
						},
						"pid": {
							"type": "integer",
							"description": "进程 ID（status/kill 操作必填）"
						}
					},
					"required": ["action"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var params struct {
				Action string `json:"action"`
				PID    int    `json:"pid"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			// 获取共享执行器的进程管理器
			executor := getSharedExecutor()
			pm := executor.GetProcessManager()

			switch params.Action {
			case "list":
				procs := pm.List()
				if len(procs) == 0 {
					return "没有后台运行的进程", nil
				}

				result := fmt.Sprintf("后台进程列表 (%d 个):\n\n", len(procs))
				for _, proc := range procs {
					result += fmt.Sprintf("PID: %d\n命令: %s\n状态: %s\n启动时间: %s\n\n",
						proc.PID, proc.Command, processStatus(proc),
						proc.StartTime.Format("2006-01-02 15:04:05"))
				}
				return result, nil

			case "status":
				if params.PID == 0 {
					return "", fmt.Errorf("status 操作需要 pid 参数")
				}

				proc, ok := pm.Get(params.PID)
				if !ok {
					return "", fmt.Errorf("进程不存在: %d", params.PID)
				}

				result := fmt.Sprintf("进程信息:\nPID: %d\n命令: %s\n状态: %s\n启动时间: %s\n运行时间: %v",
					proc.PID, proc.Command, processStatus(proc),
					proc.StartTime.Format("2006-01-02 15:04:05"),
					time.Since(proc.StartTime).Round(time.Second))

				if proc.Result != nil {
					result += fmt.Sprintf("\n退出码: %d", proc.Result.ExitCode)
				}

				return result, nil

			case "kill":
				if params.PID == 0 {
					return "", fmt.Errorf("kill 操作需要 pid 参数")
				}

				if err := pm.Kill(params.PID); err != nil {
					return "", err
				}
				return fmt.Sprintf("已发送终止信号到进程 %d", params.PID), nil

			default:
				return "", fmt.Errorf("未知操作: %s (支持: list, status, kill)", params.Action)
			}
		},
	}
}

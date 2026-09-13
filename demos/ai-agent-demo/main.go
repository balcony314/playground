package main

// ═══════════════════════════════════════════════════════════════
// main.go — AI Agent 教学 Demo 入口
// ═══════════════════════════════════════════════════════════════
//
// 使用 cobra 管理 CLI 命令，详见 cmd/ 目录。
//
// 使用方式:
//   go run .                              # 交互式 REPL（Mock 模式）
//   go run . --api-key sk-xxx             # 真实 LLM 模式
//   go run . chat "你好"                  # 单次提问
//   go run . skill list                   # 查看技能列表
//   go run . version                      # 版本信息

import (
	"ai-agent-demo/cmd"
)

func main() {
	cmd.Execute()
}

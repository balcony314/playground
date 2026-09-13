package cmd

// ═══════════════════════════════════════════════════════════════
// chat.go — 单次提问子命令
// ═══════════════════════════════════════════════════════════════
//
// 用法:
//   ai-agent-demo chat "你好"
//   ai-agent-demo chat --mock "帮我算一下 1+1"
//   ai-agent-demo chat -k sk-xxx "什么是 AI Agent"

import (
	"fmt"
	"strings"

	"ai-agent-demo/agent"
	"ai-agent-demo/agent/types"

	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat [问题]",
	Short: "单次提问（非交互式）",
	Long:  `向 Agent 发送一个问题，获取回答后退出。适合脚本调用或快速测试。`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  runChat,
}

func init() {
	rootCmd.AddCommand(chatCmd)
}

func runChat(cmd *cobra.Command, args []string) error {
	// 拼接所有参数作为用户输入
	userInput := strings.Join(args, " ")

	llm := initLLMClient()
	checkAPIConnection(llm)
	config := types.ConfigWithModel(model)
	a := agent.NewAgent(llm, config)

	reply, err := a.Run(userInput)
	if err != nil {
		return fmt.Errorf("Agent 执行失败: %w", err)
	}

	fmt.Println(reply)
	return nil
}

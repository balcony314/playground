package cmd

// ═══════════════════════════════════════════════════════════════
// root.go — Cobra Root 命令：全局 flags + 交互式 REPL
// ═══════════════════════════════════════════════════════════════

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"ai-agent-demo/agent"
	"ai-agent-demo/agent/types"

	"github.com/spf13/cobra"
)

// 全局 flags（所有子命令共享）
var (
	apiKey  string
	baseURL string
	model   string
	mock    bool
)

// rootCmd 是根命令，默认执行交互式 REPL
var rootCmd = &cobra.Command{
	Use:   "ai-agent-demo",
	Short: "🤖 AI Agent 教学 Demo",
	Long: `基于 Plan + ReAct 两阶段执行模式的 AI Agent 教学演示。
支持 OpenAI 兼容 API（OpenAI/Ollama/vLLM 等），内置 Mock 模式无需 API Key。`,
	RunE: runREPL,
}

func init() {
	// 全局 flags，所有子命令都可用
	rootCmd.PersistentFlags().StringVarP(&apiKey, "api-key", "k", "", "LLM API Key（为空则使用 Mock 模式）")
	rootCmd.PersistentFlags().StringVarP(&baseURL, "base-url", "u", "", "API 地址（默认 OpenAI，可设为 Ollama 等）")
	rootCmd.PersistentFlags().StringVarP(&model, "model", "m", "gpt-4o", "模型名称")
	rootCmd.PersistentFlags().BoolVar(&mock, "mock", false, "强制使用 Mock 模式（无需 API Key）")
}

// Execute 执行根命令（供 main.go 调用）
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// initLLMClient 根据 flags 创建 LLM 客户端（不检查连接）
func initLLMClient() agent.LLMClient {
	if mock || apiKey == "" {
		fmt.Println("🤖 LLM 模式: Mock（教学演示）")
		fmt.Println("   提示: 设置 --api-key 参数可连接真实 LLM API")
		fmt.Println("   支持: OpenAI, Ollama, vLLM, LiteLLM 等兼容 API")
		fmt.Println()
		return agent.NewMockClient()
	}

	fmt.Printf("🤖 LLM 模式: 真实 API\n")
	fmt.Printf("   模型: %s\n", model)
	if baseURL != "" {
		fmt.Printf("   地址: %s\n", baseURL)
	}
	fmt.Println()

	return agent.NewOpenAIClient(apiKey, baseURL, model)
}

// checkAPIConnection 检查 API 连接，不通则退出
func checkAPIConnection(llm agent.LLMClient) {
	client, ok := llm.(*agent.OpenAIClient)
	if !ok {
		return
	}
	fmt.Print("🔗 检查 API 连接... ")
	if err := client.Ping(); err != nil {
		fmt.Printf("❌\n\n❌ 无法连接到 API: %v\n", err)
		fmt.Println("   提示: 如果使用自定义 DNS，尝试 GODEBUG=netdns=cgo")
		os.Exit(1)
	}
	fmt.Println("✅")
	fmt.Println()
}

// runREPL 启动交互式聊天循环
func runREPL(cmd *cobra.Command, args []string) error {
	printBanner()

	llm := initLLMClient()
	checkAPIConnection(llm)
	config := types.ConfigWithModel(model)
	a := agent.NewAgent(llm, config)

	fmt.Printf("🛠️  可用工具: %s\n", strings.Join(a.ListTools(), ", "))
	fmt.Printf("✨ 当前技能: %s\n", a.CurrentSkill())
	fmt.Println()
	fmt.Println("💡 输入问题开始对话，输入 'quit' 退出，'reset' 重置对话")
	fmt.Println("   输入 'skills' 查看可用技能，'skill <名称>' 切换技能")
	fmt.Println(strings.Repeat("═", 55))

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\n🧑 你> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// REPL 内置命令
		switch cmd := strings.ToLower(input); {
		case cmd == "quit" || cmd == "exit" || cmd == "q":
			fmt.Println("\n👋 再见！")
			return nil
		case cmd == "reset" || cmd == "clear":
			a.ClearHistory()
			fmt.Println("🔄 对话已重置")
		case cmd == "history":
			printHistory(a.GetHistory())
		case cmd == "tools":
			fmt.Printf("🛠️  可用工具: %s\n", strings.Join(a.ListTools(), ", "))
		case cmd == "skills":
			fmt.Print(a.FormatSkillList())
		case cmd == "help":
			printHelp()
		case strings.HasPrefix(cmd, "skill "):
			skillName := strings.TrimSpace(strings.TrimPrefix(input, "skill "))
			if err := a.SwitchSkill(skillName); err != nil {
				fmt.Printf("❌ %v\n", err)
			}
		default:
			reply, err := a.Run(input)
			if err != nil {
				fmt.Printf("\n❌ 错误: %v\n", err)
				continue
			}
			fmt.Printf("\n🤖 助手> %s\n", reply)
		}
	}

	fmt.Println("\n👋 再见！")
	return nil
}

// ─── 界面辅助函数 ──────────────────────────────────────────────

func printBanner() {
	fmt.Print(`
╔══════════════════════════════════════════════════════╗
║           🤖 AI Agent 教学 Demo (Go)                ║
║                                                      ║
║   核心概念: Plan + ReAct 两阶段执行                  ║
║   架构: LLM + Tools + 智能循环                       ║
║   传输: stdio 交互式                                 ║
╚══════════════════════════════════════════════════════╝
`)
}

func printHelp() {
	fmt.Print(`
┌─────────────────────────────────────────────────────┐
│  📖 可用命令                                         │
├─────────────────────────────────────────────────────┤
│  <任意文本>    向 Agent 提问                         │
│  help          显示此帮助信息                         │
│  tools         列出所有可用工具                       │
│  skills        列出所有可用技能                       │
│  skill <名称>  切换到指定技能                         │
│  history       查看完整对话历史                       │
│  reset         重置对话（清空历史）                    │
│  quit          退出程序                              │
└─────────────────────────────────────────────────────┘

💡 示例问题:
  • 你好
  • 帮我算一下 sqrt(144) * 3 + 7
  • 现在几点了？
  • 搜索一下什么是 AI Agent
  • 把 "hello world" 转大写

🎭 技能说明:
  • general    - 通用助手（默认）
  • coder      - 代码助手
  • translator - 翻译官
  • analyst    - 数据分析师
  • storyteller - 故事大王

📋 执行模式:
  • 简单任务: 直接使用 ReAct 循环执行
  • 复杂任务: 先制定计划，再逐步执行
`)
}

func printHistory(history []types.Message) {
	fmt.Println("\n📜 对话历史:")
	fmt.Println(strings.Repeat("─", 50))
	for i, msg := range history {
		var prefix string
		switch msg.Role {
		case types.RoleSystem:
			prefix = "⚙️  [System]"
		case types.RoleUser:
			prefix = "🧑 [User]"
		case types.RoleAssistant:
			prefix = "🤖 [Assistant]"
		case types.RoleTool:
			prefix = "🔧 [Tool]"
		default:
			prefix = fmt.Sprintf("[%s]", msg.Role)
		}
		content := msg.Content
		if content == "" && len(msg.ToolCalls) > 0 {
			names := make([]string, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				names[j] = tc.Function.Name
			}
			content = fmt.Sprintf("(调用工具: %s)", strings.Join(names, ", "))
		}
		fmt.Printf("  %d. %s %s\n", i, prefix, agent.TruncStr(content, 100))
	}
	fmt.Println(strings.Repeat("─", 50))
}

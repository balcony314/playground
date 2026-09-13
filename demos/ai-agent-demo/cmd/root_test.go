package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"ai-agent-demo/agent"
	"ai-agent-demo/agent/types"
)

func TestInitLLMClient_MockMode(t *testing.T) {
	// 强制 Mock 模式
	mock = true
	apiKey = ""

	client := initLLMClient()
	if client == nil {
		t.Fatal("initLLMClient 返回 nil")
	}

	// 验证返回的是 MockClient
	if _, ok := client.(*agent.MockClient); !ok {
		t.Error("预期返回 MockClient")
	}
}

func TestInitLLMClient_NoAPIKey(t *testing.T) {
	// 没有 API Key 时应该使用 Mock 模式
	mock = false
	apiKey = ""

	client := initLLMClient()
	if client == nil {
		t.Fatal("initLLMClient 返回 nil")
	}

	if _, ok := client.(*agent.MockClient); !ok {
		t.Error("没有 API Key 时应返回 MockClient")
	}
}

func TestInitLLMClient_WithAPIKey(t *testing.T) {
	// 有 API Key 时应该创建 OpenAI 客户端
	mock = false
	apiKey = "sk-test-key"
	baseURL = ""
	model = "gpt-4o"

	client := initLLMClient()
	if client == nil {
		t.Fatal("initLLMClient 返回 nil")
	}

	if _, ok := client.(*agent.OpenAIClient); !ok {
		t.Error("有 API Key 时应返回 OpenAIClient")
	}
}

func TestInitLLMClient_WithBaseURL(t *testing.T) {
	// 测试自定义 base URL
	mock = false
	apiKey = "sk-test-key"
	baseURL = "http://localhost:11434/v1"
	model = "qwen2"

	client := initLLMClient()
	if client == nil {
		t.Fatal("initLLMClient 返回 nil")
	}

	if _, ok := client.(*agent.OpenAIClient); !ok {
		t.Error("应返回 OpenAIClient")
	}
}

func TestPrintBanner(t *testing.T) {
	// printBanner 只是打印，确保不 panic
	printBanner()
}

func TestPrintHelp(t *testing.T) {
	// printHelp 只是打印，确保不 panic
	printHelp()
}

func TestPrintHistory(t *testing.T) {
	// 测试空历史
	printHistory([]types.Message{})

	// 测试有消息的历史
	history := []types.Message{
		{Role: types.RoleUser, Content: "你好"},
		{Role: types.RoleAssistant, Content: "你好！有什么可以帮你的吗？"},
		{Role: types.RoleTool, Content: "工具结果"},
	}
	printHistory(history)
}

func TestPrintHistory_WithToolCalls(t *testing.T) {
	// 测试包含工具调用的历史
	history := []types.Message{
		{
			Role:    types.RoleAssistant,
			Content: "",
			ToolCalls: []types.ToolCall{
				{ID: "call_1", Function: types.FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`}},
			},
		},
	}
	printHistory(history)
}

func TestPrintHistory_UnknownRole(t *testing.T) {
	// 测试未知 role 的 default 分支
	history := []types.Message{
		{Role: types.Role("custom"), Content: "自定义角色消息"},
	}
	printHistory(history)
}

// ─── checkAPIConnection 测试 ──────────────────────────────────

func TestCheckAPIConnection_MockClient(t *testing.T) {
	// MockClient 应该直接跳过检查
	client := agent.NewMockClient()
	checkAPIConnection(client) // 不应 panic 或退出
}

func TestCheckAPIConnection_OpenAIClientSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := agent.NewOpenAIClient("sk-test", server.URL, "gpt-4o")
	checkAPIConnection(client) // 不应退出
}

// checkAPIConnection 失败时调用 os.Exit(1)，需要用子进程测试
// ─── runREPL 直接测试（重定向 stdin/stdout）────────────────────

// runREPLTest 是测试辅助函数：重定向 stdin/stdout，执行 runREPL，返回输出
func runREPLTest(t *testing.T, input string) string {
	t.Helper()

	// 保存原始 stdin/stdout
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	// 创建 stdin pipe
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建 stdin pipe 失败: %v", err)
	}
	// 创建 stdout pipe
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建 stdout pipe 失败: %v", err)
	}

	os.Stdin = stdinR
	os.Stdout = stdoutW

	// 写入测试输入
	go func() {
		stdinW.WriteString(input)
		stdinW.Close()
	}()

	// 保存并设置全局变量
	oldMock, oldAPIKey := mock, apiKey
	t.Cleanup(func() { mock, apiKey = oldMock, oldAPIKey })
	mock = true
	apiKey = ""

	// 执行 runREPL
	cmd := rootCmd
	err = runREPL(cmd, nil)

	// 关闭 stdout 写端，读取全部输出
	stdoutW.Close()
	raw, _ := io.ReadAll(stdoutR)
	output := string(raw)

	if err != nil {
		t.Fatalf("runREPL 错误: %v", err)
	}
	return output
}

func TestRunREPL_Quit(t *testing.T) {
	output := runREPLTest(t, "quit\n")
	if !strings.Contains(output, "再见") {
		t.Errorf("quit 应输出 '再见'，实际: %s", output)
	}
}

func TestRunREPL_Exit(t *testing.T) {
	output := runREPLTest(t, "exit\n")
	if !strings.Contains(output, "再见") {
		t.Errorf("exit 应输出 '再见'，实际: %s", output)
	}
}

func TestRunREPL_Help(t *testing.T) {
	output := runREPLTest(t, "help\nquit\n")
	if !strings.Contains(output, "可用命令") {
		t.Errorf("help 应输出 '可用命令'，实际: %s", output)
	}
}

func TestRunREPL_Skills(t *testing.T) {
	output := runREPLTest(t, "skills\nquit\n")
	if !strings.Contains(output, "general") {
		t.Errorf("skills 应输出技能列表，实际: %s", output)
	}
}

func TestRunREPL_Reset(t *testing.T) {
	output := runREPLTest(t, "reset\nquit\n")
	if !strings.Contains(output, "对话已重置") {
		t.Errorf("reset 应输出 '对话已重置'，实际: %s", output)
	}
}

func TestRunREPL_Tools(t *testing.T) {
	output := runREPLTest(t, "tools\nquit\n")
	if !strings.Contains(output, "calculator") {
		t.Errorf("tools 应列出工具，实际: %s", output)
	}
}

func TestRunREPL_SkillSwitch(t *testing.T) {
	output := runREPLTest(t, "skill coder\nquit\n")
	if !strings.Contains(output, "coder") {
		t.Errorf("切换技能应提到 coder，实际: %s", output)
	}
}

func TestRunREPL_InvalidSkill(t *testing.T) {
	output := runREPLTest(t, "skill nonexistent\nquit\n")
	if !strings.Contains(output, "未知技能") {
		t.Errorf("无效技能应报错，实际: %s", output)
	}
}

func TestRunREPL_Chat(t *testing.T) {
	output := runREPLTest(t, "你好\nquit\n")
	if !strings.Contains(output, "助手") {
		t.Errorf("对话应有助手回复，实际: %s", output)
	}
}

func TestRunREPL_EmptyInput(t *testing.T) {
	// 空输入应被跳过，不会崩溃
	output := runREPLTest(t, "\n\nquit\n")
	if !strings.Contains(output, "再见") {
		t.Errorf("空输入后 quit 应正常退出，实际: %s", output)
	}
}

func TestRunREPL_EOF(t *testing.T) {
	// stdin 关闭（EOF）应正常退出
	output := runREPLTest(t, "")
	if !strings.Contains(output, "再见") {
		t.Errorf("EOF 应输出 '再见'，实际: %s", output)
	}
}

// ─── Execute 测试 ───────────────────────────────────────────────

func TestExecute_Version(t *testing.T) {
	// 重置 cobra 参数
	rootCmd.SetArgs([]string{"version"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute version 错误: %v", err)
	}
}

func TestExecute_Help(t *testing.T) {
	rootCmd.SetArgs([]string{"--help"})
	err := rootCmd.Execute()
	// --help 返回 nil error
	if err != nil {
		t.Fatalf("Execute --help 错误: %v", err)
	}
}

func TestExecute_Chat(t *testing.T) {
	oldMock, oldAPIKey := mock, apiKey
	defer func() { mock, apiKey = oldMock, oldAPIKey }()
	mock = true
	apiKey = ""
	rootCmd.SetArgs([]string{"chat", "你好"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute chat 错误: %v", err)
	}
}

func TestCheckAPIConnection_Failure(t *testing.T) {
	if os.Getenv("TEST_CHECK_API_FAIL") == "1" {
		// 子进程：连接一个不存在的地址，触发 os.Exit(1)
		client := agent.NewOpenAIClient("sk-test", "http://127.0.0.1:1", "gpt-4o")
		checkAPIConnection(client)
		return
	}

	// 主进程：启动子进程并验证退出码
	cmd := exec.Command(os.Args[0], "-test.run=TestCheckAPIConnection_Failure")
	cmd.Env = append(os.Environ(), "TEST_CHECK_API_FAIL=1")
	err := cmd.Run()
	if err == nil {
		t.Fatal("子进程应以非零退出码退出")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("预期 ExitError，实际: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("退出码 = %d, 期望 1", exitErr.ExitCode())
	}
}

package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunChat_WithMockMode(t *testing.T) {
	// 重置全局 flags
	mock = true
	apiKey = ""
	baseURL = ""
	model = "gpt-4o"

	// 创建测试命令
	cmd := &cobra.Command{}

	// 执行 runChat（输出到 stdout，测试只验证不报错）
	err := runChat(cmd, []string{"你好"})
	if err != nil {
		t.Fatalf("runChat 失败: %v", err)
	}
}

func TestRunChat_WithMultipleArgs(t *testing.T) {
	mock = true
	apiKey = ""

	cmd := &cobra.Command{}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err := runChat(cmd, []string{"帮我", "算一下", "1+1"})
	if err != nil {
		t.Fatalf("runChat 失败: %v", err)
	}
}

func TestChatCmd_Args(t *testing.T) {
	// 测试参数验证：chat 命令至少需要 1 个参数
	cmd := chatCmd

	// 无参数应该失败
	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("预期参数验证失败，但通过了")
	}

	// 有参数应该成功
	err = cmd.Args(cmd, []string{"你好"})
	if err != nil {
		t.Errorf("参数验证应该通过，但失败: %v", err)
	}
}

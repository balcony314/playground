package graph

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// generateSQLPrompt 拼装 ClickHouse SQL 生成阶段的 LLM 输入消息：
// 用户消息 = 改写后的问题 + （存在时）RAG 召回的 DDL / 相似 SQL；
// 自愈重试场景下追加最近一次失败的语句与报错，要求 LLM 定向修复。
// 参考语料让 LLM 能按真实表结构生成 SQL，而非凭空猜测列名。
func generateSQLPrompt(systemPrompt string, input UserInput, attempts []Attempt) []*schema.Message {
	userMSGStr := fmt.Sprintf("问题：%s", input.Question)
	if input.DDL != "" {
		userMSGStr = fmt.Sprintf("%s，可以参考如下表 DDL：%s", userMSGStr, input.DDL)
	}
	if input.SQL != "" {
		userMSGStr = fmt.Sprintf("%s，可以参考如下相似问题的历史 SQL：%s", userMSGStr, input.SQL)
	}
	userMSGStr = appendFailureContext(userMSGStr, attempts, "SQL")

	return []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(userMSGStr),
	}
}

// generateDSLPrompt 拼装 ES DSL 生成阶段的 LLM 输入消息，结构与 generateSQLPrompt 对称：
// 用户消息 = 改写后的问题 + （存在时）RAG 召回的 Index Mapping / 相似 DSL + 失败上下文。
func generateDSLPrompt(systemPrompt string, input UserInput, attempts []Attempt) []*schema.Message {
	userMSGStr := fmt.Sprintf("问题：%s", input.Question)
	if input.Mapping != "" {
		userMSGStr = fmt.Sprintf("%s，可以参考如下 Index Mapping：%s", userMSGStr, input.Mapping)
	}
	if input.DSL != "" {
		userMSGStr = fmt.Sprintf("%s，可以参考如下相似问题的历史 DSL：%s", userMSGStr, input.DSL)
	}
	userMSGStr = appendFailureContext(userMSGStr, attempts, "DSL")

	return []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(userMSGStr),
	}
}

// appendFailureContext 自愈重试的关键注入：把最近一次失败的语句与报错
// 写进 Prompt，提示 LLM 基于错误原因修复而非盲目重新生成。
// 只取最近一次失败（多轮重试时历史错误参考价值有限，且占上下文）。
func appendFailureContext(userMSGStr string, attempts []Attempt, kind string) string {
	if len(attempts) == 0 {
		return userMSGStr
	}

	last := attempts[len(attempts)-1]
	if last.Err == "" {
		return userMSGStr
	}

	return fmt.Sprintf(
		"%s\n\n注意：上一次生成的 %s 执行失败了，请分析报错原因并修复后重新输出。\n上次生成的%s：\n%s\n报错信息：%s",
		userMSGStr, kind, kind, last.Statement, last.Err)
}

// trimStatement 清理 LLM 输出的语句：去围栏、去首尾空白与分号，
// 保证写入尝试历史与执行的语句是干净内容。
func trimStatement(content string) string {
	out := strings.TrimSpace(stripJSONFence(content))
	out = strings.TrimSuffix(out, ";")

	return strings.TrimSpace(out)
}

package graph

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GenerateMarkdownReport 根据最终 State 生成 Markdown 查询报告：
// 包含改写后的问题、各数据源的全部尝试（含失败尝试的报错）及最终执行结果。
// SQL/DSL 与查询结果分别放入代码块（sql/json）便于阅读。
func GenerateMarkdownReport(state *State) string {
	var sb strings.Builder

	sb.WriteString("## 🔍 多数据源查询报告\n\n")
	fmt.Fprintf(&sb, "**原始问题：**\n> %s\n\n", state.OldQuestion)
	fmt.Fprintf(&sb, "**改写后问题：**\n> %s\n\n", state.Question)
	fmt.Fprintf(&sb, "**涉及数据源：** %s\n\n", strings.Join(state.DataSources, "、"))

	writeSourceSection(&sb, "ClickHouse", ClickhouseSearcher, state, "sql")
	writeSourceSection(&sb, "Elasticsearch", ElasticSearchSearcher, state, "json")

	return sb.String()
}

// writeSourceSection 输出单个数据源的章节：尝试历史（失败原因）+ 最终结果
func writeSourceSection(sb *strings.Builder, title, node string, state *State, codeLang string) {
	attempts := state.Query[node]
	if len(attempts) == 0 {
		return
	}

	sb.WriteString("---\n\n")
	fmt.Fprintf(sb, "### %s\n\n", title)

	// 失败尝试：展示出错语句与报错，体现自愈重试的完整轨迹
	for i, attempt := range attempts {
		if attempt.Err == "" {
			continue
		}
		fmt.Fprintf(sb, "**尝试 %d（失败）：**\n\n", i+1)
		fmt.Fprintf(sb, "```%s\n%s\n```\n\n", codeLang, attempt.Statement)
		fmt.Fprintf(sb, "> 执行报错：%s\n\n", attempt.Err)
	}

	// 最终结果
	resp, ok := state.RespMap[node]
	if !ok {
		sb.WriteString("**查询失败：** 已达最大重试次数，未能获得结果。\n\n")
		return
	}

	last := attempts[len(attempts)-1]
	sb.WriteString("**最终执行查询：**\n\n")
	fmt.Fprintf(sb, "```%s\n%s\n```\n\n", codeLang, last.Statement)

	sb.WriteString("**查询结果：**\n\n")
	fmt.Fprintf(sb, "```json\n%s\n```\n\n", prettyPrintJSON(resp))
}

// prettyPrintJSON 将原始 JSON 字符串格式化为带缩进的美观格式；
// 输入不是合法 JSON 时原样返回。
func prettyPrintJSON(rawJSON string) string {
	var data any
	if err := json.Unmarshal([]byte(rawJSON), &data); err != nil {
		return rawJSON
	}

	prettyBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return rawJSON
	}

	return string(prettyBytes)
}

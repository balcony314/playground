package graph

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- stripJSONFence ----

func TestStripJSONFence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"无围栏原样返回", `{"a":1}`, `{"a":1}`},
		{"json 围栏剥离", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"裸围栏剥离", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"首尾空白裁剪", "  \n{\"a\":1}\n  ", `{"a":1}`},
		{"数组内容", "```json\n[\"a\",\"b\"]\n```", `["a","b"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripJSONFence(tt.input); got != tt.want {
				t.Errorf("stripJSONFence(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---- unwrapDSL ----

func TestUnwrapDSL(t *testing.T) {
	t.Run("字符串形式展开为内层 JSON", func(t *testing.T) {
		raw := json.RawMessage(`"{\"query\":{\"match_all\":{}}}"`)
		got, err := unwrapDSL(raw)
		if err != nil {
			t.Fatalf("unwrapDSL err: %v", err)
		}
		want := `{"query":{"match_all":{}}}`
		if got != want {
			t.Errorf("unwrapDSL = %q, want %q", got, want)
		}
	})

	t.Run("对象形式原样返回", func(t *testing.T) {
		raw := json.RawMessage(`{"query":{"match_all":{}}}`)
		got, err := unwrapDSL(raw)
		if err != nil {
			t.Fatalf("unwrapDSL err: %v", err)
		}
		if got != string(raw) {
			t.Errorf("unwrapDSL = %q, want %q", got, string(raw))
		}
	})

	t.Run("空输入报错", func(t *testing.T) {
		if _, err := unwrapDSL(nil); err == nil {
			t.Error("unwrapDSL(nil) should error")
		}
	})
}

// ---- parseDataSources ----

func TestParseDataSources(t *testing.T) {
	t.Run("完整节点名", func(t *testing.T) {
		got, err := parseDataSources(`["clickhouse_searcher"]`)
		if err != nil {
			t.Fatalf("parseDataSources err: %v", err)
		}
		if len(got) != 1 || got[0] != ClickhouseSearcher {
			t.Errorf("got %v, want [%s]", got, ClickhouseSearcher)
		}
	})

	t.Run("子串命中与双命中去重", func(t *testing.T) {
		got, err := parseDataSources(`["clickhouse","clickhouse_searcher"]`)
		if err != nil {
			t.Fatalf("parseDataSources err: %v", err)
		}
		if len(got) != 1 || got[0] != ClickhouseSearcher {
			t.Errorf("got %v, want deduped [%s]", got, ClickhouseSearcher)
		}
	})

	t.Run("带 json 围栏", func(t *testing.T) {
		got, err := parseDataSources("```json\n[\"elasticsearch_searcher\"]\n```")
		if err != nil {
			t.Fatalf("parseDataSources err: %v", err)
		}
		if len(got) != 1 || got[0] != ElasticSearchSearcher {
			t.Errorf("got %v, want [%s]", got, ElasticSearchSearcher)
		}
	})

	t.Run("非法节点名被过滤为空", func(t *testing.T) {
		got, err := parseDataSources(`["mysql","redis"]`)
		if err != nil {
			t.Fatalf("parseDataSources err: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("空字符串元素被过滤", func(t *testing.T) {
		got, err := parseDataSources(`["","clickhouse"]`)
		if err != nil {
			t.Fatalf("parseDataSources err: %v", err)
		}
		if len(got) != 1 || got[0] != ClickhouseSearcher {
			t.Errorf("got %v, want [%s]", got, ClickhouseSearcher)
		}
	})

	t.Run("非 JSON 输入报错", func(t *testing.T) {
		if _, err := parseDataSources(`clickhouse_searcher`); err == nil {
			t.Error("parseDataSources(non-json) should error")
		}
	})
}

// ---- GenerateMarkdownReport ----

func TestGenerateMarkdownReport(t *testing.T) {
	state := &State{
		UserInput: UserInput{
			OldQuestion: "上个月卖得最好的类目",
			Question:    "最近一个月销售额最高的前几个商品类目",
		},
		DataSources: []string{ClickhouseSearcher},
		Query: map[string][]Attempt{
			ClickhouseSearcher: {
				{Statement: "SELECT category FROM orders LIMIT 5", Err: "Table demo.orders does not exist"},
				{Statement: "SELECT category FROM demo.orders LIMIT 5"},
			},
		},
		RespMap: map[string]string{
			ClickhouseSearcher: `[{"category":"electronics"}]`,
		},
	}

	report := GenerateMarkdownReport(state)

	asserts := []struct {
		desc, substr string
	}{
		{"包含原始问题", state.OldQuestion},
		{"包含改写后问题", state.Question},
		{"包含失败尝试语句", "SELECT category FROM orders LIMIT 5"},
		{"包含失败报错", "Table demo.orders does not exist"},
		{"包含最终成功语句", "SELECT category FROM demo.orders LIMIT 5"},
		{"包含格式化结果", `"category": "electronics"`},
	}
	for _, a := range asserts {
		t.Run(a.desc, func(t *testing.T) {
			if !strings.Contains(report, a.substr) {
				t.Errorf("report missing %q\nreport:\n%s", a.substr, report)
			}
		})
	}

	// ES 无尝试记录：不应输出 ES 章节
	t.Run("ES 未涉及不输出章节", func(t *testing.T) {
		if strings.Contains(report, "### Elasticsearch") {
			t.Errorf("report should not contain ES section\nreport:\n%s", report)
		}
	})
}

func TestGenerateMarkdownReportRetryExhausted(t *testing.T) {
	state := &State{
		UserInput:   UserInput{OldQuestion: "q", Question: "q"},
		DataSources: []string{ElasticSearchSearcher},
		Query: map[string][]Attempt{
			ElasticSearchSearcher: {
				{Statement: `{"query":{"match_all":{}}}`, Err: "index not found"},
			},
		},
		// RespMap 无 ES 结果：模拟重试耗尽
	}

	report := GenerateMarkdownReport(state)
	if !strings.Contains(report, "已达最大重试次数") {
		t.Errorf("exhausted retry report should contain failure note, got:\n%s", report)
	}
}

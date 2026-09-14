package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestGetPrompt(t *testing.T) {
	// 四个内置节点 prompt 均可加载且非空
	for _, name := range []string{"input_process", "planner", "clickhouse_searcher", "elasticsearch_searcher"} {
		t.Run(name, func(t *testing.T) {
			content, err := GetPrompt(context.Background(), name)
			if err != nil {
				t.Fatalf("GetPrompt(%q) err: %v", name, err)
			}
			if strings.TrimSpace(content) == "" {
				t.Errorf("GetPrompt(%q) should not be empty", name)
			}
		})
	}
}

func TestGetPromptNotFound(t *testing.T) {
	if _, err := GetPrompt(context.Background(), "no_such_prompt"); err == nil {
		t.Error("GetPrompt(unknown) should error")
	}
}

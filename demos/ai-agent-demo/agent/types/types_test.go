package types

import (
	"strings"
	"testing"
)

func TestConfigWithModel_WithModel(t *testing.T) {
	config := ConfigWithModel("gpt-4o")
	if !strings.Contains(config.SystemPrompt, "gpt-4o") {
		t.Errorf("SystemPrompt 应包含模型名, 实际: %q", config.SystemPrompt)
	}
	if config.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, 期望 10", config.MaxTurns)
	}
}

func TestConfigWithModel_EmptyModel(t *testing.T) {
	config := ConfigWithModel("")
	defaultConfig := DefaultConfig()
	if config.SystemPrompt != defaultConfig.SystemPrompt {
		t.Error("空模型名应与默认配置的 SystemPrompt 一致")
	}
}

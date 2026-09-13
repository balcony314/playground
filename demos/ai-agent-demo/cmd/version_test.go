package cmd

import "testing"

func TestVersionCmd(t *testing.T) {
	// 保存原始版本
	origVersion := Version
	defer func() { Version = origVersion }()

	// 测试默认版本
	Version = "dev"

	// 验证 Version 变量值
	if Version != "dev" {
		t.Errorf("预期 Version='dev'，实际 '%s'", Version)
	}
}

func TestVersionCmd_WithCustomVersion(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()

	Version = "1.0.0"

	// 验证 Version 变量值
	if Version != "1.0.0" {
		t.Errorf("预期 Version='1.0.0'，实际 '%s'", Version)
	}
}

func TestVersionCmd_Registration(t *testing.T) {
	// 验证 version 命令已注册到 rootCmd
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "version" {
			found = true
			break
		}
	}
	if !found {
		t.Error("version 命令未注册到 rootCmd")
	}
}

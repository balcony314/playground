package exec

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfig_DefaultValues(t *testing.T) {
	// 清除环境变量
	os.Unsetenv("EXEC_TIMEOUT")
	os.Unsetenv("EXEC_MAX_OUTPUT")
	os.Unsetenv("EXEC_AUDIT_LOG")

	config := LoadConfig()

	if config.Timeout != DefaultTimeout {
		t.Errorf("默认超时 = %v, 期望 %v", config.Timeout, DefaultTimeout)
	}
	if config.MaxOutput != DefaultMaxOutput {
		t.Errorf("默认最大输出 = %d, 期望 %d", config.MaxOutput, DefaultMaxOutput)
	}
	if config.AuditLogPath != "" {
		t.Errorf("默认审计日志路径 = %q, 期望空字符串", config.AuditLogPath)
	}
}

func TestLoadConfig_FromEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		want    ExecConfig
	}{
		{
			name:    "自定义超时",
			envVars: map[string]string{"EXEC_TIMEOUT": "60"},
			want:    ExecConfig{Timeout: 60 * time.Second, MaxOutput: DefaultMaxOutput},
		},
		{
			name:    "自定义最大输出",
			envVars: map[string]string{"EXEC_MAX_OUTPUT": "512000"},
			want:    ExecConfig{Timeout: DefaultTimeout, MaxOutput: 512000},
		},
		{
			name:    "自定义审计日志路径",
			envVars: map[string]string{"EXEC_AUDIT_LOG": "/tmp/audit.log"},
			want:    ExecConfig{Timeout: DefaultTimeout, MaxOutput: DefaultMaxOutput, AuditLogPath: "/tmp/audit.log"},
		},
		{
			name: "全部自定义",
			envVars: map[string]string{
				"EXEC_TIMEOUT":    "120",
				"EXEC_MAX_OUTPUT": "2048",
				"EXEC_AUDIT_LOG":  "/var/log/exec.log",
			},
			want: ExecConfig{Timeout: 120 * time.Second, MaxOutput: 2048, AuditLogPath: "/var/log/exec.log"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 设置环境变量
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			config := LoadConfig()

			if config.Timeout != tt.want.Timeout {
				t.Errorf("超时 = %v, 期望 %v", config.Timeout, tt.want.Timeout)
			}
			if config.MaxOutput != tt.want.MaxOutput {
				t.Errorf("最大输出 = %d, 期望 %d", config.MaxOutput, tt.want.MaxOutput)
			}
			if config.AuditLogPath != tt.want.AuditLogPath {
				t.Errorf("审计日志路径 = %q, 期望 %q", config.AuditLogPath, tt.want.AuditLogPath)
			}
		})
	}
}

func TestLoadConfig_BoundaryValues(t *testing.T) {
	tests := []struct {
		name      string
		envVars   map[string]string
		wantTimeout time.Duration
		wantOutput int
	}{
		{
			name:        "超时低于最小值",
			envVars:     map[string]string{"EXEC_TIMEOUT": "0"},
			wantTimeout: MinTimeout,
			wantOutput:  DefaultMaxOutput,
		},
		{
			name:        "超时高于最大值",
			envVars:     map[string]string{"EXEC_TIMEOUT": "1000"},
			wantTimeout: MaxTimeout,
			wantOutput:  DefaultMaxOutput,
		},
		{
			name:        "输出低于最小值",
			envVars:     map[string]string{"EXEC_MAX_OUTPUT": "100"},
			wantTimeout: DefaultTimeout,
			wantOutput:  MinMaxOutput,
		},
		{
			name:        "输出高于最大值",
			envVars:     map[string]string{"EXEC_MAX_OUTPUT": "100000000"},
			wantTimeout: DefaultTimeout,
			wantOutput:  MaxMaxOutput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			config := LoadConfig()

			if config.Timeout != tt.wantTimeout {
				t.Errorf("超时 = %v, 期望 %v", config.Timeout, tt.wantTimeout)
			}
			if config.MaxOutput != tt.wantOutput {
				t.Errorf("最大输出 = %d, 期望 %d", config.MaxOutput, tt.wantOutput)
			}
		})
	}
}

func TestLoadConfig_InvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
	}{
		{name: "无效超时", envVars: map[string]string{"EXEC_TIMEOUT": "abc"}},
		{name: "无效输出", envVars: map[string]string{"EXEC_MAX_OUTPUT": "xyz"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			config := LoadConfig()

			// 无效值应使用默认值
			if config.Timeout != DefaultTimeout {
				t.Errorf("无效超时应使用默认值: got %v, want %v", config.Timeout, DefaultTimeout)
			}
			if config.MaxOutput != DefaultMaxOutput {
				t.Errorf("无效输出应使用默认值: got %d, want %d", config.MaxOutput, DefaultMaxOutput)
			}
		})
	}
}

func TestExecConfig_WithWorkDir(t *testing.T) {
	config := ExecConfig{
		Timeout:   60 * time.Second,
		MaxOutput: 2048,
		WorkDir:   "/old/dir",
	}

	newConfig := config.WithWorkDir("/new/dir")

	if newConfig.WorkDir != "/new/dir" {
		t.Errorf("工作目录 = %q, 期望 %q", newConfig.WorkDir, "/new/dir")
	}
	// 原配置不应被修改
	if config.WorkDir != "/old/dir" {
		t.Errorf("原配置被修改: %q", config.WorkDir)
	}
}

func TestExecConfig_WithTimeout(t *testing.T) {
	config := ExecConfig{
		Timeout:   30 * time.Second,
		MaxOutput: 1024,
	}

	tests := []struct {
		input time.Duration
		want  time.Duration
	}{
		{60 * time.Second, 60 * time.Second},
		{0, MinTimeout},
		{time.Hour, MaxTimeout},
	}

	for _, tt := range tests {
		newConfig := config.WithTimeout(tt.input)
		if newConfig.Timeout != tt.want {
			t.Errorf("WithTimeout(%v): 超时 = %v, 期望 %v", tt.input, newConfig.Timeout, tt.want)
		}
	}
}

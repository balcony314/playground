package model

import "testing"

func TestWorkerVerifyToken(t *testing.T) {
	w := Worker{Token: "secret-token-xyz"}
	tests := []struct {
		name     string
		provided string
		want     bool
	}{
		{"token 匹配", "secret-token-xyz", true},
		{"token 不匹配", "wrong-token", false},
		{"空 provided", "", false},
		{"大小写敏感", "secret-token-XYZ", false},
		{"前缀不匹配", "secret-token", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := w.VerifyToken(tt.provided); got != tt.want {
				t.Errorf("VerifyToken(%q) = %v, want %v", tt.provided, got, tt.want)
			}
		})
	}
}

func TestWorkerVerifyToken_EmptyStored(t *testing.T) {
	w := Worker{} // Token 未设置
	if w.VerifyToken("anything") {
		t.Error("空 stored token 应校验失败，got true")
	}
}

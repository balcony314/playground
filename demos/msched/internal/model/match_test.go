package model

import "testing"

func TestSelectorSubset(t *testing.T) {
	tests := []struct {
		name     string
		selector WorkerSelector
		labels   map[string]string
		want     bool
	}{
		{
			name:     "空 selector 通配匹配所有 worker",
			selector: nil,
			labels:   map[string]string{"zone": "sh"},
			want:     true,
		},
		{
			name:     "selector 是 labels 子集即匹配",
			selector: WorkerSelector{"zone": "sh", "gpu": "A100"},
			labels:   map[string]string{"zone": "sh", "gpu": "A100", "arch": "x86"},
			want:     true,
		},
		{
			name:     "labels 缺 selector 声明的维度不匹配",
			selector: WorkerSelector{"zone": "sh", "gpu": "A100"},
			labels:   map[string]string{"zone": "sh"},
			want:     false,
		},
		{
			name:     "维度值不等不匹配",
			selector: WorkerSelector{"zone": "sh"},
			labels:   map[string]string{"zone": "bj"},
			want:     false,
		},
		{
			name:     "单维度声明匹配多标签 worker",
			selector: WorkerSelector{"zone": "sh"},
			labels:   map[string]string{"zone": "sh", "gpu": "A100"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectorSubset(tt.selector, tt.labels)
			if got != tt.want {
				t.Errorf("SelectorSubset(%v, %v) = %v, want %v", tt.selector, tt.labels, got, tt.want)
			}
		})
	}
}

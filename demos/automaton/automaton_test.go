package automaton

import (
	"reflect"
	"testing"
)

func TestCheck(t *testing.T) {
	var tests = []struct {
		name     string
		src      []string
		dest     []byte
		expected []CheckResult
	}{
		{
			name:     "不重叠的多关键词匹配",
			src:      []string{"12321", "abc", "ffdsfs"},
			dest:     []byte("11abc22ffdsfs"),
			expected: []CheckResult{{2, 5, 1}, {7, 13, 2}},
		},
		{
			name:     "前缀包含关系 - 按注册顺序报告",
			src:      []string{"ab", "abc", "abcd"},
			dest:     []byte("abcd"),
			expected: []CheckResult{{0, 2, 0}, {0, 3, 1}, {0, 4, 2}},
		},
		{
			name:     "前缀包含关系 - 反序注册",
			src:      []string{"abcd", "abc", "ab"},
			dest:     []byte("abcd"),
			expected: []CheckResult{{0, 2, 2}, {0, 3, 1}, {0, 4, 0}},
		},
		{
			// Aho-Corasick 在同一结束位置只报告最长匹配，
			// "abcd" 覆盖了 "bcd" 和 "cd" 的结束位置，因此只返回 "abcd"。
			name:     "同结束位置的最长匹配优先",
			src:      []string{"cd", "bcd", "abcd"},
			dest:     []byte("abcd"),
			expected: []CheckResult{{0, 4, 2}},
		},
		{
			// 同上，"abcd" 最长，覆盖了 "bcd" 和 "cd" 的结束位置。
			name:     "同结束位置的最长匹配优先 - 反序",
			src:      []string{"abcd", "bcd", "cd"},
			dest:     []byte("abcd"),
			expected: []CheckResult{{0, 4, 0}},
		},
		{
			name:     "无匹配",
			src:      []string{"ab", "abc", "abcd"},
			dest:     []byte("acbcd"),
			expected: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auto := NewAutomaton(test.src)
			results := auto.Check(test.dest)
			if !reflect.DeepEqual(results, test.expected) {
				t.Errorf("期望 %+v，实际 %+v", test.expected, results)
			}
		})
	}
}

func BenchmarkTemplateReplace(b *testing.B) {
	text := "{{.Ywdxz}} is {{.Count}} of {{.Material}}"
	auto := NewAutomaton([]string{"{{.Material}}", "{{.Count}}", "{{.Ywdxz}}"})

	Text := text
	for i := 0; i < 10000; i++ {
		Text += text
	}
	src := []byte(Text)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		auto.Check(src)
	}
}

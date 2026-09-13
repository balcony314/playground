package eval

import (
	"testing"
)

// Test_lexicalAnalysis: 单元测试
// go test -test.run lexicalAnalysis -cover
// PASS
// coverage: 83.0% of statements
// ok      eval    0.003s
func Test_lexicalAnalysis(t *testing.T) {

	tests := []struct {
		raw               string
		expectedTokenList []*token
		expectedErr       error
	}{
		{
			raw:         "123|",
			expectedErr: UnknownCharErr,
		},
		{
			raw:         "123子",
			expectedErr: UnknownCharErr,
		},
		{
			raw: "",
		},
		{
			raw: "   ",
		},
		{
			raw: "-001",
			expectedTokenList: []*token{
				{
					id: SUB,
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "0",
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "0",
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1",
				},
			},
		},
		{
			raw: " 1234567890 ",
			expectedTokenList: []*token{
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1234567890",
				},
			},
		},
		{
			raw: "  1+2* 1234567890 /  (  0 -     1 ) % 44 ^ 7  ",
			expectedTokenList: []*token{
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1",
				},
				{
					id: ADD,
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "2",
				},
				{
					id: MUL,
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1234567890",
				},
				{
					id: DIV,
				},
				{
					id: LEFT_PAEENTHWESES,
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "0",
				},
				{
					id: SUB,
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1",
				},
				{
					id: RIGHT_PAEENTHWESES,
				},
				{
					id: MOD,
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "44",
				},
				{
					id: EXP,
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "7",
				},
			},
		},
	}

	for _, test := range tests {
		tokenList, err := lexicalAnalysis(test.raw)

		if err != test.expectedErr {
			t.Fatalf("raw:%s, expectedErr:%s, outputErr:%s", test.raw, test.expectedErr.Error(), err.Error())
		}

		if err == nil && !compareTokenList(test.expectedTokenList, tokenList) {
			t.Fatalf("raw:%s, expectedTokenList:%+v, outputTokenList:%+v", test.raw, test.expectedTokenList, tokenList)
		}

	}

}

func compareTokenList(x, y []*token) bool {

	if len(x) != len(y) {
		return false
	}

	for i := 0; i < len(x); i++ {
		if x[i].id != y[i].id {
			return false
		}
		if x[i].raw != y[i].raw {
			return false
		}
	}

	return true
}

// Benchmark_lexicalAnalysis: 性能测试
// go test -bench='Benchmark_lexicalAnalysis' -benchmem
// goos: linux
// goarch: amd64
// pkg: eval
// cpu: Intel(R) Core(TM) i5-8250U CPU @ 1.60GHz
// Benchmark_lexicalAnalysis          23658             46837 ns/op            9401 B/op        135 allocs/op
// PASS
// ok      eval    1.630s
func Benchmark_lexicalAnalysis(b *testing.B) {

	for t := 0; t < b.N; t++ {
		lexicalAnalysis("1000+3123213-44324*453432/55555+0")
	}
}

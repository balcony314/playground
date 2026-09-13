package eval

import (
	"testing"
)

// go test -test.run eval -cover
// PASS
// coverage: 3.7% of statements
// ok      eval    0.003s
func Test_eval(t *testing.T) {
	tests := []struct {
		raw         string
		root        *ast
		expectedVal int64
		expectedErr error
	}{
		{
			raw: "1+2-10",
			root: &ast{
				tokenID: '-',
				leftNode: &ast{
					tokenID: '+',
					leftNode: &ast{
						tokenID: NUMBER,
						value:   1,
					},
					rightNode: &ast{
						tokenID: NUMBER,
						value:   2,
					},
				},
				rightNode: &ast{
					tokenID: NUMBER,
					value:   10,
				},
			},
			expectedVal: -7,
		},
		{
			raw: "(1+2)/3*(10^2*300%(100+10))",
			root: &ast{
				tokenID: '*',
				leftNode: &ast{
					tokenID: '/',
					leftNode: &ast{
						tokenID:   '+',
						leftNode:  &ast{tokenID: NUMBER, value: 1},
						rightNode: &ast{tokenID: NUMBER, value: 2},
					},
					rightNode: &ast{tokenID: NUMBER, value: 3},
				},
				rightNode: &ast{
					tokenID: '%',
					leftNode: &ast{
						tokenID: '*',
						leftNode: &ast{
							tokenID:   '^',
							leftNode:  &ast{tokenID: NUMBER, value: 10},
							rightNode: &ast{tokenID: NUMBER, value: 2},
						},
						rightNode: &ast{tokenID: NUMBER, value: 300},
					},
					rightNode: &ast{
						tokenID:   '+',
						leftNode:  &ast{tokenID: NUMBER, value: 100},
						rightNode: &ast{tokenID: NUMBER, value: 10},
					},
				},
			},
			expectedVal: 80,
		},
	}

	for _, test := range tests {
		ret, err := eval(test.root)
		if err != test.expectedErr {
			t.Fatalf("raw：%s, err: %+v, expectedErr: %+v", test.raw, err, test.expectedErr)
		}
		if ret != test.expectedVal {
			t.Fatalf("raw: %s, val: %d,expectedVal: %d ", test.raw, ret, test.expectedVal)
		}
	}
}

// go test -bench='Benchmark_eval' -benchmem
// goos: linux
// goarch: amd64
// pkg: eval
// cpu: Intel(R) Core(TM) i5-8250U CPU @ 1.60GHz
// Benchmark_eval   7869852               149.1 ns/op             0 B/op          0 allocs/op
// PASS
// ok      eval    1.331s
func Benchmark_eval(b *testing.B) {

	ast := &ast{
		tokenID: '*',
		leftNode: &ast{
			tokenID: '/',
			leftNode: &ast{
				tokenID:   '+',
				leftNode:  &ast{tokenID: NUMBER, value: 1},
				rightNode: &ast{tokenID: NUMBER, value: 2},
			},
			rightNode: &ast{tokenID: NUMBER, value: 3},
		},
		rightNode: &ast{
			tokenID: '%',
			leftNode: &ast{
				tokenID: '*',
				leftNode: &ast{
					tokenID:   '^',
					leftNode:  &ast{tokenID: NUMBER, value: 10},
					rightNode: &ast{tokenID: NUMBER, value: 2},
				},
				rightNode: &ast{tokenID: NUMBER, value: 300},
			},
			rightNode: &ast{
				tokenID:   '+',
				leftNode:  &ast{tokenID: NUMBER, value: 100},
				rightNode: &ast{tokenID: NUMBER, value: 10},
			},
		},
	}

	for i := 0; i < b.N; i++ {
		eval(ast)
	}
}

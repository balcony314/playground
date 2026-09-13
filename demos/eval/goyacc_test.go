package eval

import (
	"fmt"
	"testing"
)

// go test -test.run yyParse -cover
// PASS
// coverage: 24.5% of statements
// ok      eval    0.003s
func Test_yyParse(t *testing.T) {
	tests := []struct {
		raw         string
		tokenList   []*token
		expectedAst *ast
		expectedErr error
	}{
		{
			raw: "1+2-1",
			tokenList: []*token{
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1",
				},
				{
					id: '+',
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "2",
				},
				{
					id: '-',
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "10",
				},
			},
			expectedAst: &ast{
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
		},
		{
			raw: "1+2*10",
			tokenList: []*token{
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1",
				},
				{
					id: '+',
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "2",
				},
				{
					id: '*',
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "10",
				},
			},
			expectedAst: &ast{
				tokenID: '+',
				leftNode: &ast{
					tokenID: NUMBER,
					value:   1,
				},
				rightNode: &ast{
					tokenID: '*',
					leftNode: &ast{
						tokenID: NUMBER,
						value:   2,
					},
					rightNode: &ast{
						tokenID: NUMBER,
						value:   10,
					},
				},
			},
		},
		{
			raw: "(1+2)*10",
			tokenList: []*token{
				{
					id: '(',
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "1",
				},
				{
					id: '+',
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "2",
				},
				{
					id: ')',
				},
				{
					id: '*',
				},
				{
					id:  NON_NEGATIVE_INTEGER,
					raw: "10",
				},
			},
			expectedAst: &ast{
				tokenID: '*',
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
		},
		{
			raw: "(1+2)/0*(10^2*300%(100+10))",
			tokenList: []*token{
				{id: '('},
				{id: NON_NEGATIVE_INTEGER, raw: "1"},
				{id: '+'},
				{id: NON_NEGATIVE_INTEGER, raw: "2"},
				{id: ')'},
				{id: '/'},
				{id: NON_NEGATIVE_INTEGER, raw: "0"},
				{id: '*'},
				{id: '('},
				{id: NON_NEGATIVE_INTEGER, raw: "10"},
				{id: '^'},
				{id: NON_NEGATIVE_INTEGER, raw: "2"},
				{id: '*'},
				{id: NON_NEGATIVE_INTEGER, raw: "300"},
				{id: '%'},
				{id: '('},
				{id: NON_NEGATIVE_INTEGER, raw: "100"},
				{id: '+'},
				{id: NON_NEGATIVE_INTEGER, raw: "10"},
				{id: ')'},
				{id: ')'},
			},
			expectedAst: &ast{
				tokenID: '*',
				leftNode: &ast{
					tokenID: '/',
					leftNode: &ast{
						tokenID:   '+',
						leftNode:  &ast{tokenID: NUMBER, value: 1},
						rightNode: &ast{tokenID: NUMBER, value: 2},
					},
					rightNode: &ast{tokenID: NUMBER, value: 0},
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
		},
	}

	for i := 0; i < len(tests); i++ {
		lex := &simpleLex{tokenList: tests[i].tokenList}
		yyParse(lex)

		if lex.err != nil {
			fmt.Println(lex.err.Error())
			continue
		}

		if !compareAST(lex.ast, tests[i].expectedAst) {
			println("output============")
			printlnAST(lex.ast)
			println("expect============")
			printlnAST(tests[i].expectedAst)
			t.Fatalf("raw: %s,", tests[i].raw)
		}
	}
}

func compareAST(x, y *ast) bool {

	if x == nil && y == nil {
		return true
	}

	if !(x != nil && y != nil) {
		return false
	}

	if x.tokenID != y.tokenID {
		return false
	}

	if x.value != y.value {
		return false
	}

	if !compareAST(x.leftNode, y.leftNode) {
		return false
	}

	if !compareAST(x.rightNode, y.rightNode) {
		return false
	}

	return true
}

// go test -bench='Benchmark_yyParse' -benchmem
// goos: linux
// goarch: amd64
// pkg: eval
// cpu: Intel(R) Core(TM) i5-8250U CPU @ 1.60GHz
// Benchmark_yyParse         298640              3710 ns/op             592 B/op         18 allocs/op
// PASS
// ok      eval    1.153s
func Benchmark_yyParse(b *testing.B) {

	tokenList := []*token{
		{id: '('},
		{id: NON_NEGATIVE_INTEGER, raw: "1"},
		{id: '+'},
		{id: NON_NEGATIVE_INTEGER, raw: "2"},
		{id: ')'},
		{id: '/'},
		{id: NON_NEGATIVE_INTEGER, raw: "0"},
		{id: '*'},
		{id: '('},
		{id: NON_NEGATIVE_INTEGER, raw: "10"},
		{id: '^'},
		{id: NON_NEGATIVE_INTEGER, raw: "2"},
		{id: '*'},
		{id: NON_NEGATIVE_INTEGER, raw: "300"},
		{id: '%'},
		{id: '('},
		{id: NON_NEGATIVE_INTEGER, raw: "100"},
		{id: '+'},
		{id: NON_NEGATIVE_INTEGER, raw: "10"},
		{id: ')'},
		{id: ')'},
	}

	for t := 0; t < b.N; t++ {
		yyParse(&simpleLex{tokenList: tokenList})

	}
}

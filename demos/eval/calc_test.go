package eval

import "testing"

// go test -test.run Calc -cover
// PASS
// coverage: 65.1% of statements
// ok      eval    0.003s
func Test_Calc(t *testing.T) {
	tests := []struct {
		raw         string
		expectedRet int64
		expectedErr error
	}{
		{
			raw:         "1+2",
			expectedRet: 3,
		},
		{
			raw:         "(1+2)/3*(10^2*300%(100+10))",
			expectedRet: 80,
		},
	}

	for _, test := range tests {
		v, r := Calc(test.raw)
		if r != test.expectedErr {
			t.Fatalf("raw: %s, expectErr:%+v, Err:%+v", test.raw, test.expectedErr, r)
		}
		if v != test.expectedRet {
			t.Fatalf("raw: %s, expectRet:%+v, ret:%+v", test.raw, test.expectedRet, v)
		}
	}
}

// go test -bench='Benchmark_Calc' -benchmem
// goos: linux
// goarch: amd64
// pkg: eval
// cpu: Intel(R) Core(TM) i5-8250U CPU @ 1.60GHz
// Benchmark_Calc     13041             91513 ns/op           18244 B/op        264 allocs/op
// PASS
// ok      eval    2.131s
func Benchmark_Calc(b *testing.B) {

	for i := 0; i < b.N; i++ {
		Calc("(1+2)/3*(10^2*300%(100+10))")
	}
}

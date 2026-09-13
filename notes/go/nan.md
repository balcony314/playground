# 关于math.NaN() 问题记录

## 问题
看 map  源码看到一个测试case,__为什么 k == k__
```go
func testMapNan(t *testing.T, m map[float64]int) {
	if len(m) != 3 {
		t.Error("length wrong")
	}
	s := 0
	for k, v := range m {
		if k == k {   // why ?????
			t.Error("nan disappeared")
		}
		if (v & (v - 1)) != 0 {
			t.Error("value wrong")
		}
		s |= v
	}
	if s != 7 {
		t.Error("values wrong")
	}
}
// nan is a good test because nan != nan, and nan has
// a randomized hash value.
func TestMapAssignmentNan(t *testing.T) {
	m := make(map[float64]int, 0)
	nan := math.NaN()

	// Test assignment.
	m[nan] = 1
	m[nan] = 2
	m[nan] = 4
	testMapNan(t, m)
}
```
## 结论
eg:
```
func main() {
    m := make(map[float64]int)
    m[1.4] = 1
    m[2.4] = 2
    m[math.NaN()] = 3
    m[math.NaN()] = 3
    for k, v := range m {
        fmt.Printf("[%v, %d] ", k, v)
    }
    fmt.Printf("\nk: %v, v: %d\n", math.NaN(), m[math.NaN()])
    fmt.Printf("k: %v, v: %d\n", 2.400000000001, m[2.400000000001])
    fmt.Printf("k: %v, v: %d\n", 2.4000000000000000000000001, m[2.4000000000000000000000001])
    fmt.Println(math.NaN() == math.NaN())
}
```
输出：
```
[2.4, 2] [NaN, 3] [NaN, 3] [1.4, 1] 
k: NaN, v: 0
k: 2.400000000001, v: 0
k: 2.4, v: 2
false
```
结论：
	NaN 不等于 NaN,__为什么NaN!=NaN呢？__

# skipList

Go 实现的跳表（Skip List）数据结构，按 score 排序，支持高效的插入、查找和删除。

## 特性

- O(log n) 的插入、查找、删除
- 按 score（uint64）排序，相同 score 自动覆盖旧值
- 支持按排序位置索引查找
- 最大 32 层，晋升概率 p=0.25

## 安装

```bash
go get github.com/balcony314/skiplist
```

## 使用

```go
package main

import (
    "fmt"
    skiplist "github.com/balcony314/skiplist"
)

func main() {
    skl := skiplist.Create()

    skl.Set(3, "three")
    skl.Set(1, "one")
    skl.Set(2, "two")

    fmt.Println(skl.GetByScore(2))  // "two"
    fmt.Println(skl.GetByIndex(0))  // "one"（按 score 排序后的第 0 个）
    fmt.Println(skl.Len())          // 3

    skl.DelByScore(2)
    fmt.Println(skl.Len())          // 2
}
```

## API

| 方法 | 说明 | 复杂度 |
|------|------|--------|
| `Create() SkipLister` | 创建跳表实例 | O(1) |
| `Set(score, val)` | 插入或更新元素 | O(log n) |
| `GetByScore(score) interface{}` | 按 score 查找 | O(log n) |
| `GetByIndex(index) interface{}` | 按排序位置查找 | O(n) |
| `DelByScore(score)` | 按 score 删除 | O(log n) |
| `Len() int` | 返回元素数量 | O(1) |

## 基准测试

```bash
go test -bench=. -benchmem -benchtime=10s
```

对比 map 的参考数据：

```
BenchmarkSkiplist    111519    827404 ns/op    304 B/op    2 allocs/op
BenchmarkMap       40033868      344 ns/op     98 B/op    0 allocs/op
```

跳表作为有序数据结构，单次操作比 map 慢，但支持范围查询和有序遍历。

## License

[MIT](LICENSE)

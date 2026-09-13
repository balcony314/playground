# B-Tree

Go 语言实现的 B 树数据结构，支持泛型键值存储。

## 特性

- 支持 `int` 类型键和任意类型值
- 实现插入、删除、查找、遍历操作
- 所有节点预分配最大容量，避免动态扩容
- 注释标记磁盘 I/O 位置，便于扩展为持久化实现

## 安装

```bash
go get github.com/balcony314/B-Tree
```

## 使用

```go
package main

import (
    "fmt"
    bt "github.com/balcony314/B-Tree"
)

func main() {
    // 创建 B 树，最小度数 t=2
    tree := bt.GenBT(2)

    // 插入键值对
    tree.Set(1, "one")
    tree.Set(2, "two")
    tree.Set(3, "three")

    // 查找
    if val, ok := tree.Get(2); ok {
        fmt.Println(val) // two
    }

    // 删除
    tree.Del(2)

    // 遍历（返回有序键值列表）
    keys, values := tree.Print()
    fmt.Println(keys, values)
}
```

## API

### 构造函数

```go
func GenBT(t int) BT
```

创建 B 树实例，`t` 为最小度数（t >= 2）。

### BT 接口

| 方法 | 说明 |
|------|------|
| `Set(key int, value interface{})` | 插入或更新键值对 |
| `Del(key int)` | 删除指定键 |
| `Get(key int) (value interface{}, ok bool)` | 查找键对应的值 |
| `Print() (keyList []int, valueList []interface{})` | 中序遍历，返回有序键值切片 |

## 测试

```bash
go test ./...
```

## 许可证

MIT

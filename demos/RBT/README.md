# RBT - 红黑树

Go 语言实现的红黑树数据结构，采用 CLRS 教科书的哨兵节点方案。

## 特性

- O(log n) 的查找、插入和删除操作
- 使用哨兵节点简化边界处理
- 支持任意类型的值（通过 `interface{}`）
- 迭代中序遍历返回有序键值对

## 安装

```bash
go get github.com/balcony314/RBT
```

## 使用示例

```go
package main

import (
    "fmt"
    "github.com/balcony314/RBT"
)

func main() {
    // 创建红黑树
    tree := rbt.GenRBT()

    // 插入键值对
    tree.Set(1, "one")
    tree.Set(2, "two")
    tree.Set(3, "three")

    // 查找值
    if v, ok := tree.Get(2); ok {
        fmt.Println("Found:", v) // 输出: Found: two
    }

    // 删除键
    tree.Del(2)

    // 中序遍历（返回有序键值对）
    keys, values := tree.Print()
    fmt.Println("Keys:", keys)     // 输出: Keys: [1 3]
    fmt.Println("Values:", values) // 输出: Values: [one three]
}
```

## API

### `GenRBT() RBT`

创建并返回一个新的空红黑树。

### `RBT` 接口

- `Set(key int, value interface{})` - 插入或更新键值对
- `Get(key int) (value interface{}, ok bool)` - 查找指定键的值
- `Del(key int)` - 删除指定键
- `Print() (keyList []int, valueList []interface{})` - 中序遍历返回有序键值对

## 测试

```bash
go test ./...
```

## 实现细节

红黑树是一种自平衡二叉搜索树，具有以下性质：

1. 每个节点是红色或黑色
2. 根节点是黑色
3. 所有叶子节点（NIL）是黑色
4. 红色节点的两个子节点都是黑色
5. 从根节点到叶子节点的所有路径都包含相同数量的黑色节点

本实现使用哨兵节点代替 nil 指针，简化了边界条件的处理。

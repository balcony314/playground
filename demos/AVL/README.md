# AVL

Go 语言实现的 AVL 自平衡二叉搜索树。

## 安装

```bash
go get avl
```

## 使用示例

```go
package main

import (
    "avl"
    "fmt"
)

func main() {
    tree := avl.GenAVL()

    // 插入键值对
    tree.Set(1, "one")
    tree.Set(2, "two")
    tree.Set(3, "three")

    // 查找值
    if value, ok := tree.Get(2); ok {
        fmt.Println("找到:", value) // 输出: 找到: two
    }

    // 删除键
    tree.Del(2)

    // 中序遍历（返回有序的键值列表）
    keys, values := tree.Print()
    fmt.Println("键:", keys)     // 输出: 键: [1 3]
    fmt.Println("值:", values)   // 输出: 值: [one three]
}
```

## API 文档

### 构造函数

```go
func GenAVL() AVL
```

创建并返回一个新的 AVL 树实例。

### 接口方法

#### Set

```go
Set(key int, value interface{})
```

插入或更新键值对。如果键已存在，则更新其值。

#### Get

```go
Get(key int) (value interface{}, ok bool)
```

根据键查找值。如果键存在，返回对应的值和 `true`；否则返回 `nil` 和 `false`。

#### Del

```go
Del(key int)
```

删除指定键及其对应的值。如果键不存在，不执行任何操作。

#### Print

```go
Print() (keyList []int, valueList []interface{})
```

中序遍历树，返回两个切片：有序的键列表和对应的值列表。

## 平衡原理

AVL 树通过旋转操作保持平衡，确保任意节点的左右子树高度差不超过 1。

### 旋转类型

1. **左旋转 (RR 情况)**
 ```
       a                c
     /   \            /   \
    b     c    =>    a     e
         / \        / \   / \
        d   e      b   d f   g
           / \
          f   g
 ```

2. **右旋转 (LL 情况)**
 ```
       a                b
     /   \            /   \
    b     c    =>    d     a
   / \              / \   / \
  d   e            f   g e   c
 / \
f   g
 ```

3. **左右旋转 (LR 情况)**
 ```
       a                a                e
     /   \            /   \            /   \
    b     c    =>    e     c    =>    b     a
   / \              / \              / \   / \
  d   e            b   g            d   f g   c
     / \          / \
    f   g        d   f
 ```

4. **右左旋转 (RL 情况)**
```
    a                a                d
  /   \            /   \            /   \
 b     c    =>    b     d    =>    a     c
      / \              / \        / \   / \
     d   e            f   e      b   f g   h
    / \                  / \
   f   g                g   h
```

## 测试

运行测试：

```bash
go test -v ./...
```

测试覆盖以下场景：
- 空树操作
- 批量插入（0-100）
- 查找存在的键
- 查找不存在的键
- 删除键后验证
- 重新插入已删除的键
- 更新已存在键的值

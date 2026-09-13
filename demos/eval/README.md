# eval

Go 语言实现的数学表达式求值库，支持非负整数和基本运算符。

## 功能特性

- 支持运算符：`+` `-` `*` `/` `%` `^`（加、减、乘、除、取模、幂）
- 支持括号分组 `()`
- 支持非负整数
- 返回 `int64` 结果

## 安装

```bash
go get github.com/balcony314/eval
```

## 使用方法

```go
package main

import (
    "fmt"
    "github.com/balcony314/eval"
)

func main() {
    result, err := eval.Calc("1+2*3")
    if err != nil {
        fmt.Println("Error:", err)
        return
    }
    fmt.Println("Result:", result) // 输出: 7
}
```

## 示例

```go
eval.Calc("1+2")                              // 3
eval.Calc("(1+2)/3*(10^2*300%(100+10))")       // 80
eval.Calc("2^10")                              // 1024
eval.Calc("100%3")                             // 1
```

## 架构

经典的三阶段编译/解释流水线：

```
输入字符串 → 词法分析(lexer) → 语法分析(parser) → AST 求值 → int64 结果
```

- **词法分析器**：`lex.nex`（nex 语法规范）→ 生成 `lex.nn.go`
- **语法分析器**：`goyacc.y`（yoyacc 语法规范）→ 生成 `y.go`
- **AST 求值**：`ast.go` 中的递归树遍历求值器

## 代码生成

`lex.nn.go` 和 `y.go` 是自动生成的文件，请勿手动编辑。

### 生成词法分析器 (lex.nn.go)

```bash
# 安装 nex 工具
go get github.com/blynn/nex

# 从 lex.nex 生成 lex.nn.go
nex -o lex.nn.go lex.nex
```

### 生成语法分析器 (y.go)

```bash
# 安装 goyacc 工具
go get golang.org/x/tools/cmd/goyacc

# 从 goyacc.y 生成 y.go
goyacc -o y.go goyacc.y
```

## 开发

```bash
# 构建
go build ./...

# 测试
go test ./...

# 运行所有测试并查看覆盖率
go test -cover

# 性能测试
go test -bench='Benchmark_Calc' -benchmem
```

## 运算符优先级

| 优先级 | 运算符 | 结合性 |
|--------|--------|--------|
| 低     | `+` `-` | 左结合 |
| 高     | `*` `/` `%` `^` | 左结合 |

## 许可证

MIT License

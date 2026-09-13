# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在本仓库中工作时提供指导。

## 项目概述

Go 语言实现的红黑树 (RBT)。模块名：`rbt`，需要 Go 1.16+。

## 常用命令

```bash
go build ./...         # 构建
go test ./...          # 运行测试
go test -v             # 详细输出
go test -run TestName  # 运行单个测试
```

## 架构

单文件实现 (`rbt.go`)，采用 CLRS 教科书的哨兵节点方案。

**公共 API：**
- `GenRBT() RBT` — 构造函数
- `RBT` 接口：`Set(key int, value interface{})`、`Get(key int) (interface{}, bool)`、`Del(key int)`、`Print() ([]int, []interface{})`

**关键设计：**
- 键类型仅支持 `int`；值使用 `interface{}`
- 使用哨兵节点 `null`（黑色）代替 nil 指针
- 重复键插入时就地更新值
- `Print()` 通过迭代中序遍历返回有序键/值

**依赖：** 仅 `github.com/stretchr/testify` 用于测试断言。

**代码风格：** 实现中的注释使用中文，解释红黑树的各种修正情况。

**许可证：** MIT License

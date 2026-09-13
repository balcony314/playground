# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Go 实现的跳表（Skip List）数据结构库，按 score（uint64）排序，支持 O(log n) 的查找和插入。

## 常用命令

```bash
go test ./...                              # 运行测试
go test -v                                 # 详细输出测试
go test -bench=. -benchmem -benchtime=10s  # 运行基准测试
go build ./...                             # 编译检查
go vet ./...                               # 静态检查
```

## 架构

单包库，核心文件：

- `list.go` — 全部实现，导出 `SkipLister` 接口和 `Create()` 构造函数
- `list_test.go` — 单元测试和基准测试（对比 map）

关键设计：
- `score`（uint64）作为排序键，相同 score 会覆盖旧值
- `randomLevel()` 使用 p=0.25 的几何分布，最大 32 层
- `GetByScore` O(log n)，`GetByIndex` O(n)（遍历第 0 层）
- 依赖 `github.com/stretchr/testify` 仅用于测试断言

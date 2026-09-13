# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

纯 Go 实现的 AVL 自平衡二叉搜索树库，模块名 `avl`，Go 1.16+。唯一外部依赖是 `testify`（仅测试使用）。

## 常用命令

- 构建：`go build ./...`
- 测试：`go test -v ./...`
- 静态检查：`go vet ./...`

## 架构

单包（`package avl`），无子目录，两个源文件：

- `avl.go` — 全部实现
- `avl_test.go` — 测试

**公开 API**（`AVL` 接口，`avl.go:7`）：
- `GenAVL() AVL` — 唯一构造函数（`avl.go:281`）
- `Set(key int, value interface{})` — 插入/更新
- `Del(key int)` — 删除
- `Get(key int) (value interface{}, ok bool)` — 查找
- `Print() (keyList []int, valueList []interface{})` — 中序遍历

**内部结构**：`node` 结构体（`avl.go:14`）持有 left/right 子节点、key、height、value。所有操作递归实现。

**旋转逻辑**（平衡核心）：`leftSpin`/`rightSpin` 单旋转 → `LL_logic`/`RR_logic`/`LR_logic`/`RL_logic` 四种情况 → `checkBalance` 自动判断并执行。

## 注意事项

- key 类型为 `int`，value 为 `interface{}`
- 注释为中文
- 无 linter 配置，修改后用 `go vet` 检查

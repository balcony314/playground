# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Aho-Corasick 多模式字符串匹配算法的 Go 实现。用于在一段文本中同时查找多个关键词，返回所有匹配位置及对应的 TokenID。

## 常用命令

```bash
# 运行测试
go test ./...

# 运行单个测试
go test -run TestCheck1

# 运行 benchmark
go test -bench=.

# 检查编译
go build ./...
```

## 架构

- `Automaton` 接口：对外暴露 `Check(src []byte) []CheckResult` 方法
- `engine` 结构体：核心实现，包含前缀树 (`rootNode`) 和词表映射 (`wordMap`)
- `node` 结构体：Trie 节点，包含 `fail` 指针（失配指针）、`nextNodeMap`（子节点）、`tokenID`（匹配到的词 ID）

构建流程：`NewAutomaton` → `buildPrefixTree`（构建 Trie）→ `buildMismatchPointer`（BFS 构建失配指针）

`CheckResult` 包含 `StartIndex`、`EndIndex`（左闭右开）、`TokenID`。

## 测试说明

测试文件中标注了 `//bug?` 的用例：当多个模式存在前缀包含关系时，较短模式可能不会被报告。这是当前实现的已知行为。

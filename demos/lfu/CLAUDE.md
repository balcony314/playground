# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Go 语言实现的 LFU (Least Frequently Used) 缓存库，包名 `lfu`，Go 1.14+。纯库，无 main 包。

## 常用命令

```bash
go build ./...          # 构建
go test -v ./...        # 运行全部测试（依赖 testify）
go vet ./...            # 静态检查
```

## 架构

双哈希表 + 双向链表实现 O(1) Get/Set：

- `LFU` 接口：公开 API，仅 `Set(k, v)` 和 `Get(k)` 两个方法
- `New(cap int) LFU`：构造函数
- `hash1`（`map[string]*listNode`）：key → 节点映射，O(1) 查找
- `hash2`（`map[int]*twoWayList`）：频率 → 同频节点链表，用于淘汰
- `minCount`：跟踪最小频率，淘汰时直接定位候选链表

淘汰策略：驱逐 `hash2[minCount]` 链表尾部节点（最低频中最久未使用）。


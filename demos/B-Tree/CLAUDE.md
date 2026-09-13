# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Go implementation of a B-tree data structure (package `bt`). The B-tree supports generic key-value storage with `int` keys and `interface{}` values.

## Build & Test Commands

```bash
go test ./...          # Run all tests
go test -run TestName  # Run a single test
go vet ./...           # Static analysis
```

## Architecture

**`tree.go`** — Core B-tree implementation:
- `BT` interface: `Set`, `Del`, `Get`, `Print`
- `bTree` struct: holds root node and minimum degree `t`
- `node` struct: internal/leaf node with keys, values, children pointers
- Key algorithms: `insert`/`insertNonfull` (with split), `delete`/`deleteNonOne` (with merge/borrow), `search`

**`bt_test.go`** — Tests using testify/assert for degree values `t=2` and `t=10`.

## Design Notes

- All nodes pre-allocate slices to maximum capacity (`2t-1` keys, `2t` children)
- `Print()` returns sorted key/value slices via in-order traversal
- Constructor: `GenBT(t int) BT` where `t >= 2` is the minimum degree
- Comments with `//Disk-Read` / `//Disk-Write` indicate where disk I/O would occur in a persistent implementation

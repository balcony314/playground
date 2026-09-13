# lfu

Go 语言实现的 LFU (Least Frequently Used) 缓存淘汰算法库。

## 特性

- O(1) 时间复杂度的 Get/Set 操作
- 双哈希表 + 双向链表实现
- 线程不安全（如需并发使用请自行加锁）

## 安装

```bash
go get github.com/balcony314/lfu
```

## 使用

```go
package main

import "lfu"

func main() {
    // 创建容量为 100 的缓存
    cache := lfu.New(100)

    // 设置缓存
    cache.Set("key", "value")

    // 获取缓存
    v, ok := cache.Get("key")
    if ok {
        fmt.Println(v) // "value"
    }
}
```

## API

### `New(cap int) LFU`

创建指定容量的 LFU 缓存实例。容量为 0 或负数时，缓存不可用。

### `LFU.Set(k string, v interface{})`

设置缓存键值对。若键已存在则更新值并提升访问频率。缓存满时自动淘汰最低频中最久未使用的节点。

### `LFU.Get(k string) (v interface{}, ok bool)`

获取缓存值。若存在则提升访问频率并返回值和 `true`，否则返回 `nil` 和 `false`。

## 实现原理

使用两个哈希表和双向链表实现：

- `hash1`（`map[string]*listNode`）：key → 节点映射，O(1) 查找
- `hash2`（`map[int]*twoWayList`）：频率 → 同频节点链表，用于淘汰
- `minCount`：跟踪最小频率，淘汰时直接定位候选链表

淘汰策略：驱逐 `hash2[minCount]` 链表尾部节点（最低频中最久未使用）。

## License

MIT License - 详见 [LICENSE](LICENSE)

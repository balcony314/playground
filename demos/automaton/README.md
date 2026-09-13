# automaton

Aho-Corasick 多模式字符串匹配算法的 Go 实现，用于在一段文本中同时高效查找多个关键词。

## 安装

```bash
go get github.com/balcony314/automaton
```

## 使用

```go
package main

import (
    "fmt"
    "automaton"
)

func main() {
    // 创建匹配引擎，传入关键词列表
    auto := automaton.NewAutomaton([]string{"he", "she", "his", "hers"})

    // 在文本中查找所有关键词
    results := auto.Check([]byte("ahishers"))
    for _, r := range results {
        fmt.Printf("匹配关键词[%d]，位置 [%d, %d)\n", r.TokenID, r.StartIndex, r.EndIndex)
    }
}
```

## API

### `NewAutomaton(words []string) Automaton`

创建匹配引擎实例。`words` 为关键词列表，返回结果中的 `TokenID` 即为关键词在列表中的下标。

### `Check(src []byte) []CheckResult`

在 `src` 中查找所有已注册的关键词，返回匹配结果切片。

`CheckResult` 包含：
- `StartIndex` — 匹配起始位置（包含）
- `EndIndex` — 匹配结束位置（不包含）
- `TokenID` — 关键词 ID

## License

[MIT](LICENSE)
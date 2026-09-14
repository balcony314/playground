// Package prompt 以 Markdown 文件维护各子图的系统 Prompt，编译期 go:embed 进二进制。
// 改 Prompt 只需改对应 md 文件，无需改代码、无需额外构建步骤。
package prompt

import (
	"context"
	"embed"
	"fmt"
)

//go:embed input_process.md
//go:embed planner.md
//go:embed clickhouse_searcher.md
//go:embed elasticsearch_searcher.md
var f embed.FS

// GetPrompt 按文件名（不含 .md 扩展名）读取 Prompt 内容。
// 子图 load 节点把主图传入的节点名字符串透传到这里——节点名与 md 文件名一一对应。
func GetPrompt(ctx context.Context, input string) (string, error) {
	data, err := f.ReadFile(input + ".md")
	if err != nil {
		return "", fmt.Errorf("read prompt file %q: %w", input, err)
	}

	return string(data), nil
}

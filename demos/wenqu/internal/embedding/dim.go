// Package embedding 提供 embedding 模型的运行时探测工具。
package embedding

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/embedding"
)

// GetModelDim 启动时用一条样例文本探测 embedding 模型维度。
// Milvus collection 的向量维度必须与模型一致且创建后不可修改，
// 因此维度不能写死，须在建立向量库前动态获取。
func GetModelDim(embedder embedding.Embedder) (int64, error) {
	embeddings, err := embedder.EmbedStrings(context.Background(), []string{"test"})
	if err != nil {
		return 0, fmt.Errorf("GetModelDim: %w", err)
	}

	if len(embeddings) == 0 {
		return 0, fmt.Errorf("GetModelDim: no embedding found")
	}

	return int64(len(embeddings[0])), nil
}

package db

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

// MilvusConfig Milvus 连接配置
type MilvusConfig struct {
	Address string
	APIKey  string
}

// NewMilvus 创建 Milvus 客户端并切换到指定 database，失败返回错误
func NewMilvus(cfg MilvusConfig, database string) (*milvusclient.Client, error) {
	ctx := context.Background()
	client, err := milvusclient.New(ctx, &milvusclient.ClientConfig{
		Address: cfg.Address,
		APIKey:  cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("init milvus client: %w", err)
	}

	if database != "" {
		if err := client.UseDatabase(ctx, milvusclient.NewUseDatabaseOption(database)); err != nil {
			return nil, fmt.Errorf("use milvus database %s: %w", database, err)
		}
	}
	slog.Info("milvus client ready", slog.String("address", cfg.Address), slog.String("database", database))

	return client, nil
}

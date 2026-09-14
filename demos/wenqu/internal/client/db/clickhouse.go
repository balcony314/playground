// Package db 提供外部数据源客户端的统一构造。
// 连接信息全部来自启动 flag / 环境变量，无硬编码凭证。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// ClickHouseConfig ClickHouse 连接配置
type ClickHouseConfig struct {
	Hosts    []string // 形如 "host:port" 的地址列表
	Username string
	Password string
	Database string
}

// ClickHouseClient ClickHouse 查询客户端。
// 状态图的 clickhouse_searcher 节点用它执行 LLM 生成的只读 SQL。
type ClickHouseClient struct {
	db *sql.DB
}

// NewClickHouse 建立连接并 ping 验证，失败立即返回错误（启动期暴露配置问题）。
func NewClickHouse(cfg ClickHouseConfig) (*ClickHouseClient, error) {
	conn := clickhouse.OpenDB(&clickhouse.Options{
		Addr: cfg.Hosts,
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		DialTimeout: 5 * time.Second,
	})
	conn.SetMaxOpenConns(5)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	slog.Info("clickhouse client ready", slog.Any("hosts", cfg.Hosts), slog.String("database", cfg.Database))

	return &ClickHouseClient{db: conn}, nil
}

// Close 关闭底层连接池
func (c *ClickHouseClient) Close() error {
	return c.db.Close()
}

// QueryToMaps 执行只读查询并扫描为"列名 → 值"的通用行结构，
// 供直接 JSON 序列化后写入查询报告。调用方需先经 sqlguard 校验语句。
func (c *ClickHouseClient) QueryToMaps(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query clickhouse: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.WarnContext(ctx, "close clickhouse rows", slog.Any("error", closeErr))
		}
	}()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("clickhouse rows columns: %w", err)
	}

	results := make([]map[string]any, 0)
	for rows.Next() {
		// database/sql 的通用扫描：每列扫描到 any 指针
		values := make([]any, len(cols))
		scanArgs := make([]any, len(cols))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, fmt.Errorf("clickhouse scan row: %w", err)
		}

		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeSQLValue(values[i])
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse iterate rows: %w", err)
	}

	return results, nil
}

// normalizeSQLValue 把驱动返回的值转换为 JSON 可序列化类型
func normalizeSQLValue(v any) any {
	switch val := v.(type) {
	case []byte:
		return string(val)
	default:
		return v
	}
}

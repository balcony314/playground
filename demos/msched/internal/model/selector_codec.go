// Package model - selector_codec.go 提供 WorkerSelector 的 JSONB 双向映射。
//
// WorkerSelector 同时作 Task.WorkerSelector 与 Worker.Labels 的存储类型。实现
// driver.Valuer + sql.Scanner 后，GORM 读路径（Find/First）与 raw 写路径
// （crdb.ExecuteTx 内的 tx.ExecContext/tx.QueryRowContext）共用同一套序列化，
// 消除原 rows.go 的 taskRow/workerRow DTO 层与手动 json.Marshal/Unmarshal 样板。
package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Value 实现 driver.Valuer：WorkerSelector -> JSONB []byte。
//
// nil map 输出 "{}"（tasks.worker_selector / workers.labels 均 JSONB NOT NULL，
// schema.sql 约束要求非空）。非空 map 序列化为 JSON 对象。
func (w WorkerSelector) Value() (driver.Value, error) {
	if w == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("marshal worker selector: %w", err)
	}
	return b, nil
}

// Scan 实现 sql.Scanner：JSONB []byte/string -> WorkerSelector。
//
// NULL 或空字节 -> 空 WorkerSelector（非 nil 空 map，与 model.WorkerSelector{} 语义
// 一致，供 SelectorSubset 等遍历消费）。合法 JSON 对象（含 "{}"）反序列化为 map。
func (w *WorkerSelector) Scan(src interface{}) error {
	if src == nil {
		*w = WorkerSelector{}
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("worker selector scan: unsupported source type %T", src)
	}
	if len(b) == 0 {
		*w = WorkerSelector{}
		return nil
	}
	out := WorkerSelector{}
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("unmarshal worker selector: %w", err)
	}
	*w = out
	return nil
}

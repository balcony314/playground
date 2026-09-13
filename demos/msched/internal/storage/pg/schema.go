package pg

import (
	_ "embed"

	"gorm.io/gorm"
)

//go:embed schema.sql
var schemaSQL string

// ApplySchema 在 db 上执行建表 DDL（幂等，IF NOT EXISTS）。
// 启动或集成测试时调用，确保 tasks/workers/group_stats 表与撮合复合索引就绪。
func ApplySchema(db *gorm.DB) error {
	return db.Exec(schemaSQL).Error
}

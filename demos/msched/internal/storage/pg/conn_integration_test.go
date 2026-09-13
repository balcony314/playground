//go:build integration

package pg

import (
	"sync"
	"testing"

	"github.com/balcony314/msched/internal/storage"
	"gorm.io/gorm"
)

// schemaOnce 保证测试进程内 schema 只 DROP+重建一次（CRDB DDL 慢，每测试重建致 38s）。
// 各测试通过 cleanTables 清数据隔离，schema 复用。
var schemaOnce sync.Once

// connectTestDB 连接测试 CockroachDB 并应用 schema，返回 *gorm.DB。
// 集成测试共享的 setup helper。schema 变更后 IF NOT EXISTS 不会改已有表结构，
// 首次调用 DROP 旧表再建（用最新 DDL）；后续调用复用已建 schema。
func connectTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := NewDB(storage.DefaultConfig().PG)
	if err != nil {
		t.Fatalf("connect cockroachdb: %v", err)
	}
	schemaOnce.Do(func() {
		if err := db.Exec("DROP TABLE IF EXISTS tasks, workers, group_stats CASCADE").Error; err != nil {
			t.Fatalf("drop tables: %v", err)
		}
		if err := ApplySchema(db); err != nil {
			t.Fatalf("apply schema: %v", err)
		}
	})
	return db
}

// cleanTables 清空三张表（测试隔离用）。
func cleanTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, tbl := range []string{"tasks", "workers", "group_stats"} {
		if err := db.Exec("DELETE FROM " + tbl).Error; err != nil {
			t.Fatalf("clean %s: %v", tbl, err)
		}
	}
}

// TestConnectAndApplySchema 早期验证：CockroachDB 可达 + DDL 语法兼容 + 三表就绪。
func TestConnectAndApplySchema(t *testing.T) {
	db := connectTestDB(t)
	var n int64
	for _, tbl := range []string{"tasks", "workers", "group_stats"} {
		if err := db.Raw("SELECT count(*) FROM " + tbl).Scan(&n).Error; err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
	}
}

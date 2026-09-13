// Package pg 实现 scheduler.TaskStore / scheduler.WorkerRegistry 的 PostgreSQL(CockroachDB) 版本。
//
// 详见 docs/DESIGN.md §3/§7/§8。CockroachDB 默认 SERIALIZABLE 隔离级，CAS 乐观锁 +
// group_stats 同事务维护用 crdb.ExecuteTx 自动重试。

package pg

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/balcony314/msched/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewDB 建立 CockroachDB（兼容 PostgreSQL 协议）连接（DESIGN §3）。
// insecure 模式 sslmode=disable。
func NewDB(cfg storage.PGConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open cockroachdb: %w", err)
	}
	return db, nil
}

// gtxFromTx 从 crdb.ExecuteTx 闭包的 *sql.Tx 构造绑定该事务的 *gorm.DB。
//
// 用途：crdb.ExecuteTx 闭包形参是 *sql.Tx（CRDB SERIALIZABLE 自动重试依赖整个闭包
// 重跑，GORM 的 db.Transaction 给不了）。GORM v2 的 DB.ConnPool 是导出字段，*sql.Tx
// 满足 ConnPool 接口（ExecContext/QueryContext/QueryRowContext/PrepareContext），
// 故注入后 gtx 的 ORM 方法（Where/Updates/Delete/Find 等）操作的是同一原生事务，
// 重试时整体复用。供简单批量写（DELETE/UPDATE）用 GORM ORM 表达；CAS/UPSERT/CRDB
// 特有语法（interval）等仍走 tx.ExecContext raw SQL。
func gtxFromTx(db *gorm.DB, ctx context.Context, tx *sql.Tx) *gorm.DB {
	gtx := db.Session(&gorm.Session{Context: ctx})
	gtx.ConnPool = tx
	return gtx
}

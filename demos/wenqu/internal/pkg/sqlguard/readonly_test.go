package sqlguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateReadOnly(t *testing.T) {
	testCases := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		// 查询类语句放行
		{"普通 SELECT", "SELECT * FROM demo.orders LIMIT 10", false},
		{"CTE 查询", "WITH t AS (SELECT 1) SELECT * FROM t", false},
		{"SHOW", "SHOW DATABASES", false},
		{"DESCRIBE", "DESCRIBE TABLE demo.orders", false},
		{"EXPLAIN", "EXPLAIN SELECT count() FROM demo.orders", false},
		{"小写语句", "select id from demo.orders where id = 1", false},
		{"带分号与换行", "\n  SELECT count()\nFROM demo.orders;\n", false},

		// 写操作拒绝
		{"INSERT", "INSERT INTO demo.orders VALUES (1)", true},
		{"INSERT SELECT", "INSERT INTO t SELECT * FROM demo.orders", true},
		{"UPDATE", "UPDATE demo.orders SET amount = 0", true},
		{"DELETE", "DELETE FROM demo.orders WHERE id = 1", true},
		{"DROP", "DROP TABLE demo.orders", true},
		{"TRUNCATE", "TRUNCATE TABLE demo.orders", true},
		{"CREATE", "CREATE TABLE t (id UInt32) ENGINE = Memory", true},
		{"ALTER", "ALTER TABLE demo.orders ADD COLUMN c UInt8", true},
		{"CTE 伪装的 INSERT", "WITH t AS (SELECT 1) INSERT INTO x SELECT * FROM t", true},

		// 字面量与列名不误伤
		{"字符串含 delete", "SELECT * FROM demo.orders WHERE remark = 'please delete me'", false},
		{"字符串含 drop table", "SELECT 'drop table demo.orders' AS stmt", false},
		{"列名 deleted_at", "SELECT deleted_at FROM demo.orders", false},
		{"注释含 insert", "SELECT 1 /* insert into t values (1) */", false},
		{"行注释含 delete", "SELECT 1 -- delete from t\n", false},

		// 其他
		{"空语句", "   ", true},
		{"纯注释", "-- nothing\n", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReadOnly(tc.sql)
			if tc.wantErr {
				assert.Error(t, err, "sql=%q 应被拒绝", tc.sql)
			} else {
				assert.NoError(t, err, "sql=%q 应被放行", tc.sql)
			}
		})
	}
}

// Package sqlguard 提供 LLM 生成 SQL 的只读防护。
// Prompt 层约束可被 LLM 绕过，此处为代码层兜底：仅放行查询类语句。
package sqlguard

import (
	"fmt"
	"regexp"
	"strings"
)

// 允许作为语句首关键字的白名单（大小写不敏感）。
var allowedFirstKeywords = map[string]struct{}{
	"SELECT":   {},
	"WITH":     {},
	"SHOW":     {},
	"DESCRIBE": {},
	"DESC":     {},
	"EXPLAIN":  {},
	"EXISTS":   {},
}

// 明令禁止的写操作/管理类关键字。
// ClickHouse 支持 INSERT INTO ... SELECT 形式的语句首词合法化（如 WITH 开头接 INSERT），
// 因此除首词白名单外还需做全文关键字黑名单匹配。
var forbiddenKeywords = []string{
	"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE", "TRUNCATE",
	"GRANT", "REVOKE", "RENAME", "OPTIMIZE", "EXCHANGE", "KILL", "MERGE",
	"UNDROP", "SET ROLE", "SYSTEM",
}

var (
	// 剥离单引号字符串字面量与反引号标识符，避免 'delete' 这类词误伤
	stringLiteralRe = regexp.MustCompile(`'[^']*'`)
	quotedIdentRe   = regexp.MustCompile("`[^`]*`")
	// 剥离注释（-- 行注释与 /* */ 块注释）
	lineCommentRe  = regexp.MustCompile(`--[^\n]*`)
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// stripLiteralsAndComments 移除 SQL 中的字符串字面量、引号标识符与注释，
// 使后续关键字匹配只作用于"代码"部分，不受数据内容干扰。
func stripLiteralsAndComments(sqlStr string) string {
	out := blockCommentRe.ReplaceAllString(sqlStr, " ")
	out = lineCommentRe.ReplaceAllString(out, " ")
	out = stringLiteralRe.ReplaceAllString(out, "''")
	out = quotedIdentRe.ReplaceAllString(out, " ")

	return out
}

// ValidateReadOnly 校验一条 SQL 是否为只读查询，返回 nil 表示放行。
// 校验分两步：
//  1. 首关键字必须在白名单内（SELECT/WITH/SHOW/DESCRIBE/EXPLAIN 等）；
//  2. 剥离字面量与注释后，全文不得出现写操作关键字（词边界全词匹配，
//     "deleted_at" 这类列名不会命中，'delete' 这类字符串值已被剥离）。
func ValidateReadOnly(sqlStr string) error {
	stripped := stripLiteralsAndComments(sqlStr)
	trimmed := strings.TrimSpace(stripped)
	if trimmed == "" {
		return fmt.Errorf("sqlguard: empty sql")
	}

	first, _, _ := strings.Cut(trimmed, " ")
	first = strings.TrimSuffix(strings.ToUpper(first), ";")
	if _, ok := allowedFirstKeywords[first]; !ok {
		return fmt.Errorf("sqlguard: statement starts with %q, only read-only queries are allowed", first)
	}

	for _, kw := range forbiddenKeywords {
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(kw) + `\b`)
		if err != nil {
			return fmt.Errorf("sqlguard: compile keyword %q: %w", kw, err)
		}
		if re.MatchString(stripped) {
			return fmt.Errorf("sqlguard: forbidden keyword %q detected, only read-only queries are allowed", kw)
		}
	}

	return nil
}

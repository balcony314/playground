// Package hash 提供内容摘要工具，用于向量库主键去重。
package hash

import (
	"crypto/sha256"
	"encoding/hex"
)

// GetStringSha256 返回字符串的 sha256 十六进制摘要（64 字符）。
// 向量库以语料内容的 hash 作为主键，重复灌库时天然幂等覆盖。
func GetStringSha256(str string) string {
	hashBytes := sha256.Sum256([]byte(str))

	return hex.EncodeToString(hashBytes[:])
}

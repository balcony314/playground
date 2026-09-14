// Package convert 提供数值类型安全转换。
// 自研 embedding 服务返回 [][]float64，而 Milvus 浮点向量列要求 []float32。
package convert

import (
	"fmt"
	"math"
)

// Float64To32 将 float64 转为 float32，超出 float32 表示范围时报错，
// 避免静默溢出为 ±Inf 污染向量。
func Float64To32(f float64) (float32, error) {
	if f > math.MaxFloat32 || f < -math.MaxFloat32 {
		return 0.0, fmt.Errorf("float(%v) is out of float32 range", f)
	}

	return float32(f), nil
}

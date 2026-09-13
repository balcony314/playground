package output

import (
	"fmt"
	"math"

	"github.com/balcony314/gprobe/internal/bpf"
)

// decodeArg 解码参数数据为用户态值
func decodeArg(arg bpf.ArgData) any {
	switch arg.Type {
	case bpf.ArgTypeString:
		return fmt.Sprintf("<string len=%d>", arg.Size)
	case bpf.ArgTypeSlice:
		return fmt.Sprintf("<slice len=%d>", arg.Size)
	case bpf.ArgTypeInt, bpf.ArgTypeUint:
		if len(arg.Data) < 8 {
			return uint64(0)
		}
		return bpf.ByteOrder().Uint64(arg.Data[:8])
	case bpf.ArgTypeFloat64:
		if len(arg.Data) < 8 {
			return float64(0)
		}
		bits := bpf.ByteOrder().Uint64(arg.Data[:8])
		return math.Float64frombits(bits)
	default:
		return fmt.Sprintf("<unknown type=%d>", arg.Type)
	}
}

// decodeArgs 解码参数数组。
// skipEmpty 为 true 时跳过 Size=0 的无效参数。
func decodeArgs(args [6]bpf.ArgData, skipEmpty bool) []any {
	result := make([]any, 0, len(args))
	for _, arg := range args {
		if skipEmpty && arg.Size == 0 {
			continue
		}
		result = append(result, decodeArg(arg))
	}
	return result
}

package output

import (
	"math"
	"testing"

	"github.com/balcony314/gprobe/internal/bpf"
)

func TestDecodeArg_Int(t *testing.T) {
	arg := bpf.ArgData{
		Type: bpf.ArgTypeInt,
		Size: 8,
		Data: make([]byte, 8),
	}
	// 写入 42 (低字节序)
	arg.Data[0] = 42

	result := decodeArg(arg)
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("decodeArg(Int) type = %T, want uint64", result)
	}
	if val != 42 {
		t.Errorf("decodeArg(Int) = %d, want 42", val)
	}
}

func TestDecodeArg_Uint(t *testing.T) {
	arg := bpf.ArgData{
		Type: bpf.ArgTypeUint,
		Size: 8,
		Data: make([]byte, 8),
	}
	arg.Data[0] = 100

	result := decodeArg(arg)
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("decodeArg(Uint) type = %T, want uint64", result)
	}
	if val != 100 {
		t.Errorf("decodeArg(Uint) = %d, want 100", val)
	}
}

func TestDecodeArg_Float64(t *testing.T) {
	arg := bpf.ArgData{
		Type: bpf.ArgTypeFloat64,
		Size: 8,
		Data: make([]byte, 8),
	}
	// 写入 3.14 的 IEEE 754 表示
	bits := math.Float64bits(3.14)
	bpf.ByteOrder().PutUint64(arg.Data, bits)

	result := decodeArg(arg)
	val, ok := result.(float64)
	if !ok {
		t.Fatalf("decodeArg(Float64) type = %T, want float64", result)
	}
	if math.Abs(val-3.14) > 0.001 {
		t.Errorf("decodeArg(Float64) = %f, want 3.14", val)
	}
}

func TestDecodeArg_String(t *testing.T) {
	arg := bpf.ArgData{
		Type: bpf.ArgTypeString,
		Size: 10,
		Data: make([]byte, 8),
	}

	result := decodeArg(arg)
	str, ok := result.(string)
	if !ok {
		t.Fatalf("decodeArg(String) type = %T, want string", result)
	}
	if str != "<string len=10>" {
		t.Errorf("decodeArg(String) = %q, want %q", str, "<string len=10>")
	}
}

func TestDecodeArg_Slice(t *testing.T) {
	arg := bpf.ArgData{
		Type: bpf.ArgTypeSlice,
		Size: 24,
		Data: make([]byte, 8),
	}

	result := decodeArg(arg)
	str, ok := result.(string)
	if !ok {
		t.Fatalf("decodeArg(Slice) type = %T, want string", result)
	}
	if str != "<slice len=24>" {
		t.Errorf("decodeArg(Slice) = %q, want %q", str, "<slice len=24>")
	}
}

func TestDecodeArg_UnknownType(t *testing.T) {
	arg := bpf.ArgData{
		Type: 99,
		Size: 8,
		Data: make([]byte, 8),
	}

	result := decodeArg(arg)
	str, ok := result.(string)
	if !ok {
		t.Fatalf("decodeArg(Unknown) type = %T, want string", result)
	}
	if str != "<unknown type=99>" {
		t.Errorf("decodeArg(Unknown) = %q, want %q", str, "<unknown type=99>")
	}
}

func TestDecodeArg_SmallData(t *testing.T) {
	arg := bpf.ArgData{
		Type: bpf.ArgTypeInt,
		Size: 4,
		Data: make([]byte, 4), // 小于 8 字节
	}

	result := decodeArg(arg)
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("decodeArg(SmallData) type = %T, want uint64", result)
	}
	if val != 0 {
		t.Errorf("decodeArg(SmallData) = %d, want 0", val)
	}
}

func TestDecodeArgs_SkipEmpty(t *testing.T) {
	args := [6]bpf.ArgData{
		{Type: bpf.ArgTypeInt, Size: 8, Data: make([]byte, 8)},
		{Type: bpf.ArgTypeUint, Size: 0, Data: make([]byte, 0)}, // 空参数
		{Type: bpf.ArgTypeFloat64, Size: 8, Data: make([]byte, 8)},
		{Type: bpf.ArgTypeInt, Size: 0, Data: make([]byte, 0)}, // 空参数
	}

	result := decodeArgs(args, true)
	if len(result) != 2 {
		t.Errorf("decodeArgs(skipEmpty=true) len = %d, want 2", len(result))
	}
}

func TestDecodeArgs_NoSkipEmpty(t *testing.T) {
	args := [6]bpf.ArgData{
		{Type: bpf.ArgTypeInt, Size: 8, Data: make([]byte, 8)},
		{Type: bpf.ArgTypeUint, Size: 0, Data: make([]byte, 0)},
	}

	result := decodeArgs(args, false)
	// skipEmpty=false 时会解码所有 6 个参数槽位
	if len(result) != 6 {
		t.Errorf("decodeArgs(skipEmpty=false) len = %d, want 6", len(result))
	}
}

func TestDecodeArgs_AllEmpty(t *testing.T) {
	args := [6]bpf.ArgData{}

	result := decodeArgs(args, true)
	if len(result) != 0 {
		t.Errorf("decodeArgs(all empty, skipEmpty=true) len = %d, want 0", len(result))
	}
}

func TestDecodeArgs_AllTypes(t *testing.T) {
	args := [6]bpf.ArgData{
		{Type: bpf.ArgTypeInt, Size: 8, Data: make([]byte, 8)},
		{Type: bpf.ArgTypeUint, Size: 8, Data: make([]byte, 8)},
		{Type: bpf.ArgTypeFloat64, Size: 8, Data: make([]byte, 8)},
		{Type: bpf.ArgTypeString, Size: 5, Data: make([]byte, 8)},
		{Type: bpf.ArgTypeSlice, Size: 16, Data: make([]byte, 8)},
		{Type: bpf.ArgTypePointer, Size: 8, Data: make([]byte, 8)},
	}

	result := decodeArgs(args, false)
	if len(result) != 6 {
		t.Errorf("decodeArgs(all types) len = %d, want 6", len(result))
	}
}

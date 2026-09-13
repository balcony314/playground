package bpf

import (
	"testing"
	"unsafe"
)

func TestEventSize(t *testing.T) {
	size := EventSize()
	if size <= 0 {
		t.Errorf("EventSize() = %d, want > 0", size)
	}

	// 验证与 unsafe.Sizeof 一致
	expected := int(unsafe.Sizeof(bpfFuncEvent{}))
	if size != expected {
		t.Errorf("EventSize() = %d, want %d", size, expected)
	}
}

func TestParseEvent_ValidData(t *testing.T) {
	// 构造有效的事件数据
	data := make([]byte, EventSize())

	// 填充测试数据
	bpfEvent := (*bpfFuncEvent)(unsafe.Pointer(&data[0]))
	bpfEvent.Timestamp = 1234567890
	bpfEvent.Pid = 1000
	bpfEvent.Tid = 1001
	bpfEvent.GoroutineId = 42
	bpfEvent.FuncId = 1
	bpfEvent.IsReturn = 0
	bpfEvent.DurationNs = 0
	bpfEvent.ReturnValue = 0

	// 设置第一个参数
	bpfEvent.Args[0].Type = uint64(ArgTypeInt)
	bpfEvent.Args[0].Size = 8
	// 低字节序写入 42
	bpfEvent.Args[0].Data[0] = 42

	event, err := ParseEvent(data)
	if err != nil {
		t.Fatalf("ParseEvent() error = %v", err)
	}

	if event.Timestamp != 1234567890 {
		t.Errorf("Timestamp = %d, want 1234567890", event.Timestamp)
	}
	if event.PID != 1000 {
		t.Errorf("PID = %d, want 1000", event.PID)
	}
	if event.TID != 1001 {
		t.Errorf("TID = %d, want 1001", event.TID)
	}
	if event.GoroutineID != 42 {
		t.Errorf("GoroutineID = %d, want 42", event.GoroutineID)
	}
	if event.FuncID != 1 {
		t.Errorf("FuncID = %d, want 1", event.FuncID)
	}
	if event.IsReturn {
		t.Errorf("IsReturn = true, want false")
	}

	// 验证参数
	if event.Args[0].Type != ArgTypeInt {
		t.Errorf("Args[0].Type = %d, want %d", event.Args[0].Type, ArgTypeInt)
	}
	if event.Args[0].Size != 8 {
		t.Errorf("Args[0].Size = %d, want 8", event.Args[0].Size)
	}
}

func TestParseEvent_ReturnEvent(t *testing.T) {
	data := make([]byte, EventSize())

	bpfEvent := (*bpfFuncEvent)(unsafe.Pointer(&data[0]))
	bpfEvent.Timestamp = 9876543210
	bpfEvent.Pid = 2000
	bpfEvent.Tid = 2001
	bpfEvent.FuncId = 2
	bpfEvent.IsReturn = 1
	bpfEvent.DurationNs = 1000000 // 1ms
	bpfEvent.ReturnValue = 42

	event, err := ParseEvent(data)
	if err != nil {
		t.Fatalf("ParseEvent() error = %v", err)
	}

	if !event.IsReturn {
		t.Errorf("IsReturn = false, want true")
	}
	if event.DurationNS != 1000000 {
		t.Errorf("DurationNS = %d, want 1000000", event.DurationNS)
	}
	if event.ReturnValue != 42 {
		t.Errorf("ReturnValue = %d, want 42", event.ReturnValue)
	}
}

func TestParseEvent_DataTooSmall(t *testing.T) {
	data := make([]byte, EventSize()-1)

	_, err := ParseEvent(data)
	if err == nil {
		t.Error("ParseEvent() error = nil, want error for small data")
	}
}

func TestParseEvent_ArgSizeExceedsLimit(t *testing.T) {
	data := make([]byte, EventSize())

	bpfEvent := (*bpfFuncEvent)(unsafe.Pointer(&data[0]))
	bpfEvent.Args[0].Type = uint64(ArgTypeInt)
	bpfEvent.Args[0].Size = maxArgDataSize + 1 // 超出限制

	_, err := ParseEvent(data)
	if err == nil {
		t.Error("ParseEvent() error = nil, want error for large arg size")
	}
}

func TestParseEvent_MultipleArgs(t *testing.T) {
	data := make([]byte, EventSize())

	bpfEvent := (*bpfFuncEvent)(unsafe.Pointer(&data[0]))
	bpfEvent.Timestamp = 100

	// 设置多个参数
	for i := 0; i < 3; i++ {
		bpfEvent.Args[i].Type = uint64(ArgTypeUint)
		bpfEvent.Args[i].Size = 8
		bpfEvent.Args[i].Data[0] = byte(i + 1)
	}

	event, err := ParseEvent(data)
	if err != nil {
		t.Fatalf("ParseEvent() error = %v", err)
	}

	for i := 0; i < 3; i++ {
		if event.Args[i].Type != ArgTypeUint {
			t.Errorf("Args[%d].Type = %d, want %d", i, event.Args[i].Type, ArgTypeUint)
		}
		if event.Args[i].Size != 8 {
			t.Errorf("Args[%d].Size = %d, want 8", i, event.Args[i].Size)
		}
	}

	// 验证未设置的参数
	for i := 3; i < 6; i++ {
		if event.Args[i].Size != 0 {
			t.Errorf("Args[%d].Size = %d, want 0", i, event.Args[i].Size)
		}
	}
}

func TestByteOrder(t *testing.T) {
	order := ByteOrder()
	if order == nil {
		t.Error("ByteOrder() = nil, want non-nil")
	}
}

func TestArgTypes(t *testing.T) {
	// 验证参数类型常量
	tests := []struct {
		name string
		argType ArgType
		expected uint64
	}{
		{"ArgTypeInt", ArgTypeInt, 0},
		{"ArgTypeUint", ArgTypeUint, 1},
		{"ArgTypeFloat64", ArgTypeFloat64, 2},
		{"ArgTypeString", ArgTypeString, 3},
		{"ArgTypeSlice", ArgTypeSlice, 4},
		{"ArgTypeStruct", ArgTypeStruct, 5},
		{"ArgTypeMap", ArgTypeMap, 6},
		{"ArgTypeChannel", ArgTypeChannel, 7},
		{"ArgTypePointer", ArgTypePointer, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if uint64(tt.argType) != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, uint64(tt.argType), tt.expected)
			}
		})
	}
}

func TestMaxArgDataSize(t *testing.T) {
	if maxArgDataSize != 64 {
		t.Errorf("maxArgDataSize = %d, want 64", maxArgDataSize)
	}
}

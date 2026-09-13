package output

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/balcony314/gprobe/internal/bpf"
)

func TestNewJSONOutputter(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewJSONOutputter(&buf)

	if outputter == nil {
		t.Fatal("NewJSONOutputter() = nil, want non-nil")
	}
	if outputter.funcNames == nil {
		t.Error("funcNames map is nil")
	}
}

func TestJSONOutputter_RegisterFunc(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewJSONOutputter(&buf)

	outputter.RegisterFunc(0, "main.Add")
	outputter.RegisterFunc(1, "main.Greet")

	if outputter.funcNames[0] != "main.Add" {
		t.Errorf("funcNames[0] = %q, want %q", outputter.funcNames[0], "main.Add")
	}
	if outputter.funcNames[1] != "main.Greet" {
		t.Errorf("funcNames[1] = %q, want %q", outputter.funcNames[1], "main.Greet")
	}
}

func TestJSONOutputter_Output_CallEvent(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewJSONOutputter(&buf)
	outputter.RegisterFunc(0, "main.Add")

	event := &bpf.FuncEvent{
		Timestamp: uint64(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()),
		PID:       1000,
		TID:       1001,
		FuncID:    0,
		IsReturn:  false,
		Args: [6]bpf.ArgData{
			{Type: bpf.ArgTypeInt, Size: 8, Data: []byte{42, 0, 0, 0, 0, 0, 0, 0}},
			{Type: bpf.ArgTypeInt, Size: 8, Data: []byte{100, 0, 0, 0, 0, 0, 0, 0}},
		},
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	// 解析 JSON 输出
	var jsonEvent JSONEvent
	if err := json.Unmarshal(buf.Bytes(), &jsonEvent); err != nil {
		t.Fatalf("JSON unmarshal error = %v", err)
	}

	if jsonEvent.FuncName != "main.Add" {
		t.Errorf("FuncName = %q, want %q", jsonEvent.FuncName, "main.Add")
	}
	if jsonEvent.PID != 1000 {
		t.Errorf("PID = %d, want 1000", jsonEvent.PID)
	}
	if jsonEvent.TID != 1001 {
		t.Errorf("TID = %d, want 1001", jsonEvent.TID)
	}
	if jsonEvent.IsReturn {
		t.Error("IsReturn = true, want false")
	}
	// JSONOutputter 使用 decodeArgs(event.Args, false)，会解码所有参数槽位
	if len(jsonEvent.Args) != 6 {
		t.Errorf("Args len = %d, want 6", len(jsonEvent.Args))
	}
}

func TestJSONOutputter_Output_ReturnEvent(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewJSONOutputter(&buf)
	outputter.RegisterFunc(0, "main.Add")

	event := &bpf.FuncEvent{
		Timestamp:   uint64(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()),
		PID:         1000,
		TID:         1001,
		FuncID:      0,
		IsReturn:    true,
		DurationNS:  1000000, // 1ms
		ReturnValue: 142,
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	var jsonEvent JSONEvent
	if err := json.Unmarshal(buf.Bytes(), &jsonEvent); err != nil {
		t.Fatalf("JSON unmarshal error = %v", err)
	}

	if !jsonEvent.IsReturn {
		t.Error("IsReturn = false, want true")
	}
	if jsonEvent.DurationMS != 1.0 {
		t.Errorf("DurationMS = %f, want 1.0", jsonEvent.DurationMS)
	}
	if jsonEvent.ReturnValue != 142 {
		t.Errorf("ReturnValue = %d, want 142", jsonEvent.ReturnValue)
	}
	if jsonEvent.Args != nil {
		t.Errorf("Args = %v, want nil for return event", jsonEvent.Args)
	}
}

func TestJSONOutputter_Output_UnknownFuncID(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewJSONOutputter(&buf)

	event := &bpf.FuncEvent{
		Timestamp: uint64(time.Now().UnixNano()),
		FuncID:    99, // 未注册的函数 ID
		IsReturn:  false,
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	var jsonEvent JSONEvent
	if err := json.Unmarshal(buf.Bytes(), &jsonEvent); err != nil {
		t.Fatalf("JSON unmarshal error = %v", err)
	}

	if jsonEvent.FuncName != "func_99" {
		t.Errorf("FuncName = %q, want %q", jsonEvent.FuncName, "func_99")
	}
}

func TestJSONOutputter_Output_WithGoroutineID(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewJSONOutputter(&buf)

	event := &bpf.FuncEvent{
		Timestamp:   uint64(time.Now().UnixNano()),
		FuncID:      0,
		IsReturn:    false,
		GoroutineID: 42,
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	var jsonEvent JSONEvent
	if err := json.Unmarshal(buf.Bytes(), &jsonEvent); err != nil {
		t.Fatalf("JSON unmarshal error = %v", err)
	}

	if jsonEvent.GoroutineID != 42 {
		t.Errorf("GoroutineID = %d, want 42", jsonEvent.GoroutineID)
	}
}

func TestJSONOutputter_Output_MultipleEvents(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewJSONOutputter(&buf)
	outputter.RegisterFunc(0, "main.Func")

	// 输出多个事件
	for i := 0; i < 3; i++ {
		event := &bpf.FuncEvent{
			Timestamp: uint64(time.Now().UnixNano()),
			FuncID:    0,
			IsReturn:  false,
		}
		if err := outputter.Output(event); err != nil {
			t.Fatalf("Output() error = %v", err)
		}
	}

	// 验证输出了 3 行 JSON
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Errorf("Output lines = %d, want 3", len(lines))
	}
}

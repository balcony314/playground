package output

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/balcony314/gprobe/internal/bpf"
)

func TestNewTerminalOutputter(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)

	if outputter == nil {
		t.Fatal("NewTerminalOutputter() = nil, want non-nil")
	}
	if outputter.funcNames == nil {
		t.Error("funcNames map is nil")
	}
}

func TestTerminalOutputter_RegisterFunc(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)

	outputter.RegisterFunc(0, "main.Add")
	outputter.RegisterFunc(1, "main.Greet")

	if outputter.funcNames[0] != "main.Add" {
		t.Errorf("funcNames[0] = %q, want %q", outputter.funcNames[0], "main.Add")
	}
	if outputter.funcNames[1] != "main.Greet" {
		t.Errorf("funcNames[1] = %q, want %q", outputter.funcNames[1], "main.Greet")
	}
}

func TestTerminalOutputter_Output_CallEvent(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)
	outputter.RegisterFunc(0, "main.Add")

	event := &bpf.FuncEvent{
		Timestamp: uint64(time.Date(2026, 1, 1, 12, 34, 56, 0, time.UTC).UnixNano()),
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

	output := buf.String()
	if !strings.Contains(output, "CALL") {
		t.Error("Output missing CALL")
	}
	if !strings.Contains(output, "main.Add") {
		t.Error("Output missing function name")
	}
}

func TestTerminalOutputter_Output_ReturnEvent(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)
	outputter.RegisterFunc(0, "main.Add")

	event := &bpf.FuncEvent{
		Timestamp:   uint64(time.Date(2026, 1, 1, 12, 34, 56, 0, time.UTC).UnixNano()),
		PID:         1000,
		TID:         1001,
		FuncID:      0,
		IsReturn:    true,
		DurationNS:  1500000, // 1.5ms
		ReturnValue: 142,
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "RET") {
		t.Error("Output missing RET")
	}
	if !strings.Contains(output, "main.Add") {
		t.Error("Output missing function name")
	}
	if !strings.Contains(output, "142") {
		t.Error("Output missing return value")
	}
	if !strings.Contains(output, "1.50ms") {
		t.Error("Output missing duration")
	}
}

func TestTerminalOutputter_Output_UnknownFuncID(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)

	event := &bpf.FuncEvent{
		Timestamp: uint64(time.Now().UnixNano()),
		FuncID:    99, // 未注册的函数 ID
		IsReturn:  false,
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "func_99") {
		t.Error("Output missing func_99")
	}
}

func TestTerminalOutputter_Output_WithArgs(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)
	outputter.RegisterFunc(0, "main.Func")

	event := &bpf.FuncEvent{
		Timestamp: uint64(time.Now().UnixNano()),
		FuncID:    0,
		IsReturn:  false,
		Args: [6]bpf.ArgData{
			{Type: bpf.ArgTypeInt, Size: 8, Data: []byte{1, 0, 0, 0, 0, 0, 0, 0}},
			{Type: bpf.ArgTypeUint, Size: 8, Data: []byte{2, 0, 0, 0, 0, 0, 0, 0}},
			{Type: bpf.ArgTypeFloat64, Size: 8, Data: []byte{0, 0, 0, 0, 0, 0, 0, 0}},
		},
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "CALL") {
		t.Error("Output missing CALL")
	}
}

func TestTerminalOutputter_Output_EmptyArgs(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)
	outputter.RegisterFunc(0, "main.NoArgs")

	event := &bpf.FuncEvent{
		Timestamp: uint64(time.Now().UnixNano()),
		FuncID:    0,
		IsReturn:  false,
		Args:      [6]bpf.ArgData{}, // 全部为空
	}

	err := outputter.Output(event)
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "CALL") {
		t.Error("Output missing CALL")
	}
}

func TestTerminalOutputter_Output_MultipleEvents(t *testing.T) {
	var buf bytes.Buffer
	outputter := NewTerminalOutputter(&buf)
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

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Errorf("Output lines = %d, want 3", len(lines))
	}
}

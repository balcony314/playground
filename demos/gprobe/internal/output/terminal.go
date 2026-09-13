package output

import (
	"fmt"
	"io"
	"time"

	"github.com/balcony314/gprobe/internal/bpf"
)

// ANSI 颜色常量
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
)

// TerminalOutputter 终端输出器
type TerminalOutputter struct {
	writer    io.Writer
	funcNames map[uint32]string
}

// NewTerminalOutputter 创建终端输出器
func NewTerminalOutputter(writer io.Writer) *TerminalOutputter {
	return &TerminalOutputter{
		writer:    writer,
		funcNames: make(map[uint32]string),
	}
}

// RegisterFunc 注册函数名映射
func (o *TerminalOutputter) RegisterFunc(id uint32, name string) {
	o.funcNames[id] = name
}

// Output 输出事件
func (o *TerminalOutputter) Output(event *bpf.FuncEvent) error {
	// 获取函数名
	funcName, ok := o.funcNames[event.FuncID]
	if !ok {
		funcName = fmt.Sprintf("func_%d", event.FuncID)
	}

	timestamp := time.Unix(0, int64(event.Timestamp)).Format("15:04:05.000000")

	switch {
	case event.IsReturn:
		return o.outputReturn(timestamp, funcName, event)
	default:
		return o.outputCall(timestamp, funcName, event)
	}
}

func (o *TerminalOutputter) outputCall(timestamp, funcName string, event *bpf.FuncEvent) error {
	args := decodeArgs(event.Args, true)
	_, err := fmt.Fprintf(o.writer, "%s%s %sCALL %s%s(%v)\n",
		colorCyan, timestamp,
		colorGreen, funcName, colorReset,
		args)
	return err
}

func (o *TerminalOutputter) outputReturn(timestamp, funcName string, event *bpf.FuncEvent) error {
	durationMS := float64(event.DurationNS) / 1000000.0
	_, err := fmt.Fprintf(o.writer, "%s%s %sRET  %s%s => %v (%.2fms)%s\n",
		colorCyan, timestamp,
		colorRed, colorYellow, funcName,
		event.ReturnValue, durationMS, colorReset)
	return err
}

package output

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/balcony314/gprobe/internal/bpf"
)

// JSONEvent JSON 格式的事件输出
type JSONEvent struct {
	Timestamp   string `json:"timestamp"`
	PID         uint32 `json:"pid"`
	TID         uint32 `json:"tid"`
	GoroutineID uint64 `json:"goroutine_id,omitempty"`
	FuncName    string `json:"func_name"`
	IsReturn    bool   `json:"is_return"`
	DurationMS  float64 `json:"duration_ms,omitempty"`
	ReturnValue uint64 `json:"return_value,omitempty"`
	Args        []any  `json:"args,omitempty"`
}

// JSONOutputter JSON 输出器
type JSONOutputter struct {
	writer    io.Writer
	encoder   *json.Encoder
	funcNames map[uint32]string
}

// NewJSONOutputter 创建 JSON 输出器
func NewJSONOutputter(writer io.Writer) *JSONOutputter {
	return &JSONOutputter{
		writer:    writer,
		encoder:   json.NewEncoder(writer),
		funcNames: make(map[uint32]string),
	}
}

// RegisterFunc 注册函数名映射
func (o *JSONOutputter) RegisterFunc(id uint32, name string) {
	o.funcNames[id] = name
}

// Output 输出事件
func (o *JSONOutputter) Output(event *bpf.FuncEvent) error {
	// 解码参数（返回事件不包含参数）
	var args []any
	if !event.IsReturn {
		args = decodeArgs(event.Args, false)
	}

	// 获取函数名
	funcName, ok := o.funcNames[event.FuncID]
	if !ok {
		funcName = fmt.Sprintf("func_%d", event.FuncID)
	}

	// 构造 JSON 事件
	jsonEvent := JSONEvent{
		Timestamp:   time.Unix(0, int64(event.Timestamp)).Format(time.RFC3339Nano),
		PID:         event.PID,
		TID:         event.TID,
		GoroutineID: event.GoroutineID,
		FuncName:    funcName,
		IsReturn:    event.IsReturn,
		Args:        args,
	}

	if event.IsReturn {
		jsonEvent.DurationMS = float64(event.DurationNS) / 1000000.0
		jsonEvent.ReturnValue = event.ReturnValue
	}

	return o.encoder.Encode(jsonEvent)
}

package output

import "github.com/balcony314/gprobe/internal/bpf"

// Outputter 输出接口。
//
// 实现必须是线程安全的。RegisterFunc 必须在首次 Output 调用前完成。
type Outputter interface {
	// RegisterFunc 注册函数 ID 到名称的映射。
	// 必须在 Output 之前调用，之后不可再调用。
	RegisterFunc(id uint32, name string)
	// Output 输出一个函数调用/返回事件。
	// 错误表示输出失败，调用方可决定是否继续。
	Output(event *bpf.FuncEvent) error
}

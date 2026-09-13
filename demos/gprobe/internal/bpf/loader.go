package bpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/perf"
)

// Collection 包含加载的 eBPF 程序和 map
type Collection struct {
	objs   *bpfObjects
	links  []link.Link
	reader *perf.Reader
}

// Load 加载 eBPF 程序
func Load() (*Collection, error) {
	objs := bpfObjects{}
	if err := loadBpfObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("加载 eBPF 对象失败: %w", err)
	}

	// 创建 perf event reader
	reader, err := perf.NewReader(objs.Events, os.Getpagesize())
	if err != nil {
		objs.Close()
		return nil, fmt.Errorf("创建 perf reader 失败: %w", err)
	}

	return &Collection{
		objs:   &objs,
		reader: reader,
	}, nil
}

// SetTargetPID 设置目标进程 PID
func (c *Collection) SetTargetPID(pid uint32) error {
	key := uint32(0) // CONFIG_PID
	return c.objs.ProbeConfig.Put(key, pid)
}

// SetTargetFuncID 设置目标函数 ID
func (c *Collection) SetTargetFuncID(funcID uint32) error {
	key := uint32(1) // CONFIG_FUNC_ID
	return c.objs.ProbeConfig.Put(key, funcID)
}


// AttachUprobe 挂载 uprobe 到指定函数。
//
// 返回探针使用 Uprobe (入口探针) 而非 Uretprobe，这是因为:
// 1. Go 函数可能有多个返回点，Uretprobe 只能 hook 单一返回位置
// 2. Go 的 ABI 要求在函数入口处读取返回值寄存器 (RAX)
// 3. 当前实现通过在函数入口处同时读取参数和返回值来简化设计
func (c *Collection) AttachUprobe(pid int, binaryPath string, funcName string) error {
	ex, err := link.OpenExecutable(binaryPath)
	if err != nil {
		return fmt.Errorf("打开可执行文件失败: %w", err)
	}

	// 挂载函数入口探针
	entryLink, err := ex.Uprobe(funcName, c.objs.UprobeFuncEntry, nil)
	if err != nil {
		return fmt.Errorf("挂载入口 uprobe 失败: %w", err)
	}

	// 挂载函数返回探针 (使用 Uprobe 而非 Uretprobe，原因见函数注释)
	returnLink, err := ex.Uprobe(funcName, c.objs.UprobeFuncReturn, nil)
	if err != nil {
		entryLink.Close()
		return fmt.Errorf("挂载返回 uprobe 失败: %w", err)
	}

	c.links = append(c.links, entryLink, returnLink)
	return nil
}

// ReadEvent 从 perf event 读取事件
func (c *Collection) ReadEvent() (*FuncEvent, error) {
	record, err := c.reader.Read()
	if err != nil {
		return nil, fmt.Errorf("读取 perf event 失败: %w", err)
	}

	if record.LostSamples > 0 {
		fmt.Fprintf(os.Stderr, "gprobe: 警告 - 丢失 %d 个事件\n", record.LostSamples)
		return nil, nil
	}

	return ParseEvent(record.RawSample)
}

// Close 关闭所有资源
func (c *Collection) Close() error {
	var errs []error
	for _, l := range c.links {
		if err := l.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.reader != nil {
		if err := c.reader.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.objs != nil {
		c.objs.Close()
	}
	return errors.Join(errs...)
}

// ArgData 参数数据
type ArgData struct {
	Type ArgType
	Size uint64
	Data []byte
}

// ArgType 参数类型
type ArgType uint64

const (
	ArgTypeInt ArgType = iota
	ArgTypeUint
	ArgTypeFloat64
	ArgTypeString
	ArgTypeSlice
	ArgTypeStruct
	ArgTypeMap
	ArgTypeChannel
	ArgTypePointer
)

// maxArgDataSize 参数数据最大字节数，与 bpfArgData.Data 大小一致
const maxArgDataSize = 64

// FuncEvent 函数调用事件
type FuncEvent struct {
	Timestamp   uint64
	PID         uint32
	TID         uint32
	GoroutineID uint64
	FuncID      uint32
	IsReturn    bool
	DurationNS  uint64
	ReturnValue uint64
	Args        [6]ArgData
}

// EventSize 返回事件的字节大小
func EventSize() int {
	return int(unsafe.Sizeof(bpfFuncEvent{}))
}

// ParseEvent 从字节切片解析事件
func ParseEvent(data []byte) (*FuncEvent, error) {
	if len(data) < EventSize() {
		return nil, fmt.Errorf("数据太小: %d < %d", len(data), EventSize())
	}

	// 确保数据对齐，避免 unsafe.Pointer 转换时的未定义行为
	var bpfEvent *bpfFuncEvent
	if uintptr(unsafe.Pointer(&data[0]))%unsafe.Alignof(bpfFuncEvent{}) != 0 {
		aligned := make([]byte, len(data))
		copy(aligned, data)
		bpfEvent = (*bpfFuncEvent)(unsafe.Pointer(&aligned[0]))
	} else {
		bpfEvent = (*bpfFuncEvent)(unsafe.Pointer(&data[0]))
	}

	event := &FuncEvent{
		Timestamp:   bpfEvent.Timestamp,
		PID:         bpfEvent.Pid,
		TID:         bpfEvent.Tid,
		GoroutineID: bpfEvent.GoroutineId,
		FuncID:      bpfEvent.FuncId,
		IsReturn:    bpfEvent.IsReturn != 0,
		DurationNS:  bpfEvent.DurationNs,
		ReturnValue: bpfEvent.ReturnValue,
	}

	for i := 0; i < len(bpfEvent.Args) && i < len(event.Args); i++ {
		argSize := bpfEvent.Args[i].Size
		if argSize > maxArgDataSize {
			return nil, fmt.Errorf("参数 %d 数据大小超限: %d > %d", i, argSize, maxArgDataSize)
		}
		event.Args[i] = ArgData{
			Type: ArgType(bpfEvent.Args[i].Type),
			Size: argSize,
			Data: make([]byte, argSize),
		}
		copy(event.Args[i].Data, bpfEvent.Args[i].Data[:argSize])
	}

	return event, nil
}

// ByteOrder 返回字节序
func ByteOrder() binary.ByteOrder {
	return binary.LittleEndian
}

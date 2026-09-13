package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/balcony314/gprobe/internal/bpf"
	"github.com/balcony314/gprobe/internal/output"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "gprobe",
	Short: "Go 进程动态追踪探针",
	Long:  "基于 eBPF uprobe 的 Go 进程函数入参和返回值追踪工具",
	RunE:  run,
}

var (
	pid       int
	binary    string
	funcs     []string
	outputFmt string
)

func init() {
	rootCmd.Flags().IntVarP(&pid, "pid", "p", 0, "目标进程 PID")
	rootCmd.Flags().StringVarP(&binary, "binary", "b", "", "目标二进制文件路径")
	rootCmd.Flags().StringSliceVarP(&funcs, "func", "f", []string{}, "要 hook 的函数名")
	rootCmd.Flags().StringVarP(&outputFmt, "output", "o", "terminal", "输出格式 (terminal|json)")
}

func run(cmd *cobra.Command, args []string) error {
	if err := validateArgs(); err != nil {
		return err
	}

	binaryPath, err := resolveBinaryPath()
	if err != nil {
		return err
	}
	fmt.Printf("gprobe: 目标 %s, 函数 %v\n", binaryPath, funcs)

	coll, err := bpf.Load()
	if err != nil {
		return fmt.Errorf("加载 eBPF 程序失败: %w", err)
	}
	defer coll.Close()

	if err := setupTarget(coll); err != nil {
		return err
	}

	out, err := createOutputter()
	if err != nil {
		return err
	}

	if err := attachProbes(coll, out, binaryPath); err != nil {
		return err
	}

	return runEventLoop(coll, out)
}

func validateArgs() error {
	if pid < 0 {
		return fmt.Errorf("PID 不能为负数")
	}
	if pid == 0 && binary == "" {
		return fmt.Errorf("必须指定 --pid 或 --binary")
	}
	if len(funcs) == 0 {
		return fmt.Errorf("必须指定至少一个函数名 (--func)")
	}
	return nil
}

func resolveBinaryPath() (string, error) {
	if binary != "" {
		if _, err := os.Stat(binary); err != nil {
			return "", fmt.Errorf("二进制文件不存在: %w", err)
		}
		return binary, nil
	}
	path := fmt.Sprintf("/proc/%d/exe", pid)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("进程 %d 不存在或无法访问: %w", pid, err)
	}
	return path, nil
}

func setupTarget(coll *bpf.Collection) error {
	if pid <= 0 {
		return nil
	}
	return coll.SetTargetPID(uint32(pid))
}

func createOutputter() (output.Outputter, error) {
	switch outputFmt {
	case "json":
		return output.NewJSONOutputter(os.Stdout), nil
	case "terminal":
		return output.NewTerminalOutputter(os.Stdout), nil
	default:
		return nil, fmt.Errorf("不支持的输出格式: %s", outputFmt)
	}
}

func attachProbes(coll *bpf.Collection, out output.Outputter, binaryPath string) error {
	for i, funcName := range funcs {
		if err := coll.SetTargetFuncID(uint32(i)); err != nil {
			return fmt.Errorf("设置函数 ID 失败: %w", err)
		}
		if err := coll.AttachUprobe(pid, binaryPath, funcName); err != nil {
			return fmt.Errorf("挂载 uprobe 到 %s 失败: %w", funcName, err)
		}
		out.RegisterFunc(uint32(i), funcName)
		fmt.Printf("gprobe: 已挂载 %s\n", funcName)
	}
	return nil
}

func runEventLoop(coll *bpf.Collection, out output.Outputter) error {
	fmt.Println("gprobe: 开始监听... (Ctrl+C 退出)")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go consumeEvents(ctx, coll, out)

	<-sig
	cancel()
	fmt.Println("\ngprobe: 退出")
	return nil
}

func consumeEvents(ctx context.Context, coll *bpf.Collection, out output.Outputter) {
	for ctx.Err() == nil {
		event, err := coll.ReadEvent()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "gprobe: 读取事件错误: %v\n", err)
			continue
		}
		if event == nil {
			continue
		}
		if err := out.Output(event); err != nil {
			fmt.Fprintf(os.Stderr, "gprobe: 输出事件错误: %v\n", err)
		}
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

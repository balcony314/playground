// Package callback 提供全链路 LLM / 工具调用日志 Callback。
// 挂载到 eino compose 执行链上，记录每个节点（模型调用、工具调用）的
// 输入输出与流式帧，用于问题定位与调用审计。
package callback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
)

// LoggerCallback 全链路日志回调。
// ParentCtx：承载请求上下文的日志字段（request-id 等）；
// Out：可选的调试输出通道，非 string 输入的节点过程会推送到这里（可为 nil）。
type LoggerCallback struct {
	callbacks.HandlerBuilder

	ParentCtx context.Context
	Out       chan string
}

// OnStart 节点开始：string 输入直推 Out，其余序列化后打 debug 日志
func (cb *LoggerCallback) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if inputStr, ok := input.(string); ok && cb.Out != nil {
		cb.Out <- "\n==================\n"
		cb.Out <- fmt.Sprintf(" [OnStart] %s ", inputStr)
		cb.Out <- "\n==================\n"
	}

	inputStr, _ := json.MarshalIndent(input, "", "  ") // 日志序列化失败可忽略
	slog.DebugContext(cb.ParentCtx, "OnStart", slog.String("input", string(inputStr)))

	return ctx
}

// OnEnd 节点结束：序列化输出打 debug 日志
func (cb *LoggerCallback) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	outputStr, _ := json.MarshalIndent(output, "", "  ") // 日志序列化失败可忽略
	slog.DebugContext(cb.ParentCtx, "OnEnd", slog.String("output", string(outputStr)))

	return ctx
}

// OnError 节点出错：打 error 日志
func (cb *LoggerCallback) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	slog.ErrorContext(cb.ParentCtx, "OnError", slog.Any("error", err))

	return ctx
}

// OnEndWithStreamOutput 流式输出：异步消费 StreamReader 逐帧打 debug 日志。
// 消费完必须 Close，否则流管道泄漏。
func (cb *LoggerCallback) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(cb.ParentCtx, "OnEndWithStreamOutput panic", slog.Any("error", r))
			}
		}()

		defer output.Close()

		for {
			frame, err := output.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				slog.ErrorContext(cb.ParentCtx, "OnEndWithStreamOutput Recv", slog.Any("error", err))
				return
			}

			s, err := json.Marshal(frame)
			if err != nil {
				slog.ErrorContext(cb.ParentCtx, "OnEndWithStreamOutput Marshal", slog.Any("error", err))
				return
			}

			slog.DebugContext(cb.ParentCtx, "graph info", slog.String(info.Name, string(s)))
		}
	}()

	return ctx
}

// OnStartWithStreamInput 流式输入：无观测需求，直接关闭释放
func (cb *LoggerCallback) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo,
	input *schema.StreamReader[callbacks.CallbackInput]) context.Context {

	defer input.Close()

	return ctx
}

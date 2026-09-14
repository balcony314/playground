// Package tool 统一封装 MCP（Model Context Protocol）工具的加载。
// 支持 SSE / StreamableHttp / Stdio 三种 transport，
// 并提供按工具名白名单的过滤能力，用于收窄 LLM 可调用的权限面。
package tool

import (
	"context"
	"fmt"

	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// GetMCPToolsBySSE 从 SSE transport 的 MCP server 加载全部工具
func GetMCPToolsBySSE(ctx context.Context, baseURL string, options ...transport.ClientOption) ([]tool.BaseTool, error) {
	cli, err := client.NewSSEMCPClient(baseURL, options...)
	if err != nil {
		return nil, fmt.Errorf("NewSSEMCPClient: %w", err)
	}
	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("sse client start: %w", err)
	}

	return getToolsFromCli(ctx, cli)
}

// GetMCPToolsByStreamableHTTP 从 StreamableHttp transport 的 MCP server 加载全部工具
func GetMCPToolsByStreamableHTTP(ctx context.Context, baseURL string, options ...transport.StreamableHTTPCOption) ([]tool.BaseTool, error) {
	cli, err := client.NewStreamableHttpClient(baseURL, options...)
	if err != nil {
		return nil, fmt.Errorf("NewStreamableHttpClient: %w", err)
	}
	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("streamable http client start: %w", err)
	}

	return getToolsFromCli(ctx, cli)
}

// GetMCPToolsByStdio 从 Stdio transport 的本地 MCP server 进程加载全部工具
func GetMCPToolsByStdio(ctx context.Context, command string, env []string, args ...string) ([]tool.BaseTool, error) {
	cli, err := client.NewStdioMCPClient(command, env, args...)
	if err != nil {
		return nil, fmt.Errorf("NewStdioMCPClient: %w", err)
	}
	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("stdio client start: %w", err)
	}

	return getToolsFromCli(ctx, cli)
}

// FilterToolsByNames 按工具名白名单过滤，只保留名称命中的工具。
// MCP server 暴露的工具面可能远大于 Agent 需要的范围（含写操作），
// 接入 LLM 前先在这里收窄，属于最小权限的代码层兜底。
func FilterToolsByNames(ctx context.Context, tools []tool.BaseTool, allowedNames []string) ([]tool.BaseTool, error) {
	allowed := make(map[string]struct{}, len(allowedNames))
	for _, name := range allowedNames {
		allowed[name] = struct{}{}
	}

	filtered := make([]tool.BaseTool, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("get tool info: %w", err)
		}
		if _, ok := allowed[info.Name]; ok {
			filtered = append(filtered, t)
		}
	}

	return filtered, nil
}

// getToolsFromCli 完成 MCP 握手（Initialize）并拉取 server 端工具清单
func getToolsFromCli(ctx context.Context, cli *client.Client) ([]tool.BaseTool, error) {
	initRequest := mcp.InitializeRequest{
		Request: mcp.Request{},
		Params:  mcp.InitializeParams{},
		Header:  nil,
	}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "datasearch-demo",
		Version: "1.0.0",
	}

	if _, err := cli.Initialize(ctx, initRequest); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	tools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: cli})
	if err != nil {
		return nil, fmt.Errorf("mcp get tools: %w", err)
	}

	return tools, nil
}

// Package version 暴露构建版本信息，由 ldflags 注入。
package version

var (
	// Version 在构建时由 -ldflags "-X ...version.Version=..." 注入。
	Version = "dev"
)

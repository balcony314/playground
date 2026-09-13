package cmd

// ═══════════════════════════════════════════════════════════════
// version.go — 版本信息子命令
// ═══════════════════════════════════════════════════════════════

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version 版本号，可通过 ldflags 在编译时注入
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "打印版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("ai-agent-demo %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

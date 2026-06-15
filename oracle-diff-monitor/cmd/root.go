package cmd

import (
	"fmt"
	"os"
	"oracle-diff-monitor/internal/store"

	"github.com/spf13/cobra"
)

var (
	dbPath     string
	storeInst  *store.Store
)

var rootCmd = &cobra.Command{
	Use:   "oracle-diff-monitor",
	Short: "Oracle 表结构差异监控工具",
	Long:  `配置多个 Oracle 数据库，定时比对表结构差异并通过邮件或 Webhook 通知。`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() != "serve" && cmd.Name() != "help" && cmd.Name() != "oracle-diff-monitor" {
			s, err := store.New(dbPath)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			storeInst = s
		}
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if storeInst != nil {
			storeInst.Close()
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "./data/oracle-diff.db", "SQLite 数据库路径")
}

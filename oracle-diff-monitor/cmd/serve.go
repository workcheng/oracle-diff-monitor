package cmd

import (
	"fmt"
	"log"
	"oracle-diff-monitor/internal/compare"
	"oracle-diff-monitor/internal/scheduler"
	"oracle-diff-monitor/internal/store"
	"oracle-diff-monitor/internal/web"

	"github.com/spf13/cobra"
)

var (
	port int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动 Web 管理服务",
	Long:  `启动 Web 管理界面并自动运行定时调度任务。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(dbPath)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer s.Close()

		sch := scheduler.New(s, func(pairID int64) {
			compare.RunComparison(s, pairID)
		})
		if err := sch.Start(); err != nil {
			return fmt.Errorf("start scheduler: %w", err)
		}
		defer sch.Stop()

		server := web.NewServer(s, sch)
		addr := fmt.Sprintf(":%d", port)
		log.Printf("Web 服务已启动: http://localhost:%d", port)
		return server.Run(addr)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntVarP(&port, "port", "p", 8080, "服务监听端口")
}

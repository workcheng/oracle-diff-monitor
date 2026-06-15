package cmd

import (
	"encoding/json"
	"fmt"
	"oracle-diff-monitor/internal/compare"
	"oracle-diff-monitor/internal/models"
	"oracle-diff-monitor/internal/store"

	"github.com/spf13/cobra"
)

var (
	pairID int64
	output string
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "执行一次表结构比对",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(dbPath)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer s.Close()

		result, diffs, err := compare.RunComparison(s, pairID)
		if err != nil {
			return fmt.Errorf("比对错误: %w", err)
		}

		switch output {
		case "json":
			out := map[string]interface{}{"run": result, "diffs": diffs}
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(data))
		default:
			fmt.Printf("比对完成: run_id=%d, 状态=%s, 扫描表=%d, 差异表=%d, 差异数=%d\n",
				result.ID, result.Status, result.TotalTables, result.DiffTables, len(diffs))
			for _, d := range diffs {
				fmt.Printf("  [%s] %s / %s: %s -> %s\n", models.DiffTypeLabel(d.DiffType), d.TableName, d.ColumnName, d.SourceValue, d.TargetValue)
			}
		}
		return nil
	},
}

var checkListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有比对任务",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(dbPath)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer s.Close()

		pairs, err := s.ListComparePairs()
		if err != nil {
			return err
		}
		fmt.Printf("%-4s %-20s %-8s %-8s %s\n", "ID", "名称", "源库ID", "目标库ID", "启用")
		for _, p := range pairs {
			fmt.Printf("%-4d %-20s %-8d %-8d %v\n", p.ID, p.Name, p.SourceDBID, p.TargetDBID, p.Enabled)
		}
		return nil
	},
}

var checkResultsCmd = &cobra.Command{
	Use:   "results",
	Short: "查看比对结果",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(dbPath)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer s.Close()

		runs, err := s.ListCompareRuns(pairID, 10)
		if err != nil {
			return err
		}
		for _, r := range runs {
			fmt.Printf("Run #%d: %s [%s] 表:%d 差异:%d\n", r.ID, r.StartedAt.Format("2006-01-02 15:04:05"), r.Status, r.TotalTables, r.DiffTables)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(checkCmd)
	checkCmd.Flags().Int64VarP(&pairID, "pair-id", "i", 0, "比对任务ID")
	checkCmd.Flags().StringVarP(&output, "output", "o", "text", "输出格式 (text/json)")
	checkCmd.MarkFlagRequired("pair-id")

	checkCmd.AddCommand(checkListCmd)
	checkResultsCmd.Flags().Int64VarP(&pairID, "pair-id", "i", 0, "比对任务ID")
	checkResultsCmd.MarkFlagRequired("pair-id")
	checkCmd.AddCommand(checkResultsCmd)
}

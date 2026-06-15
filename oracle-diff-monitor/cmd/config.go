package cmd

import (
	"fmt"
	"oracle-diff-monitor/internal/models"
	"oracle-diff-monitor/internal/oracle"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "管理配置",
}

var configListDBCmd = &cobra.Command{
	Use:   "list-db",
	Short: "列出所有数据库连接",
	RunE: func(cmd *cobra.Command, args []string) error {
		dbs, err := storeInst.ListDatabases()
		if err != nil {
			return err
		}
		fmt.Printf("%-4s %-20s %-15s %-5s %-20s %s\n", "ID", "名称", "主机", "端口", "SERVICE_NAME", "用户")
		for _, d := range dbs {
			svc := d.ServiceName
			if svc == "" {
				svc = d.SID
			}
			fmt.Printf("%-4d %-20s %-15s %-5d %-20s %s\n", d.ID, d.Name, d.Host, d.Port, svc, d.Username)
		}
		return nil
	},
}

var configAddDBCmd = &cobra.Command{
	Use:   "add-db",
	Short: "添加数据库连接",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		host, _ := cmd.Flags().GetString("host")
		port, _ := cmd.Flags().GetInt("port")
		service, _ := cmd.Flags().GetString("service")
		sid, _ := cmd.Flags().GetString("sid")
		user, _ := cmd.Flags().GetString("user")
		pass, _ := cmd.Flags().GetString("pass")

		if name == "" || host == "" || user == "" || pass == "" {
			return fmt.Errorf("name, host, user, pass 为必填项")
		}
		if service == "" && sid == "" {
			return fmt.Errorf("service 或 sid 至少填一个")
		}

		db := &models.Database{
			Name:        name,
			Type:        "oracle",
			Host:        host,
			Port:        port,
			ServiceName: service,
			SID:         sid,
			Username:    user,
			Password:    pass,
		}
		id, err := storeInst.CreateDatabase(db)
		if err != nil {
			return fmt.Errorf("create db: %w", err)
		}
		fmt.Printf("数据库连接已添加, ID=%d\n", id)
		return nil
	},
}

var configTestDBCmd = &cobra.Command{
	Use:   "test-db",
	Short: "测试数据库连接",
	RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetInt64("id")
		db, err := storeInst.GetDatabase(id)
		if err != nil {
			return fmt.Errorf("get db: %w", err)
		}

		client, err := oracle.NewClient(db)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
		client.Close()
		fmt.Println("连接成功 ✓")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configListDBCmd)
	configCmd.AddCommand(configAddDBCmd)

	configAddDBCmd.Flags().String("name", "", "连接名称")
	configAddDBCmd.Flags().String("host", "", "主机地址")
	configAddDBCmd.Flags().Int("port", 1521, "端口")
	configAddDBCmd.Flags().String("service", "", "Service Name")
	configAddDBCmd.Flags().String("sid", "", "SID")
	configAddDBCmd.Flags().String("user", "", "用户名")
	configAddDBCmd.Flags().String("pass", "", "密码")

	configCmd.AddCommand(configTestDBCmd)
	configTestDBCmd.Flags().Int64("id", 0, "数据库ID")
	configTestDBCmd.MarkFlagRequired("id")
}

# Oracle Diff Monitor

Oracle 数据库表结构差异监控工具。配置多个 Oracle 数据库，定时比对表结构差异并通过邮件或 Webhook 通知。

## 功能

- **多数据库管理** — 添加/编辑/删除/测试连接 Oracle 数据库
- **表结构比对** — 配置源库→目标库比对任务，支持按 Schema + 表名过滤
- **丰富比对维度** — 检查表存在性、列（类型/长度/精度/可空/默认值/顺序）、索引、主键/外键/约束差异
- **定时调度** — 支持 Cron 表达式定时自动执行比对
- **多渠道通知** — 有差异/出错/成功时通过 SMTP 邮件或 Webhook 通知
- **Web 管理面板** — 仪表盘、差异详情、结果历史、JSON 导出

## 快速开始

### 前提条件

- Go 1.26+
- CGO 编译器（用于 SQLite 驱动）
- Oracle 数据库（用于比对）

### 编译

```bash
cd oracle-diff-monitor
go build -o oracle-diff-monitor.exe .
```

### 运行 Web 服务

```bash
oracle-diff-monitor serve --port 8080 --db ./data/oracle-diff.db
```

启动后访问 http://localhost:8080

### 命令行工具

```bash
# 查看帮助
oracle-diff-monitor --help

# 检查并执行一次比对
oracle-diff-monitor check --pair 1

# 管理数据库配置
oracle-diff-monitor config --list
oracle-diff-monitor config --add
```

## 界面截图

| 页面 | 功能 |
|------|------|
| 仪表盘 | 系统概览、最近比对记录 |
| 数据库管理 | 增删改查 Oracle 数据库连接 |
| 比对任务 | 配置源库/目标库比对 |
| 比对结果 | 历史记录、差异详情 |
| 系统设置 | 通知渠道、定时调度 |

## 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go 1.26 |
| Web 框架 | Gin |
| 数据库 | SQLite (go-sqlite3) |
| Oracle 驱动 | go-ora |
| 调度器 | robfig/cron |
| 邮件 | go-simple-mail |
| 前端 | 原生 HTML + Tailwind CSS |

## 性能优化

比对引擎支持三层加速：

1. **源/目标并行查询** — 源库和目标库的元数据查询同时执行
2. **批量拉取** — 一次查询获取所有表的列/索引/约束信息，避免逐表 N+1 查询
3. **多表并发比对** — 8-worker goroutine 池并行处理内存中的表比对

## 项目结构

```
oracle-diff-monitor/
├── main.go              # 入口
├── cmd/                 # CLI 命令
│   ├── root.go          # Cobra 根命令
│   ├── serve.go         # Web 服务
│   ├── check.go         # 手动比对
│   └── config.go        # 数据库配置管理
├── internal/
│   ├── models/          # 数据模型
│   ├── store/           # SQLite 存储层
│   ├── oracle/          # Oracle 客户端 + 比对引擎
│   ├── compare/         # 比对运行器 + 通知触发
│   ├── scheduler/       # 定时调度器
│   ├── notifier/        # 通知（邮件/Webhook）
│   └── web/             # Web 界面 + 模板
└── data/                # SQLite 数据库文件
```

## 许可

MIT

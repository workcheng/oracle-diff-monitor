# Oracle Diff Monitor

Oracle 数据库表结构差异监控工具。配置多个 Oracle 数据库，定时比对表结构差异并通过邮件、Webhook 或钉钉通知。

## 功能

- **多数据库管理** — 添加/编辑/删除/测试连接 Oracle 数据库
- **表结构比对** — 配置源库→目标库比对任务，支持按 Schema + 表名过滤
- **丰富比对维度** — 检查表存在性、列（类型/长度/精度/可空/默认值/顺序）、索引、主键/外键/约束差异
- **智能匹配** — 自动识别 Oracle 自动生成的约束/索引名称（SYS_Cnnnnn），按内容而非名称匹配，避免误报
- **指定表比对** — 支持从表清单中勾选需要比对的部分表，支持搜索过滤
- **定时调度** — 支持 Cron 表达式定时自动执行比对
- **多渠道通知** — 有差异/出错/成功时通过 SMTP 邮件、Webhook 或钉钉机器人通知
- **通知摘要** — 钉钉通知发送差异统计汇总（差异类型统计 + 差异表TOP），而非全量明细
- **差异详情链接** — 通知中附带 Web 端差异详情链接，点击即可查看完整报告
- **Web 管理面板** — 仪表盘（含耗时统计）、差异详情（显示源库/目标库）、结果历史、JSON 导出

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
# 本机访问
oracle-diff-monitor serve --port 8080

# 指定外部访问地址（通知中的链接才能点击）
oracle-diff-monitor serve --port 8080 --notify-base-url http://your-server:8080
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

## 界面说明

| 页面 | 功能 |
|------|------|
| 仪表盘 | 系统概览、最近比对记录（含耗时） |
| 数据库管理 | 增删改查 Oracle 数据库连接 |
| 比对任务 | 配置源库/目标库比对、勾选指定表、配置通知触发条件 |
| 比对结果 | 历史记录（含耗时）、差异详情（显示源库/目标库）、JSON 导出 |
| 系统设置 | 通知渠道（邮件/Webhook/钉钉）、定时调度 |

### 通知配置

1. **创建通知渠道** — 系统设置 → 通知渠道，支持三种类型：
   - **邮件**：SMTP（支持 SSL/STARTTLS）
   - **Webhook**：通用 HTTP POST，自定义 Headers
   - **钉钉**：钉钉群机器人，支持加签（HMAC-SHA256），markdown 格式消息
2. **关联比对任务** — 编辑比对任务 → 通知配置，勾选触发条件（有差异时/出错时/成功时）
3. **设置外部地址** — 启动时指定 `--notify-base-url`，通知中将附带差异详情链接

### 指定表比对

编辑比对任务时，点击"加载表清单"从源库获取表列表，勾选需要比对的表后点击"确认选择"保存。不勾选则比对全部表。

## 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go 1.26 |
| Web 框架 | Gin |
| 数据库 | SQLite (go-sqlite3) |
| Oracle 驱动 | go-ora |
| 调度器 | robfig/cron |
| 邮件 | 标准库 net/smtp + crypto/tls |
| 前端 | 原生 HTML + Tailwind CSS |

## 性能优化

比对引擎支持三层加速：

1. **源/目标并行查询** — 源库和目标库的元数据查询同时执行
2. **批量拉取** — 一次查询获取所有表的列/索引/约束信息，避免逐表 N+1 查询（从 10N~16N 次查询降至 6 次）
3. **多表并发比对** — 8-worker goroutine 池并行处理内存中的表比对

### 内容匹配优化

索引、主键、约束的比对采用两阶段匹配：
- **阶段1（名称匹配）**：按名称精准匹配并比较内容差异
- **阶段2（内容匹配）**：名称不匹配时按内容指纹二次配对（索引用 Type+Uniqueness+Columns，约束用 SearchCondition/Columns）

避免 Oracle 自动生成名称（SYS_Cnnnnn）在不同库上不同导致的误报。

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
│   ├── notifier/        # 通知（邮件/Webhook/钉钉）
│   └── web/             # Web 界面 + 模板
└── data/                # SQLite 数据库文件
```

## 许可

MIT

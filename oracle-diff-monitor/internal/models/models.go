package models

import "time"

type Database struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	ServiceName string    `json:"service_name"`
	SID         string    `json:"sid"`
	Username    string    `json:"username"`
	Password    string    `json:"password"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ComparePair struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	SourceDBID  int64     `json:"source_db_id"`
	TargetDBID  int64     `json:"target_db_id"`
	SchemaName  string    `json:"schema_name"`
	TableFilter string    `json:"table_filter"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

type CompareRun struct {
	ID              int64      `json:"id"`
	PairID          int64      `json:"pair_id"`
	Status          string     `json:"status"`
	TotalTables     int        `json:"total_tables"`
	ProcessedTables int        `json:"processed_tables"`
	DiffTables      int        `json:"diff_tables"`
	CurrentTable    string     `json:"current_table"`
	ProgressMsg     string     `json:"progress_msg"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	ErrorMsg        string     `json:"error_msg"`
}

type DiffDetail struct {
	ID          int64     `json:"id"`
	RunID       int64     `json:"run_id"`
	TableName   string    `json:"table_name"`
	DiffType    string    `json:"diff_type"`
	ColumnName  string    `json:"column_name"`
	SourceValue string    `json:"source_value"`
	TargetValue string    `json:"target_value"`
	CreatedAt   time.Time `json:"created_at"`
}

type Notification struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	ConfigJSON string    `json:"config_json"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

type EmailConfig struct {
	SMTPHost    string   `json:"smtp_host"`
	SMTPPort    int      `json:"smtp_port"`
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	FromAddr    string   `json:"from_addr"`
	ToAddresses []string `json:"to_addresses"`
	UseTLS      bool     `json:"use_tls"`
}

type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type DingTalkConfig struct {
	URL     string            `json:"url"`
	Secret  string            `json:"secret"`
	Headers map[string]string `json:"headers"`
}

type CompareNotification struct {
	ID             int64 `json:"id"`
	PairID         int64 `json:"pair_id"`
	NotificationID int64 `json:"notification_id"`
	OnDiff         bool  `json:"on_diff"`
	OnError        bool  `json:"on_error"`
	OnSuccess      bool  `json:"on_success"`
}

type Schedule struct {
	ID        int64     `json:"id"`
	PairID    int64     `json:"pair_id"`
	CronExpr  string    `json:"cron_expr"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type TableInfo struct {
	TableName string `json:"table_name"`
}

type ColumnInfo struct {
	ColumnName    string `json:"column_name"`
	DataType      string `json:"data_type"`
	DataLength    int    `json:"data_length"`
	DataPrecision int    `json:"data_precision"`
	DataScale     int    `json:"data_scale"`
	Nullable      string `json:"nullable"`
	DataDefault   string `json:"data_default"`
	ColumnID      int    `json:"column_id"`
}

type IndexInfo struct {
	IndexName  string   `json:"index_name"`
	IndexType  string   `json:"index_type"`
	Uniqueness string   `json:"uniqueness"`
	Columns    []string `json:"columns"`
}

type ConstraintInfo struct {
	ConstraintName  string   `json:"constraint_name"`
	ConstraintType  string   `json:"constraint_type"`
	TableName       string   `json:"table_name"`
	SearchCondition string   `json:"search_condition"`
	RConstrainName  string   `json:"r_constraint_name"`
	Status          string   `json:"status"`
	Columns         []string `json:"columns"`
	RTableName      string   `json:"r_table_name"`
	RColumns        []string `json:"r_columns"`
	DeleteRule      string   `json:"delete_rule"`
}

type SchemaDiff struct {
	RunID       int64
	SourceName  string
	TargetName  string
	Differences []DiffDetail
}

func DiffTypeLabel(t string) string {
	labels := map[string]string{
		"missing_table":                 "源库缺少表",
		"extra_table":                   "源库多余表",
		"missing_column":                "源库缺少列",
		"extra_column":                  "源库多余列",
		"type_mismatch":                 "数据类型不一致",
		"length_mismatch":               "数据长度不一致",
		"precision_mismatch":            "精度不一致",
		"scale_mismatch":                "小数位数不一致",
		"nullable_mismatch":             "可为空属性不一致",
		"default_mismatch":              "默认值不一致",
		"column_order_mismatch":         "列顺序不一致",
		"missing_index":                 "源库缺少索引",
		"extra_index":                   "源库多余索引",
		"index_type_mismatch":           "索引类型不一致",
		"index_column_mismatch":         "索引列不一致",
		"missing_pk":                    "源库缺少主键",
		"extra_pk":                      "源库多余主键",
		"pk_column_mismatch":            "主键列不一致",
		"missing_fk":                    "源库缺少外键",
		"extra_fk":                      "源库多余外键",
		"fk_column_mismatch":            "外键列不一致",
		"fk_ref_mismatch":               "外键引用不一致",
		"missing_constraint":            "源库缺少约束",
		"extra_constraint":              "源库多余约束",
		"constraint_condition_mismatch": "约束条件不一致",
	}
	if l, ok := labels[t]; ok {
		return l
	}
	return t
}

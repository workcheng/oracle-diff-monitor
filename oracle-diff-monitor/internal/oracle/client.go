package oracle

import (
	"database/sql"
	"fmt"
	"oracle-diff-monitor/internal/models"
	"strings"

	go_ora "github.com/sijms/go-ora/v2"
)

type Client struct {
	db     *sql.DB
	config *models.Database
}

func NewClient(config *models.Database) (*Client, error) {
	connStr := buildConnStr(config)
	db, err := sql.Open("oracle", connStr)
	if err != nil {
		return nil, fmt.Errorf("open oracle: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping oracle: %w", err)
	}
	return &Client{db: db, config: config}, nil
}

func (c *Client) Close() error {
	return c.db.Close()
}

func (c *Client) TestConnection() error {
	return c.db.Ping()
}

func (c *Client) GetTables(schema string, filter string) ([]string, error) {
	query := `SELECT TABLE_NAME FROM ALL_TABLES`
	var args []interface{}
	if schema != "" {
		query += ` WHERE OWNER = :1`
		args = append(args, strings.ToUpper(schema))
	} else {
		query += ` WHERE OWNER = USER`
	}
	if filter != "" {
		f := strings.ReplaceAll(filter, "%", "%")
		cond := ` AND TABLE_NAME LIKE :` + fmt.Sprintf("%d", len(args)+1)
		query += cond
		args = append(args, strings.ToUpper(f))
	}
	query += ` ORDER BY TABLE_NAME`

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, strings.TrimSpace(name))
	}
	return tables, nil
}

// ---- Original per-table queries (kept for backward compatibility) ----

func (c *Client) GetColumns(schema, table string) ([]models.ColumnInfo, error) {
	query := `SELECT COLUMN_NAME, DATA_TYPE, NVL(DATA_LENGTH, 0), NVL(DATA_PRECISION, 0), NVL(DATA_SCALE, 0), NULLABLE, NVL(DATA_DEFAULT, ''), COLUMN_ID
		FROM ALL_TAB_COLUMNS WHERE OWNER = :1 AND TABLE_NAME = :2 ORDER BY COLUMN_ID`

	rows, err := c.db.Query(query, strings.ToUpper(schema), strings.ToUpper(table))
	if err != nil {
		return nil, fmt.Errorf("query columns: %w", err)
	}
	defer rows.Close()

	var cols []models.ColumnInfo
	for rows.Next() {
		c := models.ColumnInfo{}
		var dataLength, dataPrecision, dataScale, columnID sql.NullInt64
		var dataDefault sql.NullString
		if err := rows.Scan(&c.ColumnName, &c.DataType, &dataLength, &dataPrecision, &dataScale, &c.Nullable, &dataDefault, &columnID); err != nil {
			return nil, err
		}
		c.ColumnName = strings.TrimSpace(c.ColumnName)
		if dataLength.Valid {
			c.DataLength = int(dataLength.Int64)
		}
		if dataPrecision.Valid {
			c.DataPrecision = int(dataPrecision.Int64)
		}
		if dataScale.Valid {
			c.DataScale = int(dataScale.Int64)
		}
		if dataDefault.Valid {
			c.DataDefault = dataDefault.String
		}
		if columnID.Valid {
			c.ColumnID = int(columnID.Int64)
		}
		cols = append(cols, c)
	}
	return cols, nil
}

func (c *Client) GetIndexes(schema, table string) ([]models.IndexInfo, error) {
	query := `SELECT i.INDEX_NAME, i.INDEX_TYPE, i.UNIQUENESS
		FROM ALL_INDEXES i WHERE i.OWNER = :1 AND i.TABLE_NAME = :2 ORDER BY i.INDEX_NAME`

	rows, err := c.db.Query(query, strings.ToUpper(schema), strings.ToUpper(table))
	if err != nil {
		return nil, fmt.Errorf("query indexes: %w", err)
	}
	defer rows.Close()

	var indexes []models.IndexInfo
	for rows.Next() {
		idx := models.IndexInfo{}
		if err := rows.Scan(&idx.IndexName, &idx.IndexType, &idx.Uniqueness); err != nil {
			return nil, err
		}
		idx.IndexName = strings.TrimSpace(idx.IndexName)

		cols, err := c.getIndexColumns(schema, idx.IndexName)
		if err != nil {
			return nil, err
		}
		idx.Columns = cols
		indexes = append(indexes, idx)
	}
	return indexes, nil
}

func (c *Client) getIndexColumns(schema, indexName string) ([]string, error) {
	query := `SELECT COLUMN_NAME FROM ALL_IND_COLUMNS WHERE INDEX_OWNER = :1 AND INDEX_NAME = :2 ORDER BY COLUMN_POSITION`
	rows, err := c.db.Query(query, strings.ToUpper(schema), strings.ToUpper(indexName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, strings.TrimSpace(name))
	}
	return cols, nil
}

func (c *Client) GetConstraints(schema, table string) ([]models.ConstraintInfo, error) {
	query := `SELECT CONSTRAINT_NAME, CONSTRAINT_TYPE, NVL(SEARCH_CONDITION, ''), NVL(R_CONSTRAINT_NAME, ''), NVL(STATUS, 'ENABLED')
		FROM ALL_CONSTRAINTS WHERE OWNER = :1 AND TABLE_NAME = :2 ORDER BY CONSTRAINT_NAME`

	rows, err := c.db.Query(query, strings.ToUpper(schema), strings.ToUpper(table))
	if err != nil {
		return nil, fmt.Errorf("query constraints: %w", err)
	}
	defer rows.Close()

	var constraints []models.ConstraintInfo
	for rows.Next() {
		con := models.ConstraintInfo{}
		var searchCondition, rConstraintName, status sql.NullString
		if err := rows.Scan(&con.ConstraintName, &con.ConstraintType, &searchCondition, &rConstraintName, &status); err != nil {
			return nil, err
		}
		con.ConstraintName = strings.TrimSpace(con.ConstraintName)
		if searchCondition.Valid {
			con.SearchCondition = searchCondition.String
		}
		if rConstraintName.Valid {
			con.RConstrainName = rConstraintName.String
		}
		if status.Valid {
			con.Status = status.String
		} else {
			con.Status = "ENABLED"
		}

		cols, err := c.getConstraintColumns(schema, con.ConstraintName)
		if err != nil {
			return nil, err
		}
		con.Columns = cols

		if con.ConstraintType == "R" && con.RConstrainName != "" {
			c.getReferencedInfo(schema, &con)
		}
		constraints = append(constraints, con)
	}
	return constraints, nil
}

func (c *Client) getConstraintColumns(schema, constraintName string) ([]string, error) {
	query := `SELECT COLUMN_NAME FROM ALL_CONS_COLUMNS WHERE OWNER = :1 AND CONSTRAINT_NAME = :2 ORDER BY POSITION`
	rows, err := c.db.Query(query, strings.ToUpper(schema), strings.ToUpper(constraintName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, strings.TrimSpace(name))
	}
	return cols, nil
}

func (c *Client) getReferencedInfo(schema string, con *models.ConstraintInfo) {
	query := `SELECT c.TABLE_NAME, LISTAGG(cc.COLUMN_NAME, ',') WITHIN GROUP (ORDER BY cc.POSITION)
		FROM ALL_CONSTRAINTS c JOIN ALL_CONS_COLUMNS cc ON c.OWNER = cc.OWNER AND c.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
		WHERE c.OWNER = :1 AND c.CONSTRAINT_NAME = :2 GROUP BY c.TABLE_NAME`
	row := c.db.QueryRow(query, strings.ToUpper(schema), strings.ToUpper(con.RConstrainName))
	var rTable, rCols string
	if err := row.Scan(&rTable, &rCols); err == nil {
		con.RTableName = strings.TrimSpace(rTable)
		con.RColumns = strings.Split(rCols, ",")
		for i := range con.RColumns {
			con.RColumns[i] = strings.TrimSpace(con.RColumns[i])
		}
	}

	deleteRuleQuery := `SELECT DELETE_RULE FROM ALL_CONSTRAINTS WHERE OWNER = :1 AND CONSTRAINT_NAME = :2`
	row2 := c.db.QueryRow(deleteRuleQuery, strings.ToUpper(schema), strings.ToUpper(con.ConstraintName))
	var deleteRule sql.NullString
	if err := row2.Scan(&deleteRule); err == nil && deleteRule.Valid {
		con.DeleteRule = deleteRule.String
	}
}

// ---- Batch queries for performance optimization ----

const oracleInBatchSize = 900

// batchStrings splits a slice into batches of the given size (for Oracle IN clause limit).
func batchStrings(items []string, batchSize int) [][]string {
	if len(items) == 0 {
		return nil
	}
	var batches [][]string
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batches = append(batches, items[i:end])
	}
	return batches
}

// buildInClause builds Oracle positional placeholders (:1, :2, ...) for n items
// and appends the upper-cased items to args. Returns the clause string "(:N, :N+1, ...)".
func buildInClause(items []string, args *[]interface{}) string {
	if len(items) == 0 {
		return "(NULL)"
	}
	places := make([]string, len(items))
	for i, item := range items {
		*args = append(*args, strings.ToUpper(item))
		places[i] = fmt.Sprintf(":%d", len(*args))
	}
	return "(" + strings.Join(places, ",") + ")"
}

// GetAllColumns returns all columns for the given tables, keyed by TABLE_NAME.
func (c *Client) GetAllColumns(schema string, tables []string) (map[string][]models.ColumnInfo, error) {
	result := make(map[string][]models.ColumnInfo)
	if len(tables) == 0 {
		return result, nil
	}

	for _, batch := range batchStrings(tables, oracleInBatchSize) {
		var args []interface{}
		args = append(args, strings.ToUpper(schema))
		inClause := buildInClause(batch, &args)

		query := `SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE, NVL(DATA_LENGTH, 0), NVL(DATA_PRECISION, 0),
			NVL(DATA_SCALE, 0), NULLABLE, NVL(DATA_DEFAULT, ''), COLUMN_ID
			FROM ALL_TAB_COLUMNS WHERE OWNER = :1 AND TABLE_NAME IN ` + inClause + ` ORDER BY TABLE_NAME, COLUMN_ID`

		rows, err := c.db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("query all columns: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var tableName string
			col := models.ColumnInfo{}
			var dataLength, dataPrecision, dataScale, columnID sql.NullInt64
			var dataDefault sql.NullString
			if err := rows.Scan(&tableName, &col.ColumnName, &col.DataType, &dataLength, &dataPrecision, &dataScale, &col.Nullable, &dataDefault, &columnID); err != nil {
				return nil, err
			}
			tableName = strings.TrimSpace(tableName)
			col.ColumnName = strings.TrimSpace(col.ColumnName)
			if dataLength.Valid {
				col.DataLength = int(dataLength.Int64)
			}
			if dataPrecision.Valid {
				col.DataPrecision = int(dataPrecision.Int64)
			}
			if dataScale.Valid {
				col.DataScale = int(dataScale.Int64)
			}
			if dataDefault.Valid {
				col.DataDefault = dataDefault.String
			}
			if columnID.Valid {
				col.ColumnID = int(columnID.Int64)
			}
			result[tableName] = append(result[tableName], col)
		}
	}
	return result, nil
}

// GetAllIndexes returns all indexes for the given tables, keyed by TABLE_NAME.
func (c *Client) GetAllIndexes(schema string, tables []string) (map[string][]models.IndexInfo, error) {
	result := make(map[string][]models.IndexInfo)
	if len(tables) == 0 {
		return result, nil
	}

	// 1) Fetch all indexes for all tables in batches
	type indexRow struct {
		TableName  string
		IndexName  string
		IndexType  string
		Uniqueness string
	}
	var allIndexRows []indexRow

	for _, batch := range batchStrings(tables, oracleInBatchSize) {
		var args []interface{}
		args = append(args, strings.ToUpper(schema))
		inClause := buildInClause(batch, &args)

		query := `SELECT TABLE_NAME, INDEX_NAME, INDEX_TYPE, UNIQUENESS
			FROM ALL_INDEXES WHERE OWNER = :1 AND TABLE_NAME IN ` + inClause + ` ORDER BY TABLE_NAME, INDEX_NAME`

		rows, err := c.db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("query all indexes: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var ir indexRow
			if err := rows.Scan(&ir.TableName, &ir.IndexName, &ir.IndexType, &ir.Uniqueness); err != nil {
				return nil, err
			}
			ir.TableName = strings.TrimSpace(ir.TableName)
			ir.IndexName = strings.TrimSpace(ir.IndexName)
			allIndexRows = append(allIndexRows, ir)
		}
	}

	if len(allIndexRows) == 0 {
		return result, nil
	}

	// 2) Batch fetch index columns for all index names
	allIndexNames := make([]string, len(allIndexRows))
	for i, ir := range allIndexRows {
		allIndexNames[i] = ir.IndexName
	}
	indexColumns := c.batchGetIndexColumns(schema, allIndexNames)

	// 3) Assemble results
	for _, ir := range allIndexRows {
		result[ir.TableName] = append(result[ir.TableName], models.IndexInfo{
			IndexName:  ir.IndexName,
			IndexType:  ir.IndexType,
			Uniqueness: ir.Uniqueness,
			Columns:    indexColumns[ir.IndexName],
		})
	}
	return result, nil
}

func (c *Client) batchGetIndexColumns(schema string, indexNames []string) map[string][]string {
	result := make(map[string][]string)
	if len(indexNames) == 0 {
		return result
	}

	for _, batch := range batchStrings(indexNames, oracleInBatchSize) {
		var args []interface{}
		args = append(args, strings.ToUpper(schema))
		inClause := buildInClause(batch, &args)

		query := `SELECT INDEX_NAME, COLUMN_NAME FROM ALL_IND_COLUMNS
			WHERE INDEX_OWNER = :1 AND INDEX_NAME IN ` + inClause + ` ORDER BY INDEX_NAME, COLUMN_POSITION`

		rows, err := c.db.Query(query, args...)
		if err != nil {
			// Non-fatal: return partial results
			return result
		}
		defer rows.Close()

		for rows.Next() {
			var idxName, colName string
			if err := rows.Scan(&idxName, &colName); err != nil {
				continue
			}
			result[strings.TrimSpace(idxName)] = append(result[strings.TrimSpace(idxName)], strings.TrimSpace(colName))
		}
	}
	return result
}

// GetAllConstraints returns all constraints for the given tables, keyed by TABLE_NAME.
func (c *Client) GetAllConstraints(schema string, tables []string) (map[string][]models.ConstraintInfo, error) {
	result := make(map[string][]models.ConstraintInfo)
	if len(tables) == 0 {
		return result, nil
	}

	// 1) Fetch all constraints for all tables in batches
	type conRow struct {
		TableName       string
		ConstraintName  string
		ConstraintType  string
		SearchCondition string
		RConstrainName  string
		Status          string
	}
	var allConRows []conRow

	for _, batch := range batchStrings(tables, oracleInBatchSize) {
		var args []interface{}
		args = append(args, strings.ToUpper(schema))
		inClause := buildInClause(batch, &args)

		query := `SELECT TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE, SEARCH_CONDITION, R_CONSTRAINT_NAME, STATUS
			FROM ALL_CONSTRAINTS WHERE OWNER = :1 AND TABLE_NAME IN ` + inClause + ` ORDER BY TABLE_NAME, CONSTRAINT_NAME`

		rows, err := c.db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("query all constraints: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var cr conRow
			var sc, rcn, st sql.NullString
			if err := rows.Scan(&cr.TableName, &cr.ConstraintName, &cr.ConstraintType, &sc, &rcn, &st); err != nil {
				return nil, err
			}
			cr.TableName = strings.TrimSpace(cr.TableName)
			cr.ConstraintName = strings.TrimSpace(cr.ConstraintName)
			if sc.Valid {
				cr.SearchCondition = sc.String
			}
			if rcn.Valid {
				cr.RConstrainName = rcn.String
			}
			if st.Valid {
				cr.Status = st.String
			} else {
				cr.Status = "ENABLED"
			}
			allConRows = append(allConRows, cr)
		}
	}

	if len(allConRows) == 0 {
		return result, nil
	}

	// 2) Batch fetch constraint columns for all constraint names
	allConNames := make([]string, len(allConRows))
	for i, cr := range allConRows {
		allConNames[i] = cr.ConstraintName
	}
	constraintColumns := c.batchGetConstraintColumns(schema, allConNames)

	// 3) Batch fetch FK referenced info
	rConstrainNames := make([]string, 0)
	for _, cr := range allConRows {
		if cr.ConstraintType == "R" && cr.RConstrainName != "" {
			rConstrainNames = append(rConstrainNames, cr.RConstrainName)
		}
	}
	referencedInfo := c.batchGetReferencedInfo(schema, rConstrainNames)

	// 4) Batch fetch FK delete rules
	fkDeleteRules := c.batchGetDeleteRules(schema, allConNames)

	// 5) Assemble results
	for _, cr := range allConRows {
		con := models.ConstraintInfo{
			ConstraintName:  cr.ConstraintName,
			ConstraintType:  cr.ConstraintType,
			SearchCondition: cr.SearchCondition,
			RConstrainName:  cr.RConstrainName,
			Status:          cr.Status,
			Columns:         constraintColumns[cr.ConstraintName],
		}
		if ri, ok := referencedInfo[cr.RConstrainName]; cr.ConstraintType == "R" && cr.RConstrainName != "" && ok {
			con.RTableName = ri.RTableName
			con.RColumns = ri.RColumns
		}
		if dr, ok := fkDeleteRules[cr.ConstraintName]; ok {
			con.DeleteRule = dr
		}
		result[cr.TableName] = append(result[cr.TableName], con)
	}
	return result, nil
}

func (c *Client) batchGetConstraintColumns(schema string, constraintNames []string) map[string][]string {
	result := make(map[string][]string)
	if len(constraintNames) == 0 {
		return result
	}

	for _, batch := range batchStrings(constraintNames, oracleInBatchSize) {
		var args []interface{}
		args = append(args, strings.ToUpper(schema))
		inClause := buildInClause(batch, &args)

		query := `SELECT CONSTRAINT_NAME, COLUMN_NAME FROM ALL_CONS_COLUMNS
			WHERE OWNER = :1 AND CONSTRAINT_NAME IN ` + inClause + ` ORDER BY CONSTRAINT_NAME, POSITION`

		rows, err := c.db.Query(query, args...)
		if err != nil {
			return result
		}
		defer rows.Close()

		for rows.Next() {
			var conName, colName string
			if err := rows.Scan(&conName, &colName); err != nil {
				continue
			}
			result[strings.TrimSpace(conName)] = append(result[strings.TrimSpace(conName)], strings.TrimSpace(colName))
		}
	}
	return result
}

type refInfo struct {
	RTableName string
	RColumns   []string
}

func (c *Client) batchGetReferencedInfo(schema string, rConstrainNames []string) map[string]refInfo {
	result := make(map[string]refInfo)
	if len(rConstrainNames) == 0 {
		return result
	}

	for _, batch := range batchStrings(rConstrainNames, oracleInBatchSize) {
		var args []interface{}
		args = append(args, strings.ToUpper(schema))
		inClause := buildInClause(batch, &args)

		query := `SELECT c.CONSTRAINT_NAME, c.TABLE_NAME, LISTAGG(cc.COLUMN_NAME, ',') WITHIN GROUP (ORDER BY cc.POSITION) AS COLS
			FROM ALL_CONSTRAINTS c JOIN ALL_CONS_COLUMNS cc ON c.OWNER = cc.OWNER AND c.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
			WHERE c.OWNER = :1 AND c.CONSTRAINT_NAME IN ` + inClause + ` GROUP BY c.CONSTRAINT_NAME, c.TABLE_NAME`

		rows, err := c.db.Query(query, args...)
		if err != nil {
			return result
		}
		defer rows.Close()

		for rows.Next() {
			var conName, rTable, rCols string
			if err := rows.Scan(&conName, &rTable, &rCols); err != nil {
				continue
			}
			cols := strings.Split(rCols, ",")
			for i := range cols {
				cols[i] = strings.TrimSpace(cols[i])
			}
			result[strings.TrimSpace(conName)] = refInfo{RTableName: strings.TrimSpace(rTable), RColumns: cols}
		}
	}
	return result
}

func (c *Client) batchGetDeleteRules(schema string, constraintNames []string) map[string]string {
	result := make(map[string]string)
	if len(constraintNames) == 0 {
		return result
	}

	for _, batch := range batchStrings(constraintNames, oracleInBatchSize) {
		var args []interface{}
		args = append(args, strings.ToUpper(schema))
		inClause := buildInClause(batch, &args)

		query := `SELECT CONSTRAINT_NAME, DELETE_RULE FROM ALL_CONSTRAINTS
			WHERE OWNER = :1 AND CONSTRAINT_NAME IN ` + inClause

		rows, err := c.db.Query(query, args...)
		if err != nil {
			return result
		}
		defer rows.Close()

		for rows.Next() {
			var conName string
			var dr sql.NullString
			if err := rows.Scan(&conName, &dr); err != nil {
				continue
			}
			if dr.Valid {
				result[strings.TrimSpace(conName)] = dr.String
			} else {
				result[strings.TrimSpace(conName)] = "NO ACTION"
			}
		}
	}
	return result
}

func buildConnStr(cfg *models.Database) string {
	service := cfg.ServiceName
	if service == "" && cfg.SID != "" {
		service = cfg.SID
	}
	if service == "" {
		service = "ORCL"
	}
	return go_ora.BuildUrl(cfg.Host, cfg.Port, service, cfg.Username, cfg.Password, nil)
}

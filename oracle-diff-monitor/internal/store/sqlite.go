package store

import (
	"database/sql"
	"fmt"
	"log"
	"oracle-diff-monitor/internal/models"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS databases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			type TEXT DEFAULT 'oracle',
			host TEXT NOT NULL,
			port INTEGER DEFAULT 1521,
			service_name TEXT DEFAULT '',
			sid TEXT DEFAULT '',
			username TEXT NOT NULL,
			password TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS compare_pairs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			source_db_id INTEGER NOT NULL REFERENCES databases(id),
			target_db_id INTEGER NOT NULL REFERENCES databases(id),
			schema_name TEXT DEFAULT '',
			table_filter TEXT DEFAULT '',
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS compare_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pair_id INTEGER NOT NULL REFERENCES compare_pairs(id),
			status TEXT DEFAULT 'running',
			total_tables INTEGER DEFAULT 0,
			processed_tables INTEGER DEFAULT 0,
			diff_tables INTEGER DEFAULT 0,
			current_table TEXT DEFAULT '',
			progress_msg TEXT DEFAULT '',
			error_msg TEXT DEFAULT '',
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS diff_details (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL REFERENCES compare_runs(id),
			table_name TEXT NOT NULL,
			diff_type TEXT NOT NULL,
			column_name TEXT DEFAULT '',
			source_value TEXT DEFAULT '',
			target_value TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS compare_run_tables (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL REFERENCES compare_runs(id),
			table_name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'done',
			diff_count INTEGER DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(run_id, table_name)
		);
		CREATE TABLE IF NOT EXISTS notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			config_json TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS compare_notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pair_id INTEGER NOT NULL REFERENCES compare_pairs(id),
			notification_id INTEGER NOT NULL REFERENCES notifications(id),
			on_diff INTEGER DEFAULT 1,
			on_error INTEGER DEFAULT 1,
			on_success INTEGER DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pair_id INTEGER NOT NULL REFERENCES compare_pairs(id),
			cron_expr TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return err
	}
	for _, col := range []struct {
		name       string
		definition string
	}{
		{"processed_tables", "INTEGER DEFAULT 0"},
		{"current_table", "TEXT DEFAULT ''"},
		{"progress_msg", "TEXT DEFAULT ''"},
	} {
		if err := s.ensureColumn("compare_runs", col.name, col.definition); err != nil {
			return err
		}
	}
	if err := s.ensureColumn("compare_pairs", "selected_tables", "TEXT DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(table, name, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notNull, pk int
		var colName, colType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if colName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, definition))
	return err
}

// ---- Databases ----

func (s *Store) CreateDatabase(db *models.Database) (int64, error) {
	encPass, err := encrypt(db.Password)
	if err != nil {
		return 0, fmt.Errorf("encrypt password: %w", err)
	}
	res, err := s.db.Exec(
		`INSERT INTO databases (name, type, host, port, service_name, sid, username, password) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		db.Name, db.Type, db.Host, db.Port, db.ServiceName, db.SID, db.Username, encPass,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetDatabase(id int64) (*models.Database, error) {
	row := s.db.QueryRow(
		`SELECT id, name, type, host, port, COALESCE(service_name,''), COALESCE(sid,''), username, password, created_at, updated_at FROM databases WHERE id = ?`, id,
	)
	d := &models.Database{}
	var encPass string
	err := row.Scan(&d.ID, &d.Name, &d.Type, &d.Host, &d.Port, &d.ServiceName, &d.SID, &d.Username, &encPass, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	d.Password, _ = decrypt(encPass)
	return d, nil
}

func (s *Store) ListDatabases() ([]*models.Database, error) {
	rows, err := s.db.Query(
		`SELECT id, name, type, host, port, COALESCE(service_name,''), COALESCE(sid,''), username, password, created_at, updated_at FROM databases ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.Database
	for rows.Next() {
		d := &models.Database{}
		var encPass string
		if err := rows.Scan(&d.ID, &d.Name, &d.Type, &d.Host, &d.Port, &d.ServiceName, &d.SID, &d.Username, &encPass, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Password, _ = decrypt(encPass)
		list = append(list, d)
	}
	return list, nil
}

func (s *Store) UpdateDatabase(d *models.Database) error {
	d.UpdatedAt = time.Now()
	encPass, err := encrypt(d.Password)
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}
	_, err = s.db.Exec(
		`UPDATE databases SET name=?, host=?, port=?, service_name=?, sid=?, username=?, password=?, updated_at=? WHERE id=?`,
		d.Name, d.Host, d.Port, d.ServiceName, d.SID, d.Username, encPass, d.UpdatedAt, d.ID,
	)
	return err
}

func (s *Store) DeleteDatabase(id int64) error {
	_, err := s.db.Exec(`DELETE FROM databases WHERE id = ?`, id)
	return err
}

// ---- ComparePairs ----

func (s *Store) CreateComparePair(p *models.ComparePair) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO compare_pairs (name, source_db_id, target_db_id, schema_name, table_filter, selected_tables, enabled) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.SourceDBID, p.TargetDBID, p.SchemaName, p.TableFilter, p.SelectedTables, boolToInt(p.Enabled),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetComparePair(id int64) (*models.ComparePair, error) {
	row := s.db.QueryRow(
		`SELECT id, name, source_db_id, target_db_id, COALESCE(schema_name,''), COALESCE(table_filter,''), COALESCE(selected_tables,''), enabled, created_at FROM compare_pairs WHERE id = ?`, id,
	)
	p := &models.ComparePair{}
	var enabled int
	err := row.Scan(&p.ID, &p.Name, &p.SourceDBID, &p.TargetDBID, &p.SchemaName, &p.TableFilter, &p.SelectedTables, &enabled, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.Enabled = enabled == 1
	return p, nil
}

func (s *Store) ListComparePairs() ([]*models.ComparePair, error) {
	rows, err := s.db.Query(
		`SELECT id, name, source_db_id, target_db_id, COALESCE(schema_name,''), COALESCE(table_filter,''), COALESCE(selected_tables,''), enabled, created_at FROM compare_pairs ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.ComparePair
	for rows.Next() {
		p := &models.ComparePair{}
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &p.SourceDBID, &p.TargetDBID, &p.SchemaName, &p.TableFilter, &p.SelectedTables, &enabled, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Enabled = enabled == 1
		list = append(list, p)
	}
	return list, nil
}

func (s *Store) UpdateComparePair(p *models.ComparePair) error {
	_, err := s.db.Exec(
		`UPDATE compare_pairs SET name=?, source_db_id=?, target_db_id=?, schema_name=?, table_filter=?, selected_tables=?, enabled=? WHERE id=?`,
		p.Name, p.SourceDBID, p.TargetDBID, p.SchemaName, p.TableFilter, p.SelectedTables, boolToInt(p.Enabled), p.ID,
	)
	return err
}

func (s *Store) DeleteComparePair(id int64) error {
	s.db.Exec(`DELETE FROM diff_details WHERE run_id IN (SELECT id FROM compare_runs WHERE pair_id = ?)`, id)
	s.db.Exec(`DELETE FROM compare_notifications WHERE pair_id = ?`, id)
	s.db.Exec(`DELETE FROM schedules WHERE pair_id = ?`, id)
	s.db.Exec(`DELETE FROM compare_runs WHERE pair_id = ?`, id)
	_, err := s.db.Exec(`DELETE FROM compare_pairs WHERE id = ?`, id)
	return err
}

// ---- CompareRuns ----

func (s *Store) CreateCompareRun(r *models.CompareRun) (int64, error) {
	r.StartedAt = time.Now()
	res, err := s.db.Exec(
		`INSERT INTO compare_runs (pair_id, status, total_tables, processed_tables, diff_tables, current_table, progress_msg, error_msg, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.PairID, r.Status, r.TotalTables, r.ProcessedTables, r.DiffTables, r.CurrentTable, r.ProgressMsg, r.ErrorMsg, r.StartedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateCompareRun(r *models.CompareRun) error {
	now := time.Now()
	r.FinishedAt = &now
	_, err := s.db.Exec(
		`UPDATE compare_runs SET status=?, total_tables=?, processed_tables=?, diff_tables=?, current_table=?, progress_msg=?, error_msg=?, finished_at=? WHERE id=?`,
		r.Status, r.TotalTables, r.ProcessedTables, r.DiffTables, r.CurrentTable, r.ProgressMsg, r.ErrorMsg, *r.FinishedAt, r.ID,
	)
	return err
}

func (s *Store) UpdateCompareRunProgress(r *models.CompareRun) error {
	_, err := s.db.Exec(
		`UPDATE compare_runs SET total_tables=?, processed_tables=?, current_table=?, progress_msg=? WHERE id=?`,
		r.TotalTables, r.ProcessedTables, r.CurrentTable, r.ProgressMsg, r.ID,
	)
	return err
}

func (s *Store) GetLatestRunningCompareRun(pairID int64) (*models.CompareRun, error) {
	row := s.db.QueryRow(
		`SELECT id, pair_id, status, total_tables, processed_tables, diff_tables, COALESCE(current_table,''), COALESCE(progress_msg,''), COALESCE(error_msg,''), started_at, finished_at FROM compare_runs WHERE pair_id = ? AND status = 'running' ORDER BY started_at DESC LIMIT 1`,
		pairID,
	)
	r := &models.CompareRun{}
	var ft sql.NullTime
	err := row.Scan(&r.ID, &r.PairID, &r.Status, &r.TotalTables, &r.ProcessedTables, &r.DiffTables, &r.CurrentTable, &r.ProgressMsg, &r.ErrorMsg, &r.StartedAt, &ft)
	if err != nil {
		return nil, err
	}
	if ft.Valid {
		r.FinishedAt = &ft.Time
	}
	return r, nil
}

func (s *Store) ListRunningCompareRuns() ([]*models.CompareRun, error) {
	rows, err := s.db.Query(
		`SELECT id, pair_id, status, total_tables, processed_tables, diff_tables, COALESCE(current_table,''), COALESCE(progress_msg,''), COALESCE(error_msg,''), started_at, finished_at FROM compare_runs WHERE status = 'running' ORDER BY started_at`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.CompareRun
	for rows.Next() {
		r := &models.CompareRun{}
		var ft sql.NullTime
		if err := rows.Scan(&r.ID, &r.PairID, &r.Status, &r.TotalTables, &r.ProcessedTables, &r.DiffTables, &r.CurrentTable, &r.ProgressMsg, &r.ErrorMsg, &r.StartedAt, &ft); err != nil {
			return nil, err
		}
		if ft.Valid {
			r.FinishedAt = &ft.Time
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

func (s *Store) ListCompareRuns(pairID int64, limit int) ([]*models.CompareRun, error) {
	rows, err := s.db.Query(
		`SELECT id, pair_id, status, total_tables, processed_tables, diff_tables, COALESCE(current_table,''), COALESCE(progress_msg,''), COALESCE(error_msg,''), started_at, finished_at FROM compare_runs WHERE pair_id = ? ORDER BY started_at DESC LIMIT ?`,
		pairID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.CompareRun
	for rows.Next() {
		r := &models.CompareRun{}
		var ft sql.NullTime
		if err := rows.Scan(&r.ID, &r.PairID, &r.Status, &r.TotalTables, &r.ProcessedTables, &r.DiffTables, &r.CurrentTable, &r.ProgressMsg, &r.ErrorMsg, &r.StartedAt, &ft); err != nil {
			return nil, err
		}
		if ft.Valid {
			r.FinishedAt = &ft.Time
		}
		list = append(list, r)
	}
	return list, nil
}

func (s *Store) GetLatestRuns(limit int) ([]*models.CompareRun, error) {
	rows, err := s.db.Query(
		`SELECT id, pair_id, status, total_tables, processed_tables, diff_tables, COALESCE(current_table,''), COALESCE(progress_msg,''), COALESCE(error_msg,''), started_at, finished_at FROM compare_runs ORDER BY started_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.CompareRun
	for rows.Next() {
		r := &models.CompareRun{}
		var ft sql.NullTime
		if err := rows.Scan(&r.ID, &r.PairID, &r.Status, &r.TotalTables, &r.ProcessedTables, &r.DiffTables, &r.CurrentTable, &r.ProgressMsg, &r.ErrorMsg, &r.StartedAt, &ft); err != nil {
			return nil, err
		}
		if ft.Valid {
			r.FinishedAt = &ft.Time
		}
		list = append(list, r)
	}
	return list, nil
}

func (s *Store) GetCompareRun(id int64) (*models.CompareRun, error) {
	row := s.db.QueryRow(
		`SELECT id, pair_id, status, total_tables, processed_tables, diff_tables, COALESCE(current_table,''), COALESCE(progress_msg,''), COALESCE(error_msg,''), started_at, finished_at FROM compare_runs WHERE id = ?`, id,
	)
	r := &models.CompareRun{}
	var ft sql.NullTime
	err := row.Scan(&r.ID, &r.PairID, &r.Status, &r.TotalTables, &r.ProcessedTables, &r.DiffTables, &r.CurrentTable, &r.ProgressMsg, &r.ErrorMsg, &r.StartedAt, &ft)
	if err != nil {
		return nil, err
	}
	if ft.Valid {
		r.FinishedAt = &ft.Time
	}
	return r, nil
}

// ---- DiffDetails ----

func (s *Store) InsertDiffDetails(runID int64, diffs []models.DiffDetail) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO diff_details (run_id, table_name, diff_type, column_name, source_value, target_value) VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, d := range diffs {
		d.RunID = runID
		d.CreatedAt = time.Now()
		if _, err := stmt.Exec(runID, d.TableName, d.DiffType, d.ColumnName, d.SourceValue, d.TargetValue); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteDiffDetailsByTable(runID int64, tableName string) error {
	_, err := s.db.Exec(`DELETE FROM diff_details WHERE run_id = ? AND table_name = ?`, runID, tableName)
	return err
}

func (s *Store) UpsertCompareRunTable(runID int64, tableName, status string, diffCount int) error {
	_, err := s.db.Exec(
		`INSERT INTO compare_run_tables (run_id, table_name, status, diff_count, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, table_name) DO UPDATE SET status=excluded.status, diff_count=excluded.diff_count, updated_at=excluded.updated_at`,
		runID, tableName, status, diffCount, time.Now(),
	)
	return err
}

func (s *Store) GetCompletedRunTables(runID int64) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT table_name FROM compare_run_tables WHERE run_id = ? AND status = 'done'`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	completed := make(map[string]bool)
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return nil, err
		}
		completed[tableName] = true
	}
	return completed, rows.Err()
}

func (s *Store) GetDiffDetails(runID int64) ([]*models.DiffDetail, error) {
	rows, err := s.db.Query(
		`SELECT id, run_id, table_name, diff_type, COALESCE(column_name,''), COALESCE(source_value,''), COALESCE(target_value,''), created_at FROM diff_details WHERE run_id = ? ORDER BY table_name, diff_type, column_name`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.DiffDetail
	for rows.Next() {
		d := &models.DiffDetail{}
		if err := rows.Scan(&d.ID, &d.RunID, &d.TableName, &d.DiffType, &d.ColumnName, &d.SourceValue, &d.TargetValue, &d.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, nil
}

// ---- Notifications ----

func (s *Store) CreateNotification(n *models.Notification) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO notifications (name, type, config_json, enabled) VALUES (?, ?, ?, ?)`,
		n.Name, n.Type, n.ConfigJSON, boolToInt(n.Enabled),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListNotifications() ([]*models.Notification, error) {
	rows, err := s.db.Query(
		`SELECT id, name, type, config_json, enabled, created_at FROM notifications ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.Notification
	for rows.Next() {
		n := &models.Notification{}
		var enabled int
		if err := rows.Scan(&n.ID, &n.Name, &n.Type, &n.ConfigJSON, &enabled, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Enabled = enabled == 1
		list = append(list, n)
	}
	return list, nil
}

func (s *Store) GetNotification(id int64) (*models.Notification, error) {
	row := s.db.QueryRow(
		`SELECT id, name, type, config_json, enabled, created_at FROM notifications WHERE id = ?`, id,
	)
	n := &models.Notification{}
	var enabled int
	err := row.Scan(&n.ID, &n.Name, &n.Type, &n.ConfigJSON, &enabled, &n.CreatedAt)
	if err != nil {
		return nil, err
	}
	n.Enabled = enabled == 1
	return n, nil
}

func (s *Store) UpdateNotification(n *models.Notification) error {
	_, err := s.db.Exec(
		`UPDATE notifications SET name=?, type=?, config_json=?, enabled=? WHERE id=?`,
		n.Name, n.Type, n.ConfigJSON, boolToInt(n.Enabled), n.ID,
	)
	return err
}

func (s *Store) DeleteNotification(id int64) error {
	s.db.Exec(`DELETE FROM compare_notifications WHERE notification_id = ?`, id)
	_, err := s.db.Exec(`DELETE FROM notifications WHERE id = ?`, id)
	return err
}

// ---- CompareNotifications ----

func (s *Store) SetCompareNotifications(pairID int64, links []models.CompareNotification) error {
	if _, err := s.db.Exec(`DELETE FROM compare_notifications WHERE pair_id = ?`, pairID); err != nil {
		log.Printf("SetCompareNotifications: delete error: %v", err)
		return err
	}
	for _, l := range links {
		l.PairID = pairID
		if _, err := s.db.Exec(
			`INSERT INTO compare_notifications (pair_id, notification_id, on_diff, on_error, on_success) VALUES (?, ?, ?, ?, ?)`,
			pairID, l.NotificationID, boolToInt(l.OnDiff), boolToInt(l.OnError), boolToInt(l.OnSuccess),
		); err != nil {
			log.Printf("SetCompareNotifications: insert error: %v", err)
			return err
		}
	}
	log.Printf("SetCompareNotifications: pair %d saved %d links", pairID, len(links))
	return nil
}

func (s *Store) GetCompareNotifications(pairID int64) ([]*models.CompareNotification, error) {
	rows, err := s.db.Query(
		`SELECT id, pair_id, notification_id, on_diff, on_error, on_success FROM compare_notifications WHERE pair_id = ?`,
		pairID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.CompareNotification
	for rows.Next() {
		l := &models.CompareNotification{}
		var d, e, su int
		if err := rows.Scan(&l.ID, &l.PairID, &l.NotificationID, &d, &e, &su); err != nil {
			return nil, err
		}
		l.OnDiff = d == 1
		l.OnError = e == 1
		l.OnSuccess = su == 1
		list = append(list, l)
	}
	return list, nil
}

// ---- Schedules ----

func (s *Store) CreateSchedule(sc *models.Schedule) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO schedules (pair_id, cron_expr, enabled) VALUES (?, ?, ?)`,
		sc.PairID, sc.CronExpr, boolToInt(sc.Enabled),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListSchedules() ([]*models.Schedule, error) {
	rows, err := s.db.Query(
		`SELECT id, pair_id, cron_expr, enabled, created_at FROM schedules ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.Schedule
	for rows.Next() {
		sc := &models.Schedule{}
		var enabled int
		if err := rows.Scan(&sc.ID, &sc.PairID, &sc.CronExpr, &enabled, &sc.CreatedAt); err != nil {
			return nil, err
		}
		sc.Enabled = enabled == 1
		list = append(list, sc)
	}
	return list, nil
}

func (s *Store) GetSchedule(id int64) (*models.Schedule, error) {
	row := s.db.QueryRow(
		`SELECT id, pair_id, cron_expr, enabled, created_at FROM schedules WHERE id = ?`, id,
	)
	sc := &models.Schedule{}
	var enabled int
	err := row.Scan(&sc.ID, &sc.PairID, &sc.CronExpr, &enabled, &sc.CreatedAt)
	if err != nil {
		return nil, err
	}
	sc.Enabled = enabled == 1
	return sc, nil
}

func (s *Store) GetSchedulesByPairID(pairID int64) ([]*models.Schedule, error) {
	rows, err := s.db.Query(
		`SELECT id, pair_id, cron_expr, enabled, created_at FROM schedules WHERE pair_id = ?`,
		pairID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*models.Schedule
	for rows.Next() {
		sc := &models.Schedule{}
		var enabled int
		if err := rows.Scan(&sc.ID, &sc.PairID, &sc.CronExpr, &enabled, &sc.CreatedAt); err != nil {
			return nil, err
		}
		sc.Enabled = enabled == 1
		list = append(list, sc)
	}
	return list, nil
}

func (s *Store) UpdateSchedule(sc *models.Schedule) error {
	_, err := s.db.Exec(
		`UPDATE schedules SET cron_expr=?, enabled=? WHERE id=?`,
		sc.CronExpr, boolToInt(sc.Enabled), sc.ID,
	)
	return err
}

func (s *Store) DeleteSchedule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM schedules WHERE id = ?`, id)
	return err
}

// ---- Stats ----

func (s *Store) GetStats() (map[string]int, error) {
	stats := make(map[string]int)
	var dbCount, pairCount, runCount, diffCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM databases`).Scan(&dbCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM compare_pairs`).Scan(&pairCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM compare_runs`).Scan(&runCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM diff_details`).Scan(&diffCount)
	stats["databases"] = dbCount
	stats["pairs"] = pairCount
	stats["runs"] = runCount
	stats["diffs"] = diffCount
	return stats, nil
}

// ---- helpers ----

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

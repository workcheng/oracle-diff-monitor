package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"oracle-diff-monitor/internal/compare"
	"oracle-diff-monitor/internal/models"
	"oracle-diff-monitor/internal/oracle"
	"oracle-diff-monitor/internal/scheduler"
	"oracle-diff-monitor/internal/store"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
)

//go:embed templates/*
var templateFS embed.FS

type Server struct {
	store     *store.Store
	scheduler *scheduler.Scheduler
	router    *gin.Engine
}

type templateRenderer struct {
	templates map[string]*template.Template
}

func (r *templateRenderer) Instance(name string, data interface{}) render.Render {
	tmpl := r.templates[name]
	if tmpl == nil {
		panic("template not found: " + name)
	}
	return render.HTML{Template: tmpl, Name: name, Data: data}
}

func NewServer(s *store.Store, sch *scheduler.Scheduler) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	funcMap := template.FuncMap{
		"DiffTypeLabel": models.DiffTypeLabel,
		"Duration": func(run *models.CompareRun) string {
			if run.FinishedAt == nil {
				return "运行中"
			}
			d := run.FinishedAt.Sub(run.StartedAt)
			m := int(d.Minutes())
			s := int(d.Seconds()) % 60
			if m > 0 {
				return fmt.Sprintf("%d分%d秒", m, s)
			}
			return fmt.Sprintf("%d秒", s)
		},
	}
	r.HTMLRender = loadTemplates(funcMap)

	svr := &Server{store: s, scheduler: sch, router: r}
	svr.registerRoutes()
	return svr
}

func loadTemplates(funcMap template.FuncMap) render.HTMLRender {
	pages := []string{
		"index.html", "databases.html", "database_form.html",
		"compare.html", "pair_form.html", "results.html", "result_detail.html",
		"settings.html", "notification_form.html", "schedule_form.html",
	}

	templates := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		tmpl := template.New("").Funcs(funcMap)
		templates[page] = template.Must(tmpl.ParseFS(templateFS, "templates/layout.html", "templates/"+page))
	}
	return &templateRenderer{templates: templates}
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}

func (s *Server) registerRoutes() {
	r := s.router

	r.GET("/", s.dashboard)

	r.GET("/databases", s.listDatabases)
	r.GET("/databases/new", s.newDatabaseForm)
	r.POST("/databases", s.createDatabase)
	r.GET("/databases/:id/edit", s.editDatabaseForm)
	r.POST("/databases/:id", s.updateDatabase)
	r.POST("/databases/:id/delete", s.deleteDatabase)
	r.POST("/databases/:id/test", s.testDatabase)

	r.GET("/pairs", s.listPairs)
	r.GET("/pairs/new", s.newPairForm)
	r.POST("/pairs", s.createPair)
	r.GET("/pairs/:id/edit", s.editPairForm)
	r.POST("/pairs/:id", s.updatePair)
	r.POST("/pairs/:id/delete", s.deletePair)
	r.POST("/pairs/:id/run", s.runPair)
	r.GET("/pairs/:id/tables", s.listPairTables)

	r.GET("/runs", s.listRuns)
	r.GET("/runs/:id", s.runDetail)
	r.GET("/runs/:id/export", s.exportRun)

	r.GET("/settings", s.settings)
	r.POST("/settings/notifications", s.createNotification)
	r.GET("/settings/notifications/:id/edit", s.editNotificationForm)
	r.POST("/settings/notifications/:id", s.updateNotification)
	r.POST("/settings/notifications/:id/delete", s.deleteNotification)
	r.POST("/settings/schedules", s.createSchedule)
	r.GET("/settings/schedules/:id/edit", s.editScheduleForm)
	r.POST("/settings/schedules/:id", s.updateSchedule)
	r.POST("/settings/schedules/:id/delete", s.deleteSchedule)

	r.POST("/pairs/:id/notifications", s.updatePairNotifications)

	r.GET("/api/stats", s.apiStats)
	r.GET("/api/runs/latest", s.apiLatestRuns)
}

func (s *Server) dashboard(c *gin.Context) {
	stats, _ := s.store.GetStats()
	runs, _ := s.store.GetLatestRuns(10)
	pairs, _ := s.store.ListComparePairs()

	pairMap := make(map[int64]*models.ComparePair)
	for _, p := range pairs {
		pairMap[p.ID] = p
	}

	type runWithPair struct {
		*models.CompareRun
		PairName string
	}
	var runList []runWithPair
	for _, r := range runs {
		name := fmt.Sprintf("Pair #%d", r.PairID)
		if p, ok := pairMap[r.PairID]; ok {
			name = p.Name
		}
		runList = append(runList, runWithPair{CompareRun: r, PairName: name})
	}

	c.HTML(http.StatusOK, "index.html", gin.H{
		"title": "仪表盘", "stats": stats, "runs": runList,
	})
}

// ---- Databases ----

func (s *Server) listDatabases(c *gin.Context) {
	dbs, err := s.store.ListDatabases()
	if err != nil {
		c.HTML(http.StatusInternalServerError, "databases.html", gin.H{"error": err.Error()})
		return
	}
	c.HTML(http.StatusOK, "databases.html", gin.H{"title": "数据库管理", "databases": dbs})
}

func (s *Server) newDatabaseForm(c *gin.Context) {
	c.HTML(http.StatusOK, "database_form.html", gin.H{"title": "添加数据库", "db": nil})
}

func (s *Server) createDatabase(c *gin.Context) {
	db := &models.Database{
		Name:        c.PostForm("name"),
		Type:        "oracle",
		Host:        c.PostForm("host"),
		Username:    c.PostForm("username"),
		Password:    c.PostForm("password"),
		ServiceName: c.PostForm("service_name"),
		SID:         c.PostForm("sid"),
	}
	db.Port, _ = strconv.Atoi(c.DefaultPostForm("port", "1521"))

	if _, err := s.store.CreateDatabase(db); err != nil {
		c.HTML(http.StatusBadRequest, "database_form.html", gin.H{"error": err.Error(), "db": db})
		return
	}
	c.Redirect(http.StatusFound, "/databases")
}

func (s *Server) editDatabaseForm(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	db, err := s.store.GetDatabase(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/databases")
		return
	}
	c.HTML(http.StatusOK, "database_form.html", gin.H{"title": "编辑数据库", "db": db})
}

func (s *Server) updateDatabase(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	existing, err := s.store.GetDatabase(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/databases")
		return
	}

	pass := c.PostForm("password")
	if pass == "" {
		pass = existing.Password
	}

	db := &models.Database{
		ID:          id,
		Name:        c.PostForm("name"),
		Host:        c.PostForm("host"),
		Username:    c.PostForm("username"),
		Password:    pass,
		ServiceName: c.PostForm("service_name"),
		SID:         c.PostForm("sid"),
	}
	db.Port, _ = strconv.Atoi(c.DefaultPostForm("port", "1521"))

	if err := s.store.UpdateDatabase(db); err != nil {
		c.HTML(http.StatusBadRequest, "database_form.html", gin.H{"error": err.Error(), "db": db})
		return
	}
	c.Redirect(http.StatusFound, "/databases")
}

func (s *Server) deleteDatabase(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	s.store.DeleteDatabase(id)
	c.Redirect(http.StatusFound, "/databases")
}

func (s *Server) testDatabase(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	db, err := s.store.GetDatabase(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "获取数据库配置失败: " + err.Error()})
		return
	}

	client, err := oracle.NewClient(db)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "连接失败: " + err.Error()})
		return
	}
	defer client.Close()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "连接成功 ✓"})
}

// ---- Compare Pairs ----

func (s *Server) listPairs(c *gin.Context) {
	pairs, err := s.store.ListComparePairs()
	if err != nil {
		c.HTML(http.StatusInternalServerError, "compare.html", gin.H{"error": err.Error()})
		return
	}
	dbs, _ := s.store.ListDatabases()
	dbMap := make(map[int64]*models.Database)
	for _, d := range dbs {
		dbMap[d.ID] = d
	}
	c.HTML(http.StatusOK, "compare.html", gin.H{"title": "比对任务", "pairs": pairs, "dbMap": dbMap})
}

func (s *Server) newPairForm(c *gin.Context) {
	dbs, _ := s.store.ListDatabases()
	notifs, _ := s.store.ListNotifications()
	c.HTML(http.StatusOK, "pair_form.html", gin.H{
		"title":         "新建比对任务",
		"pair":          nil,
		"databases":     dbs,
		"notifications": notifs,
		"notifMap":      make(map[int64]*models.CompareNotification),
	})
}

func (s *Server) createPair(c *gin.Context) {
	sourceID, _ := strconv.ParseInt(c.PostForm("source_db_id"), 10, 64)
	targetID, _ := strconv.ParseInt(c.PostForm("target_db_id"), 10, 64)
	pair := &models.ComparePair{
		Name:           c.PostForm("name"),
		SourceDBID:     sourceID,
		TargetDBID:     targetID,
		SchemaName:     c.PostForm("schema_name"),
		TableFilter:    c.PostForm("table_filter"),
		SelectedTables: c.PostForm("selected_tables"),
		Enabled:        c.PostForm("enabled") == "on",
	}

	if _, err := s.store.CreateComparePair(pair); err != nil {
		dbs, _ := s.store.ListDatabases()
		c.HTML(http.StatusBadRequest, "pair_form.html", gin.H{"error": err.Error(), "pair": pair, "databases": dbs})
		return
	}
	c.Redirect(http.StatusFound, "/pairs")
}

func (s *Server) editPairForm(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	pair, err := s.store.GetComparePair(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/pairs")
		return
	}
	dbs, _ := s.store.ListDatabases()
	notifs, _ := s.store.ListNotifications()
	links, _ := s.store.GetCompareNotifications(id)

	notifMap := make(map[int64]*models.CompareNotification)
	for _, l := range links {
		notifMap[l.NotificationID] = l
	}

	// Parse selected tables into a slice and JSON for the template
	var selectedTableList []string
	if pair.SelectedTables != "" {
		for _, t := range strings.Split(pair.SelectedTables, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				selectedTableList = append(selectedTableList, t)
			}
		}
	}
	selectedTableJSON := "[]"
	if len(selectedTableList) > 0 {
		if b, err := json.Marshal(selectedTableList); err == nil {
			selectedTableJSON = string(b)
		}
	}

	c.HTML(http.StatusOK, "pair_form.html", gin.H{
		"title":            "编辑比对任务",
		"pair":             pair,
		"databases":        dbs,
		"notifications":    notifs,
		"notifMap":         notifMap,
		"selectedTables":   selectedTableList,
		"selectedTableJSON": selectedTableJSON,
	})
}

func (s *Server) updatePair(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	sourceID, _ := strconv.ParseInt(c.PostForm("source_db_id"), 10, 64)
	targetID, _ := strconv.ParseInt(c.PostForm("target_db_id"), 10, 64)
	pair := &models.ComparePair{
		ID:          id,
		Name:        c.PostForm("name"),
		SourceDBID:  sourceID,
		TargetDBID:  targetID,
		SchemaName:  c.PostForm("schema_name"),
		TableFilter: c.PostForm("table_filter"),
		SelectedTables: c.PostForm("selected_tables"),
		Enabled:     c.PostForm("enabled") == "on",
	}
	if err := s.store.UpdateComparePair(pair); err != nil {
		dbs, _ := s.store.ListDatabases()
		c.HTML(http.StatusBadRequest, "pair_form.html", gin.H{"error": err.Error(), "pair": pair, "databases": dbs})
		return
	}
	c.Redirect(http.StatusFound, "/pairs")
}

func (s *Server) deletePair(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	s.store.DeleteComparePair(id)
	c.Redirect(http.StatusFound, "/pairs")
}

func (s *Server) listPairTables(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	pair, err := s.store.GetComparePair(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pair not found"})
		return
	}

	// Use source DB to list available tables
	db, err := s.store.GetDatabase(pair.SourceDBID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source db not found"})
		return
	}

	client, err := oracle.NewClient(db)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer client.Close()

	tables, err := client.GetTables(pair.SchemaName, pair.TableFilter)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}

	// Parse already-selected tables
	selectedSet := make(map[string]bool)
	if pair.SelectedTables != "" {
		for _, t := range strings.Split(pair.SelectedTables, ",") {
			selectedSet[strings.TrimSpace(t)] = true
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "tables": tables, "selected": selectedSet})
}

func (s *Server) runPair(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	go func() {
		compare.RunComparison(s.store, id)
	}()

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "比对任务已启动"})
}

// ---- Runs ----

func (s *Server) listRuns(c *gin.Context) {
	pairIDStr := c.Query("pair_id")
	var runs []*models.CompareRun
	var err error

	if pairIDStr != "" {
		pairID, _ := strconv.ParseInt(pairIDStr, 10, 64)
		runs, err = s.store.ListCompareRuns(pairID, 100)
	} else {
		runs, err = s.store.GetLatestRuns(100)
	}
	if err != nil {
		c.HTML(http.StatusInternalServerError, "results.html", gin.H{"error": err.Error()})
		return
	}

	pairs, _ := s.store.ListComparePairs()
	pairMap := make(map[int64]*models.ComparePair)
	for _, p := range pairs {
		pairMap[p.ID] = p
	}

	type runWithPair struct {
		*models.CompareRun
		PairName string
	}
	var runList []runWithPair
	for _, r := range runs {
		name := fmt.Sprintf("Pair #%d", r.PairID)
		if p, ok := pairMap[r.PairID]; ok {
			name = p.Name
		}
		runList = append(runList, runWithPair{CompareRun: r, PairName: name})
	}

	c.HTML(http.StatusOK, "results.html", gin.H{"title": "比对结果", "runs": runList, "pairs": pairs})
}

func (s *Server) runDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	run, err := s.store.GetCompareRun(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/runs")
		return
	}
	diffs, _ := s.store.GetDiffDetails(id)

	pairs, _ := s.store.ListComparePairs()
	pairMap := make(map[int64]*models.ComparePair)
	for _, p := range pairs {
		pairMap[p.ID] = p
	}
	pairName := fmt.Sprintf("Pair #%d", run.PairID)
	var sourceDBName, targetDBName string
	if p, ok := pairMap[run.PairID]; ok {
		pairName = p.Name
		if src, err := s.store.GetDatabase(p.SourceDBID); err == nil {
			sourceDBName = src.Name
		}
		if tgt, err := s.store.GetDatabase(p.TargetDBID); err == nil {
			targetDBName = tgt.Name
		}
	}

	grouped := make(map[string][]*models.DiffDetail)
	for _, d := range diffs {
		grouped[d.TableName] = append(grouped[d.TableName], d)
	}

	c.HTML(http.StatusOK, "result_detail.html", gin.H{
		"title": "差异详情", "run": run, "diffs": diffs,
		"grouped": grouped, "pairName": pairName,
		"sourceDB": sourceDBName, "targetDB": targetDBName,
	})
}

func (s *Server) exportRun(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	run, err := s.store.GetCompareRun(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/runs")
		return
	}
	diffs, _ := s.store.GetDiffDetails(id)

	data := map[string]interface{}{
		"run":   run,
		"diffs": diffs,
	}
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=run_%d.json", id))
	json.NewEncoder(c.Writer).Encode(data)
}

// ---- Settings ----

func (s *Server) settings(c *gin.Context) {
	notifs, _ := s.store.ListNotifications()
	schedules, _ := s.store.ListSchedules()
	pairs, _ := s.store.ListComparePairs()

	type scheduleWithPair struct {
		*models.Schedule
		PairName string
	}
	var schedList []scheduleWithPair
	pairMap := make(map[int64]*models.ComparePair)
	for _, p := range pairs {
		pairMap[p.ID] = p
	}
	for _, sc := range schedules {
		name := fmt.Sprintf("Pair #%d", sc.PairID)
		if p, ok := pairMap[sc.PairID]; ok {
			name = p.Name
		}
		schedList = append(schedList, scheduleWithPair{Schedule: sc, PairName: name})
	}

	c.HTML(http.StatusOK, "settings.html", gin.H{
		"title":         "系统设置",
		"notifications": notifs,
		"schedules":     schedList,
		"pairs":         pairs,
	})
}

func (s *Server) createNotification(c *gin.Context) {
	n := &models.Notification{
		Name:    c.PostForm("name"),
		Type:    c.PostForm("type"),
		Enabled: c.PostForm("enabled") == "on",
	}

	var cfgJSON []byte
	if n.Type == "email" {
		toRaw := c.PostForm("to_addresses")
		toList := splitAndTrim(toRaw, ",")
		cfg := models.EmailConfig{
			SMTPHost:    c.PostForm("smtp_host"),
			SMTPPort:    465,
			Username:    c.PostForm("smtp_user"),
			Password:    c.PostForm("smtp_pass"),
			FromAddr:    c.PostForm("from_addr"),
			ToAddresses: toList,
			UseTLS:      c.PostForm("use_tls") == "on",
		}
		if p, err := strconv.Atoi(c.PostForm("smtp_port")); err == nil {
			cfg.SMTPPort = p
		}
		cfgJSON, _ = json.Marshal(cfg)
	} else if n.Type == "webhook" {
		cfg := models.WebhookConfig{
			URL:     c.PostForm("webhook_url"),
			Headers: parseHeaders(c.PostForm("webhook_headers")),
		}
		cfgJSON, _ = json.Marshal(cfg)
	} else if n.Type == "dingtalk" {
		cfg := models.DingTalkConfig{
			URL:     c.PostForm("webhook_url"),
			Secret:  c.PostForm("dingtalk_secret"),
			Headers: parseHeaders(c.PostForm("webhook_headers")),
		}
		cfgJSON, _ = json.Marshal(cfg)
	}
	n.ConfigJSON = string(cfgJSON)

	if _, err := s.store.CreateNotification(n); err != nil {
		log.Printf("create notification error: %v", err)
	}
	c.Redirect(http.StatusFound, "/settings")
}

func (s *Server) deleteNotification(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	s.store.DeleteNotification(id)
	c.Redirect(http.StatusFound, "/settings")
}

func (s *Server) editNotificationForm(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	n, err := s.store.GetNotification(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/settings")
		return
	}

	data := gin.H{
		"title":        "编辑通知渠道",
		"notification": n,
		"emailCfg":     nil,
		"webhookCfg":   nil,
	}

	if n.Type == "email" {
		var cfg models.EmailConfig
		if json.Unmarshal([]byte(n.ConfigJSON), &cfg) == nil {
			data["emailCfg"] = cfg
		}
	} else if n.Type == "webhook" {
		var cfg models.WebhookConfig
		if json.Unmarshal([]byte(n.ConfigJSON), &cfg) == nil {
			data["webhookCfg"] = cfg
		}
	} else if n.Type == "dingtalk" {
		var cfg models.DingTalkConfig
		if json.Unmarshal([]byte(n.ConfigJSON), &cfg) == nil {
			data["webhookCfg"] = cfg
			data["dingtalkSecret"] = cfg.Secret
		}
	}

	c.HTML(http.StatusOK, "notification_form.html", data)
}

func (s *Server) updateNotification(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	n := &models.Notification{
		ID:      id,
		Name:    c.PostForm("name"),
		Type:    c.PostForm("type"),
		Enabled: c.PostForm("enabled") == "on",
	}

	var cfgJSON []byte
	if n.Type == "email" {
		toRaw := c.PostForm("to_addresses")
		toList := splitAndTrim(toRaw, ",")
		cfg := models.EmailConfig{
			SMTPHost:    c.PostForm("smtp_host"),
			SMTPPort:    465,
			Username:    c.PostForm("smtp_user"),
			Password:    c.PostForm("smtp_pass"),
			FromAddr:    c.PostForm("from_addr"),
			ToAddresses: toList,
			UseTLS:      c.PostForm("use_tls") == "on",
		}
		if p, err := strconv.Atoi(c.PostForm("smtp_port")); err == nil {
			cfg.SMTPPort = p
		}
		cfgJSON, _ = json.Marshal(cfg)
	} else if n.Type == "webhook" {
		cfg := models.WebhookConfig{
			URL:     c.PostForm("webhook_url"),
			Headers: parseHeaders(c.PostForm("webhook_headers")),
		}
		cfgJSON, _ = json.Marshal(cfg)
	} else if n.Type == "dingtalk" {
		cfg := models.DingTalkConfig{
			URL:     c.PostForm("webhook_url"),
			Secret:  c.PostForm("dingtalk_secret"),
			Headers: parseHeaders(c.PostForm("webhook_headers")),
		}
		cfgJSON, _ = json.Marshal(cfg)
	}
	n.ConfigJSON = string(cfgJSON)

	if err := s.store.UpdateNotification(n); err != nil {
		log.Printf("update notification error: %v", err)
	}
	c.Redirect(http.StatusFound, "/settings")
}

func (s *Server) createSchedule(c *gin.Context) {
	pairID, _ := strconv.ParseInt(c.PostForm("pair_id"), 10, 64)
	sc := &models.Schedule{
		PairID:   pairID,
		CronExpr: c.PostForm("cron_expr"),
		Enabled:  c.PostForm("enabled") == "on",
	}
	if _, err := s.store.CreateSchedule(sc); err != nil {
		c.Redirect(http.StatusFound, "/settings")
		return
	}
	schedules, _ := s.store.ListSchedules()
	for _, sc2 := range schedules {
		if sc2.PairID == pairID && sc2.CronExpr == sc.CronExpr {
			s.scheduler.AddOrUpdate(sc2)
			break
		}
	}
	c.Redirect(http.StatusFound, "/settings")
}

func (s *Server) deleteSchedule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	s.scheduler.Remove(id)
	s.store.DeleteSchedule(id)
	c.Redirect(http.StatusFound, "/settings")
}

func (s *Server) editScheduleForm(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	sc, err := s.store.GetSchedule(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/settings")
		return
	}
	pairs, _ := s.store.ListComparePairs()
	c.HTML(http.StatusOK, "schedule_form.html", gin.H{
		"title": "编辑定时任务", "schedule": sc, "pairs": pairs,
	})
}

func (s *Server) updateSchedule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	pairID, _ := strconv.ParseInt(c.PostForm("pair_id"), 10, 64)
	sc := &models.Schedule{
		ID:       id,
		PairID:   pairID,
		CronExpr: c.PostForm("cron_expr"),
		Enabled:  c.PostForm("enabled") == "on",
	}
	s.store.UpdateSchedule(sc)
	s.scheduler.AddOrUpdate(sc)
	c.Redirect(http.StatusFound, "/settings")
}

func (s *Server) updatePairNotifications(c *gin.Context) {
	pairID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	log.Printf("updatePairNotifications: pairID=%d", pairID)

	notifications, _ := s.store.ListNotifications()
	log.Printf("updatePairNotifications: found %d notification channels", len(notifications))
	var links []models.CompareNotification

	for _, n := range notifications {
		onDiff := c.PostForm(fmt.Sprintf("notif_%d_diff", n.ID)) == "on"
		onError := c.PostForm(fmt.Sprintf("notif_%d_error", n.ID)) == "on"
		onSuccess := c.PostForm(fmt.Sprintf("notif_%d_success", n.ID)) == "on"
		log.Printf("updatePairNotifications: notif %d (name=%s) diff=%v error=%v success=%v",
			n.ID, n.Name, onDiff, onError, onSuccess)

		if onDiff || onError || onSuccess {
			links = append(links, models.CompareNotification{
				PairID:         pairID,
				NotificationID: n.ID,
				OnDiff:         onDiff,
				OnError:        onError,
				OnSuccess:      onSuccess,
			})
		}
	}

	log.Printf("updatePairNotifications: saving %d links for pair %d", len(links), pairID)
	if err := s.store.SetCompareNotifications(pairID, links); err != nil {
		log.Printf("updatePairNotifications: SetCompareNotifications error: %v", err)
	} else {
		log.Printf("updatePairNotifications: saved successfully")
	}
	c.Redirect(http.StatusFound, "/pairs/"+strconv.FormatInt(pairID, 10)+"/edit")
}

// ---- API ----

func (s *Server) apiStats(c *gin.Context) {
	stats, _ := s.store.GetStats()
	c.JSON(http.StatusOK, stats)
}

func (s *Server) apiLatestRuns(c *gin.Context) {
	runs, _ := s.store.GetLatestRuns(10)
	c.JSON(http.StatusOK, runs)
}

// ---- helpers ----

func splitAndTrim(s, sep string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, part := range strings.Split(s, sep) {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func parseHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	if raw == "" {
		return headers
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key != "" {
				headers[key] = val
			}
		}
	}
	return headers
}

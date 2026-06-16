package compare

import (
	"fmt"
	"log"
	"strings"
	"oracle-diff-monitor/internal/models"
	"oracle-diff-monitor/internal/notifier"
	"oracle-diff-monitor/internal/oracle"
	"oracle-diff-monitor/internal/store"
)

func RunComparison(s *store.Store, pairID int64) (*models.CompareRun, []models.DiffDetail, error) {
	pair, err := s.GetComparePair(pairID)
	if err != nil {
		return nil, nil, fmt.Errorf("get pair: %w", err)
	}

	sourceDB, err := s.GetDatabase(pair.SourceDBID)
	if err != nil {
		return nil, nil, fmt.Errorf("get source db: %w", err)
	}
	targetDB, err := s.GetDatabase(pair.TargetDBID)
	if err != nil {
		return nil, nil, fmt.Errorf("get target db: %w", err)
	}

	run := &models.CompareRun{PairID: pairID, Status: "running", ProgressMsg: "正在准备比对任务"}
	run.ID, err = s.CreateCompareRun(run)
	if err != nil {
		return nil, nil, fmt.Errorf("create run: %w", err)
	}

	run.ProgressMsg = "正在连接源库"
	s.UpdateCompareRunProgress(run)
	sourceClient, err := oracle.NewClient(sourceDB)
	if err != nil {
		run.Status = "failed"
		run.ErrorMsg = fmt.Sprintf("连接源库失败: %v", err)
		run.ProgressMsg = "源库连接失败"
		s.UpdateCompareRun(run)
		NotifyCompareResult(s, pair.ID, run, nil)
		return run, nil, fmt.Errorf("%s", run.ErrorMsg)
	}
	defer sourceClient.Close()

	run.ProgressMsg = "正在连接目标库"
	s.UpdateCompareRunProgress(run)
	targetClient, err := oracle.NewClient(targetDB)
	if err != nil {
		run.Status = "failed"
		run.ErrorMsg = fmt.Sprintf("连接目标库失败: %v", err)
		run.ProgressMsg = "目标库连接失败"
		s.UpdateCompareRun(run)
		NotifyCompareResult(s, pair.ID, run, nil)
		return run, nil, fmt.Errorf("%s", run.ErrorMsg)
	}
	defer targetClient.Close()

	run.ProgressMsg = "正在加载表清单并批量比对"
	s.UpdateCompareRunProgress(run)
	comp := oracle.NewComparator(sourceClient, targetClient)

	var totalTables int
	var selectedTableList []string
	if pair.SelectedTables != "" {
		for _, t := range strings.Split(pair.SelectedTables, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				selectedTableList = append(selectedTableList, t)
			}
		}
	}
	diffs, err := comp.CompareAllTables(pair.SchemaName, pair.TableFilter, func(processed, total int, table, message string) {
		totalTables = total
		run.TotalTables = total
		run.ProcessedTables = processed
		run.CurrentTable = table
		run.ProgressMsg = message
		s.UpdateCompareRunProgress(run)
	}, selectedTableList)
	if err != nil {
		run.Status = "failed"
		run.ErrorMsg = fmt.Sprintf("比对失败: %v", err)
		run.ProgressMsg = "比对失败"
		s.UpdateCompareRun(run)
		NotifyCompareResult(s, pair.ID, run, nil)
		return run, nil, fmt.Errorf("%s", run.ErrorMsg)
	}

	diffTables := make(map[string]bool)
	for _, d := range diffs {
		diffTables[d.TableName] = true
	}

	run.Status = "success"
	run.TotalTables = totalTables
	run.ProcessedTables = totalTables
	run.DiffTables = len(diffTables)
	run.CurrentTable = ""
	run.ProgressMsg = "比对完成"
	s.UpdateCompareRun(run)

	if len(diffs) > 0 {
		s.InsertDiffDetails(run.ID, diffs)
	}

	NotifyCompareResult(s, pair.ID, run, diffs)

	return run, diffs, nil
}

func NotifyCompareResult(s *store.Store, pairID int64, run *models.CompareRun, diffs []models.DiffDetail) {
	log.Printf("NotifyCompareResult: called for pairID=%d, diffs=%d, status=%s", pairID, len(diffs), run.Status)

	pair, err := s.GetComparePair(pairID)
	if err != nil {
		log.Printf("NotifyCompareResult: GetComparePair failed: %v", err)
		return
	}

	links, err := s.GetCompareNotifications(pairID)
	if err != nil {
		log.Printf("NotifyCompareResult: GetCompareNotifications error: %v", err)
		return
	}
	log.Printf("NotifyCompareResult: found %d notification links for pair %d", len(links), pairID)
	if len(links) == 0 {
		return
	}

	var notifIDs []int64
	hasDiff := len(diffs) > 0
	hasError := run.Status == "failed"
	log.Printf("NotifyCompareResult: hasDiff=%v hasError=%v", hasDiff, hasError)

	notifications, err := s.ListNotifications()
	if err != nil {
		log.Printf("NotifyCompareResult: ListNotifications error: %v", err)
		return
	}
	log.Printf("NotifyCompareResult: total %d notification channels", len(notifications))
	notifMap := make(map[int64]*models.Notification)
	for _, n := range notifications {
		notifMap[n.ID] = n
	}

	for _, link := range links {
		if hasDiff && link.OnDiff {
			notifIDs = append(notifIDs, link.NotificationID)
		}
		if hasError && link.OnError {
			notifIDs = append(notifIDs, link.NotificationID)
		}
		if !hasDiff && !hasError && link.OnSuccess {
			notifIDs = append(notifIDs, link.NotificationID)
		}
	}
	log.Printf("NotifyCompareResult: %d notifIDs matched conditions", len(notifIDs))

	if len(notifIDs) == 0 {
		return
	}

	enabledNotifs := make([]*models.Notification, 0)
	for _, id := range notifIDs {
		if n, ok := notifMap[id]; ok && n.Enabled {
			enabledNotifs = append(enabledNotifs, n)
		}
	}
	log.Printf("NotifyCompareResult: %d enabled notifications to send", len(enabledNotifs))
	if len(enabledNotifs) == 0 {
		return
	}

	dispatcher := notifier.NewDispatcher(enabledNotifs)
	log.Printf("NotifyCompareResult: dispatcher created")

	var diffPtrs []*models.DiffDetail
	for i := range diffs {
		diffPtrs = append(diffPtrs, &diffs[i])
	}

	log.Printf("NotifyCompareResult: sending notification for pair '%s'...", pair.Name)
	if hasError {
		dispatcher.SendErrorReport(pair.Name, run.ErrorMsg, notifIDs)
	} else {
		dispatcher.SendDiffReport(run, diffPtrs, pair.Name, notifIDs)
	}
	log.Printf("NotifyCompareResult: done")
}

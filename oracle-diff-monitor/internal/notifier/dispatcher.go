package notifier

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"oracle-diff-monitor/internal/models"
)

// DetailBaseURL is the external URL of the web UI, used to build clickable
// links in notification messages. Set by the serve command.
var DetailBaseURL = "http://localhost:8080"

type Notifier interface {
	Send(subject, body string) error
	Type() string
}

type Dispatcher struct {
	notifiers map[int64]Notifier
}

func NewDispatcher(notifications []*models.Notification) *Dispatcher {
	d := &Dispatcher{notifiers: make(map[int64]Notifier)}
	for _, n := range notifications {
		if !n.Enabled {
			continue
		}
		notif, err := createNotifier(n)
		if err != nil {
			log.Printf("create notifier %s (id=%d) failed: %v, skipped", n.Name, n.ID, err)
			continue
		}
		d.notifiers[n.ID] = notif
	}
	return d
}

func createNotifier(n *models.Notification) (Notifier, error) {
	switch n.Type {
	case "email":
		return NewEmailNotifier(n.ConfigJSON)
	case "webhook":
		return NewWebhookNotifier(n.ConfigJSON)
	case "dingtalk":
		return NewDingTalkNotifier(n.ConfigJSON)
	default:
		return nil, fmt.Errorf("unknown notifier type: %s", n.Type)
	}
}

func (d *Dispatcher) SendDiffReport(run *models.CompareRun, diffs []*models.DiffDetail, pairName string, notifIDs []int64) {
	subject := fmt.Sprintf("[Oracle Diff] %s - 发现 %d 处差异", pairName, len(diffs))

	for _, id := range notifIDs {
		if n, ok := d.notifiers[id]; ok {
			var body string
			if n.Type() == "dingtalk" {
				body = BuildDiffSummary(run, diffs, pairName)
			} else {
				body = BuildDiffReportHTML(run, diffs, pairName)
			}
			if err := n.Send(subject, body); err != nil {
				log.Printf("send notification %d failed: %v", id, err)
			}
		}
	}
}

func (d *Dispatcher) SendErrorReport(pairName string, errMsg string, notifIDs []int64) {
	subject := fmt.Sprintf("[Oracle Diff] %s - 比对出错", pairName)
	body := fmt.Sprintf("<h2>比对任务 %s 执行出错</h2><pre>%s</pre>", pairName, errMsg)

	for _, id := range notifIDs {
		if n, ok := d.notifiers[id]; ok {
			if err := n.Send(subject, body); err != nil {
				log.Printf("send notification %d failed: %v", id, err)
			}
		}
	}
}

func BuildDiffReportHTML(run *models.CompareRun, diffs []*models.DiffDetail, pairName string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<h2>比对任务: %s</h2>`, pairName))
	b.WriteString(fmt.Sprintf(`<p>执行时间: %s | 扫描表: %d | 差异表: %d | 总差异: %d</p>`,
		run.StartedAt.Format("2006-01-02 15:04:05"),
		run.TotalTables, run.DiffTables, len(diffs)))

	// Add link to detail page
	detailURL := fmt.Sprintf("%s/runs/%d", strings.TrimRight(DetailBaseURL, "/"), run.ID)
	b.WriteString(fmt.Sprintf(`<p><a href="%s">查看详细差异 →</a></p>`, detailURL))
	b.WriteString(fmt.Sprintf(`<p>详情链接: <a href="%s">%s</a></p>`, detailURL, detailURL))

	if len(diffs) == 0 {
		b.WriteString("<p style='color:green;font-weight:bold'>✓ 未发现差异</p>")
		return b.String()
	}

	tableGroups := make(map[string][]*models.DiffDetail)
	for _, d := range diffs {
		tableGroups[d.TableName] = append(tableGroups[d.TableName], d)
	}

	b.WriteString(`<table border="1" cellpadding="6" cellspacing="0" style="border-collapse:collapse;width:100%">`)
	b.WriteString(`<tr style="background:#f0f0f0"><th>表名</th><th>差异类型</th><th>列/对象</th><th>源库</th><th>目标库</th></tr>`)

	for table, items := range tableGroups {
		for i, d := range items {
			if i == 0 {
				b.WriteString(fmt.Sprintf(`<tr><td rowspan="%d">%s</td>`, len(items), table))
			}
			diffType := models.DiffTypeLabel(d.DiffType)
			b.WriteString(fmt.Sprintf(`<td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				diffType, d.ColumnName, d.SourceValue, d.TargetValue))
		}
	}
	b.WriteString(`</table>`)
	return b.String()
}

// BuildDiffSummary generates a concise text summary of differences (for DingTalk).
func BuildDiffSummary(run *models.CompareRun, diffs []*models.DiffDetail, pairName string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## 比对任务: %s\n\n", pairName))
	b.WriteString(fmt.Sprintf("执行时间: %s | 扫描表: %d | 差异表: %d | 总差异: %d\n\n",
		run.StartedAt.Format("01-02 15:04"),
		run.TotalTables, run.DiffTables, len(diffs)))

	if len(diffs) == 0 {
		b.WriteString("✓ 未发现差异")
		return b.String()
	}

	// Group by diff type
	typeCount := make(map[string]int)
	tableCount := make(map[string]int)
	for _, d := range diffs {
		typeCount[d.DiffType]++
		tableCount[d.TableName]++
	}

	// Diff type stats
	b.WriteString("#### 差异类型统计\n\n")
	// Sort types by count descending
	typeList := make([]struct {
		label string
		count int
	}, 0, len(typeCount))
	for t, c := range typeCount {
		typeList = append(typeList, struct {
			label string
			count int
		}{models.DiffTypeLabel(t), c})
	}
	sort.Slice(typeList, func(i, j int) bool {
		return typeList[i].count > typeList[j].count
	})
	for _, item := range typeList {
		b.WriteString(fmt.Sprintf("- %s: %d\n", item.label, item.count))
	}

	// Table ranking (top items, avoid exceeding DingTalk's 20KB limit)
	b.WriteString("\n#### 差异表 TOP\n\n")
	tableList := make([]struct {
		name  string
		count int
	}, 0, len(tableCount))
	for t, c := range tableCount {
		tableList = append(tableList, struct {
			name  string
			count int
		}{t, c})
	}
	sort.Slice(tableList, func(i, j int) bool {
		return tableList[i].count > tableList[j].count
	})
	// Show at most 30 tables to keep message size manageable
	maxTables := 30
	if len(tableList) < maxTables {
		maxTables = len(tableList)
	}
	for i := 0; i < maxTables; i++ {
		item := tableList[i]
		b.WriteString(fmt.Sprintf("%d. %s (%d处)\n", i+1, item.name, item.count))
	}
	if len(tableList) > maxTables {
		b.WriteString(fmt.Sprintf("... 还有 %d 张表未列出\n", len(tableList)-maxTables))
	}

	// Detail link
	detailURL := fmt.Sprintf("%s/runs/%d", strings.TrimRight(DetailBaseURL, "/"), run.ID)
	b.WriteString(fmt.Sprintf("\n详情: %s", detailURL))

	return b.String()
}

func CreateNotifierFromConfig(n *models.Notification) (Notifier, error) {
	switch n.Type {
	case "email":
		return NewEmailNotifier(n.ConfigJSON)
	case "webhook":
		return NewWebhookNotifier(n.ConfigJSON)
	case "dingtalk":
		return NewDingTalkNotifier(n.ConfigJSON)
	}
	return nil, fmt.Errorf("unknown type: %s", n.Type)
}

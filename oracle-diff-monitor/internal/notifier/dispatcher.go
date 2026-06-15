package notifier

import (
	"fmt"
	"strings"
	"oracle-diff-monitor/internal/models"
)

type Notifier interface {
	Send(subject, body string) error
	Type() string
}

type Dispatcher struct {
	notifiers map[int64]Notifier
}

func NewDispatcher(notifications []*models.Notification) (*Dispatcher, error) {
	d := &Dispatcher{notifiers: make(map[int64]Notifier)}
	for _, n := range notifications {
		if !n.Enabled {
			continue
		}
		notif, err := createNotifier(n)
		if err != nil {
			return nil, fmt.Errorf("create notifier %s: %w", n.Name, err)
		}
		d.notifiers[n.ID] = notif
	}
	return d, nil
}

func createNotifier(n *models.Notification) (Notifier, error) {
	switch n.Type {
	case "email":
		return NewEmailNotifier(n.ConfigJSON)
	case "webhook":
		return NewWebhookNotifier(n.ConfigJSON)
	default:
		return nil, fmt.Errorf("unknown notifier type: %s", n.Type)
	}
}

func (d *Dispatcher) SendDiffReport(run *models.CompareRun, diffs []*models.DiffDetail, pairName string, notifIDs []int64) {
	subject := fmt.Sprintf("[Oracle Diff] %s - 发现 %d 处差异", pairName, len(diffs))
	body := BuildDiffReportHTML(run, diffs, pairName)

	for _, id := range notifIDs {
		if n, ok := d.notifiers[id]; ok {
			if err := n.Send(subject, body); err != nil {
				fmt.Printf("send notification %d failed: %v\n", id, err)
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
				fmt.Printf("send notification %d failed: %v\n", id, err)
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

func CreateNotifierFromConfig(n *models.Notification) (Notifier, error) {
	switch n.Type {
	case "email":
		return NewEmailNotifier(n.ConfigJSON)
	case "webhook":
		return NewWebhookNotifier(n.ConfigJSON)
	}
	return nil, fmt.Errorf("unknown type: %s", n.Type)
}

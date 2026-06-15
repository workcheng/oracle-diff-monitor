package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"oracle-diff-monitor/internal/models"
)

type WebhookNotifier struct {
	config models.WebhookConfig
}

func NewWebhookNotifier(configJSON string) (*WebhookNotifier, error) {
	var cfg models.WebhookConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("parse webhook config: %w", err)
	}
	return &WebhookNotifier{config: cfg}, nil
}

func (w *WebhookNotifier) Send(subject, body string) error {
	payload := map[string]string{
		"subject": subject,
		"message": body,
		"time":    time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook: %w", err)
	}

	req, err := http.NewRequest("POST", w.config.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.config.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func (w *WebhookNotifier) Type() string { return "webhook" }

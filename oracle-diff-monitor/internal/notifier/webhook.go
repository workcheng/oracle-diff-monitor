package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
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
	log.Printf("Webhook: POST %s (subject=%q)", w.config.URL, subject)

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

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	respBody := strings.TrimSpace(buf.String())

	log.Printf("Webhook: %s responded with status %d, body=%s", w.config.URL, resp.StatusCode, respBody)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func (w *WebhookNotifier) Type() string { return "webhook" }

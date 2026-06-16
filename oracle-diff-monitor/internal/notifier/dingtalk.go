package notifier

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"oracle-diff-monitor/internal/models"
	"strconv"
	"strings"
	"time"
)

type DingTalkNotifier struct {
	config models.DingTalkConfig
}

func NewDingTalkNotifier(configJSON string) (*DingTalkNotifier, error) {
	var cfg models.DingTalkConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("parse dingtalk config: %w", err)
	}
	return &DingTalkNotifier{config: cfg}, nil
}

func (d *DingTalkNotifier) Send(subject, body string) error {
	// Build request URL
	requestURL := d.config.URL

	// Add signature if secret is configured
	if d.config.Secret != "" {
		timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		sign := signDingTalk(timestamp, d.config.Secret)
		sep := "?"
		if strings.Contains(requestURL, "?") {
			sep = "&"
		}
		requestURL = fmt.Sprintf("%s%stimestamp=%s&sign=%s", requestURL, sep, timestamp, sign)
	}

	log.Printf("DingTalk: POST %s (subject=%q)", requestURL, subject)

	// Convert HTML body to plain text for DingTalk
	text := htmlToPlainText(body)
	title := subject

	markdownText := fmt.Sprintf("## %s\n\n%s", title, text)

	// DingTalk markdown limit: 20000 bytes total payload.
	// Truncate to ~18000 bytes to leave room for JSON wrapper.
	const maxTextBytes = 18000
	if len(markdownText) > maxTextBytes {
		truncMsg := "\n\n... (报告过长，已截断)"
		cutoff := maxTextBytes - len(truncMsg)
		if cutoff < 100 {
			cutoff = maxTextBytes
			truncMsg = ""
		}
		markdownText = markdownText[:cutoff] + truncMsg
	}

	payload := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": title,
			"text":  markdownText,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal dingtalk: %w", err)
	}

	req, err := http.NewRequest("POST", requestURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range d.config.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send dingtalk: %w", err)
	}
	defer resp.Body.Close()

	// Read and log response body
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	respBody := strings.TrimSpace(buf.String())

	log.Printf("DingTalk: responded with status %d, body=%s", resp.StatusCode, respBody)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("dingtalk returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func (d *DingTalkNotifier) Type() string { return "dingtalk" }

// signDingTalk calculates the HMAC-SHA256 signature for DingTalk webhook.
func signDingTalk(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// htmlToPlainText converts simple HTML to plain text (for DingTalk markdown).
func htmlToPlainText(html string) string {
	var b strings.Builder
	lines := strings.Split(html, "\n")
	inTable := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Table rows
		if strings.HasPrefix(line, "<tr") {
			b.WriteString("\n")
			inTable = true
			continue
		}
		if strings.HasPrefix(line, "</table>") {
			b.WriteString("\n")
			inTable = false
			continue
		}
		if inTable {
			// Extract cell content
			cell := stripTags(line)
			if cell != "" {
				b.WriteString("  ")
				b.WriteString(cell)
			}
			continue
		}
		// Regular lines - strip HTML tags
		text := stripTags(line)
		if text != "" {
			// Headers
			if strings.HasPrefix(line, "<h2") {
				b.WriteString("\n### ")
				b.WriteString(text)
				b.WriteString("\n")
			} else if strings.HasPrefix(line, "<p") {
				b.WriteString(text)
				b.WriteString("\n")
			} else {
				b.WriteString(text)
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// stripTags removes HTML tags from a string.
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

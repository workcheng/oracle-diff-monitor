package notifier

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/smtp"
	"oracle-diff-monitor/internal/models"
	"strings"
)

type EmailNotifier struct {
	config models.EmailConfig
}

func NewEmailNotifier(configJSON string) (*EmailNotifier, error) {
	var cfg models.EmailConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("parse email config: %w", err)
	}
	return &EmailNotifier{config: cfg}, nil
}

func (e *EmailNotifier) Send(subject, body string) error {
	useSSL := e.config.SMTPPort == 465
	log.Printf("Email: connecting to SMTP %s:%d (SSL=%v), from=%s, to=%v",
		e.config.SMTPHost, e.config.SMTPPort, useSSL, e.config.FromAddr, e.config.ToAddresses)

	addr := fmt.Sprintf("%s:%d", e.config.SMTPHost, e.config.SMTPPort)

	// 1. Connect with SSL or STARTTLS
	var client *smtp.Client
	var err error

	if useSSL {
		// SSL direct connect (port 465)
		tlsConfig := &tls.Config{
			ServerName: e.config.SMTPHost,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("SSL connect failed: %w", err)
		}
		client, err = smtp.NewClient(conn, e.config.SMTPHost)
		if err != nil {
			conn.Close()
			return fmt.Errorf("create SMTP client failed: %w", err)
		}
	} else {
		// STARTTLS (port 587)
		client, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("connect SMTP failed: %w", err)
		}
	}
	defer client.Close()

	log.Printf("Email: connected to SMTP %s:%d OK", e.config.SMTPHost, e.config.SMTPPort)

	// 2. STARTTLS if not SSL
	if !useSSL {
		if err := client.StartTLS(&tls.Config{ServerName: e.config.SMTPHost}); err != nil {
			return fmt.Errorf("STARTTLS failed: %w", err)
		}
	}

	// 3. Auth
	auth := smtp.PlainAuth("", e.config.Username, e.config.Password, e.config.SMTPHost)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth failed: %w", err)
	}

	// 4. From
	if err := client.Mail(e.config.FromAddr); err != nil {
		return fmt.Errorf("set from failed: %w", err)
	}

	// 5. Recipients
	for _, to := range e.config.ToAddresses {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("add recipient %s failed: %w", to, err)
		}
	}

	// 6. Build and send message
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("get data writer failed: %w", err)
	}

	msg := buildMimeMessage(e.config.FromAddr, e.config.ToAddresses, subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		w.Close()
		return fmt.Errorf("write message failed: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer failed: %w", err)
	}

	log.Printf("Email: sent OK to %v", e.config.ToAddresses)
	return nil
}

func (e *EmailNotifier) Type() string { return "email" }

// buildMimeMessage builds a MIME email message with base64-encoded HTML content.
func buildMimeMessage(from string, to []string, subject, htmlBody string) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject)))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	buf.WriteString("Content-Transfer-Encoding: base64\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(base64Encode([]byte(htmlBody)))

	return buf.String()
}

// base64Encode encodes data to base64 with 76-char line wrapping.
func base64Encode(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	const lineLength = 76
	var buf strings.Builder
	for i := 0; i < len(encoded); i += lineLength {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		end := i + lineLength
		if end > len(encoded) {
			end = len(encoded)
		}
		buf.WriteString(encoded[i:end])
	}
	return buf.String()
}

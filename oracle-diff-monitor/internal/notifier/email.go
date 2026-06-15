package notifier

import (
	"encoding/json"
	"fmt"
	"oracle-diff-monitor/internal/models"

	mail "github.com/xhit/go-simple-mail/v2"
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
	server := mail.NewSMTPClient()
	server.Host = e.config.SMTPHost
	server.Port = e.config.SMTPPort
	server.Username = e.config.Username
	server.Password = e.config.Password
	server.ConnectTimeout = 10
	server.SendTimeout = 30

	if e.config.UseTLS {
		server.Encryption = mail.EncryptionSTARTTLS
	} else {
		server.Encryption = mail.EncryptionSSL
	}

	smtpClient, err := server.Connect()
	if err != nil {
		return fmt.Errorf("connect smtp: %w", err)
	}
	defer smtpClient.Close()

	for _, to := range e.config.ToAddresses {
		msg := mail.NewMSG()
		msg.SetFrom(e.config.FromAddr).
			AddTo(to).
			SetSubject(subject).
			SetBody(mail.TextHTML, body)

		if msg.Error != nil {
			return msg.Error
		}

		if err := msg.Send(smtpClient); err != nil {
			return fmt.Errorf("send email to %s: %w", to, err)
		}
	}
	return nil
}

func (e *EmailNotifier) Type() string { return "email" }

// Package mail sends transactional email. Without SMTP configured it logs the
// message so local development needs no external service.
package mail

import (
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"

	"luduslog/internal/config"
)

type Mailer struct {
	cfg config.Config
	log *slog.Logger
}

func New(cfg config.Config, log *slog.Logger) *Mailer { return &Mailer{cfg: cfg, log: log} }

func (m *Mailer) Configured() bool { return m.cfg.SMTPHost != "" }

func (m *Mailer) Send(to, subject, body string) error {
	from := m.cfg.SMTPFrom
	if from == "" {
		from = "noreply@" + strings.TrimPrefix(strings.TrimPrefix(m.cfg.BaseURL, "https://"), "http://")
	}
	msg := "From: " + m.cfg.Name + " <" + from + ">\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" + body
	if !m.Configured() {
		m.log.Info("email (SMTP not configured)", "to", to, "subject", subject, "body", body)
		return nil
	}
	addr := fmt.Sprintf("%s:%d", m.cfg.SMTPHost, m.cfg.SMTPPort)
	var auth smtp.Auth
	if m.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", m.cfg.SMTPUser, m.cfg.SMTPPass, m.cfg.SMTPHost)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
}

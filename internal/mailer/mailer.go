// Package mailer sends transactional email, over SMTP (Mailtrap before go-live) or through
// Microsoft 365 (see Graph).
package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/arshadm25/whatsapp_crm/internal/config"
)

type Message struct {
	To      string
	Subject string
	Text    string
}

type Mailer interface {
	Send(ctx context.Context, m Message) error
}

type SMTP struct {
	cfg config.Mail
}

func NewSMTP(cfg config.Mail) *SMTP { return &SMTP{cfg: cfg} }

// New returns the Mailer the configuration selects.
func New(cfg config.Mail) Mailer {
	if cfg.Provider == "microsoft365" {
		return NewGraph(cfg)
	}
	return NewSMTP(cfg)
}

func (s *SMTP) Send(_ context.Context, m Message) error {
	from, err := checkMessage(s.cfg.From, m)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", m.Subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.Text, "\n", "\r\n"))

	addr := net.JoinHostPort(s.cfg.SMTPHost, strconv.Itoa(s.cfg.SMTPPort))
	var auth smtp.Auth
	if s.cfg.Username != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.SMTPHost)
	}
	return smtp.SendMail(addr, auth, from.Address, []string{m.To}, []byte(b.String()))
}

func checkMessage(fromHeader string, m Message) (*mail.Address, error) {
	from, err := mail.ParseAddress(fromHeader)
	if err != nil {
		return nil, fmt.Errorf("mailer: bad from address: %w", err)
	}
	if strings.ContainsAny(m.To+m.Subject, "\r\n") {
		return nil, fmt.Errorf("mailer: header injection attempt")
	}
	return from, nil
}

// Log is a Mailer that only logs; used in tests.
type Log struct{ Logger *slog.Logger }

func (l Log) Send(_ context.Context, m Message) error {
	l.Logger.Info("email (not sent)", "to", m.To, "subject", m.Subject)
	return nil
}

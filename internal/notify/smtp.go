package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
)

type Message struct {
	ID      string
	To      string
	Subject string
	Text    string
	HTML    string
}
type Sender interface {
	Send(context.Context, Message) error
}
type SMTP struct{ cfg config.SMTPConfig }

func NewSMTP(cfg config.SMTPConfig) *SMTP { return &SMTP{cfg: cfg} }
func (s *SMTP) Ready() bool {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.DeliveryMode))
	if mode != "" && mode != "smtp" && mode != "preview" {
		return false
	}
	_, err := mail.ParseAddress(s.cfg.From)
	if s.cfg.Host == "" || strings.ContainsAny(s.cfg.Host, "\r\n\t /") || strings.ContainsAny(s.cfg.From, "\r\n") || s.cfg.Port <= 0 || s.cfg.Port > 65535 || err != nil {
		return false
	}
	if s.cfg.TLS && s.cfg.StartTLS {
		return false
	}
	return s.cfg.TLS || s.cfg.StartTLS || s.cfg.AllowInsecure
}

// DeliveryMode describes the configured transport, not inbox delivery. Preview
// is explicit: loopback SMTP can also be a genuine outbound relay.
func (s *SMTP) DeliveryMode() string {
	if !s.Ready() {
		return "disabled"
	}
	if strings.EqualFold(strings.TrimSpace(s.cfg.DeliveryMode), "preview") {
		return "preview"
	}
	return "smtp"
}

func (s *SMTP) Send(ctx context.Context, m Message) (sendErr error) {
	if !s.Ready() {
		return errors.New("SMTP service is not configured")
	}
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return errors.New("invalid SMTP sender")
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return errors.New("invalid notification recipient")
	}
	if strings.ContainsAny(m.ID+m.Subject, "\r\n") {
		return errors.New("invalid notification headers")
	}
	if s.cfg.TLS && s.cfg.StartTLS {
		return errors.New("configure either implicit TLS or STARTTLS, not both")
	}
	if !s.cfg.TLS && !s.cfg.StartTLS && !s.cfg.AllowInsecure {
		return errors.New("plain SMTP requires explicit allow_insecure configuration")
	}
	timeout := s.cfg.Timeout.Duration
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsConfig := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if s.cfg.TLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("SMTP connection canceled: %w", ctx.Err())
		}
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return errors.New("SMTP greeting failed")
	}
	defer client.Close()
	if s.cfg.StartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not offer required STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return errors.New("SMTP TLS negotiation failed")
		}
	}
	if s.cfg.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return errors.New("SMTP sender was rejected")
	}
	if err = client.Rcpt(to.Address); err != nil {
		return errors.New("SMTP recipient was rejected")
	}
	data, err := client.Data()
	if err != nil {
		return errors.New("SMTP data command failed")
	}
	if err = writeSMTPMessage(data, from, to, m); err != nil {
		return errors.New("SMTP message could not be written")
	}
	if err = data.Close(); err != nil {
		return errors.New("SMTP acceptance was not confirmed")
	}
	// DATA's 250 reply is the acceptance boundary. QUIT failure does not mean
	// the message was rejected and must not cause an automatic duplicate send.
	_ = client.Quit()
	return nil
}

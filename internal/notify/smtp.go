package notify

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/store"
)

type Message struct {
	ID      string
	To      string
	Subject string
	Text    string
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
	sum := sha256.Sum256([]byte(m.ID))
	messageID := "proofbridge-" + hex.EncodeToString(sum[:16])
	domain := "proofbridge.local"
	if i := strings.LastIndex(from.Address, "@"); i >= 0 {
		domain = from.Address[i+1:]
	}
	headers := "From: " + from.String() + "\r\nTo: " + to.String() + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", m.Subject) + "\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\nMessage-ID: <" + messageID + "@" + domain + ">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n"
	if _, err = data.Write([]byte(headers)); err != nil {
		return errors.New("SMTP message headers could not be written")
	}
	encoded := quotedprintable.NewWriter(data)
	if _, err = encoded.Write([]byte(m.Text)); err != nil {
		return errors.New("SMTP message body could not be written")
	}
	if err = encoded.Close(); err != nil {
		return errors.New("SMTP message body could not be completed")
	}
	if err = data.Close(); err != nil {
		return errors.New("SMTP acceptance was not confirmed")
	}
	// DATA's 250 reply is the acceptance boundary. QUIT failure does not mean
	// the message was rejected and must not cause an automatic duplicate send.
	_ = client.Quit()
	return nil
}

func FromNotification(n store.AutomationNotification, publicURL string) Message {
	var result store.AutomationResult
	_ = json.Unmarshal(n.Snapshot, &result)
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = "ProofBridge task"
	}
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:100])
	}
	title = strings.NewReplacer("\r", " ", "\n", " ").Replace(title)
	link := strings.TrimRight(publicURL, "/") + "/orchestrate/runs/" + url.PathEscape(n.RunID)
	if n.Kind == "attention" {
		return Message{ID: n.ID, To: n.Recipient, Subject: "[ProofBridge] 任务需要处理：" + title, Text: fmt.Sprintf("任务需要你的审批或回答。\n任务：%s\n任务编号：%s\n提交编号：%s\n\n请连接实验室 VPN 并登录本人账号查看和处理：\n%s\n\n这不是任务完成报告。邮件不包含审批命令或原生问题正文。", title, n.RunID, n.SubmissionID, link)}
	}
	label := "运行结束，验证未通过"
	switch result.Execution.Status {
	case store.OrchestrationFailed:
		label = "执行失败，验证结果请查看报告"
	case store.OrchestrationCanceled:
		label = "运行已取消"
	}
	if result.Execution.Status == store.OrchestrationCompleted && result.Verification.Status == "pass" {
		label = "验证通过"
	}
	verification := result.Verification.Status
	if verification == "" {
		verification = "unknown"
	}
	summary := result.Verification.Summary
	if len([]rune(summary)) > 3000 {
		summary = string([]rune(summary)[:3000]) + "…"
	}
	body := fmt.Sprintf("任务：%s\n任务编号：%s\n提交编号：%s\n执行状态：%s\n结束原因：%s\n验证来源：%s\n验证状态：%s\n\n%s\n", title, n.RunID, n.SubmissionID, result.Execution.Status, result.Execution.TerminalReason, result.Verification.Authority, verification, summary)
	if len(result.Verification.UnmetObligations) > 0 {
		body += "\n未完成项：\n"
		for i, item := range result.Verification.UnmetObligations {
			if i >= 20 {
				body += "…\n"
				break
			}
			if len([]rune(item)) > 300 {
				item = string([]rune(item)[:300]) + "…"
			}
			body += "- " + item + "\n"
		}
	}
	body += "\n详细结果（需要实验室 VPN 和本人账号）：\n" + link + "\n\n“运行结束”不等于“证明通过”。SMTP 接受投递不表示已进入收件箱。\n"
	return Message{ID: n.ID, To: n.Recipient, Subject: "[ProofBridge] " + label + "：" + title, Text: body}
}

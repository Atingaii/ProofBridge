package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/store"
)

func smtpFixture(t *testing.T) (config.SMTPConfig, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	messages := make(chan string, 10)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				w := bufio.NewWriter(conn)
				send := func(line string) { _, _ = w.WriteString(line + "\r\n"); _ = w.Flush() }
				send("220 local.test ESMTP")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					switch {
					case strings.HasPrefix(line, "EHLO"):
						send("250-local.test")
						send("250 8BITMIME")
					case strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
						send("250 accepted")
					case line == "DATA":
						send("354 continue")
						var body strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if l == ".\r\n" {
								break
							}
							body.WriteString(l)
						}
						messages <- body.String()
						send("250 accepted")
					case line == "QUIT":
						// Dropping after DATA acceptance must not report delivery failure.
						return
					default:
						send("500 unsupported")
					}
				}
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return config.SMTPConfig{Host: "127.0.0.1", Port: addr.Port, From: "ProofBridge <sender@example.test>", AllowInsecure: true, Timeout: config.Duration{Duration: 2 * time.Second}}, messages
}

func TestSMTPActualLocalDeliveryStableIdentityAndUTF8(t *testing.T) {
	cfg, received := smtpFixture(t)
	sender := NewSMTP(cfg)
	for i := 0; i < 2; i++ {
		if err := sender.Send(context.Background(), Message{ID: "notification-1", To: "owner@example.test", Subject: "运行结束，验证未通过", Text: "证明义务仍未完成。\n查看内网链接。"}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for i := 0; i < 2; i++ {
		select {
		case raw := <-received:
			msg, err := mail.ReadMessage(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			subject, err := (&mime.WordDecoder{}).DecodeHeader(msg.Header.Get("Subject"))
			if err != nil || subject != "运行结束，验证未通过" {
				t.Fatalf("subject %q %v", subject, err)
			}
			body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
			if err != nil || !strings.Contains(string(body), "证明义务仍未完成") {
				t.Fatalf("body %q %v", body, err)
			}
			ids = append(ids, msg.Header.Get("Message-ID"))
		case <-time.After(3 * time.Second):
			t.Fatal("local SMTP did not accept message")
		}
	}
	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("retry changed message identity: %#v", ids)
	}
}

func TestSMTPDeliveryModeIsExplicitAndPreservesLocalRelays(t *testing.T) {
	base := config.SMTPConfig{Host: "127.0.0.1", Port: 1025, From: "sender@example.test", AllowInsecure: true}
	for _, tc := range []struct {
		name, configured, want string
	}{
		{"omitted-local-relay", "", "smtp"},
		{"outbound", "smtp", "smtp"},
		{"local-preview", "preview", "preview"},
		{"normalized", " Preview ", "preview"},
		{"unknown-fails-closed", "preveiw", "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.DeliveryMode = tc.configured
			sender := NewSMTP(cfg)
			if got := sender.DeliveryMode(); got != tc.want || sender.Ready() != (tc.want != "disabled") {
				t.Fatalf("mode=%s ready=%v, want mode=%s", got, sender.Ready(), tc.want)
			}
		})
	}
	base.DeliveryMode = "preview"
	base.Host = ""
	if sender := NewSMTP(base); sender.Ready() || sender.DeliveryMode() != "disabled" {
		t.Fatal("preview mode bypassed missing transport configuration")
	}
}

func TestSMTPRejectsMissingTLSAndHeaderInjection(t *testing.T) {
	cfg, _ := smtpFixture(t)
	cfg.StartTLS = true
	if err := NewSMTP(cfg).Send(context.Background(), Message{ID: "id", To: "owner@example.test", Subject: "task", Text: "x"}); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatal("required TLS silently downgraded:", err)
	}
	cfg.StartTLS = false
	if err := NewSMTP(cfg).Send(context.Background(), Message{ID: "id", To: "owner@example.test", Subject: "task\r\nBcc: injected@example.test", Text: "x"}); err == nil {
		t.Fatal("accepted injected header")
	}
	cfg.AllowInsecure = false
	if err := NewSMTP(cfg).Send(context.Background(), Message{ID: "id", To: "owner@example.test", Subject: "task", Text: "x"}); err == nil {
		t.Fatal("plain SMTP without explicit configuration")
	}
}
func TestSMTPContextCancelsWaitingServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	cfg := config.SMTPConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, From: "sender@example.test", AllowInsecure: true, Timeout: config.Duration{Duration: 10 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := NewSMTP(cfg).Send(ctx, Message{ID: "timeout", To: "owner@example.test", Subject: "task", Text: "x"}); err == nil {
		t.Fatal("unresponsive server succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("context did not interrupt SMTP")
	}
	select {
	case c := <-accepted:
		_ = c.Close()
	default:
	}
}
func TestNotificationMessageSeparatesExecutionFromVerification(t *testing.T) {
	result := store.AutomationResult{Title: "Proof A", Execution: store.AutomationExecutionResult{Status: "completed", TerminalReason: "stalled"}, Verification: store.AutomationVerificationResult{Status: "continue", Authority: "agent", Summary: "Theorem still incomplete"}}
	raw, _ := json.Marshal(result)
	n := store.AutomationNotification{ID: "mail", RunID: "orc_1", SubmissionID: "submission_1", Recipient: "owner@example.test", Kind: "completion", Snapshot: raw}
	message := FromNotification(n, "https://lab.example.test/")
	if strings.Contains(message.Subject, "验证通过") || !strings.Contains(message.Subject, "验证未通过") {
		t.Fatal("completed was called verified:", message.Subject)
	}
	if !strings.Contains(message.Text, "/orchestrate/runs/orc_1") || !strings.Contains(message.Text, "submission_1") {
		t.Fatal("report lacks task boundary and usable route")
	}
	result.Execution.Status = "failed"
	result.Verification.Status = "pass"
	n.Snapshot, _ = json.Marshal(result)
	message = FromNotification(n, "https://lab.example.test")
	if strings.Contains(message.Subject, "验证通过") || !strings.Contains(message.Subject, "执行失败") {
		t.Fatal("failed execution produced a success subject")
	}
	n.Kind = "attention"
	message = FromNotification(n, "https://lab.example.test")
	if !strings.Contains(message.Subject, "需要处理") || !strings.Contains(message.Text, "这不是任务完成报告") {
		t.Fatal("attention called task completion")
	}
}

package hub

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type automationSMTPReceipt struct {
	raw      string
	accepted bool
}

// This sink speaks SMTP over a real local TCP connection. Its first DATA
// rejection exercises durable worker retry without an external mail provider.
func automationSMTPSink(t *testing.T, rejectFirst int32) (config.SMTPConfig, <-chan automationSMTPReceipt) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	receipts := make(chan automationSMTPReceipt, 10)
	var attempts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				r := bufio.NewReader(conn)
				w := bufio.NewWriter(conn)
				reply := func(line string) {
					_, _ = w.WriteString(line + "\r\n")
					_ = w.Flush()
				}
				reply("220 proofbridge-local.test ESMTP")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
						reply("250 accepted")
					case line == "DATA":
						reply("354 send message")
						var raw strings.Builder
						for {
							bodyLine, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if bodyLine == ".\r\n" {
								break
							}
							raw.WriteString(bodyLine)
						}
						accepted := attempts.Add(1) > rejectFirst
						if accepted {
							reply("250 accepted")
						} else {
							reply("451 temporary local failure")
						}
						receipts <- automationSMTPReceipt{raw: raw.String(), accepted: accepted}
					case line == "QUIT":
						reply("221 bye")
						return
					default:
						reply("500 unsupported")
					}
				}
			}()
		}
	}()
	return config.SMTPConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, From: "ProofBridge <sender@example.test>", AllowInsecure: true, Timeout: config.Duration{Duration: time.Second}}, receipts
}

func TestAutomationNotificationWorkerDeliversAfterClientExitAndRetries(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	s.cfg.Automation.PublicURL = "https://lab.example.test"
	var receipts <-chan automationSMTPReceipt
	s.cfg.Automation.SMTP, receipts = automationSMTPSink(t, 1)
	if _, err := st.PutAutomationSettings(ctx, uid, "account", json.RawMessage(`{"email":"owner@example.test","completionEnabled":true,"attentionEnabled":true}`), 0); err != nil {
		t.Fatal(err)
	}
	sub, _, err := st.ReserveAutomationSubmission(ctx, uid, "start", "worker-client-exited", "digest", "background-mail", json.RawMessage(`{"verificationMode":"agent"}`))
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{UserID: uid, AgentID: agentID, Title: "Local mail verification", Mode: "collaboration", Prompt: "Verify target", AutomationSubmissionID: sub.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "Verify target"}); err != nil {
		t.Fatal(err)
	}
	// The task has a persisted endpoint but no browser, MCP observer, or live
	// Bridge connection. Closing an interaction must cancel its queued email.
	s.queueAutomationAttention(ctx, run.ID, "approval:instance:execution:request")
	s.cancelAutomationAttention(ctx, run.ID, "approval:instance:execution:request")
	notifications, err := st.ListAutomationNotifications(ctx, uid, run.ID, sub.ID, 100)
	if err != nil || len(notifications) != 1 || notifications[0].State != "canceled" {
		t.Fatalf("resolved attention was not canceled: %+v %v", notifications, err)
	}
	if err = st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "run.end", Status: store.OrchestrationCompleted, RunEndData: &protocol.RunEndData{TerminalReason: "max-turns", VerifierVerdict: &protocol.VerifierVerdict{Status: "continue", Reason: "One proof obligation remains"}}}); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.runAutomationNotifications(workerCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("notification worker did not stop after cancellation")
		}
	})
	var ids []string
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	accepted := false
	for !accepted {
		select {
		case receipt := <-receipts:
			message, err := mail.ReadMessage(strings.NewReader(receipt.raw))
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, message.Header.Get("Message-ID"))
			if !receipt.accepted {
				continue
			}
			accepted = true
			subject, err := (&mime.WordDecoder{}).DecodeHeader(message.Header.Get("Subject"))
			if err != nil || !strings.Contains(subject, "验证未通过") {
				t.Fatalf("mail incorrectly claims proof success: %q %v", subject, err)
			}
			body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
			if err != nil || !strings.Contains(string(body), sub.ID) || !strings.Contains(string(body), "https://lab.example.test/orchestrate/runs/"+run.ID) || !strings.Contains(string(body), "One proof obligation remains") {
				t.Fatalf("SMTP report lost submission or final result: %q %v", body, err)
			}
		case <-deadline.C:
			t.Fatal("worker did not deliver the durable completion report to local SMTP")
		}
	}
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("SMTP retry changed identity or count: %+v", ids)
	}
	// Give the worker its next tick. A completed outbox record must not send
	// again, and the previously closed attention must never be sent.
	select {
	case extra := <-receipts:
		t.Fatalf("duplicate or canceled attention mail delivered: accepted=%v", extra.accepted)
	case <-time.After(1500 * time.Millisecond):
	}
	notifications, err = st.ListAutomationNotifications(ctx, uid, run.ID, sub.ID, 100)
	if err != nil || len(notifications) != 2 {
		t.Fatalf("notification states unavailable: %+v %v", notifications, err)
	}
	for _, n := range notifications {
		if n.Kind == "completion" && (n.State != "sent" || n.Attempts != 2 || n.SentAt == 0 || n.NextAttemptAt != 0) {
			t.Fatalf("successful SMTP receipt was not durable: %+v", n)
		}
		if n.Kind == "attention" && (n.State != "canceled" || n.Attempts != 0) {
			t.Fatalf("closed attention was delivered: %+v", n)
		}
	}
	t.Log("Local TCP SMTP rejected the first DATA, accepted one completion on retry with the same Message-ID; no observer or Bridge connection was present; closed attention sent zero messages.")
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification worker did not exit after context cancellation")
	}
}

func TestAutomationMailReadinessRequiresUsableSafeConfiguration(t *testing.T) {
	valid := config.SMTPConfig{Host: "smtp.example.test", Port: 587, From: "sender@example.test", StartTLS: true}
	for _, tc := range []struct {
		name string
		edit func(*config.Config)
		want bool
	}{
		{"STARTTLS", func(*config.Config) {}, true},
		{"disabled", func(c *config.Config) { c.Automation.Enabled = false }, false},
		{"missing-host", func(c *config.Config) { c.Automation.SMTP.Host = "" }, false},
		{"invalid-sender", func(c *config.Config) { c.Automation.SMTP.From = "invalid" }, false},
		{"invalid-port", func(c *config.Config) { c.Automation.SMTP.Port = 0 }, false},
		{"unsafe-plain", func(c *config.Config) { c.Automation.SMTP.StartTLS = false }, false},
		{"TLS-conflict", func(c *config.Config) { c.Automation.SMTP.TLS = true }, false},
		{"implicit-TLS", func(c *config.Config) { c.Automation.SMTP.TLS = true; c.Automation.SMTP.StartTLS = false }, true},
		{"explicit-local-test", func(c *config.Config) { c.Automation.SMTP.StartTLS = false; c.Automation.SMTP.AllowInsecure = true }, true},
		{"missing-public-url", func(c *config.Config) { c.Automation.PublicURL = ""; c.Gateway.Host = "0.0.0.0" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Automation.PublicURL = "https://lab.example.test"
			cfg.Automation.SMTP = valid
			tc.edit(&cfg)
			s := &Server{cfg: &cfg}
			if got := s.automationMailReady(); got != tc.want {
				t.Fatalf("mail readiness=%v, want=%v", got, tc.want)
			}
		})
	}
}

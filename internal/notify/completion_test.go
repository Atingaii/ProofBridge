package notify

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/store"
)

var completionPreviewDir = flag.String("completion-email-preview-dir", "", "Optional output directory for synthetic rendered mail previews")

func summaryMessage(result store.AutomationResult) Message {
	raw, _ := json.Marshal(result)
	return FromNotification(store.AutomationNotification{ID: "synthetic-notification", Kind: "completion", RunID: "synthetic-run", SubmissionID: "synthetic-submission", Recipient: "owner@example.test", Snapshot: raw}, "https://lab.example.test")
}

func TestCompletionSummaryOutcomeAndPrivateContent(t *testing.T) {
	for _, tc := range []struct{ execution, verification, want string }{
		{"completed", "pass", "验证通过"}, {"completed", "continue", "未完成验证"}, {"completed", "unknown", "未完成验证"}, {"failed", "pass", "执行失败"}, {"canceled", "pass", "已取消"}, {"", "pass", "结果待确认"},
	} {
		t.Run(tc.execution+tc.verification, func(t *testing.T) {
			r := store.AutomationResult{Title: "Task", Execution: store.AutomationExecutionResult{Status: tc.execution, TerminalReason: "provider-private-error"}, Verification: store.AutomationVerificationResult{Status: tc.verification, Summary: strings.Repeat("private-proof-log", 2000), UnmetObligations: []string{"private-obligation"}}}
			r.Configuration = json.RawMessage(`{"private":"private-configuration-secret"}`)
			m := summaryMessage(r)
			if !strings.Contains(m.Subject, tc.want) || !strings.Contains(m.Text, tc.want) || !strings.Contains(m.HTML, tc.want) {
				t.Fatal("incorrect public outcome")
			}
			for _, secret := range []string{"private-proof-log", "private-obligation", "provider-private-error", "private-configuration-secret"} {
				if strings.Contains(m.Text+m.HTML, secret) {
					t.Fatal("mail copied private or long evidence")
				}
			}
			if len(m.Text) > 2500 || len(m.HTML) > 10000 {
				t.Fatal("notification is not bounded")
			}
		})
	}
}

func TestCompletionSummaryUsageCoverageAndCache(t *testing.T) {
	for _, tc := range []struct {
		name         string
		usage        *store.AutomationUsageSummary
		want, absent string
	}{
		{"old-snapshot", nil, "未记录", "$0"},
		{"boundary-unknown", &store.AutomationUsageSummary{}, "未记录", "$0"},
		{"no-price", &store.AutomationUsageSummary{Available: true, TotalTokens: 105, InputTokens: 20, CacheTokens: 80, OutputTokens: 5}, "未知", "$0"},
		{"partial-price", &store.AutomationUsageSummary{Available: true, TotalTokens: 105, InputTokens: 20, CacheTokens: 80, OutputTokens: 5, EstimatedCostUSD: .12}, "仅已知部分的估算", "$0.000000"},
		{"known-zero", &store.AutomationUsageSummary{Available: true, CostKnown: true, AccountingStatus: "complete"}, "$0.000000", "未知"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := summaryMessage(store.AutomationResult{UsageSummary: tc.usage})
			if !strings.Contains(m.Text, tc.want) || tc.absent != "" && strings.Contains(m.Text, tc.absent) {
				t.Fatal("unknown or partial pricing was presented as a bill")
			}
			if tc.usage != nil && tc.usage.TotalTokens == 105 && (!strings.Contains(m.Text, "总 Token：105") || !strings.Contains(m.Text, "输入（含缓存）：100，其中缓存：80") || !strings.Contains(m.Text, "输出：5")) {
				t.Fatal("cache tokens counted twice or excluded from inclusive input")
			}
		})
	}
}

func TestCompletionHTMLIsEscapedAndHasNoRemoteResources(t *testing.T) {
	r := store.AutomationResult{Title: `<img src=x onerror="alert(1)"><script>private()</script>` + strings.Repeat("超长", 500) + "\r\nBcc: injected", Execution: store.AutomationExecutionResult{Status: "completed"}, Verification: store.AutomationVerificationResult{Status: "pass"}}
	m := summaryMessage(r)
	if strings.Contains(m.Subject, "\n") || strings.Contains(m.HTML, "<script>") || strings.Contains(m.HTML, "<img") || !strings.Contains(m.HTML, "&lt;img") || strings.Contains(m.HTML, "ZgotmplZ") {
		t.Fatal("unsafe title or failed contextual escaping")
	}
	if strings.Count(m.HTML, "<a ") != 1 || strings.Contains(m.HTML, "<link") || strings.Contains(m.HTML, "<iframe") {
		t.Fatal("mail requires external resources or contains extra links")
	}
	// Some clients strip attributes from <html>; retain language and direction
	// on the body's direct child as well, and avoid announcing layout tables.
	if !strings.Contains(m.HTML, `<html lang="zh-CN" dir="ltr">`) || !strings.Contains(m.HTML, `<table lang="zh-CN" dir="ltr" role="presentation"`) || strings.Count(m.HTML, `<table `) != strings.Count(m.HTML, `role="presentation"`) {
		t.Fatal("email language, direction or presentational table semantics missing")
	}
	raw, _ := json.Marshal(r)
	bad := FromNotification(store.AutomationNotification{Snapshot: raw}, "javascript:alert(1)")
	if strings.Contains(bad.HTML, "href=") || strings.Contains(bad.Text, "javascript:") {
		t.Fatal("unsafe configured URL became a link")
	}
}

func TestCompletionSMTPActualMultipartAlternative(t *testing.T) {
	cfg, received := smtpFixture(t)
	m := summaryMessage(store.AutomationResult{Title: "合成测试", Execution: store.AutomationExecutionResult{Status: "completed"}, Verification: store.AutomationVerificationResult{Status: "pass"}})
	if err := NewSMTP(cfg).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-received:
		message, err := mail.ReadMessage(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		kind, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
		if err != nil || kind != "multipart/alternative" {
			t.Fatal("not a multipart alternative")
		}
		reader := multipart.NewReader(message.Body, params["boundary"])
		for _, want := range []string{"text/plain", "text/html"} {
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			kind, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			body, err := io.ReadAll(part)
			if err != nil || kind != want || !strings.Contains(string(body), "验证通过") || !strings.Contains(string(body), "synthetic-submission") {
				t.Fatal("decoded MIME part lost result or identity")
			}
		}
		if _, err = reader.NextPart(); err != io.EOF {
			t.Fatal("extra MIME parts")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SMTP did not receive multipart message")
	}
}

func TestCompletionEmailPreviewArtifacts(t *testing.T) {
	dir := *completionPreviewDir
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"success", "incomplete", "cancelled", "execution-error", "unknown-usage", "partial-cost", "long-title"} {
		r := store.AutomationResult{Title: "[合成演示] ArraySet 主定理验证", Execution: store.AutomationExecutionResult{Status: "completed"}, Verification: store.AutomationVerificationResult{Status: "pass"}, UsageSummary: &store.AutomationUsageSummary{Available: true, InputTokens: 124000, CacheTokens: 28000, OutputTokens: 8600, TotalTokens: 160600, EstimatedCostUSD: .48216, CostKnown: true, AccountingStatus: "complete"}}
		switch name {
		case "incomplete":
			r.Verification.Status = "continue"
		case "cancelled":
			r.Execution.Status = "canceled"
		case "execution-error":
			r.Execution.Status = "failed"
		case "unknown-usage":
			r.UsageSummary = nil
		case "partial-cost":
			r.UsageSummary.CostKnown = false
			r.UsageSummary.AccountingStatus = "partial"
		case "long-title":
			r.Title += "：" + strings.Repeat("并发对象建模与线性化证明", 20)
		}
		m := summaryMessage(r)
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(m.HTML), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(m.Text), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

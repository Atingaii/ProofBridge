package hub

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestAutomationMailTestUsesOnlyOwnedSavedAddressAndLimitsPerUser(t *testing.T) {
	s, st, uid, _ := newOrchestrationTestServer(t)
	var receipts <-chan automationSMTPReceipt
	s.cfg.Automation.SMTP, receipts = automationSMTPSink(t, 0)
	if _, err := st.PutAutomationSettings(context.Background(), uid, "account", json.RawMessage(`{"email":"owner@example.test"}`), 0); err != nil {
		t.Fatal(err)
	}
	result := authedJSON(t, s, uid, "POST", "/api/automation/mail/test", map[string]any{}, 200)
	if result["status"] != "accepted" || result["smtpAccepted"] != true {
		t.Fatal("SMTP acceptance not reported")
	}
	select {
	case receipt := <-receipts:
		parsed, err := mail.ReadMessage(strings.NewReader(receipt.raw))
		if err != nil {
			t.Fatal(err)
		}
		subject, err := (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject"))
		if err != nil || subject != "ProofBridge 测试邮件" {
			t.Fatal("not a fixed test subject")
		}
		recipient, err := mail.ParseAddress(parsed.Header.Get("To"))
		if err != nil || recipient.Address != "owner@example.test" {
			t.Fatal("mail did not use the caller's saved address")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("real local SMTP did not receive test")
	}
	second := authedJSON(t, s, uid, "POST", "/api/automation/mail/test", map[string]any{}, 429)
	if second["code"] != "MAIL_TEST_RATE_LIMIT" {
		t.Fatal("second account request not throttled")
	}
	// User limiting does not depend on spoofable client IP headers.
	req := httptest.NewRequest("POST", "/api/automation/mail/test", strings.NewReader("{}"))
	req.Header.Set("X-Forwarded-For", "another")
	w := httptest.NewRecorder()
	s.handleAutomationMailTest(w, req, uid)
	if w.Code != 429 {
		t.Fatal("source IP bypassed account limit")
	}
	user, err := st.UpsertUser(context.Background(), "mail-second", "strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.PutAutomationSettings(context.Background(), user.ID, "account", json.RawMessage(`{"email":"second@example.test"}`), 0); err != nil {
		t.Fatal(err)
	}
	authedJSON(t, s, user.ID, "POST", "/api/automation/mail/test", map[string]any{}, 200)
	select {
	case receipt := <-receipts:
		if !strings.Contains(receipt.raw, "second@example.test") {
			t.Fatal("account recipient crossed identities")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second user's email missing")
	}
	runs, err := st.ListAllOrchestrationRuns(context.Background(), uid)
	if err != nil || len(runs) != 0 {
		t.Fatal("mail test created a proof run")
	}
}
func TestAutomationMailTestRejectsArbitraryRecipientAndReportsFailures(t *testing.T) {
	s, st, uid, _ := newOrchestrationTestServer(t)
	var receipts <-chan automationSMTPReceipt
	s.cfg.Automation.SMTP, receipts = automationSMTPSink(t, 1)
	for _, body := range []map[string]any{{"to": "other@example.test"}, {"subject": "custom"}, {"email": "other@example.test"}} {
		authedJSON(t, s, uid, "POST", "/api/automation/mail/test", body, 400)
	}
	authedJSON(t, s, uid, "POST", "/api/automation/mail/test", map[string]any{}, 409)
	if _, err := st.PutAutomationSettings(context.Background(), uid, "account", json.RawMessage(`{"email":"owner@example.test"}`), 0); err != nil {
		t.Fatal(err)
	}
	failed := authedJSON(t, s, uid, "POST", "/api/automation/mail/test", map[string]any{}, 502)
	if failed["code"] != "MAIL_TEST_FAILED" {
		t.Fatal("SMTP rejection falsely reported as success")
	}
	<-receipts
	unauth := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(unauth, httptest.NewRequest("POST", "/api/automation/mail/test", strings.NewReader("{}")))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatal("anonymous mail accepted")
	}
	// Only browser sessions manage personal mail; the personal MCP token cannot.
	token, _, err := s.personalAutomationToken(context.Background(), uid, false)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/automation/mail/test", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(response, req)
	if response.Code != 401 {
		t.Fatal("personal MCP PAT authorized browser management")
	}
	s.cfg.Automation.SMTP.Host = ""
	unavailable := authedJSON(t, s, uid, "POST", "/api/automation/mail/test", map[string]any{}, 503)
	if unavailable["code"] != "MAIL_NOT_CONFIGURED" {
		t.Fatal("missing service lacks actionable setup error")
	}
	req = httptest.NewRequest("POST", "/api/automation/mail/test", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://untrusted.example.test")
	response = httptest.NewRecorder()
	s.handleAutomationMailTest(response, req, uid)
	if response.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	body, _ := io.ReadAll(response.Result().Body)
	if strings.Contains(string(body), token) {
		t.Fatal("credentials leaked")
	}
}

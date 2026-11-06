package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/notify"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

const automationMailTestInterval = time.Minute

// A personal test validates the configured transport without granting an
// arbitrary-recipient mail API or creating a proof run.
func (s *Server) handleAutomationMailTest(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.automationManagementReady(w, r) {
		return
	}
	var args struct{}
	if err := serverutil.DecodeJSON(r, &args, 4<<10); err != nil {
		serverutil.WriteError(w, 400, "BAD_REQUEST", "测试邮件不接受收件人或自定义正文。请使用个人中心已保存的邮箱。")
		return
	}
	if !s.automationMailReady() {
		serverutil.WriteError(w, 503, "MAIL_NOT_CONFIGURED", "邮件服务尚未准备好。请让管理员检查 SMTP 环境变量和站点访问地址后重启 Hub。")
		return
	}
	prefs, err := s.automationPreferences(r.Context(), uid, "account")
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	var settings struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(prefs.Data, &settings) != nil {
		serverutil.WriteError(w, 500, "PREFERENCES_UNAVAILABLE", "暂时无法读取邮件设置，请重新加载后重试。")
		return
	}
	addr, err := mail.ParseAddress(settings.Email)
	if settings.Email == "" || err != nil || addr.Address != settings.Email || strings.ContainsAny(settings.Email, "\r\n") {
		serverutil.WriteError(w, 409, "EMAIL_REQUIRED", "请先保存自己的有效收件邮箱，再发送测试邮件。")
		return
	}
	if !s.claimAutomationMailTest(uid) {
		w.Header().Set("Retry-After", "60")
		serverutil.WriteError(w, 429, "MAIL_TEST_RATE_LIMIT", "每个账户每分钟可发送一封测试邮件，请稍后再试。")
		return
	}
	timeout := s.cfg.Automation.SMTP.Timeout.Duration
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	message := notify.Message{
		ID: store.NewID("mailtest"), To: settings.Email, Subject: "ProofBridge 测试邮件",
		Text: "这是一封由 ProofBridge Hub 发送的测试邮件。\n\n这封邮件用于检查你已保存的收件邮箱与部署的 SMTP 发送配置。\n它不是证明任务的完成报告，也不会启动或修改证明任务。\n\n如果你没有主动发送测试，请检查个人中心的登录和邮箱设置。\n",
	}
	if err = notify.NewSMTP(s.cfg.Automation.SMTP).Send(ctx, message); err != nil {
		serverutil.WriteError(w, 502, "MAIL_TEST_FAILED", "SMTP 服务未接受测试邮件。请联系管理员检查网络、发件身份、服务额度和凭据后重试。")
		return
	}
	serverutil.WriteJSON(w, 200, map[string]any{"status": "accepted", "smtpAccepted": true, "message": "邮件服务已接受测试邮件，请检查收件箱与垃圾邮件。本地测试服务请查看其收件箱。"})
}

func (s *Server) claimAutomationMailTest(uid string) bool {
	now := time.Now()
	key := "automation-mail-test|" + uid
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if bucket, exists := s.rates[key]; exists && now.Before(bucket.reset) {
		return false
	}
	s.rates[key] = rateBucket{count: 1, reset: now.Add(automationMailTestInterval)}
	return true
}

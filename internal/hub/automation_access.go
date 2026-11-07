package hub

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

const automationTokenPurpose = "proofbridge-personal-mcp-token-v1"

func (s *Server) automationPublicURL() (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(s.cfg.Automation.PublicURL), "/")
	if raw == "" {
		host := s.cfg.Gateway.Host
		if host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
			raw = "http://" + net.JoinHostPort(host, strconv.Itoa(s.cfg.Gateway.Port))
		} else {
			return "", &operationError{503, "AUTOMATION_URL_MISSING", "请由部署管理员设置 AUTOMATION_PUBLIC_URL，然后重新复制配置。"}
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", &operationError{503, "AUTOMATION_URL_INVALID", "自动化访问地址配置无效，请联系部署管理员。"}
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" {
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return "", &operationError{503, "AUTOMATION_TLS_REQUIRED", "内网 MCP 地址需使用 HTTPS；本地回环测试可使用 HTTP。"}
		}
	}
	return raw, nil
}

func (s *Server) automationCipher() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(automationTokenPurpose + "\x00" + s.cfg.Auth.JWTSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Server) sealAutomationToken(token string) (string, error) {
	aead, err := s.automationCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	raw := aead.Seal(nonce, nonce, []byte(token), []byte(automationTokenPurpose))
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (s *Server) openAutomationToken(raw string) (string, error) {
	aead, err := s.automationCipher()
	if err != nil {
		return "", err
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) < aead.NonceSize() {
		return "", errors.New("invalid protected automation token")
	}
	text, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(automationTokenPurpose))
	if err != nil {
		return "", errors.New("automation token could not be decrypted; regenerate it in account settings")
	}
	defer clear(text)
	return string(text), nil
}
func automationTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) personalAutomationToken(ctx context.Context, uid string, rotate bool) (string, store.AutomationToken, error) {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	record, err := s.store.GetAutomationToken(ctx, uid)
	if err == nil && !rotate {
		if record.RevokedAt != 0 {
			return "", record, nil
		}
		token, e := s.openAutomationToken(record.Ciphertext)
		return token, record, e
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", record, err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", record, err
	}
	token := "pb_mcp_" + base64.RawURLEncoding.EncodeToString(raw)
	protected, err := s.sealAutomationToken(token)
	if err != nil {
		return "", record, err
	}
	record = store.AutomationToken{UserID: uid, TokenHash: automationTokenHash(token), Ciphertext: protected, CreatedAt: time.Now().Unix()}
	if err = s.store.PutAutomationToken(ctx, record); err != nil {
		return "", record, err
	}
	return token, record, nil
}

func (s *Server) automationManagementReady(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !s.cfg.Automation.Enabled {
		serverutil.WriteError(w, 503, "AUTOMATION_DISABLED", "自动化入口未启用，请联系维护者。")
		return false
	}
	if !s.checkOrigin(r) {
		serverutil.WriteError(w, 403, "ORIGIN_DENIED", "请求来源不允许。")
		return false
	}
	return true
}
func (s *Server) handleAutomationAccess(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.automationManagementReady(w, r) {
		return
	}
	base, err := s.automationPublicURL()
	if err != nil {
		writeOperationError(w, err)
		return
	}
	token, record, err := s.personalAutomationToken(r.Context(), uid, false)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	settings, _ := s.automationPreferences(r.Context(), uid, "account")
	var prefs map[string]any
	_ = json.Unmarshal(settings.Data, &prefs)
	codex := ""
	claude := ""
	if token != "" {
		codex = fmt.Sprintf("[mcp_servers.proofbridge]\nurl = %q\nhttp_headers = { Authorization = %q }\n", base+"/mcp", "Bearer "+token)
		raw, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"proofbridge": map[string]any{"type": "http", "url": base + "/mcp", "headers": map[string]string{"Authorization": "Bearer " + token}}}}, "", "  ")
		claude = string(raw)
	}
	install := func(client string) string {
		return "curl -fsSL " + shellQuoteAutomation(base+"/skills/install.sh") + " | sh -s -- --client " + client + " --scope user --hub " + shellQuoteAutomation(base)
	}
	var value any = token
	if token == "" {
		value = nil
	}
	serverutil.WriteJSON(w, 200, map[string]any{"token": value, "createdAt": record.CreatedAt, "revoked": record.RevokedAt != 0, "mcpUrl": base + "/mcp", "codexConfig": codex, "claudeConfig": claude, "skillInstallCommands": map[string]string{"codex": install("codex"), "claude": install("claude")}, "mailReady": s.automationMailReady(), "mailDeliveryMode": s.automationMailDeliveryMode(), "email": prefs["email"], "tokenExpiresAt": nil})
}
func shellQuoteAutomation(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (s *Server) handleRotateAutomationToken(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.automationManagementReady(w, r) {
		return
	}
	if _, _, err := s.personalAutomationToken(r.Context(), uid, true); err != nil {
		writeOperationError(w, err)
		return
	}
	s.handleAutomationAccess(w, r, uid)
}
func (s *Server) handleRevokeAutomationToken(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.automationManagementReady(w, r) {
		return
	}
	s.automationMu.Lock()
	err := s.store.RevokeAutomationToken(r.Context(), uid)
	s.automationMu.Unlock()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeOperationError(w, err)
		return
	}
	s.handleAutomationAccess(w, r, uid)
}

func (s *Server) automationPreferences(ctx context.Context, uid, scope string) (store.AutomationSettings, error) {
	if scope == "" {
		scope = "account"
	}
	row, err := s.store.GetAutomationSettings(ctx, uid, scope)
	if errors.Is(err, store.ErrNotFound) {
		row = store.AutomationSettings{Scope: scope, Data: json.RawMessage(`{}`)}
		err = nil
	}
	if err == nil && scope == "account" {
		defaults := map[string]any{"completionEnabled": true, "attentionEnabled": true, "monitoringMode": "interactive"}
		var saved map[string]any
		if json.Unmarshal(row.Data, &saved) == nil {
			for key, value := range saved {
				defaults[key] = value
			}
		}
		row.Data, _ = json.Marshal(defaults)
	}
	return row, err
}
func (s *Server) validateAutomationScope(ctx context.Context, uid, scope string) error {
	if scope == "" || scope == "account" {
		return nil
	}
	parts := strings.SplitN(scope, ":", 3)
	if len(parts) != 3 || parts[0] != "workspace" || parts[2] == "" {
		return &operationError{400, "INVALID_SCOPE", "scope 必须为 account 或 workspace:<endpointId>:<cwd>。"}
	}
	_, err := s.visibleAgentByID(ctx, uid, parts[1])
	return err
}
func (s *Server) putAutomationPreferences(ctx context.Context, uid, scope string, data json.RawMessage, version int64) (store.AutomationSettings, error) {
	if scope == "" {
		scope = "account"
	}
	if err := s.validateAutomationScope(ctx, uid, scope); err != nil {
		return store.AutomationSettings{}, err
	}
	if version < 0 {
		return store.AutomationSettings{}, &operationError{400, "INVALID_VERSION", "expectedVersion 不能为负数。"}
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return store.AutomationSettings{}, &operationError{400, "INVALID_PREFERENCES", "data 必须是偏好对象。"}
	}
	allowed := map[string]bool{"email": true, "completionEnabled": true, "attentionEnabled": true, "defaultEndpointId": true, "defaultPresetId": true, "monitoringMode": true}
	for key, raw := range values {
		if !allowed[key] {
			return store.AutomationSettings{}, &operationError{400, "INVALID_PREFERENCES", "不支持的偏好字段：" + key}
		}
		if key == "completionEnabled" || key == "attentionEnabled" {
			var v bool
			if string(raw) == "null" || json.Unmarshal(raw, &v) != nil {
				return store.AutomationSettings{}, &operationError{400, "INVALID_PREFERENCES", key + " 必须是布尔值。"}
			}
			continue
		}
		var value string
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
			return store.AutomationSettings{}, &operationError{400, "INVALID_PREFERENCES", key + " 必须是文本。"}
		}
		if key == "email" && value != "" {
			addr, err := mail.ParseAddress(value)
			if err != nil || addr.Address != value || strings.ContainsAny(value, "\r\n") {
				return store.AutomationSettings{}, &operationError{400, "INVALID_EMAIL", "请输入有效的收件邮箱。"}
			}
		}
		if key == "defaultEndpointId" && value != "" {
			if _, err := s.visibleAgentByID(ctx, uid, value); err != nil {
				return store.AutomationSettings{}, err
			}
		}
		if key == "defaultPresetId" && value != "" {
			if _, err := s.store.GetAutomationPreset(ctx, uid, value); err != nil {
				return store.AutomationSettings{}, err
			}
		}
		if key == "monitoringMode" && value != "interactive" && value != "background-mail" {
			return store.AutomationSettings{}, &operationError{400, "INVALID_MONITORING_MODE", "monitoringMode 必须是 interactive 或 background-mail。"}
		}
	}
	if _, err := s.store.PutAutomationSettings(ctx, uid, scope, data, version); err != nil {
		return store.AutomationSettings{}, err
	}
	return s.automationPreferences(ctx, uid, scope)
}
func (s *Server) handleGetAutomationPreferences(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.automationManagementReady(w, r) {
		return
	}
	scope := r.URL.Query().Get("scope")
	if err := s.validateAutomationScope(r.Context(), uid, scope); err != nil {
		writeAutomationError(w, err)
		return
	}
	row, err := s.automationPreferences(r.Context(), uid, scope)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	serverutil.WriteJSON(w, 200, row)
}
func (s *Server) handlePutAutomationPreferences(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.automationManagementReady(w, r) {
		return
	}
	var req struct {
		Scope           string          `json:"scope"`
		ExpectedVersion int64           `json:"expectedVersion"`
		Data            json.RawMessage `json:"data"`
	}
	if err := serverutil.DecodeJSON(r, &req, 64<<10); err != nil {
		serverutil.WriteError(w, 400, "BAD_REQUEST", "偏好请求无效。")
		return
	}
	row, err := s.putAutomationPreferences(r.Context(), uid, req.Scope, req.Data, req.ExpectedVersion)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	serverutil.WriteJSON(w, 200, row)
}
func (s *Server) handleAutomationNotifications(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.automationManagementReady(w, r) {
		return
	}
	run := r.URL.Query().Get("runId")
	if run != "" {
		if _, err := s.store.OrchestrationRunByID(r.Context(), run, uid); err != nil {
			writeAutomationError(w, err)
			return
		}
	}
	rows, err := s.store.ListAutomationNotifications(r.Context(), uid, run, r.URL.Query().Get("submissionId"), 50)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	serverutil.WriteJSON(w, 200, s.automationNotificationStatus(rows))
}
func writeAutomationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		serverutil.WriteError(w, 404, "NOT_FOUND", "找不到本人可访问的资源。")
	case errors.Is(err, store.ErrConflict):
		serverutil.WriteError(w, 409, "CONFLICT", "配置已更新或请求键已使用。请重新读取后再操作。")
	default:
		writeOperationError(w, err)
	}
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

// AutomationToken is deliberately not JSON-serializable credential material.
type AutomationToken struct {
	UserID     string `json:"-"`
	TokenHash  string `json:"-"`
	Ciphertext string `json:"-"`
	CreatedAt  int64  `json:"createdAt"`
	RevokedAt  int64  `json:"revokedAt,omitempty"`
}

type AutomationSettings struct {
	UserID    string          `json:"-"`
	Scope     string          `json:"scope"`
	Version   int64           `json:"version"`
	Data      json.RawMessage `json:"data"`
	UpdatedAt int64           `json:"updatedAt"`
}

type AutomationPreset struct {
	ID        string          `json:"id"`
	UserID    string          `json:"-"`
	Name      string          `json:"name"`
	Version   int64           `json:"version"`
	Config    json.RawMessage `json:"config"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

type AutomationSubmission struct {
	ID               string          `json:"submissionId"`
	UserID           string          `json:"-"`
	Operation        string          `json:"operation"`
	RequestKey       string          `json:"clientRequestId,omitempty"`
	RequestDigest    string          `json:"-"`
	RunID            string          `json:"runId,omitempty"`
	PromptSeq        int64           `json:"promptSeq,omitempty"`
	TerminalSeq      int64           `json:"-"`
	State            string          `json:"state"`
	MonitoringMode   string          `json:"monitoringMode"`
	Configuration    json.RawMessage `json:"configuration"`
	Result           json.RawMessage `json:"result,omitempty"`
	Recipient        string          `json:"-"`
	NotifyCompletion bool            `json:"-"`
	NotifyAttention  bool            `json:"-"`
	CreatedAt        int64           `json:"createdAt"`
	UpdatedAt        int64           `json:"updatedAt"`
}

type automationScanner interface{ Scan(...any) error }

const automationSubmissionColumns = `id,user_id,operation,request_key,request_digest,COALESCE(run_id,''),prompt_seq,terminal_seq,state,monitoring_mode,configuration_json,result_json,recipient,notify_completion,notify_attention,created_at,updated_at`

func (s *Store) migrateAutomation(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS automation_tokens (
			user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE, ciphertext TEXT NOT NULL,
			created_at INTEGER NOT NULL, revoked_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS automation_settings (
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			scope TEXT NOT NULL, version INTEGER NOT NULL, data_json TEXT NOT NULL,
			updated_at INTEGER NOT NULL, PRIMARY KEY(user_id,scope)
		)`,
		`CREATE TABLE IF NOT EXISTS automation_presets (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL, version INTEGER NOT NULL, configuration_json TEXT NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_presets_user ON automation_presets(user_id,updated_at DESC,id)`,
		`CREATE TABLE IF NOT EXISTS automation_submissions (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			operation TEXT NOT NULL, request_key TEXT NOT NULL, request_digest TEXT NOT NULL,
			run_id TEXT REFERENCES orchestration_runs(id) ON DELETE CASCADE,
			prompt_seq INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL DEFAULT 'validating',
			terminal_seq INTEGER NOT NULL DEFAULT 0,
			monitoring_mode TEXT NOT NULL DEFAULT 'interactive',
			configuration_json TEXT NOT NULL DEFAULT '{}', result_json TEXT NOT NULL DEFAULT '',
			recipient TEXT NOT NULL DEFAULT '', notify_completion INTEGER NOT NULL DEFAULT 0,
			notify_attention INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			UNIQUE(user_id,operation,request_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_submissions_run ON automation_submissions(run_id,prompt_seq DESC,created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS automation_notifications (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			run_id TEXT NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
			submission_id TEXT NOT NULL REFERENCES automation_submissions(id) ON DELETE CASCADE,
			kind TEXT NOT NULL, interaction_id TEXT NOT NULL DEFAULT '', recipient TEXT NOT NULL,
			snapshot_json TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'queued',
			attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER NOT NULL DEFAULT 0,
			claim_token TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			sent_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE(user_id,run_id,submission_id,kind,interaction_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_notifications_due ON automation_notifications(state,next_attempt_at,lease_until)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate automation: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) GetAutomationToken(ctx context.Context, uid string) (AutomationToken, error) {
	return scanAutomationToken(s.db.QueryRowContext(ctx, `SELECT user_id,token_hash,ciphertext,created_at,revoked_at FROM automation_tokens WHERE user_id=?`, uid))
}
func (s *Store) AutomationTokenByHash(ctx context.Context, hash string) (AutomationToken, error) {
	return scanAutomationToken(s.db.QueryRowContext(ctx, `SELECT user_id,token_hash,ciphertext,created_at,revoked_at FROM automation_tokens WHERE token_hash=? AND revoked_at=0`, hash))
}
func scanAutomationToken(row automationScanner) (AutomationToken, error) {
	var t AutomationToken
	if err := row.Scan(&t.UserID, &t.TokenHash, &t.Ciphertext, &t.CreatedAt, &t.RevokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrNotFound
		}
		return t, err
	}
	return t, nil
}
func (s *Store) PutAutomationToken(ctx context.Context, t AutomationToken) error {
	if t.UserID == "" || t.TokenHash == "" || t.Ciphertext == "" {
		return errors.New("user, hash and encrypted token are required")
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO automation_tokens(user_id,token_hash,ciphertext,created_at,revoked_at) VALUES(?,?,?,?,0)
		ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,ciphertext=excluded.ciphertext,created_at=excluded.created_at,revoked_at=0`, t.UserID, t.TokenHash, t.Ciphertext, t.CreatedAt)
	return err
}
func (s *Store) RevokeAutomationToken(ctx context.Context, uid string) error {
	if uid == "" {
		return errors.New("user is required")
	}
	now := time.Now().Unix()
	// A tombstone prevents a settings read from treating a never-issued token
	// as permission to issue one. Revocation never needs to decrypt an old PAT.
	_, err := s.db.ExecContext(ctx, `INSERT INTO automation_tokens(user_id,token_hash,ciphertext,created_at,revoked_at) VALUES(?,?,'',?,?)
		ON CONFLICT(user_id) DO UPDATE SET revoked_at=excluded.revoked_at`, uid, "revoked:"+uid, now, now)
	return err
}
func validAutomationJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && len(raw) <= 256*1024 && json.Valid(raw)
}

func (s *Store) GetAutomationSettings(ctx context.Context, uid, scope string) (AutomationSettings, error) {
	return scanAutomationSettings(s.db.QueryRowContext(ctx, `SELECT user_id,scope,version,data_json,updated_at FROM automation_settings WHERE user_id=? AND scope=?`, uid, scope))
}
func scanAutomationSettings(row automationScanner) (AutomationSettings, error) {
	var v AutomationSettings
	var raw string
	if err := row.Scan(&v.UserID, &v.Scope, &v.Version, &raw, &v.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v, ErrNotFound
		}
		return v, err
	}
	v.Data = json.RawMessage(raw)
	return v, nil
}
func (s *Store) PutAutomationSettings(ctx context.Context, uid, scope string, data json.RawMessage, expectedVersion int64) (AutomationSettings, error) {
	if uid == "" || scope == "" || len(scope) > 4096 || expectedVersion < 0 || !validAutomationJSON(data) {
		return AutomationSettings{}, errors.New("invalid automation preferences")
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AutomationSettings{}, err
	}
	defer tx.Rollback()
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT version FROM automation_settings WHERE user_id=? AND scope=?`, uid, scope).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AutomationSettings{}, err
	}
	if current != expectedVersion {
		return AutomationSettings{}, ErrConflict
	}
	if current == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO automation_settings(user_id,scope,version,data_json,updated_at) VALUES(?,?,1,?,?)`, uid, scope, string(data), now)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE automation_settings SET version=version+1,data_json=?,updated_at=? WHERE user_id=? AND scope=? AND version=?`, string(data), now, uid, scope, expectedVersion)
	}
	if err != nil {
		return AutomationSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return AutomationSettings{}, err
	}
	return AutomationSettings{UserID: uid, Scope: scope, Version: current + 1, Data: append(json.RawMessage(nil), data...), UpdatedAt: now}, nil
}

func scanAutomationPreset(row automationScanner) (AutomationPreset, error) {
	var p AutomationPreset
	var raw string
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Version, &raw, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Config = json.RawMessage(raw)
	return p, err
}
func (s *Store) GetAutomationPreset(ctx context.Context, uid, id string) (AutomationPreset, error) {
	return scanAutomationPreset(s.db.QueryRowContext(ctx, `SELECT id,user_id,name,version,configuration_json,created_at,updated_at FROM automation_presets WHERE user_id=? AND id=?`, uid, id))
}
func (s *Store) ListAutomationPresets(ctx context.Context, uid string) ([]AutomationPreset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,name,version,configuration_json,created_at,updated_at FROM automation_presets WHERE user_id=? ORDER BY updated_at DESC,id LIMIT 500`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationPreset{}
	for rows.Next() {
		p, err := scanAutomationPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SaveAutomationPreset(ctx context.Context, p AutomationPreset, expectedVersion int64) (AutomationPreset, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.UserID == "" || p.Name == "" || len(p.Name) > 240 || !validAutomationJSON(p.Config) || expectedVersion < 0 {
		return p, errors.New("invalid execution preset")
	}
	now := time.Now().Unix()
	if p.ID == "" {
		if expectedVersion != 0 {
			return p, ErrConflict
		}
		p.ID = NewID("aep")
		p.Version = 1
		p.CreatedAt = now
		p.UpdatedAt = now
		_, err := s.db.ExecContext(ctx, `INSERT INTO automation_presets(id,user_id,name,version,configuration_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, p.ID, p.UserID, p.Name, p.Version, string(p.Config), now, now)
		return p, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	current, err := scanAutomationPreset(tx.QueryRowContext(ctx, `SELECT id,user_id,name,version,configuration_json,created_at,updated_at FROM automation_presets WHERE user_id=? AND id=?`, p.UserID, p.ID))
	if err != nil {
		return p, err
	}
	if current.Version != expectedVersion {
		return p, ErrConflict
	}
	p.Version = current.Version + 1
	p.CreatedAt = current.CreatedAt
	p.UpdatedAt = now
	_, err = tx.ExecContext(ctx, `UPDATE automation_presets SET name=?,version=?,configuration_json=?,updated_at=? WHERE id=? AND user_id=? AND version=?`, p.Name, p.Version, string(p.Config), now, p.ID, p.UserID, expectedVersion)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (s *Store) DeleteAutomationPreset(ctx context.Context, uid, id string, expectedVersion int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM automation_presets WHERE id=? AND user_id=?`, id, uid).Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if version != expectedVersion {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM automation_presets WHERE id=? AND user_id=?`, id, uid); err != nil {
		return err
	}
	// Remove a deleted default reference without touching immutable run snapshots.
	rows, err := tx.QueryContext(ctx, `SELECT scope,data_json FROM automation_settings WHERE user_id=?`, uid)
	if err != nil {
		return err
	}
	type preference struct {
		scope string
		data  map[string]any
	}
	var updates []preference
	for rows.Next() {
		var scope, raw string
		if err = rows.Scan(&scope, &raw); err != nil {
			rows.Close()
			return err
		}
		var obj map[string]any
		if json.Unmarshal([]byte(raw), &obj) == nil && obj["defaultPresetId"] == id {
			delete(obj, "defaultPresetId")
			updates = append(updates, preference{scope, obj})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, v := range updates {
		raw, _ := json.Marshal(v.data)
		if _, err = tx.ExecContext(ctx, `UPDATE automation_settings SET data_json=?,version=version+1,updated_at=? WHERE user_id=? AND scope=?`, string(raw), time.Now().Unix(), uid, v.scope); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanAutomationSubmission(row automationScanner) (AutomationSubmission, error) {
	var r AutomationSubmission
	var configuration, result string
	err := row.Scan(&r.ID, &r.UserID, &r.Operation, &r.RequestKey, &r.RequestDigest, &r.RunID, &r.PromptSeq, &r.TerminalSeq, &r.State, &r.MonitoringMode, &configuration, &result, &r.Recipient, &r.NotifyCompletion, &r.NotifyAttention, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.Configuration = json.RawMessage(configuration)
	if result != "" {
		r.Result = json.RawMessage(result)
	}
	return r, err
}
func (s *Store) AutomationSubmissionByRequest(ctx context.Context, uid, operation, key string) (AutomationSubmission, error) {
	return scanAutomationSubmission(s.db.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE user_id=? AND operation=? AND request_key=?`, uid, operation, key))
}
func (s *Store) AutomationSubmissionByID(ctx context.Context, uid, id string) (AutomationSubmission, error) {
	return scanAutomationSubmission(s.db.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE user_id=? AND id=?`, uid, id))
}
func (s *Store) LatestAutomationSubmissionForRun(ctx context.Context, uid, runID string) (AutomationSubmission, error) {
	return scanAutomationSubmission(s.db.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE user_id=? AND run_id=? ORDER BY prompt_seq DESC,created_at DESC,id DESC LIMIT 1`, uid, runID))
}
func (s *Store) ReserveAutomationSubmission(ctx context.Context, uid, operation, key, digest, monitoringMode string, configuration json.RawMessage) (AutomationSubmission, bool, error) {
	if uid == "" || operation == "" || key == "" || digest == "" || len(key) > 240 || len(digest) > 256 {
		return AutomationSubmission{}, false, errors.New("invalid submission identity")
	}
	if monitoringMode == "" {
		monitoringMode = "interactive"
	}
	if monitoringMode != "interactive" && monitoringMode != "background-mail" {
		return AutomationSubmission{}, false, errors.New("invalid monitoring mode")
	}
	if len(configuration) == 0 {
		configuration = json.RawMessage(`{}`)
	}
	if !validAutomationJSON(configuration) {
		return AutomationSubmission{}, false, errors.New("invalid configuration snapshot")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AutomationSubmission{}, false, err
	}
	defer tx.Rollback()
	existing, err := scanAutomationSubmission(tx.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE user_id=? AND operation=? AND request_key=?`, uid, operation, key))
	if err == nil {
		if existing.RequestDigest != digest {
			return existing, false, ErrConflict
		}
		return existing, false, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return AutomationSubmission{}, false, err
	}
	prefs, err := automationNotificationPreferencesTx(ctx, tx, uid)
	if err != nil {
		return AutomationSubmission{}, false, err
	}
	now := time.Now().Unix()
	r := AutomationSubmission{ID: NewID("asub"), UserID: uid, Operation: operation, RequestKey: key, RequestDigest: digest, State: "validating", MonitoringMode: monitoringMode, Configuration: configuration, Recipient: prefs.Email, NotifyCompletion: prefs.CompletionEnabled, NotifyAttention: prefs.AttentionEnabled, CreatedAt: now, UpdatedAt: now}
	if monitoringMode == "background-mail" {
		r.NotifyCompletion = true
		r.NotifyAttention = true
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO automation_submissions(id,user_id,operation,request_key,request_digest,state,monitoring_mode,configuration_json,recipient,notify_completion,notify_attention,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, uid, operation, key, digest, r.State, monitoringMode, string(configuration), r.Recipient, boolInt(r.NotifyCompletion), boolInt(r.NotifyAttention), now, now)
	if err != nil {
		return AutomationSubmission{}, false, err
	}
	return r, true, tx.Commit()
}
func (s *Store) SetAutomationSubmissionConfiguration(ctx context.Context, id, uid string, configuration json.RawMessage) error {
	if !validAutomationJSON(configuration) {
		return errors.New("invalid configuration snapshot")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE automation_submissions SET configuration_json=?,updated_at=? WHERE id=? AND user_id=? AND state='validating' AND prompt_seq=0`, string(configuration), time.Now().Unix(), id, uid)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) SetAutomationSubmissionState(ctx context.Context, uid, id, state string) error {
	if state != "dispatch_unconfirmed" && state != "rejected" {
		return errors.New("invalid submission state")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE automation_submissions SET state=?,updated_at=? WHERE id=? AND user_id=? AND state<>'terminal'`, state, time.Now().Unix(), id, uid)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func bindAutomationSubmissionTx(ctx context.Context, tx *sql.Tx, id, uid, runID string) error {
	if id == "" {
		return nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE automation_submissions SET run_id=?,updated_at=? WHERE id=? AND user_id=? AND state='validating' AND run_id IS NULL`, runID, time.Now().Unix(), id, uid)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) ClaimAutomationRunForContinue(ctx context.Context, runID, uid, submissionID string) (bool, error) {
	return s.claimAutomationRunForContinue(ctx, runID, uid, submissionID)
}
func (s *Store) claimAutomationRunForContinue(ctx context.Context, runID, uid, submissionID string) (bool, error) {
	if runID == "" {
		return false, errors.New("run id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	run, err := scanOrchestrationRun(tx.QueryRowContext(ctx, `SELECT `+orchestrationRunColumns+` FROM orchestration_runs WHERE id=?`, runID))
	if err != nil {
		if errors.Is(err, ErrNotFound) && uid == "" {
			return false, nil
		}
		return false, err
	}
	if uid != "" && uid != run.UserID {
		return false, ErrNotFound
	}
	uid = run.UserID
	if run.DeleteRequested || !automationTerminal(run.Status) {
		return false, nil
	}
	if err = automationReconcileRunTx(ctx, tx, run); err != nil {
		return false, err
	}
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `UPDATE orchestration_runs SET status=?,error=NULL,updated_at=?,archived_at=0 WHERE id=? AND user_id=? AND delete_requested=0 AND status IN (?,?,?)`, OrchestrationRunning, now, runID, uid, OrchestrationCompleted, OrchestrationFailed, OrchestrationCanceled)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if err = bindAutomationSubmissionTx(ctx, tx, submissionID, uid, runID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// AutomationResult freezes the accepted task's public outcome, never credential/native-resume data.
type AutomationResult struct {
	SchemaVersion  int                          `json:"schemaVersion"`
	RunID          string                       `json:"runId"`
	SubmissionID   string                       `json:"submissionId"`
	PromptSeq      int64                        `json:"promptSeq"`
	Title          string                       `json:"title"`
	EndpointID     string                       `json:"endpointId"`
	Execution      AutomationExecutionResult    `json:"execution"`
	Verification   AutomationVerificationResult `json:"verification"`
	Configuration  json.RawMessage              `json:"configuration"`
	FinishedAt     int64                        `json:"finishedAt"`
	UsageCutoffSeq int64                        `json:"usageCutoffSeq,omitempty"`
	UsageSummary   *AutomationUsageSummary      `json:"usageSummary,omitempty"`
}

// Tokens use the statistics convention: InputTokens excludes cache tokens.
// Only the outbox receives this small, credential-free frozen projection.
type AutomationUsageSummary struct {
	Available        bool    `json:"available"`
	Reason           string  `json:"reason,omitempty"`
	Scope            string  `json:"scope"`
	ThroughSeq       int64   `json:"throughSeq,omitempty"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheTokens      int64   `json:"cacheTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
	CostKnown        bool    `json:"costKnown"`
	AccountingStatus string  `json:"accountingStatus,omitempty"`
	AccountingSource string  `json:"accountingSource,omitempty"`
}
type AutomationExecutionResult struct {
	Status         string `json:"status"`
	TerminalReason string `json:"terminalReason,omitempty"`
}
type AutomationVerificationResult struct {
	Verdict          *protocol.VerifierVerdict `json:"verdict,omitempty"`
	Conclusion       *protocol.RunConclusion   `json:"conclusion,omitempty"`
	Authority        string                    `json:"authority"`
	Status           string                    `json:"status"`
	Summary          string                    `json:"summary"`
	Checks           []protocol.VerifierCheck  `json:"checks"`
	UnmetObligations []string                  `json:"unmetObligations"`
}

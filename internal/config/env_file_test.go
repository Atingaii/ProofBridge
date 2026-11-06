package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	before, present := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, before)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
func TestExplicitEnvironmentFileLoadAndProcessPrecedence(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "mail.env")
	for _, key := range []string{"SMTP_HOST", "SMTP_FROM", "SMTP_PASSWORD", "SMTP_PORT", "APP_ENV"} {
		unsetEnvForTest(t, key)
	}
	t.Setenv("CODEX_BRIDGE_CONFIG_DIR", root)
	t.Setenv("CODEX_BRIDGE_ENV_FILE", file)
	t.Setenv("SMTP_USERNAME", "process-user")
	raw := "APP_ENV=trial\nSMTP_HOST=smtp.resend.com\nSMTP_PORT=465\nSMTP_TLS=true\nSMTP_STARTTLS=false\nSMTP_USERNAME=file-user\nSMTP_PASSWORD='secret # $literal'\nSMTP_FROM=\"ProofBridge <mail@example.test>\"\n"
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trial.yaml"), []byte("gateway:\n  port: 9021\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Env != "trial" || cfg.Gateway.Port != 9021 || cfg.Automation.SMTP.Host != "smtp.resend.com" || cfg.Automation.SMTP.Port != 465 || cfg.Automation.SMTP.Username != "process-user" || cfg.Automation.SMTP.Password != "secret # $literal" || !cfg.Automation.SMTP.TLS || cfg.Automation.SMTP.StartTLS {
		t.Fatal("explicit env file not applied with process precedence")
	}
	if _, present := os.LookupEnv("SMTP_PASSWORD"); present {
		t.Fatal("file values leaked into global process environment")
	}
	t.Setenv("SMTP_HOST", "")
	cfg, err = Load(root)
	if err != nil || cfg.Automation.SMTP.Host != "" {
		t.Fatal("explicit empty process environment must override file")
	}
}
func TestEnvironmentFileSyntaxIsLiteralAndErrorsDoNotExposeSecrets(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	raw := []byte("\xef\xbb\xbf# comment\r\n export SMTP_HOST=smtp.example.test # comment\r\nSMTP_PASSWORD='p # $HOME'\nSMTP_FROM=\"ProofBridge <mail@example.test>\" # comment\nLITERAL=$(touch " + marker + ")\nQUOTED=\"value\\\\slash\\\"quote\"\n")
	parsed, err := parseEnvironmentFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed["SMTP_PASSWORD"] != "p # $HOME" || parsed["LITERAL"] != "$(touch "+marker+")" || parsed["QUOTED"] != "value\\slash\"quote" {
		t.Fatal("env data was expanded or quoted incorrectly")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("environment file executed shell text")
	}
	for _, bad := range []string{"SMTP_PASSWORD='fixture-private-secret", "bad-key=fixture-private-secret", "SMTP_PASSWORD=\"fixture-private-secret\" trailing", "fixture-private-secret"} {
		_, err := parseEnvironmentFile([]byte(bad))
		if err == nil || strings.Contains(err.Error(), "fixture-private-secret") {
			t.Fatalf("invalid input accepted or leaked its value: %v", err)
		}
	}
}
func TestEnvironmentFileIsExplicitAndMissingSelectionFails(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_BRIDGE_CONFIG_DIR", root)
	t.Setenv("CODEX_BRIDGE_ENV_FILE", "")
	unsetEnvForTest(t, "SMTP_PASSWORD")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SMTP_PASSWORD=must-not-load\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil || cfg.Automation.SMTP.Password != "" {
		t.Fatal("an implicit .env was loaded")
	}
	t.Setenv("CODEX_BRIDGE_ENV_FILE", filepath.Join(root, "missing.env"))
	if _, err = Load(root); err == nil {
		t.Fatal("missing explicitly configured env file ignored")
	}
}

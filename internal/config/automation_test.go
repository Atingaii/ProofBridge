package config

import (
	"testing"
	"time"
)

func TestAutomationDefaultsAndEnvironment(t *testing.T) {
	cfg := Default()
	if !cfg.Automation.Enabled || !cfg.Automation.SMTP.StartTLS || cfg.Automation.SMTP.AllowInsecure || cfg.Automation.SMTP.Timeout.Duration != 15*time.Second {
		t.Fatalf("unsafe or unavailable defaults: %+v", cfg.Automation)
	}
	t.Setenv("AUTOMATION_ENABLED", "false")
	t.Setenv("AUTOMATION_PUBLIC_URL", "https://lab.example.test")
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "fixture-secret")
	t.Setenv("SMTP_FROM", "sender@example.test")
	t.Setenv("SMTP_STARTTLS", "false")
	t.Setenv("SMTP_ALLOW_INSECURE", "true")
	t.Setenv("SMTP_TIMEOUT", "3s")
	applyEnv(&cfg)
	if cfg.Automation.Enabled || cfg.Automation.PublicURL != "https://lab.example.test" || cfg.Automation.SMTP.Host != "127.0.0.1" || cfg.Automation.SMTP.Port != 2525 || cfg.Automation.SMTP.Password != "fixture-secret" || cfg.Automation.SMTP.StartTLS || !cfg.Automation.SMTP.AllowInsecure || cfg.Automation.SMTP.Timeout.Duration != 3*time.Second {
		t.Fatal("automation environment not applied")
	}
}

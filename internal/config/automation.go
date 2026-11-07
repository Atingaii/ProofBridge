package config

import (
	"os"
	"time"
)

func applyAutomationEnv(cfg *Config) { applyAutomationEnvWithLookup(cfg, os.LookupEnv) }

func applyAutomationEnvWithLookup(cfg *Config, lookup envLookup) {
	if v, ok := lookup("AUTOMATION_ENABLED"); ok {
		cfg.Automation.Enabled = parseBool(v, cfg.Automation.Enabled)
	}
	if v, ok := lookup("AUTOMATION_PUBLIC_URL"); ok {
		cfg.Automation.PublicURL = v
	}
	if v, ok := lookup("SMTP_HOST"); ok {
		cfg.Automation.SMTP.Host = v
	}
	if v, ok := lookup("SMTP_DELIVERY_MODE"); ok {
		cfg.Automation.SMTP.DeliveryMode = v
	}
	if v, ok := lookup("SMTP_PORT"); ok {
		cfg.Automation.SMTP.Port = parseInt(v, cfg.Automation.SMTP.Port)
	}
	if v, ok := lookup("SMTP_USERNAME"); ok {
		cfg.Automation.SMTP.Username = v
	}
	if v, ok := lookup("SMTP_PASSWORD"); ok {
		cfg.Automation.SMTP.Password = v
	}
	if v, ok := lookup("SMTP_FROM"); ok {
		cfg.Automation.SMTP.From = v
	}
	if v, ok := lookup("SMTP_STARTTLS"); ok {
		cfg.Automation.SMTP.StartTLS = parseBool(v, cfg.Automation.SMTP.StartTLS)
	}
	if v, ok := lookup("SMTP_TLS"); ok {
		cfg.Automation.SMTP.TLS = parseBool(v, cfg.Automation.SMTP.TLS)
	}
	if v, ok := lookup("SMTP_ALLOW_INSECURE"); ok {
		cfg.Automation.SMTP.AllowInsecure = parseBool(v, cfg.Automation.SMTP.AllowInsecure)
	}
	if v, _ := lookup("SMTP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Automation.SMTP.Timeout = Duration{Duration: d}
		}
	}
}

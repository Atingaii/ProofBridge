package config

import (
	"os"
	"time"
)

func applyAutomationEnv(cfg *Config) {
	if v, ok := os.LookupEnv("AUTOMATION_ENABLED"); ok {
		cfg.Automation.Enabled = parseBool(v, cfg.Automation.Enabled)
	}
	if v, ok := os.LookupEnv("AUTOMATION_PUBLIC_URL"); ok {
		cfg.Automation.PublicURL = v
	}
	if v, ok := os.LookupEnv("SMTP_HOST"); ok {
		cfg.Automation.SMTP.Host = v
	}
	if v, ok := os.LookupEnv("SMTP_PORT"); ok {
		cfg.Automation.SMTP.Port = parseInt(v, cfg.Automation.SMTP.Port)
	}
	if v, ok := os.LookupEnv("SMTP_USERNAME"); ok {
		cfg.Automation.SMTP.Username = v
	}
	if v, ok := os.LookupEnv("SMTP_PASSWORD"); ok {
		cfg.Automation.SMTP.Password = v
	}
	if v, ok := os.LookupEnv("SMTP_FROM"); ok {
		cfg.Automation.SMTP.From = v
	}
	if v, ok := os.LookupEnv("SMTP_STARTTLS"); ok {
		cfg.Automation.SMTP.StartTLS = parseBool(v, cfg.Automation.SMTP.StartTLS)
	}
	if v, ok := os.LookupEnv("SMTP_TLS"); ok {
		cfg.Automation.SMTP.TLS = parseBool(v, cfg.Automation.SMTP.TLS)
	}
	if v, ok := os.LookupEnv("SMTP_ALLOW_INSECURE"); ok {
		cfg.Automation.SMTP.AllowInsecure = parseBool(v, cfg.Automation.SMTP.AllowInsecure)
	}
	if v := os.Getenv("SMTP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Automation.SMTP.Timeout = Duration{Duration: d}
		}
	}
}

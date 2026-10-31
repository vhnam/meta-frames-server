package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
)

type Config struct {
	Port        string
	DatabaseURL string
	CORSOrigins []string
	StorageDir  string
	Production  bool
	DBMaxConns  int32 // 0 keeps the pgx default

	// Accounts (internal/auth).
	SessionSameSite string // lax (default), strict or none
	AppURL          string // front-end root, used for links in emails and after Google sign-in
	APIURL          string // this server's public root, for Google's redirect back
	MailFrom        string
	SMTPAddr        string // host:port; empty prints emails to stdout instead of sending them
	SMTPUsername    string
	SMTPPassword    string
	// Sign in with Google; disabled unless both are set.
	GoogleClientID     string
	GoogleClientSecret string
}

// Load reads the settings from the environment. It reports every invalid value at once.
func Load() (Config, error) {
	var problems []error
	config := Config{
		Port:        valueOrDefault("PORT", "8080"),
		DatabaseURL: valueOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/metaframes?sslmode=disable"),
		CORSOrigins: splitList(valueOrDefault("CORS_ORIGINS", "http://localhost:5173")),
		StorageDir:  valueOrDefault("STORAGE_DIR", "data/scans"),
		Production:  os.Getenv("ENV") == "production",

		SessionSameSite: strings.ToLower(os.Getenv("SESSION_SAMESITE")),
		AppURL:          valueOrDefault("APP_URL", "http://localhost:5173"),
		APIURL:          valueOrDefault("API_URL", "http://localhost:8080"),
		MailFrom:        valueOrDefault("MAIL_FROM", "no-reply@localhost"),
		SMTPAddr:        os.Getenv("SMTP_ADDR"),
		SMTPUsername:    os.Getenv("SMTP_USERNAME"),
		SMTPPassword:    os.Getenv("SMTP_PASSWORD"),

		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
	}

	maxConns, err := nonNegativeInt32("DB_MAX_CONNS")
	config.DBMaxConns = maxConns
	problems = append(problems, err)

	if port, err := strconv.Atoi(config.Port); err != nil || port < 1 || port > 65535 {
		problems = append(problems, fmt.Errorf("PORT must be a number from 1 to 65535, got %q", config.Port))
	}
	problems = append(problems,
		checkURL("DATABASE_URL", config.DatabaseURL, "postgres", "postgresql"),
		checkURL("APP_URL", config.AppURL, "http", "https"),
		checkURL("API_URL", config.APIURL, "http", "https"),
	)
	for _, origin := range config.CORSOrigins {
		problems = append(problems, checkURL("CORS_ORIGINS", origin, "http", "https"))
	}
	switch config.SessionSameSite {
	case "", "lax", "strict", "none":
	default:
		problems = append(problems, fmt.Errorf("SESSION_SAMESITE must be lax, strict or none, got %q", config.SessionSameSite))
	}
	if _, err := mail.ParseAddress(config.MailFrom); err != nil {
		problems = append(problems, fmt.Errorf("MAIL_FROM must be an email address, got %q", config.MailFrom))
	}
	if config.SMTPAddr != "" {
		if _, _, err := net.SplitHostPort(config.SMTPAddr); err != nil {
			problems = append(problems, fmt.Errorf("SMTP_ADDR must be host:port, got %q", config.SMTPAddr))
		}
	}
	if (config.GoogleClientID == "") != (config.GoogleClientSecret == "") {
		problems = append(problems, errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together"))
	}

	if err := errors.Join(problems...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return config, nil
}

// nonNegativeInt32 reads an optional setting; unset gives 0, which keeps the default.
func nonNegativeInt32(name string) (int32, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got %q", name, raw)
	}
	return int32(value), nil
}

func checkURL(name, raw string, schemes ...string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || !slices.Contains(schemes, parsed.Scheme) {
		return fmt.Errorf("%s must be a %s URL, got %q", name, strings.Join(schemes, " or "), redact(raw))
	}
	return nil
}

// redact hides a password embedded in a URL so errors can be logged.
func redact(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && parsed.User != nil {
		return parsed.Redacted()
	}
	return raw
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// splitList splits a comma-separated list, dropping blanks.
func splitList(list string) []string {
	var items []string
	for _, item := range strings.Split(list, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

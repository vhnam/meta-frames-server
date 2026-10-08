package config

import (
	"os"
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

func Load() Config {
	return Config{
		Port:        valueOrDefault("PORT", "8080"),
		DatabaseURL: valueOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/metaframes?sslmode=disable"),
		CORSOrigins: splitList(valueOrDefault("CORS_ORIGINS", "http://localhost:5173")),
		StorageDir:  valueOrDefault("STORAGE_DIR", "data/scans"),
		Production:  os.Getenv("ENV") == "production",
		DBMaxConns:  intOrZero("DB_MAX_CONNS"),

		SessionSameSite: os.Getenv("SESSION_SAMESITE"),
		AppURL:          valueOrDefault("APP_URL", "http://localhost:5173"),
		APIURL:          valueOrDefault("API_URL", "http://localhost:8080"),
		MailFrom:        valueOrDefault("MAIL_FROM", "no-reply@localhost"),
		SMTPAddr:        os.Getenv("SMTP_ADDR"),
		SMTPUsername:    os.Getenv("SMTP_USERNAME"),
		SMTPPassword:    os.Getenv("SMTP_PASSWORD"),

		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
	}
}

func intOrZero(name string) int32 {
	value, err := strconv.ParseInt(os.Getenv(name), 10, 32)
	if err != nil || value < 0 {
		return 0
	}
	return int32(value)
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

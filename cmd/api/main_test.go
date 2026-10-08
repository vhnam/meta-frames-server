package main

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"meta-frames-server/internal/config"
)

func TestNewLoggerUsesJSONInProduction(test *testing.T) {
	if _, isJSON := newLogger(true).Handler().(*slog.JSONHandler); !isJSON {
		test.Fatal("production must log JSON")
	}
	if _, isText := newLogger(false).Handler().(*slog.TextHandler); !isText {
		test.Fatal("development must log text")
	}
	if !newLogger(false).Enabled(context.Background(), slog.LevelInfo) {
		test.Fatal("info logs must be enabled")
	}
}

func TestParseSameSite(test *testing.T) {
	for value, want := range map[string]http.SameSite{"": http.SameSiteLaxMode, "Lax": http.SameSiteLaxMode, "strict": http.SameSiteStrictMode, "none": http.SameSiteNoneMode} {
		if got, err := parseSameSite(value); err != nil || got != want {
			test.Fatalf("parseSameSite(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	if _, err := parseSameSite("sometimes"); err == nil {
		test.Fatal("an unknown SameSite mode must fail")
	}
}

func TestMailerFallsBackToStdout(test *testing.T) {
	if newMailer(config.Config{}) != nil {
		test.Fatal("without SMTP_ADDR emails go to stdout (nil mailer)")
	}
	if newMailer(config.Config{SMTPAddr: "smtp.example:587", SMTPUsername: "user"}) == nil {
		test.Fatal("SMTP_ADDR must give an SMTP mailer")
	}
}

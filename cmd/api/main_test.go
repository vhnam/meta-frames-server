package main

import (
	"context"
	"log/slog"
	"testing"
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

package config

import (
	"reflect"
	"testing"
)

func TestLoadUsesDefaults(test *testing.T) {
	for _, name := range []string{"PORT", "DATABASE_URL", "CORS_ORIGINS", "STORAGE_DIR", "ENV", "SESSION_SAMESITE", "APP_URL", "SMTP_ADDR"} {
		test.Setenv(name, "")
	}
	loaded := Load()

	if loaded.Port != "8080" || loaded.StorageDir != "data/scans" || loaded.Production {
		test.Fatalf("defaults = %+v", loaded)
	}
	if loaded.DatabaseURL == "" || !reflect.DeepEqual(loaded.CORSOrigins, []string{"http://localhost:5173"}) {
		test.Fatalf("defaults = %+v", loaded)
	}
	if loaded.SessionSameSite != "" || loaded.AppURL != "http://localhost:5173" || loaded.SMTPAddr != "" {
		test.Fatalf("defaults = %+v", loaded)
	}
}

func TestLoadReadsTheEnvironment(test *testing.T) {
	test.Setenv("PORT", "9000")
	test.Setenv("DATABASE_URL", "postgres://example")
	test.Setenv("CORS_ORIGINS", "https://a.example, https://b.example ,, ")
	test.Setenv("STORAGE_DIR", "/var/scans")
	test.Setenv("ENV", "production")
	test.Setenv("SESSION_SAMESITE", "none")
	test.Setenv("APP_URL", "https://app.example")
	test.Setenv("SMTP_ADDR", "smtp.example:587")
	loaded := Load()

	if loaded.Port != "9000" || loaded.DatabaseURL != "postgres://example" || loaded.StorageDir != "/var/scans" || !loaded.Production {
		test.Fatalf("loaded = %+v", loaded)
	}
	if loaded.SessionSameSite != "none" || loaded.AppURL != "https://app.example" || loaded.SMTPAddr != "smtp.example:587" {
		test.Fatalf("loaded = %+v", loaded)
	}
	if want := []string{"https://a.example", "https://b.example"}; !reflect.DeepEqual(loaded.CORSOrigins, want) {
		test.Fatalf("CORSOrigins = %v, want %v", loaded.CORSOrigins, want)
	}
}

package config

import (
	"reflect"
	"testing"
)

func TestLoadUsesDefaults(test *testing.T) {
	for _, name := range []string{"PORT", "DATABASE_URL", "CORS_ORIGINS", "STORAGE_DIR", "ENV"} {
		test.Setenv(name, "")
	}
	loaded := Load()

	if loaded.Port != "8080" || loaded.StorageDir != "data/scans" || loaded.Production {
		test.Fatalf("defaults = %+v", loaded)
	}
	if loaded.DatabaseURL == "" || !reflect.DeepEqual(loaded.CORSOrigins, []string{"http://localhost:5173"}) {
		test.Fatalf("defaults = %+v", loaded)
	}
}

func TestLoadReadsTheEnvironment(test *testing.T) {
	test.Setenv("PORT", "9000")
	test.Setenv("DATABASE_URL", "postgres://example")
	test.Setenv("CORS_ORIGINS", "https://a.example, https://b.example ,, ")
	test.Setenv("STORAGE_DIR", "/var/scans")
	test.Setenv("ENV", "production")
	loaded := Load()

	if loaded.Port != "9000" || loaded.DatabaseURL != "postgres://example" || loaded.StorageDir != "/var/scans" || !loaded.Production {
		test.Fatalf("loaded = %+v", loaded)
	}
	if want := []string{"https://a.example", "https://b.example"}; !reflect.DeepEqual(loaded.CORSOrigins, want) {
		test.Fatalf("CORSOrigins = %v, want %v", loaded.CORSOrigins, want)
	}
}

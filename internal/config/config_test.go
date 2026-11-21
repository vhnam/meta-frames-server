package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadUsesDefaults(test *testing.T) {
	for _, name := range []string{"PORT", "DATABASE_URL", "CORS_ORIGINS", "STORAGE_DIR", "ENV", "SESSION_SAMESITE", "APP_URL", "SMTP_ADDR", "DB_MAX_CONNS", "API_URL", "MAIL_FROM", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"} {
		test.Setenv(name, "")
	}
	loaded, err := Load()
	if err != nil {
		test.Fatal(err)
	}

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
	loaded, err := Load()
	if err != nil {
		test.Fatal(err)
	}

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

func TestLoadRejectsInvalidValues(test *testing.T) {
	cases := map[string]string{
		"DB_MAX_CONNS":     "abc",
		"PORT":             "70000",
		"DATABASE_URL":     "localhost:5432/db",
		"APP_URL":          "not a url",
		"CORS_ORIGINS":     "ftp://example.com",
		"SESSION_SAMESITE": "sometimes",
		"MAIL_FROM":        "nobody",
		"SMTP_ADDR":        "smtp.example",
		"GOOGLE_CLIENT_ID": "id-only",
	}
	for name, value := range cases {
		test.Run(name, func(test *testing.T) {
			test.Setenv(name, value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				test.Fatalf("Load() error = %v, want it to name %s", err, name)
			}
		})
	}
}

func TestLoadReportsEveryProblemAndHidesPasswords(test *testing.T) {
	test.Setenv("PORT", "x")
	test.Setenv("DATABASE_URL", "mysql://user:secret@host/db")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "PORT") || !strings.Contains(err.Error(), "DATABASE_URL") {
		test.Fatalf("Load() error = %v, want both settings", err)
	}
	if strings.Contains(err.Error(), "secret") {
		test.Fatalf("error leaks the password: %v", err)
	}
}

func TestLoadCollectsMobileClientIDs(test *testing.T) {
	cases := map[string]struct {
		android, ios, legacy string
		want                 []string
	}{
		"android only":          {android: "android-client", want: []string{"android-client"}},
		"ios only":              {ios: " ios-client ", want: []string{"ios-client"}},
		"both":                  {android: "android-client", ios: "ios-client", want: []string{"android-client", "ios-client"}},
		"blank":                 {android: "  ", ios: "", want: nil},
		"removed variable only": {legacy: "old-client", want: nil},
		"legacy with current":   {android: " android-client ", legacy: "old-client", want: []string{"android-client"}},
		"comma in platform ID":  {ios: " ios-one,ios-two ", want: []string{"ios-one,ios-two"}},
	}
	for name, testCase := range cases {
		test.Run(name, func(test *testing.T) {
			test.Setenv("GOOGLE_ANDROID_CLIENT_ID", testCase.android)
			test.Setenv("GOOGLE_IOS_CLIENT_ID", testCase.ios)
			test.Setenv("GOOGLE_MOBILE_CLIENT_IDS", testCase.legacy)
			loaded, err := Load()
			if err != nil {
				test.Fatal(err)
			}
			if !reflect.DeepEqual(loaded.GoogleMobileClientIDs, testCase.want) {
				test.Fatalf("GoogleMobileClientIDs = %q, want %q", loaded.GoogleMobileClientIDs, testCase.want)
			}
		})
	}
}

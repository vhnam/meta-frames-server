package main

import (
	"context"
	"log/slog"
	"net"
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

func TestServerAcceptsHTTP1AndUnencryptedHTTP2(test *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	server := newServer(listener.Addr().String(), http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	go func() { _ = server.Serve(listener) }()
	test.Cleanup(func() { _ = server.Close() })

	http1 := new(http.Protocols)
	http1.SetHTTP1(true)
	unencryptedHTTP2 := new(http.Protocols)
	unencryptedHTTP2.SetUnencryptedHTTP2(true)

	for want, protocols := range map[string]*http.Protocols{"HTTP/1.1": http1, "HTTP/2.0": unencryptedHTTP2} {
		transport := &http.Transport{Protocols: protocols}
		test.Cleanup(transport.CloseIdleConnections)
		response, err := (&http.Client{Transport: transport}).Get("http://" + listener.Addr().String())
		if err != nil {
			test.Fatalf("%s request: %v", want, err)
		}
		_ = response.Body.Close()
		if response.Proto != want || response.StatusCode != http.StatusNoContent {
			test.Fatalf("response = %s %d, want %s %d", response.Proto, response.StatusCode, want, http.StatusNoContent)
		}
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

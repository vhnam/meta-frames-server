package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aarondl/authboss/v3"
	"github.com/aarondl/authboss/v3/defaults"
	"github.com/jackc/pgx/v5/pgxpool"

	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/config"
	"meta-frames-server/internal/controllers"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/server"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/storage"
)

func main() {
	cfg := config.Load()
	logger := newLogger(cfg.Production)
	slog.SetDefault(logger)

	if err := run(cfg); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func newLogger(production bool) *slog.Logger {
	if production {
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	db.ConfigurePool(poolCfg)
	if cfg.DBMaxConns > 0 {
		poolCfg.MaxConns = cfg.DBMaxConns
	}
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(poolCfg.ConnConfig); err != nil {
		return err
	}

	files, err := storage.NewLocal(cfg.StorageDir)
	if err != nil {
		return err
	}

	store := db.NewPostgresStore(pool)
	accounts, err := newAccounts(cfg, store)
	if err != nil {
		return err
	}

	appServices := services.New(store, files, clock.System{})
	handler, err := server.NewHandler(controllers.New(appServices), accounts, cfg.CORSOrigins)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second, // scan uploads are multipart, so keep this generous
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func newAccounts(cfg config.Config, store db.Store) (*auth.Auth, error) {
	sameSite, err := parseSameSite(cfg.SessionSameSite)
	if err != nil {
		return nil, err
	}
	return auth.New(store, auth.Config{
		// Browsers drop SameSite=None cookies that are not Secure.
		SecureCookie: cfg.Production || sameSite == http.SameSiteNoneMode,
		SameSite:     sameSite,
		AppURL:       cfg.AppURL,
		APIURL:       cfg.APIURL,
		MailFrom:     cfg.MailFrom,
		Mailer:       newMailer(cfg),
		Google:       auth.GoogleConfig{ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret},
	})
}

// parseSameSite reads SESSION_SAMESITE. Lax (the default) suits a front end on the same site as
// the API; a front end on another site needs none, which then relies on CORS_ORIGINS against CSRF.
func parseSameSite(value string) (http.SameSite, error) {
	switch strings.ToLower(value) {
	case "", "lax":
		return http.SameSiteLaxMode, nil
	case "strict":
		return http.SameSiteStrictMode, nil
	case "none":
		return http.SameSiteNoneMode, nil
	}
	return 0, fmt.Errorf("SESSION_SAMESITE must be lax, strict or none, got %q", value)
}

// newMailer sends through SMTP_ADDR, or returns nil so emails are printed to stdout.
func newMailer(cfg config.Config) authboss.Mailer {
	if cfg.SMTPAddr == "" {
		if cfg.Production {
			slog.Warn("SMTP_ADDR is not set; password-recovery emails are only printed to stdout")
		}
		return nil
	}
	var smtpAuth smtp.Auth
	if cfg.SMTPUsername != "" {
		host, _, _ := net.SplitHostPort(cfg.SMTPAddr)
		smtpAuth = smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, host)
	}
	return auth.AsyncMailer{Mailer: defaults.NewSMTPMailer(cfg.SMTPAddr, smtpAuth)}
}

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

	appServices := services.New(db.NewPostgresStore(pool), files, clock.System{})
	handler, err := server.NewHandler(controllers.New(appServices), cfg.CORSOrigins)
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

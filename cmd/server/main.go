package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"repair-request/internal/app"
	"repair-request/internal/config"
	"repair-request/internal/database"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	for i := 1; i < 11; i++ {
		if err = database.Migrate(ctx, pool); err == nil {
			break
		}
		slog.Info("database migration failed", "error", err)
		slog.Info("retry", "count", i)
		time.Sleep(time.Second)
	}
	if err != nil {
		slog.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	if cfg.DevSeedUsers {
		if err := database.SeedUsers(ctx, pool, cfg.BcryptCost); err != nil {
			slog.Error("seed users failed", "error", err)
			os.Exit(1)
		}
	}

	application, err := app.New(cfg, pool)
	if err != nil {
		slog.Error("app init failed", "error", err)
		os.Exit(1)
	}
	application.CleanupExpiredSessions(context.Background())

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("server started", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}

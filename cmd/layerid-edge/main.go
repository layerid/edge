// Command layerid-edge runs the scoring engine.
//
// Configuration is via env (12-factor). See README for the full set.
//
// Phase: scaffold. Real intel-service upstream client, Postgres consume
// path, and JWT auth all land in subsequent commits — see ../../DESIGN.md.
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

	"github.com/layerid/edge/internal/api"
	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/score/constscorer"
	"github.com/layerid/edge/internal/score/weighted"
)

const (
	defaultListenAddr     = ":8080"
	defaultDefaultScorer  = "weighted"
	buildVersion          = "0.1.0-scaffold"
	gracefulShutdownGrace = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	addr := envOr("LAYERID_EDGE_LISTEN", defaultListenAddr)
	defaultScorer := envOr("LAYERID_EDGE_DEFAULT_SCORER", defaultDefaultScorer)

	reg := score.NewRegistry()
	mustRegister(reg, weighted.New())
	mustRegister(reg, constscorer.Allow())
	mustRegister(reg, constscorer.Deny())
	mustRegister(reg, constscorer.StepUp())

	logger.Info("scorers registered",
		"names", reg.Names(),
		"default", defaultScorer,
	)

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(reg, defaultScorer, buildVersion).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Run server, capture errors on a channel for the shutdown select.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("layerid-edge listening", "addr", addr, "version", buildVersion)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Wait for signal or server error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("signal received, shutting down", "signal", sig)
	case err := <-errCh:
		logger.Error("server error", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gracefulShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustRegister(r *score.Registry, s score.Scorer) {
	if err := r.Register(s); err != nil {
		slog.Error("scorer registration failed", "name", s.Name(), "err", err)
		os.Exit(1)
	}
}

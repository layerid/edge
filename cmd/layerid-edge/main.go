// Command layerid-edge runs the scoring engine.
//
// Configuration via env vars (12-factor):
//
//   LAYERID_EDGE_LISTEN          (default ":8080")
//   LAYERID_EDGE_DEFAULT_SCORER  (default "weighted")
//   LAYERID_EDGE_REGION          (default "dev")
//   LAYERID_EDGE_AUTH_DISABLED   (default "" — set to "1" for local dev)
//
// Phase: in-memory store, no Postgres wiring yet, no JWKS fetch. See
// DESIGN.md for the migration plan.
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
	"github.com/layerid/edge/internal/auth"
	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/score/constscorer"
	"github.com/layerid/edge/internal/score/weighted"
	"github.com/layerid/edge/internal/store/memory"
)

const (
	defaultListenAddr    = ":8080"
	defaultDefaultScorer = "weighted"
	defaultRegion        = "dev"
	buildVersion         = "0.2.0-scaffold"
	gracefulShutdown     = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	addr := envOr("LAYERID_EDGE_LISTEN", defaultListenAddr)
	defaultScorer := envOr("LAYERID_EDGE_DEFAULT_SCORER", defaultDefaultScorer)
	region := envOr("LAYERID_EDGE_REGION", defaultRegion)
	authDisabled := os.Getenv("LAYERID_EDGE_AUTH_DISABLED") == "1"

	reg := score.NewRegistry()
	mustRegister(reg, weighted.New())
	mustRegister(reg, constscorer.Allow())
	mustRegister(reg, constscorer.Deny())
	mustRegister(reg, constscorer.StepUp())

	logger.Info("scorers registered",
		"names", reg.Names(),
		"default", defaultScorer,
	)

	st := memory.New(time.Now)
	logger.Warn("using in-memory store — set up Postgres before production",
		"package", "internal/store/memory",
	)

	// Auth middleware: for now, no JWKS configured. When auth.layerid.io
	// is up, swap StaticKeys for a JWKS client. In auth-disabled mode we
	// inject synthetic claims (tenant_0) so handlers see a valid context.
	var authMW func(http.Handler) http.Handler
	if !authDisabled {
		logger.Warn("auth middleware enabled with empty key resolver — set up JWKS before production")
		authMW = auth.Middleware(auth.VerifyOptions{
			Keys: auth.StaticKeys{},
		})
	} else {
		logger.Warn("LAYERID_EDGE_AUTH_DISABLED=1 — injecting synthetic tenant_0 claims; dev only")
		authMW = auth.DevPassthrough()
	}

	srv := &http.Server{
		Addr: addr,
		Handler: api.New(api.Options{
			Registry:       reg,
			Store:          st,
			DefaultScorer:  defaultScorer,
			Region:         region,
			Version:        buildVersion,
			AuthMiddleware: authMW,
		}).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("layerid-edge listening",
			"addr", addr,
			"version", buildVersion,
			"region", region,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("signal received, shutting down", "signal", sig)
	case err := <-errCh:
		logger.Error("server error", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gracefulShutdown)
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

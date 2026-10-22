// Command payahub is the single backend binary:
//
//	payahub migrate   apply database migrations (idempotent)
//	payahub api       serve the /v1 API (inserts jobs, works none)
//	payahub worker    work submission jobs
//	payahub dev       migrate, seed the sandbox key, then api + worker in one process
//
// Configuration is by environment: DATABASE_URL, PAYAHUB_ADDR (default :8080),
// PAYAHUB_WORKERS (concurrent submissions per worker process, default 100).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/harrisyn/payahub/internal/api"
	_ "github.com/harrisyn/payahub/internal/connector/all"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
)

const sandboxKey = "sk_sandbox_dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: payahub migrate|api|worker|dev")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1], logger); err != nil {
		logger.Error("exit", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, cmd string, logger *slog.Logger) error {
	st, err := store.Open(ctx, env("DATABASE_URL", "postgres://localhost:5432/payahub"))
	if err != nil {
		return err
	}
	defer st.Close()

	workers, _ := strconv.Atoi(env("PAYAHUB_WORKERS", "100"))
	switch cmd {
	case "migrate":
		return st.Migrate(ctx)
	case "api":
		return serve(ctx, st, 0, logger)
	case "worker":
		return work(ctx, st, workers, logger)
	case "dev":
		if err := st.Migrate(ctx); err != nil {
			return err
		}
		if err := st.EnsureOrgWithKey(ctx, "org_sandbox", "Sandbox organisation", "sandbox", sandboxKey); err != nil {
			return err
		}
		logger.Info("sandbox ready", "api_key", sandboxKey)
		return serve(ctx, st, workers, logger)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func work(ctx context.Context, st *store.Store, workers int, logger *slog.Logger) error {
	rc, err := queue.NewClient(st, workers, logger)
	if err != nil {
		return err
	}
	if err := rc.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	// Finish in-flight submissions; anything unfinished is retried by the next worker.
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return rc.Stop(stopCtx)
}

func serve(ctx context.Context, st *store.Store, workers int, logger *slog.Logger) error {
	rc, err := queue.NewClient(st, workers, logger)
	if err != nil {
		return err
	}
	if workers > 0 {
		if err := rc.Start(ctx); err != nil {
			return err
		}
	}
	srv := &http.Server{
		Addr:              env("PAYAHUB_ADDR", ":8080"),
		Handler:           (&api.Server{Store: st, Queue: rc, Logger: logger}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Info("listening", "addr", srv.Addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdown)
	if workers > 0 {
		err = errors.Join(err, rc.Stop(shutdown))
	}
	return err
}

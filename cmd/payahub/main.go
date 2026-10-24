// Command payahub is the single backend binary:
//
//	payahub migrate   apply database migrations (idempotent)
//	payahub api       serve the /v1 API (inserts jobs, works none)
//	payahub worker    work submission jobs
//	payahub dev       migrate, seed the sandbox key, then api + worker in one process
//	payahub operator EMAIL [off]  let a console user edit the shared catalogue (or stop them)
//
// Configuration is by environment: DATABASE_URL, PAYAHUB_ADDR (default :8080),
// PAYAHUB_WORKERS (concurrent submissions per worker process, default 100),
// PAYAHUB_VAULT_KEY (base64 32-byte key that seals payer credentials; required
// for api and worker, generated per run by dev), PAYAHUB_PAYSTACK_SECRET_KEY
// (offers Paystack for wallet funding and withdrawals) and
// PAYAHUB_PAYMENTS_SANDBOX=1 (offers the sandbox payment provider; always on
// in dev).
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
	"github.com/harrisyn/payahub/internal/mail"
	"github.com/harrisyn/payahub/internal/payment"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

const sandboxKey = "sk_sandbox_dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: payahub migrate|api|worker|dev|operator")
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
	if cmd == "migrate" {
		return st.Migrate(ctx)
	}
	if cmd == "operator" {
		if len(os.Args) < 3 {
			return errors.New("usage: payahub operator EMAIL [off]")
		}
		on := !(len(os.Args) > 3 && os.Args[3] == "off")
		if err := st.SetOperator(ctx, os.Args[2], on); err != nil {
			return fmt.Errorf("%s: %w", os.Args[2], err)
		}
		logger.Info("operator updated", "email", os.Args[2], "operator", on)
		return nil
	}
	key := os.Getenv("PAYAHUB_VAULT_KEY")
	if key == "" && cmd == "dev" {
		key = vault.NewKey()
		logger.Warn("PAYAHUB_VAULT_KEY not set: using a key for this run only; saved payer credentials will not open after a restart")
	}
	v, err := vault.New(key)
	if err != nil {
		return fmt.Errorf("PAYAHUB_VAULT_KEY: %w", err)
	}
	// The shared catalogue lives in the database; every process loads it now
	// and picks up operators' changes within a few seconds.
	if err := st.RefreshCatalog(ctx); err != nil && cmd != "dev" {
		return fmt.Errorf("loading the catalogue (run payahub migrate first): %w", err)
	}
	if k := os.Getenv("PAYAHUB_PAYSTACK_SECRET_KEY"); k != "" {
		payment.Register(payment.NewPaystack(k))
	}
	if cmd == "dev" || os.Getenv("PAYAHUB_PAYMENTS_SANDBOX") == "1" {
		payment.Register(&payment.Sandbox{Amount: st.SandboxAmount})
	}
	mailer, err := mailerFrom(logger)
	if err != nil {
		return err
	}
	go st.WatchCatalog(ctx, 5*time.Second, func(err error) { logger.Warn("catalogue refresh failed", "err", err) })
	switch cmd {
	case "api":
		return serve(ctx, st, v, 0, logger, mailer)
	case "worker":
		return work(ctx, st, v, workers, logger, mailer)
	case "dev":
		if err := st.Migrate(ctx); err != nil {
			return err
		}
		if err := st.RefreshCatalog(ctx); err != nil {
			return err
		}
		if err := st.EnsureOrgWithKey(ctx, "org_sandbox", "Sandbox organisation", "sandbox", sandboxKey); err != nil {
			return err
		}
		logger.Info("sandbox ready", "api_key", sandboxKey)
		return serve(ctx, st, v, workers, logger, mailer)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// mailerFrom sends through PAYAHUB_SMTP_URL (smtp://user:password@host:587)
// from PAYAHUB_MAIL_FROM; without them, email is only logged.
func mailerFrom(logger *slog.Logger) (mail.Sender, error) {
	raw := os.Getenv("PAYAHUB_SMTP_URL")
	if raw == "" {
		return mail.Log{Logger: logger}, nil
	}
	m, err := mail.FromURL(raw, env("PAYAHUB_MAIL_FROM", "no-reply@payahub.com"))
	if err != nil {
		return nil, fmt.Errorf("PAYAHUB_SMTP_URL: %w", err)
	}
	return m, nil
}

func work(ctx context.Context, st *store.Store, v *vault.Vault, workers int, logger *slog.Logger, mailer mail.Sender) error {
	rc, err := queue.NewClient(st, v, workers, logger, mailer)
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

func serve(ctx context.Context, st *store.Store, v *vault.Vault, workers int, logger *slog.Logger, mailer mail.Sender) error {
	rc, err := queue.NewClient(st, v, workers, logger, mailer)
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
		Handler:           (&api.Server{Store: st, Queue: rc, Vault: v, Logger: logger, ConsoleURL: os.Getenv("PAYAHUB_CONSOLE_URL")}).Handler(),
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

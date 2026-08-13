// Command api serves the Overlap HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embeds the IANA timezone database in the binary. The production image is
	// built FROM scratch and has no /usr/share/zoneinfo, and every slot in this
	// product is derived from a named zone. Relying on the base image to supply
	// one is a dependency that fails silently and totally.
	_ "time/tzdata"

	"github.com/maazin/Overlap/api/internal/server"
	"github.com/maazin/Overlap/api/internal/store"
	"github.com/maazin/Overlap/api/internal/tz"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// run owns the whole lifecycle so that every exit path returns an error rather
// than calling os.Exit from deep inside the program, which would skip deferred
// cleanup.
func run(logger *slog.Logger) error {
	// NotifyContext cancels ctx on SIGINT/SIGTERM. Fly.io sends SIGTERM on
	// deploy, so this is what makes a rolling restart not drop live requests.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := server.ConfigFromEnv()

	// Checked before anything else because a missing zone database makes every
	// event unschedulable, and the resulting errors point at the request rather
	// than at the real cause.
	if err := tz.Available(); err != nil {
		return err
	}

	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is not set")
	}

	// Connecting before listening means a bad database URL is a startup
	// failure the deploy will show, not a 500 on the first real request.
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect store: %w", err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: server.New(cfg, logger, st).Routes(),

		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		// WriteTimeout is deliberately left at zero. Phase 5 adds an SSE
		// endpoint that holds a response open indefinitely, and a write
		// deadline would sever it. Per-handler deadlines cover the rest.
		WriteTimeout: 0,

		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	// The listener error travels back on a channel so the select below can wait
	// on "server died" and "signal received" at the same time.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining")
	}

	// Shutdown gets its own context: ctx is already cancelled by this point, so
	// reusing it would abort the drain immediately.
	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(drainCtx); err != nil {
		return err
	}
	logger.Info("shutdown complete")
	return nil
}

// Command migrate applies pending migrations and exits.
//
// The API already migrates at startup, so this is not needed for a normal
// deploy. It exists for the two cases where booting a server to move a schema
// is the wrong shape: CI, which needs the schema in place before the
// integration tests run, and an operator who wants to apply a migration and
// look at the result before any traffic arrives.
//
// It shares internal/migrate with the server rather than reimplementing
// anything, so what runs here is what runs in production.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "time/tzdata"

	"github.com/maazin/Overlap/api/internal/migrate"
	"github.com/maazin/Overlap/api/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := server.ConfigFromEnv()
	if cfg.DatabaseURL == "" {
		logger.Error("DATABASE_URL is not set")
		os.Exit(1)
	}

	if err := migrate.Up(ctx, cfg.DatabaseURL, logger); err != nil {
		// A cancelled run is an operator pressing ctrl-c, which does not
		// deserve the same stack of noise as a broken migration.
		if errors.Is(err, context.Canceled) {
			logger.Info("migration cancelled")
			os.Exit(130)
		}
		logger.Error("migration failed", "err", err)
		os.Exit(1)
	}
}

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/maazin/Overlap/api/internal/store"
)

// startPurge runs the expiry sweep on a ticker until ctx ends.
//
// The sweep is what makes `expires_at` mean something. Without it the column
// is decorative: the README promises the data goes away when the link expires,
// and nothing was enforcing that.
//
// It returns a stop function that ends the sweeper and blocks until it has
// stopped, so shutdown waits for an in-flight delete rather than tearing the
// pool out from under it.
//
// Stop cancels rather than only waiting. Not every exit from run() goes
// through a cancelled ctx: a failed ListenAndServe returns while the signal
// context is still live, and a wait that only watched ctx would turn that
// error into a hang.
func startPurge(ctx context.Context, st *store.Store, every time.Duration, log *slog.Logger) func() {
	if every <= 0 {
		log.Info("expiry sweep disabled")
		return func() {}
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)

		// One sweep at startup, before the first tick. A machine that restarts
		// more often than the interval would otherwise never sweep at all.
		sweep(ctx, st, log)

		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep(ctx, st, log)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// sweepTimeout bounds one pass. A sweep that cannot finish inside this is
// either stuck or facing a backlog far larger than expected; either way the
// next tick gets a fresh attempt rather than two passes overlapping.
const sweepTimeout = 2 * time.Minute

func sweep(ctx context.Context, st *store.Store, log *slog.Logger) {
	sweepCtx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	n, err := st.PurgeExpired(sweepCtx)
	if err != nil {
		// Worth a log and nothing more. A failed sweep costs disk space until
		// the next tick, which is not a reason to take the process down.
		log.Error("expiry sweep failed", "deleted", n, "err", err)
		return
	}
	if n > 0 {
		log.Info("expired events purged", "deleted", n)
	}
}

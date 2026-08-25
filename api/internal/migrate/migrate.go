// Package migrate brings a database up to the schema the binary was built
// with.
//
// Running migrations from the API process rather than from a deploy step is a
// deliberate trade. The production image is FROM scratch, so it has no shell
// for a release command to expand DATABASE_URL into and no second binary to
// invoke. More importantly, it removes the failure this project is most likely
// to hit: /api/health is a liveness probe that never touches the database, so a
// deploy against an unmigrated schema reports healthy while every real request
// fails. Migrating before the listener opens turns that into a startup error
// the deploy actually shows.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	// Registers the "pgx" driver for database/sql. goose speaks database/sql
	// and the rest of the API speaks pgxpool; this adapter ships inside the
	// pgx module already, so it costs no new dependency.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/maazin/Overlap/api/migrations"
)

// lockKey is an arbitrary but fixed identifier for the migration advisory
// lock. Any process running this code uses the same number, which is the whole
// point: two machines starting at once must not both try to apply the same
// migration.
const lockKey int64 = 8021957462310

// lockTimeout bounds how long a starting process waits for another one to
// finish migrating. Generous, because the wait is legitimate: the holder is
// doing work this process needs done. Bounded, because waiting forever on a
// lock nobody will release is a hang with no error message.
const lockTimeout = 2 * time.Minute

// Up applies every pending migration, or returns an error explaining why it
// could not.
//
// It is safe to call from several processes at once. The advisory lock
// serialises them, and whichever one arrives second finds nothing to do.
func Up(ctx context.Context, databaseURL string, log *slog.Logger) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database for migrations: %w", err)
	}
	defer db.Close()

	// One connection, held for the duration. Advisory locks belong to a
	// session, so taking the lock on a pooled connection and releasing it on
	// whichever connection came back next would release someone else's lock,
	// or nobody's.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	if _, err := conn.ExecContext(lockCtx, "select pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		// A fresh context: ctx may already be cancelled by the time this runs,
		// and failing to release would leave the next deploy waiting out the
		// full lockTimeout for no reason.
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer releaseCancel()
		if _, err := conn.ExecContext(releaseCtx, "select pg_advisory_unlock($1)", lockKey); err != nil {
			log.Warn("could not release migration lock", "err", err)
		}
	}()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(gooseLogger{log})
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	before, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	after, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if before == after {
		log.Info("schema up to date", "version", after)
	} else {
		log.Info("schema migrated", "from", before, "to", after)
	}
	return nil
}

// gooseLogger routes goose's own output through slog so migration lines land
// in the same JSON stream as everything else rather than on bare stdout.
type gooseLogger struct{ log *slog.Logger }

func (g gooseLogger) Printf(format string, v ...any) {
	g.log.Info("goose: " + line(format, v...))
}

func (g gooseLogger) Fatalf(format string, v ...any) {
	// Deliberately not fatal. goose calls this for errors it considers
	// terminal, but the caller above already turns a failed Up into a returned
	// error, and os.Exit from a library skips every deferred cleanup including
	// the lock release directly above.
	g.log.Error("goose: " + line(format, v...))
}

// line renders a goose message as a single log value. goose writes for a
// terminal and ends most lines with a newline, which inside a JSON log field
// is just an escaped character taking up space.
func line(format string, v ...any) string {
	return strings.TrimRight(fmt.Sprintf(format, v...), "\n")
}

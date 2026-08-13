// Package store is the only place that talks to Postgres. It converts between
// sqlc's generated pgtype values and the domain types the rest of the API uses,
// so no pgtype ever escapes into a handler.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maazin/Overlap/api/internal/dbgen"
)

// Store owns the connection pool.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// New opens the pool and verifies it can reach the database.
//
// Connecting eagerly means a bad DATABASE_URL fails at startup, where it is
// obvious, rather than on the first request that happens to need the database.
func New(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	// Fly's shared-cpu machines and a small Postgres do not benefit from a
	// large pool; the cost of too many connections lands on the database.
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{pool: pool, q: dbgen.New(pool)}, nil
}

// Close releases the pool. It blocks until in-flight queries finish.
func (s *Store) Close() { s.pool.Close() }

// Ping reports whether the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

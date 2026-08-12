package store

import (
	"crypto/rand"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// slugAlphabet is exactly 32 characters, which lets each random byte be masked
// to an index without the modulo bias a non-power-of-two alphabet would incur.
//
// Excluded: 0/O and 1/l, because a slug is read aloud and typed by hand when
// someone copies it out of a group chat badly.
const slugAlphabet = "23456789abcdefghijkmnpqrstuvwxyz"

// slugLength of 8 over a 32-symbol alphabet is 40 bits. At any plausible volume
// for this product the chance of a collision is negligible, and the retry below
// makes the remainder harmless rather than merely unlikely.
const slugLength = 8

// slugAttempts bounds the collision retry. Reaching the limit means something
// is wrong with the random source, not that we were unlucky.
const slugAttempts = 5

func newSlug() (string, error) {
	b := make([]byte, slugLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i, c := range b {
		b[i] = slugAlphabet[c&31]
	}
	return string(b), nil
}

// isUniqueViolation reports whether err is Postgres' unique_violation for the
// named constraint.
//
// The constraint name is checked rather than just the SQLSTATE, so that a
// future unique index on this table cannot quietly turn a real error into a
// retry loop that eventually reports "could not allocate a slug".
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

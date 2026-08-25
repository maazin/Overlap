// Package migrations carries the schema history as embedded files.
//
// The files are the same ones `make migrate` feeds to the goose CLI in
// development. Embedding them rather than shipping a directory means the
// production image, which is FROM scratch and has no filesystem to speak of,
// still has everything it needs to bring an empty database up to date.
package migrations

import "embed"

// FS holds every migration, rooted at this directory.
//
// The pattern deliberately excludes this file: `*.sql` matches the migrations
// and nothing else, so adding Go code here can never end up in the set goose
// tries to run.
//
//go:embed *.sql
var FS embed.FS

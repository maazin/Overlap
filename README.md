# Overlap

> Everyone's free time, in fifteen seconds.
> Connect a calendar or tap three buttons. No grid to paint.

Build plan and rationale live in `OVERLAP-PRD.md`.

## Layout

```
api/                 Go 1.26 HTTP API — deploys to Fly.io
  cmd/api/           main, lifecycle, graceful shutdown
  internal/server/   config, routing, middleware, handlers
  internal/solver/   pure scoring and dominance engine (no IO, no clock)
  migrations/        goose SQL migrations
web/                 SvelteKit 2 / Svelte 5 — deploys to Vercel
docker-compose.yml   local Postgres 16 on :5434
Makefile             every routine command
```

## Prerequisites

Go 1.26+, Node 20+, Docker. `make tools` fetches a pinned `goose` into `./bin`.

## Running locally

```bash
make db-up          # Postgres 16 on :5434, waits until healthy
make api            # API on :8080
make web            # SvelteKit on :5173
```

Then open http://localhost:5173 — it should read `api: ok`.

`make dev` does all three at once. `make help` lists everything.

Postgres is on **5434**, not the usual 5432 or 5433, because both were already
taken on the machine this was set up on. Change it in `docker-compose.yml`,
`Makefile` and `.env.example` together if you want it elsewhere.

## Testing

```bash
make check          # go vet + gofmt check + go test -race
```

The solver is the part worth testing hardest, and it is written to make that
easy: pure functions over plain structs, no database, no clock reads, time
enters only as a parameter.

## Deploying

Both halves deploy independently. Do the API first — the web app needs its URL.

**API → Fly.io**

```bash
cd api
fly apps create overlap-api
fly postgres create --name overlap-db && fly postgres attach overlap-db
fly secrets set ALLOWED_ORIGINS=https://<your-vercel-domain>
fly deploy
```

`fly postgres attach` sets `DATABASE_URL` itself. The image is a 9 MB `scratch`
build; it carries the CA bundle and Go's zoneinfo database explicitly, because
without zoneinfo every `time.LoadLocation` call fails and the entire slot model
breaks in a way that never shows up locally.

**Web → Vercel**

Set `PUBLIC_API_URL` to the Fly URL, then deploy `web/`. The project uses
`@sveltejs/adapter-auto`, which resolves to the Vercel adapter during a Vercel
build; pin `@sveltejs/adapter-vercel` if you would rather not rely on detection.

`ALLOWED_ORIGINS` on the API must list the Vercel origin exactly, scheme
included, or the browser fetch fails CORS while curl keeps working.

## Conventions

- Every timestamp is `timestamptz`. No naive datetimes in any layer.
- The solver stays pure: no DB access, no clock reads, no globals in scoring.
- Calendar data is free/busy only. Event details are never fetched, stored or
  logged. This is a stated privacy commitment, so treat it as a code invariant.
- Calendar-derived responses carry `source = 'calendar'` and stay overridable.

# Overlap

> Everyone's free time, in fifteen seconds.
> Connect a calendar or tap three buttons. No grid to paint.

Build plan and rationale live in `OVERLAP-PRD.md`.

## Layout

```
api/                 Go 1.26 HTTP API — deploys to Render or Fly.io
  cmd/api/           main, lifecycle, graceful shutdown
  internal/server/   config, routing, middleware, handlers
  internal/slots/    pure window -> absolute instant expansion (DST lives here)
  internal/dayparts/ pure coarse-grid mapping, evaluated in the responder's zone
  internal/solver/   pure scoring and dominance engine (no IO, no clock)
  internal/results/  binds stored rows to solver inputs; owns what silence means
  internal/ics/      pure RFC 5545 rendering for the decided-event download
  internal/sse/      in-process pub/sub broker and the event-stream encoding
  internal/fetchguard/ SSRF-hardened fetching of caller-supplied URLs
  internal/icsparse/ pure iCalendar -> busy intervals, including recurrence
  internal/store/    the only package that talks to Postgres
  internal/dbgen/    sqlc-generated, do not edit
  internal/tz/       IANA zone resolution for untrusted names
  migrations/        goose SQL migrations
  queries/           SQL that sqlc generates from
web/                 SvelteKit 2 / Svelte 5 — deploys to Vercel
docker-compose.yml   local Postgres 16 on :5434
Makefile             every routine command
```

## Prerequisites

Go 1.26+, Node 20+, Docker. `make tools` fetches pinned `goose` and `sqlc` binaries into `./bin`.

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
make check              # go vet + gofmt check + go test -race, no infrastructure
make test-integration   # the above plus tests that need real Postgres
```

Integration tests skip themselves unless `TEST_DATABASE_URL` is set, so
`go test ./...` stays runnable with nothing running.

The solver is the part worth testing hardest, and it is written to make that
easy: pure functions over plain structs, no database, no clock reads, time
enters only as a parameter.

## API

```
GET  /api/health                        liveness only; does not touch Postgres
POST /api/events                        create; returns { slug, organizer_token? }
GET  /api/events/{slug}                 event, slots, participants, and your own
                                        answers when a token is sent
POST /api/events/{slug}/participants    join by name; returns { token }
PUT  /api/events/{slug}/responses       upsert the full response set
                                        header X-Participant-Token
GET  /api/events/{slug}/solve           ranked slots, coverage and exclusions
POST /api/events/{slug}/decide          organizer locks a slot
POST /api/events/{slug}/reopen          organizer unlocks it again
GET  /api/events/{slug}/decided.ics     calendar download for the locked slot
GET  /api/events/{slug}/stream          SSE: response_submitted, decided, ping
POST /api/events/{slug}/calendar/ics    subscribe to a calendar feed
DELETE /api/events/{slug}/calendar      drop imported blocks
```

**Calendar import reads free/busy only.** `internal/icsparse` extracts start and
end times and nothing else: SUMMARY, DESCRIPTION, LOCATION and ATTENDEE are
never read, never stored and never logged. `busy_blocks` has no column they
could go in, which is a cheaper guarantee than remembering not to write them.

A stated tier always beats an inferred one, in both directions: connecting a
calendar never overwrites an answer somebody typed, and submitting the grid
never deletes the blocks the calendar contributed. Imported tiers carry
`source = 'calendar'` and render hatched, so an inference never looks like a
choice.

**Fetching a URL a stranger supplied is the dangerous part**, not the parsing.
`internal/fetchguard` resolves the host itself, refuses loopback, private,
link-local, carrier-NAT and cloud-metadata addresses, re-checks every redirect,
and dials the address it checked rather than the name -- resolving twice is the
DNS-rebinding hole. `ALLOW_PRIVATE_CALENDAR_HOSTS=true` relaxes this for local
development and is ignored unless `APP_ENV=development`.

**Dominance.** `/solve` returns a verdict naming the situation rather than
leaving the client to infer it: `decidable`, `waiting_on_required`,
`waiting_on_relevant`, `tied`, `no_slots`, `decided`. While any *required*
participant is silent nothing is ever decidable, because they could still veto
the leader. That is not a limitation, it is the correct answer, and it is what
lets the page say "Waiting on Sam" instead of "4 of 6 replied".

**Live updates.** Messages carry no payload; every one means "refetch". A
dropped or coalesced event therefore costs one stale second and can never leave
a page holding a half-applied update, and reconnecting needs no replay.

Recovery does not trust the browser to notice a dropped stream, because it does
not reliably notice -- a killed server can leave `EventSource` in the OPEN state
with no error and no retry. The server pings every 15 seconds and the client
treats silence as death, redialling and refetching. It also refetches when a
backgrounded tab becomes visible, which is how most staleness actually happens.

**No score is ever returned.** The composite orders the ranking on the server
and stays there. A number like 0.7855 printed next to a time reads as precision
the model does not have and invites arguing with it, so what leaves the server
is who can come and who it costs.

**Silence versus refusal.** `internal/results` fills a responder's unmentioned
slots with "no" and leaves a non-responder genuinely unknown. Both directions
matter: if a responder's gaps read as unknown nothing could ever be settled, and
if a non-responder's silence read as "no" the tool would rule out times on
behalf of someone who has not spoken.

**Identity.** A participant token is an opaque 256-bit value the client keeps in
`localStorage`, scoped to the event slug. The database stores only its SHA-256
digest, so a leaked backup cannot be replayed. Lookup is keyed on
`(event_id, token_hash)`, which makes a token minted for one event a miss in
another rather than a comparison someone has to remember to write.

**The input model.** A response is submitted as coarse day-part selections plus
optional per-slot overrides. The server expands the coarse part using the
*responder's* timezone and records `source = 'coarse'`; per-slot overrides land
on top as `source = 'manual'`. Sending day parts rather than pre-expanded slots
means the client and server cannot disagree about which slots a tap covered.

A coarse-only submission is a complete, valid response. Bailing out before the
fine pass still contributes signal, which is the whole point of the two stages.

Slots come back as absolute instants and are rendered in the viewer's zone by
the client. The server never guesses who is looking.

`GET` also returns `dst_notes` when the window crossed a transition, naming any
local time that was dropped because it never occurred, or that occurred twice
and was resolved to its first occurrence. Silence about either would mean
handing back a schedule that does not match what was asked for.

## Deploying

Both halves deploy independently. Do the API first — the web app needs its URL.

**Database → Neon**

Any Postgres 16 works. Neon is what these docs assume, because its free plan is
permanent, needs no card, and allows commercial use. Create a project, copy the
connection string, keep `sslmode=require`.

Avoid a free database that expires. Render's, for one, is deleted 30 days after
creation, which is a data-loss deadline wearing a free tier's clothes.

The pool is configured for a database that sleeps. `MinConns` is zero on
purpose: a floor of one holds a connection open and health-checks it every
minute, and against a provider that scales compute to zero that heartbeat is
activity. The database never sleeps, and an app with no users burns its monthly
compute allowance around the clock.

**API → Render**

The repo has a [render.yaml](render.yaml) blueprint. Point a Blueprint at the
repo, then set `DATABASE_URL`, `WEB_URL` and `ALLOWED_ORIGINS` in the
dashboard, since none of the three belong in a file in a public repo or are
known before the web app exists.

Free instances spin down after 15 minutes of inactivity and take roughly 30 to
60 seconds to come back. Read that number against how this product is used: the
link goes into a group chat, and the first person to tap it waits out the cold
start on a blank screen. Everything after that is warm. The API is a single
static binary that migrates on boot, so the wait is the platform starting a
container rather than anything in the app.

**API → Fly.io**

Still supported, and [api/fly.toml](api/fly.toml) is unchanged. It costs money,
because `auto_stop_machines = 'off'` with `min_machines_running = 1` keeps a
machine up around the clock. That buys the thing Render's free tier gives up:
no cold start, and an SSE stream nothing suspends underneath.

```bash
cd api
fly apps create overlap-api
fly postgres create --name overlap-db && fly postgres attach overlap-db
fly secrets set ALLOWED_ORIGINS=https://<your-vercel-domain> WEB_URL=https://<your-vercel-domain>
fly deploy
```

`fly postgres attach` sets `DATABASE_URL` itself.

**Either way**, the image is a small `scratch` build carrying the CA bundle and
Go's zoneinfo database explicitly, because without zoneinfo every
`time.LoadLocation` call fails and the entire slot model breaks in a way that
never shows up locally. It reads `PORT`, so any host that injects one works.

Both secrets above are required and fail differently. `ALLOWED_ORIGINS` failing
is loud, since every browser request is refused at once. `WEB_URL` failing is
silent: it falls back to `http://localhost:5173` and writes that into the URL
field of every downloaded `.ics`, which still downloads and still imports.

**Migrations** run at startup, inside the API process, before the listener
opens. There is no release command and no goose binary in the image. The
reasoning is in `internal/migrate`: `/api/health` is a liveness probe that
never touches the database, so a deploy against an unmigrated schema would
report healthy while every real request failed. A startup error is the version
of that failure a deploy can actually show you.

A concurrent-safe advisory lock guards the run, so two machines starting at
once cannot both apply the same migration. `RUN_MIGRATIONS=false` turns it off
if something else owns the schema, and `go run ./cmd/migrate` applies pending
migrations without starting a server.

**Expired events** are deleted by a sweep inside the API, every
`PURGE_INTERVAL` (6h by default, `0` to disable). Participants, responses and
busy blocks follow through `on delete cascade`; groups survive, because
graduation exists precisely so a group outlives the event that made it.

**Abuse controls** are two mechanisms guarding two different things.

A per-address token bucket covers the endpoints that write rows without
needing a token: creating an event, joining one, joining a group, and the two
claim routes. `RATE_LIMIT_BURST` at once, `RATE_LIMIT_PER_MINUTE` sustained,
`RATE_LIMIT_MAX_KEYS` addresses tracked. It is in-process, so counters reset on
deploy. That is the intended trade: a limiter backed by Postgres would put a
write on the hot path of every request against the same small database it is
protecting, and this enforces no quota anyone paid for, so losing counters on a
deploy costs nothing an abuser can use.

Set `CLIENT_IP_HEADER` wherever a proxy sits in front. It takes a
comma-separated list and the first header present on a request wins:
`Fly-Client-IP` on Fly, `True-Client-IP, CF-Connecting-IP` on Render, both
already set in their config files. It is empty by default because trusting a
header nothing upstream overwrites lets a client mint a fresh bucket per
request.

Each header is read from its **rightmost** entry, because a proxy appends the
address it received from, so anything left of the last entry came from the
caller. Reading from the left lets anyone prepend a value and pick their own
bucket.

Name single-value headers the proxy overwrites, and avoid `X-Forwarded-For`.
It is a list, and behind two proxies its last entry is an internal hop that
changes between requests, so every request gets its own bucket and nothing is
ever limited. Render was configured that way at first, and 45 concurrent
requests against production returned zero refusals. Both failure directions are
silent, which is why this is worth checking against the deployed service rather
than reasoning about.

`GET /api/groups/{slug}/proposal` needs a different control, because it takes
no token and one call refreshes every connected member's calendar. A per-caller
limit still lets a hundred addresses each trigger a full fan-out, so the guard
is keyed on the event instead: a `PROPOSAL_COOLDOWN` window plus single-flight,
which collapses concurrent callers into one computation. Measured on a running
server, 100 requests to that endpoint produced 1 outbound calendar fetch.

Neither control stops an attacker who rotates source addresses. That is a
property of per-address limiting rather than of this implementation, and it is
why the expensive path is bounded by the resource rather than by the caller.

**Web → Vercel**

Set `PUBLIC_API_URL` to the Fly URL, then deploy `web/`. The project uses
`@sveltejs/adapter-auto`, which resolves to the Vercel adapter during a Vercel
build; pin `@sveltejs/adapter-vercel` if you would rather not rely on detection.

`ALLOWED_ORIGINS` on the API must list the Vercel origin exactly, scheme
included, or the browser fetch fails CORS while curl keeps working.

**Before you deploy**

Decide the rollback rule in advance, because deciding it mid-incident is how a
bad deploy stays up.

```bash
fly releases                  # find the version you were on
fly deploy --image <previous> # or: fly releases rollback
```

Roll back on any of these:

- `/api/health` failing after the grace period, which means the process is not
  coming up at all.
- Startup logs showing a migration error. The schema is the one thing a rollback
  does not undo; a migration that failed halfway needs `make migrate-down`
  against the production URL before the older image will run.
- Errors on `PUT /responses` or `GET /solve`, the two paths every user hits.
- The results page going quiet. SSE failures are invisible from the server side,
  so watch a real event rather than a metric.

The SSE broker is in-process. `min_machines_running = 1` with
`auto_stop_machines = 'off'` is what keeps that correct: at two machines, a
response saved on one never reaches watchers on the other, and it fails
silently because the heartbeat keeps flowing. Move the broker to Postgres
`LISTEN/NOTIFY` before scaling out.

## Conventions

- Every timestamp is `timestamptz`. No naive datetimes in any layer.
- The solver stays pure: no DB access, no clock reads, no globals in scoring.
- Calendar data is free/busy only. Event details are never fetched, stored or
  logged. This is a stated privacy commitment, so treat it as a code invariant.
- Calendar-derived responses carry `source = 'calendar'` and stay overridable.

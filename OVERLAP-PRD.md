# Overlap — Product Requirements & Build Plan

**Status:** ready to build
**Owner:** Maazin Shaikh
**Timeline:** open ended
**Stack:** Go, SvelteKit, Postgres — deliberately unfamiliar territory

> Everyone's free time, in fifteen seconds.
> Connect a calendar or tap three buttons. No grid to paint.

---

## 1. Problem

When2Meet is used by millions of people who hate it. It's been essentially unchanged for fifteen years. The failures are structural, not cosmetic:

**It ends where the job is half done.** It shows the overlap and abandons you. No way to lock a time, no invite, no calendar event. You return to the group chat to actually decide, which is the thing you were escaping.

**Mobile is miserable.** Drag-painting a grid with a thumb is genuinely bad, and most people open the link on a phone.

**You transcribe your own calendar by hand.** The real work is checking what you're already committed to and manually reproducing it. Tedious and error-prone, so everyone over-reports availability.

**Binary availability is a lie.** "Free" and "not free" can't express "I could, but I'd rather not," so people either block time they'd accept or claim time they'll resent.

**Everyone counts equally.** A slot that misses the one person the meeting is actually about looks identical to one that misses a bystander.

**The two-day dead zone.** You have four of six responses and no idea whether the last two could change anything, so you wait.

## 2. Competitive reality

LettuceMeet and Crab.fit already exist and are both nicer than When2Meet. **"It's prettier" is not an available wedge.** Neither does calendar import, preference tiers, required-versus-optional attendees, or dominance analysis. That's where the differentiation lives.

## 3. Non-goals

- Not a Calendly clone in v1. The 1:1 booking surface comes after the group product works.
- Not recurring meeting scheduling. Real need, different problem, later. Groups remove setup cost; they do not introduce recurrence.
- Not a calendar app. Overlap reads free/busy and never shows event details.
- No social graph, no feed, no profiles.

## 4. Core constraints

> **Anonymous by default. A joiner must be able to respond in under thirty seconds without signing in.**

Calendar import is a power-up, never a gate. The moment responding requires an account, Overlap loses to the group chat.

> **A group is never required to schedule anything. The one-off link flow must always work standalone.**

This is an invariant, not an intention. Groups add retention, and the failure mode is a signup wall creeping toward the front door until Overlap becomes Doodle. Any change that makes the anonymous link path worse in order to push someone toward a group is rejected on principle, regardless of what it does to the metrics.

## 5. The input model

The grid is the wrong primitive because it demands maximum information when the task needs minimum. Replace "paint your availability" with **narrow, then confirm.**

**Stage 0 — calendar prefill (optional, one tap).** Known-busy is pre-filled and visibly labeled as coming from your calendar. This changes the modality from "specify everything" to "correct a proposal," which is a fundamentally easier task.

**Stage 1 — coarse pass.** Days × morning/afternoon/evening. For a one-week window that's 21 thumb-sized targets, tri-state, tap to cycle. Most people finish here and the organizer already has usable data.

**Stage 2 — fine pass, scoped.** Actual slots, but only inside blocks you marked yes or if-needed. Six slots to rate instead of ninety. Preference tiers live here.

A response is valid after Stage 1. Bailing early still contributes signal, which the grid completely fails at.

## 6. Preference tiers

| Tier | Value | Meaning |
|---|---|---|
| `preferred` | 1.0 | actively good for me |
| `ok` | 0.7 | fine |
| `if_needed` | 0.3 | I'd rather not, but I'll make it work |
| `no` | — | hard block |
| `unknown` | — | hasn't responded |

Attendees are `required` or `optional`, set by the organizer.

## 7. Scoring model

For each candidate slot:

**Elimination.** Any `required` attendee marked `no` kills the slot. An `optional` attendee marked `no` does not, but the exclusion is recorded and shown.

**Composite score.**

```
mean_w  = weighted mean of tier values (required weight 1.0, optional weight 0.5)
min_r   = minimum tier value among required responders
base    = α·mean_w + (1−α)·min_r          α = 0.7

penalty = 0.15 per attendee for whom the slot falls outside 08:00–21:00 local
          (capped at 0.45 total)

score   = base − penalty
```

The α blend is the one real parameter. Pure mean picks slots that are great for four people and grim for one. Pure min picks the bland slot nobody objects to. 70/30 mostly optimizes group happiness while refusing to strand a required attendee on `if_needed`.

**Ranking:** required coverage must be 100%, then composite score, then optional coverage, then earliest.

**Headline number stays simple.** Users see "5 of 6 can make it." The composite score orders things behind the scenes; never show it as a number, it reads as arbitrary.

## 8. Dominance analysis

The two-day dead zone is computable.

For each slot, compute `lo(s)` assuming every non-responder answers as badly as possible, and `hi(s)` assuming they answer as well as possible. Because a person can independently give different tiers to different slots, the worst case for A relative to B is: non-responders give A their minimum and B their maximum.

```
decidable  ⟺  ∃ s such that lo(s) ≥ max over t≠s of hi(t)
```

**Consequence worth internalizing:** if any *required* attendee hasn't responded, `lo(s) = 0` for every slot and nothing is ever decidable. That's not a limitation, it's the correct answer, and it produces a genuinely useful message:

> **Waiting on Sam (required). Nothing can be decided until he responds.**

That names exactly who is blocking, which is more than any existing tool does.

**Relevance ranking of non-responders.** For each unknown participant, check whether pinning their response to best-case versus worst-case changes which slot leads. If it doesn't, their answer is irrelevant and they shouldn't be nagged.

> **Thursday 3pm wins regardless of what Ana and Dev say. Decide now.**
> **Only Sam's answer separates Thursday and Friday.**

Complexity is O(slots × participants). Irrelevant at this scale.

**Positioning:** this is the second-scroll feature, not the headline. Speed converts the first visit. Dominance is what makes someone tell a friend.

## 9. Groups and the graduation path

Ephemeral links win the first use. They also mean the fifth meeting with the same six people costs exactly as much effort as the first, which is why nothing in this category retains anyone.

Groups fix that, but only if they're never the entry point.

**link → event → group → proposal.** Each step earns the next, and friction only ever appears after value has been delivered.

**Graduation.** When an event resolves, one line: *"Schedule with these five again?"* That's the only moment someone will accept setup cost, because they just watched the thing work. Accepting mints a group from the event's participants, carrying over names, timezones, roles, and any calendar connections.

**Membership is a join link, name only.** No email required, no account. Someone opens the group link, picks a name, they're in. Members who connected a calendar have a durable Google identity behind them; name-only members are bound to a device token and lose membership if they clear storage. Recovery is the same trust model When2Meet already uses: open the link on a new device, pick your name from the member list, claim it. There's no sensitive data behind that door and pretending otherwise would just add friction.

**No notification channel, and that's fine.** Without emails, creating event #2 doesn't notify anyone; the organizer still pastes the link into the group chat. Correct outcome. The chat is already the notification layer and always will be. The group's job is persistence, not delivery. What changes is that the link landing in the chat arrives pre-populated.

**Auto-proposal is the payoff.** A group whose members have connected calendars already has availability before anyone taps anything. From the second event onward:

> **Thursday 3pm works for all six of you.**
> Confirm · Or open it for responses

This flips the product from a poll into a scheduler. No competitor can do it, because none of them hold both persistent membership and calendar access.

**The proposal is always a suggestion, never an automatic booking.** Free/busy shows where nobody is committed; it says nothing about whether 8am on a Monday is something people actually want. The default action stays "open for responses," with confirming available as the fast path. Silently booking from calendar data alone would be exactly the kind of confident wrongness this product exists to avoid.

**Group event scope.** Groups do not introduce recurring scheduling. Each event is still a discrete event with a window, a slot size, and a decision. Groups only remove the setup cost.

**Any member can create an event.** These are peer groups — roommates, friends, a club board — not org charts. No admin role, no permissions UI, nothing to configure.

### History, and the one rule that makes it safe

The group page shows past events and the times they landed on, and that history feeds back into ranking. Done naively this is how a product starts feeling like it's overruling people.

**History is a tiebreak, never a score term.** Two slots are compared on composite score first; only if they fall within `HistoryEpsilon` (0.05) of each other does the group's habit break the tie. A slot people actually preferred cannot be beaten by a pattern, however strong that pattern is.

That guarantee is structural rather than tuned. Adding a history bonus to the score would make it a question of picking a small enough constant, and small enough constants drift. Ordering the comparisons makes it impossible by construction. `TestHistoryCannotOverturnStatedPreference` pins it with twenty consecutive Thursdays losing to one stated preference for Friday.

**History decays, with a 30-day half life.** Without decay a group ossifies on whatever it picked a year ago, and the feature silently suppresses exactly the drift it should be surfacing — someone's new Thursday class, a teammate who moved timezone. Affinity is bucketed by weekday and local hour, since no two events share absolute slots.

**Where history is genuinely useful is proposals, not ranking.** When a group creates an event, seed the default window from the habit. That's a strong, safe use: it shapes what gets asked, and what people answer still decides.

## 10. Timezone model

**Store absolute instants. Render in the viewer's local zone. Always.**

- `slot_start` is `timestamptz`, i.e. a real moment in time
- Each participant has an IANA zone name, auto-detected via `Intl.DateTimeFormat().resolvedOptions().timeZone`, overridable
- The event window is defined in the **organizer's** local dates and times ("Nov 1–7, 9am–5pm") and expanded to absolute slots using the organizer's zone

**The DST trap, called out explicitly because it will bite you.** Go's `time.Date` normalizes nonexistent local times instead of erroring. On a spring-forward day, a 2:30am slot silently becomes 3:30am, and on fall-back an ambiguous local time resolves to one of two instants with no warning. Slot generation must detect both cases and handle them deliberately. This is a required test case, not an edge case to discover in production.

## 11. Architecture

```
SvelteKit (Vercel)  ──HTTP + SSE──▶  Go API (Fly.io)  ──▶  Postgres
                                          │
                                          ├──▶ Google Calendar FreeBusy
                                          └──▶ ICS feed fetch
```

| Layer | Choice | Notes |
|---|---|---|
| Language | **Go 1.22+** | New to you. Most-listed of the unfamiliar options in job postings. |
| Router | stdlib `net/http` + `chi` | Go 1.22 routing patterns cover most of it |
| DB access | `pgx` v5 + `sqlc` | sqlc generates type-safe Go from SQL. Teaches good Go, no ORM magic. |
| Migrations | `goose` | |
| Live updates | **SSE** via `http.Flusher` | ~40 lines, no library, no reconnection complexity |
| OAuth | `golang.org/x/oauth2` + Calendar API | **FreeBusy scope only** |
| ICS | `github.com/arran4/golang-ical` | |
| Frontend | **SvelteKit 2 / Svelte 5 runes** | New to you. Better than React for animation-heavy interactive UI. |
| Styling | Tailwind | |
| DB | Postgres 16 | Deliberately familiar — spend the learning budget on Go |
| Hosting | Fly.io (API + Postgres), Vercel (web) | |

**Why not WebSockets.** Group scheduling is asynchronous. People fill it in on a commute; nobody watches the grid. SSE gives live updates for a fraction of the complexity, and one-directional streaming is the honest fit for a read-mostly view.

**Why Postgres stays familiar.** Two new things is a learning project. Four is an abandoned repo.

## 12. Data model

```sql
create table groups (
  id           uuid primary key default gen_random_uuid(),
  slug         text unique not null,          -- the join link
  name         text not null,
  created_from uuid,                          -- event this graduated from
  created_at   timestamptz not null default now()
);

create table group_members (
  id            uuid primary key default gen_random_uuid(),
  group_id      uuid not null references groups(id) on delete cascade,
  token         text not null,                -- device-bound identity
  display_name  text not null,
  tz            text not null,
  default_role  text not null default 'optional',
  google_sub    text,                         -- durable identity when calendar-connected
  refresh_token bytea,                        -- encrypted at rest
  joined_at     timestamptz not null default now()
);
create unique index group_members_group_token_idx on group_members (group_id, token);

create table events (
  id                  uuid primary key default gen_random_uuid(),
  group_id            uuid references groups(id) on delete set null,  -- null = one-off link
  slug                text unique not null,
  title               text not null,
  organizer_tz        text not null,           -- IANA name
  window_start        date not null,
  window_end          date not null,
  day_start           time not null default '09:00',
  day_end             time not null default '17:00',
  slot_minutes        int  not null default 30,
  status              text not null default 'open',   -- open|decided|expired
  decided_slot_start  timestamptz,
  created_at          timestamptz not null default now(),
  expires_at          timestamptz not null default now() + interval '60 days'
);

create table participants (
  id              uuid primary key default gen_random_uuid(),
  event_id        uuid not null references events(id) on delete cascade,
  token           text not null,                       -- opaque, client-stored
  display_name    text not null,
  tz              text not null,
  role            text not null default 'optional',    -- required|optional
  email           text,                                -- optional, edit-link only
  calendar_source text not null default 'none',        -- none|google|ics
  is_organizer    boolean not null default false,
  responded_at    timestamptz,
  created_at      timestamptz not null default now()
);
create unique index participants_event_token_idx on participants (event_id, token);

create table responses (
  participant_id uuid not null references participants(id) on delete cascade,
  slot_start     timestamptz not null,
  tier           smallint not null,                    -- 3 pref, 2 ok, 1 if_needed, 0 no
  source         text not null default 'manual',       -- manual|calendar|coarse
  primary key (participant_id, slot_start)
);

create table busy_blocks (
  id             uuid primary key default gen_random_uuid(),
  participant_id uuid not null references participants(id) on delete cascade,
  start_ts       timestamptz not null,
  end_ts         timestamptz not null,
  source         text not null                          -- google|ics
);

create table results (
  event_id     uuid primary key references events(id) on delete cascade,
  ranked       jsonb not null,     -- [{slot_start, score, coverage, excludes[]}]
  dominance    jsonb not null,     -- {decidable, leader, blocking_participants[]}
  computed_at  timestamptz not null default now()
);
```

**`source` on responses matters.** A tier that came from calendar import must be visually distinguishable from one the user set, and must be overridable. Silently treating inferred data as stated data is the fastest way to lose trust.

## 13. API

```
POST   /api/events                          create
       → { slug, organizer_token }
GET    /api/events/{slug}                   full state (tz-aware, viewer-local)
PATCH  /api/events/{slug}                   organizer only: title, roles, window
POST   /api/events/{slug}/participants      join
       → { participant_id, token }
PUT    /api/events/{slug}/responses         upsert full response set
       header X-Participant-Token
POST   /api/events/{slug}/calendar/google   OAuth callback → busy blocks
POST   /api/events/{slug}/calendar/ics      body { url } → busy blocks
GET    /api/events/{slug}/solve             ranked slots + dominance
POST   /api/events/{slug}/decide            organizer locks a slot
GET    /api/events/{slug}/decided.ics       download invite
POST   /api/events/{slug}/edit-link         email a magic edit link
GET    /api/events/{slug}/stream            SSE: response_submitted, decided

POST   /api/events/{slug}/graduate          mint a group from this event's participants
       → { group_slug }
GET    /api/groups/{slug}                   members, past events, pending proposal
POST   /api/groups/{slug}/members           join by link, name only
       → { member_id, token }
POST   /api/groups/{slug}/members/claim     re-claim membership on a new device
       body { member_id }
POST   /api/groups/{slug}/events            new event, participants pre-populated
       → { event_slug, proposal? }
GET    /api/groups/{slug}/proposal          calendar-derived suggestion, if any
```

**Identity.** Opaque token in `localStorage`, scoped to the event slug. Optional email gets you a magic edit link for switching devices. No passwords, no accounts, no When2Meet-style name-plus-password nonsense.

**Group identity is deliberately weak.** Membership is a device token, and re-claiming on a new device is a name picker with no verification. That's the same trust level When2Meet already operates at, and the data behind it is "which afternoons someone is free," not anything worth protecting with a password. Do not add auth here. If group calendar connections ever expose more than free/busy, revisit — but the FreeBusy-only rule means they won't.

---

# Build phases

Each phase has a definition of done. Do not start N+1 until N passes. This matters more than usual because you're learning Go while building.

## Phase 0 — Skeleton and deploy (2h)

Go module, SvelteKit app, Postgres via docker-compose, goose initialized, `GET /api/health`, SvelteKit page rendering it. **Deploy both to production immediately, empty.**

**DoD:** a live URL renders "api: ok".

Deploying on day zero costs two hours. Discovering your deploy is broken when the project is otherwise finished costs the project.

## Phase 1 — Events and slot generation (4h)

Migrations for `events`. Slug generation with collision retry. Event creation. **Slot expansion from organizer-local window to absolute `timestamptz` values.**

This phase is small in code and large in correctness. Write the DST tests first.

**DoD:** create an event spanning a DST transition in `America/New_York` and get the correct number of slots with correct absolute instants, verified against hand-computed UTC values. Both spring-forward (nonexistent local time) and fall-back (ambiguous local time) are explicitly handled and tested.

## Phase 2 — Participants and response input (8h)

Migrations for `participants`, `responses`. Join, token auth middleware, response upsert.

Frontend: coarse pass (days × three blocks, tri-state, tap to cycle), fine pass scoped to coarse-yes regions, timezone auto-detect with manual override.

**DoD:** on a phone, join an event and submit a complete response in under thirty seconds without pinch-zooming or precise dragging. Time yourself. If it's over thirty seconds the input model needs work, not the code.

## Phase 3 — Solver (5h)

Composite scoring, required-versus-optional elimination, unsociable-hour penalty, ranking.

Pure functions over plain structs, no DB access inside the solver. It should be testable with a literal fixture and no database.

**DoD:** a six-person fixture with mixed roles and tiers produces the hand-verified ranking. Table-driven tests cover: required `no` eliminates, optional `no` doesn't, α blend changes the winner in a constructed case, unsociable penalty demotes a 7am slot.

## Phase 4 — Results and decide (5h)

Heatmap by coverage, ranked list with "5 of 6 can make it" and named exclusions, organizer locks a slot, `.ics` download, decided-state view.

**DoD:** full flow from creation to a downloaded `.ics` that imports correctly into Google Calendar and Apple Calendar.

## Phase 5 — SSE live updates (2h)

`GET /stream` with `http.Flusher`. In-process `map[eventID]map[chan]struct{}` with a mutex. Broadcast on response submit and decide. Frontend `EventSource` with reconnect-and-refetch rather than event replay.

**DoD:** two browsers on one event, one submits, the other's heatmap updates within a second without a refresh. Kill the server, restart it, client reconnects and shows correct state.

This is your concurrency story. Write it yourself, don't reach for a library.

## Phase 6 — Calendar import (8h)

**Start the Google OAuth verification submission at the beginning of this phase, not the end.** Approval takes weeks. Build against an unverified app in the meantime.

- Google OAuth with **FreeBusy scope only** — you need busy blocks, never event details. Narrower scope is both easier to verify and a materially better privacy story you should state on the landing page.
- ICS URL paste as the no-OAuth path, covering Apple and Outlook
- Map busy blocks to `no` tiers with `source = 'calendar'`, visually distinct and overridable

**DoD:** connect a real Google account, land on the response page with busy time pre-filled and labeled, override one block, submit.

## Phase 7 — Dominance (4h)

`lo(s)` / `hi(s)` computation, decidability check, non-responder relevance ranking.

**DoD:** fixture where four of six have responded and the leader is provably dominant produces "decide now." A second fixture where a required attendee hasn't responded produces "waiting on Sam (required)." A third correctly identifies that only one of three non-responders can change the outcome.

## Phase 8 — Launch (6h)

Empty and error states. **OG tags** so the link previews properly in iMessage, WhatsApp, and Discord — a bare URL in a group chat gets ignored, so this is growth, not polish. Mobile pass assuming every joiner is on a phone. Landing page leading with speed and dominance on the second scroll. Analytics: events created, response rate, time-to-decided, repeat organizers.

## Phase 9 — Groups (6h)

Migrations for `groups`, `group_members`, and `events.group_id`. Graduate endpoint minting a group from a resolved event's participants. Group join link, name-only membership, name-picker claim on a new device. Group page listing members and past events. New event from a group with participants pre-populated.

**Do not build notifications.** No emails, no push. The organizer pastes the link into the group chat exactly as before. Persistence is the feature.

Group page lists past events and the times they landed on. Wire `RankWithHistory` with an affinity built from those decisions.

**DoD:** resolve an event, graduate it to a group, create a second event from that group with zero re-entry of names, timezones, or roles. Then clear browser storage, reopen the group link, reclaim your membership by name, and confirm your calendar connection survived. History influences a near-tie and provably cannot overturn a stated preference.

## Phase 10 — Auto-proposal (5h)

For groups where members have connected calendars, derive availability from stored free/busy before anyone responds. Surface a proposal on group event creation.

Two actions, and the ordering matters: **Open for responses** is the default and primary button, **Confirm this time** is secondary. Free/busy knows where nobody is committed; it knows nothing about whether 8am Monday is something anyone wants. A proposal is a shortcut, never an automatic booking.

Stale calendar data is the trap. Refresh free/busy at proposal time rather than trusting what was cached at connect time, and show when it was last read.

**DoD:** a group of three with connected calendars creates an event and sees a correct proposal without anyone responding. A member with a conflicting calendar entry is correctly excluded from it. Deliberately stale cached data does not silently produce a wrong proposal.

---

# Later

**1:1 booking surface.** The same availability engine, different front door. Group flow is many-people-one-event; booking is one-person-many-events. Signed-in users get a persistent availability page and a shareable link. Ships only after the group product has real usage.

**Recurring.** "A weekly slot that works all semester." Real need, meaningfully harder, and it's where fairness rotation starts to matter.

**Outlook and Apple OAuth.** ICS covers them adequately at first.

---

# Instructions for Claude Code

**Standing rules:**

1. **One phase at a time.** Do not scaffold ahead. Stop at the DoD and report what was done and skipped.
2. **Idiomatic Go, not translated Python.** Explicit error returns, no panics in request paths, accept interfaces and return structs, `context.Context` as the first parameter on anything doing IO. If code looks like Python with braces, rewrite it.
3. **No ORM.** `sqlc` generates from SQL. Do not introduce GORM.
4. **The solver is pure.** No database access, no clock reads, no globals inside scoring. Time is a parameter.
5. **Never store or log calendar event details.** Only busy intervals. This is a privacy commitment stated on the landing page, so it's a code invariant.
6. **Calendar-derived responses carry `source = 'calendar'`** everywhere and are always user-overridable.
7. **Every timestamp is `timestamptz`.** No naive datetimes anywhere, in any layer.
8. Ask before adding a dependency.

**Per-phase prompt shape:**

> Implement Phase N of OVERLAP-PRD.md. Read the PRD and existing code first. Only touch files this phase needs. Write tests alongside, not after. Stop when the DoD passes and report what you did and what you skipped.

**Since Go is new, ask it to explain rather than just produce.** After each phase: "walk me through the concurrency and error handling choices you made and why they're idiomatic." The point of picking an unfamiliar stack is learning it, and a phase you can't explain is a phase you didn't learn.

---

# Testing

| Phase | Focus |
|---|---|
| 1 | DST transitions both directions, slot count, absolute instant correctness |
| 2 | token rejection cross-event, coarse-to-fine inheritance, partial response validity |
| 3 | solver fixtures, table-driven tier and role combinations |
| 5 | SSE reconnect, broadcast to N clients, no goroutine leak on disconnect |
| 6 | ICS parsing malformed input, overlapping busy blocks, all-day events |
| 7 | dominance fixtures, required non-responder case, relevance ranking |
| 9 | graduation carries roles and timezones, claim-by-name on a fresh device, group event pre-population, history tiebreak invariant |
| 10 | proposal excludes members with conflicts, stale cache is refused, empty proposal falls back to a normal poll |

Phases 1, 3, and 7 are the ones that matter. Timezone correctness and the solver are the technical core, and they're what you'll be asked about.

**Goroutine leaks in Phase 5 are the classic Go beginner bug.** Every SSE connection spawns work that must clean up on client disconnect. Test it with `runtime.NumGoroutine()` before and after.

---

# Metrics

| Metric | Meaning |
|---|---|
| Events created | Top of funnel |
| Responses per event | Viral loop health; below 3 it isn't working |
| **Response completion rate** | Opened the link but didn't submit = the input model failed |
| Median time to respond | Target under 30 seconds |
| Calendar connect rate | Is the power-up worth the OAuth work? |
| Time from creation to decided | The number the product exists to reduce |
| Graduation rate | Resolved events that become groups. Tests whether the offer lands. |
| **Second-event rate** | Groups that schedule again. The retention number the whole group model exists to move. |
| Proposal acceptance | Auto-proposals confirmed without opening for responses |
| **Repeat organizers** | The only number that tells you it's real |

---

# Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Google OAuth verification delay | **High** | Submit at the start of Phase 6; anonymous-first means it throttles a feature, not the launch |
| Learning Go slows everything | Medium | Postgres stays familiar; phases are small; open-ended timeline |
| Coarse-then-fine confuses people | **High** | It's novel, which cuts both ways. Watch five real people use it before building anything on top of it. |
| LettuceMeet and Crab.fit are good enough | Medium | Wedge is calendar import and dominance, not aesthetics |
| Calendar privacy concerns | Medium | FreeBusy scope only, never store event details, say so prominently |
| DST bugs surface in production | Medium | Phase 1 tests are non-negotiable |
| Signup wall creeps toward the front door | **High** | Section 4 invariant: the anonymous link path may never be degraded to push groups |
| Auto-proposal books a time nobody wants | Medium | Open-for-responses stays the default action; free/busy is not preference |
| History makes the product feel like it overrules people | Medium | Tiebreak-only by construction, 30-day decay, invariant test |

---

# Resume bullets

**Technical**

> Built a timezone-correct group scheduling engine in Go handling DST transition edge cases (nonexistent and ambiguous local times) with absolute-instant storage and per-viewer local rendering, verified by table-driven tests across transition boundaries.

> Implemented dominance analysis over incomplete response sets, computing whether outstanding participants can change the outcome and identifying which specific non-responders are decision-relevant, eliminating the wait-for-everyone deadlock in group scheduling.

> Designed a preference-tier scoring model blending group mean and worst-case individual satisfaction with required-versus-optional attendee semantics, replacing the binary available/unavailable model used by existing tools.

**Product** (fill in post-launch)

> Launched Overlap, a group scheduling tool with N events created and a median response time of M seconds, versus the multi-minute grid interaction of incumbent tools.

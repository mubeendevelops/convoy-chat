# ConvoyChat

A production-grade, Slack-like real-time chat application: a Go API serving
REST + WebSocket, a Next.js 14 frontend, PostgreSQL for persistence, and
Redis for presence state and cross-server Pub/Sub broadcast.

**Features:** JWT auth with refresh-token rotation · channels, groups, and
direct messages · room/member management with admin/member roles
(promote/demote, kick, admin succession) · a system-admin dashboard
(system-wide room/presence visibility + message moderation, a separate
authority from per-room admin) · real-time messaging with persistence and
history, editing, and deletion · presence (online/away/offline) · typing
indicators · read receipts · emoji reactions · multi-server broadcast via
Redis Pub/Sub.

File uploads was considered and decided against — this stays a pure
text/reaction/read-receipt chat app. See
[Known limitations](#known-limitations--v2) below for what's left.

## Architecture

```
Browser — Next.js 14 (Vercel)
REST (lib/api.ts)  +  1 WebSocket per session (hooks/useWebSocket.tsx)
  │
  ▼
Go API — cmd/api, chi router (Render)
  │
  ├── REST handlers (internal/handlers)
  │     auth · users · rooms · messages · reactions
  │
  └── WebSocket layer (internal/websocket)
        ├── Hub    — single goroutine; owns the client set + room→clients
        │            index; fans events out to every locally-connected
        │            client in that room
        └── Broker — bridges the Hub to Redis Pub/Sub so an event reaches
                      clients connected to *other* server instances too
                      (see "Multi-server broadcast" below)
  │
  ▼
internal/store — the only package that touches pgx / go-redis directly
  │
  ├── PostgreSQL — durable source of truth
  │     users · rooms · room_members · messages
  │     message_reactions · message_read_receipts
  │
  └── Redis — ephemeral only; nothing here is load-bearing
        presence:conns:{user_id} / presence:status:{user_id}   (TTL'd)
        idempotency:message:{room_id}:{user_id}:{key}          (TTL'd)
        PUBLISH/SUBSCRIBE on room:{room_id}
```

A message send is persisted to Postgres _before_ it's broadcast, so history
is never missing something a connected client already saw. Redis holds
nothing that can't be lost safely: presence keys expire and self-heal within
one 15s heartbeat, and idempotency keys just guard a 5-minute retry window —
a Redis restart never loses a message, room, or user.

### Multi-server broadcast

Each server instance dynamically `SUBSCRIBE`s a Redis channel
(`room:{room_id}`) only while it has at least one local client in that room,
and delivers an event to local clients only when it arrives back through
that subscription — including on the server that originated it. One
delivery path means no double-delivery and no per-instance dedup logic.

```
Instance 1 (2 local clients in #general)  ──┐
                                              │  each SUBSCRIBEs room:<id> while it
Instance 2 (1 local client in #general)   ──┤  has ≥1 local client in that room
                                              │
                                              ▼
                                    Redis PUBLISH room:<id>
                                              │
                                              ▼
                     fanned out to both subscribed instances, which each
                     deliver to their own local clients — never each other's
```

Subscribing is synchronous and confirmed _before_ a joining client's
`user.joined` is published (Redis `PUBLISH` silently drops to a channel with
no live subscriber, so publishing first would lose the joiner's own event).
Unsubscribing on a room's last local leave is fire-and-forget — a late
unsubscribe only wastes a little effort delivering to an empty room, it
never loses anything.

## Tech stack

**Backend** — Go 1.25, module `github.com/mubeendevelops/convoy-chat`:

| Library                              | Version |
| ------------------------------------ | ------- |
| github.com/go-chi/chi/v5             | v5.3.0  |
| github.com/gorilla/websocket         | v1.5.3  |
| github.com/redis/go-redis/v9         | v9.21.0 |
| github.com/jackc/pgx/v5              | v5.10.0 |
| github.com/google/uuid               | v1.6.0  |
| github.com/golang-jwt/jwt/v5         | v5.3.1  |
| golang.org/x/crypto (bcrypt)         | v0.53.0 |
| github.com/golang-migrate/migrate/v4 | v4.19.1 |
| github.com/go-chi/cors               | v1.2.2  |

**Frontend** — Next.js 14.2.35 (App Router), React 18.3.x, TypeScript ~5.9,
Tailwind CSS 3.4.19, shadcn/ui, @tanstack/react-query 5.101.2,
@tanstack/react-virtual 3.14.5, zustand 5.0.14, next-themes 0.4.6. A raw
`WebSocket` client — not Socket.IO, since the backend speaks plain WS via
gorilla.

**Infra** — PostgreSQL (`postgres:17-alpine` in dev), Redis
(`redis:7-alpine`), Docker + docker-compose. Deploy targets: Render
(backend), Vercel (frontend) — see [DEPLOYMENT.md](DEPLOYMENT.md).

## Repo layout

```
cmd/api/            main.go, router.go, logging.go, migrate.go (-migrate mode),
                    promote.go (-promote-admin <email> mode — grants the
                    first system admin; see Getting started below)
internal/
  auth/              JWT generate/validate, bcrypt password hashing, auth middleware
  websocket/         Hub, Broker (Redis Pub/Sub bridge), Client (read/write pumps),
                      inbound event dispatch, presence + typing state
  handlers/          REST handlers: health, auth, users, rooms, messages, reactions, admin
  models/            User, Room, Message, Presence types
  store/             ALL Postgres/Redis access lives here — handlers and the
                      websocket package never touch pgx/go-redis directly
  config/            env loading + validation
  httpx/             shared JSON response/error helpers
  testutil/          spins up real Postgres+Redis testcontainers for integration tests
migrations/          golang-migrate up/down .sql pairs
frontend/            Next.js 14 app (App Router) — see frontend/README.md for the
                      create-next-app defaults; app/, components/, hooks/, lib/
docker-compose.yml         local dev: Postgres (host port 5433) + Redis
docker-compose.prod.yml    production-oriented full stack (own Compose project)
Dockerfile                 multi-stage Go build → alpine, non-root, ~22MB,
                            doubles as the migration-init image via `-migrate`
Makefile                   build/run/test/lint/migrate-up/migrate-down
```

## Getting started (local development)

**Prerequisites:** Go 1.25+, Node 20+ (LTS), Docker + Docker Compose, git.

```bash
# 1. Clone and enter the repo
git clone <this-repo-url> convoy-chat
cd convoy-chat

# 2. Backend env — the checked-in placeholder JWT_SECRET is already 32+
#    chars, so this works out of the box for local dev. Never reuse it
#    anywhere real.
cp .env.example .env

# 3. Start Postgres + Redis
docker compose up -d

# 4. Load env vars into this shell, then apply migrations
set -a && source .env && set +a
go run ./cmd/api -migrate

# 5. Run the backend (same shell, so the env vars are still loaded)
go run ./cmd/api
# → starting server addr=:8080 env=development
```

To use the admin dashboard, sign up a user through the app first, then grant
it system-admin status (no REST endpoint does this, by design — see
CLAUDE.md's admin-dashboard entry):

```bash
go run ./cmd/api -promote-admin you@example.com
```

Log out and back in afterward — `is_system_admin` comes from the login
response and isn't live-refetched mid-session.

In a second terminal, verify it's healthy:

```bash
curl http://localhost:8080/health
# {"postgres":"ok","redis":"ok"}
```

Then bring up the frontend in a third terminal:

```bash
cd frontend
cp .env.example .env.local   # defaults already point at localhost:8080
npm install
npm run dev
# → open http://localhost:3000
```

Sign up two different users (e.g. in a second browser or an incognito
window), create a channel or start a DM, and send messages between them to
see real-time delivery, presence, typing, and read receipts.

> **Don't run `npm run build` while `npm run dev` is live against the same
> `.next/` directory** — it corrupts the webpack module cache. Stop the dev
> server first, or use a separate checkout.

### Useful commands

```bash
make build / run / test / lint / migrate-up / migrate-down   # backend, from repo root
go test ./... -race                                          # full backend suite;
                                                               # integration tests need
                                                               # Docker and skip gracefully
                                                               # without it
npm run build / npm run lint                                  # frontend, from frontend/
```

`make migrate-down` (and any migration command besides plain "up") needs the
separate `golang-migrate` CLI:

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1
```

Local dev Postgres binds host port **5433**, not 5432 (avoids clashing with
a native Postgres install some machines already run on 5432 — the
container's internal port is still 5432). Remap freely in
`docker-compose.yml` + `.env` if that's not a concern on your machine.

## Environment variables

**Backend** (`.env` — see `.env.example`):

| Var                    | Default (dev)           | Purpose                                                                                 |
| ---------------------- | ----------------------- | --------------------------------------------------------------------------------------- |
| `PORT`                 | `8080`                  | HTTP listen port                                                                        |
| `APP_ENV`              | `development`           | `development` \| `production` (switches structured logging to JSON)                     |
| `DATABASE_URL`         | — (required)            | pgx pool DSN, e.g. `postgres://convoy:convoy@localhost:5433/convoychat?sslmode=disable` |
| `REDIS_URL`            | — (required)            | e.g. `redis://localhost:6379/0`                                                         |
| `JWT_SECRET`           | — (required)            | HS256 signing secret, 32+ chars; server refuses to boot without it                      |
| `JWT_TTL`              | `15m`                   | Access-token lifetime — short, since refresh tokens cover staying signed in beyond it   |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:3000` | Comma-separated; also gates the WebSocket `Origin` check                                |
| `MIGRATIONS_PATH`      | `migrations`            | Only read by `-migrate` mode; relative to the working directory                         |

**Frontend** (`frontend/.env.local` — see `frontend/.env.example`):

| Var                   | Default (dev)            | Purpose                                                                                                                     |
| --------------------- | ------------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| `NEXT_PUBLIC_API_URL` | `http://localhost:8080`  | REST base URL                                                                                                               |
| `NEXT_PUBLIC_WS_URL`  | `ws://localhost:8080/ws` | WebSocket URL (`wss://` in production — a browser on an `https://` page blocks a plain `ws://` connection as mixed content) |

Production values for both, plus the managed Postgres/Redis and Vercel
setup, are in [DEPLOYMENT.md](DEPLOYMENT.md).

## REST API

Base path `/api/v1` unless noted. Every error response uses one JSON shape:
`{"error": {"code": "...", "message": "..."}}` with codes `invalid_input`
(400), `unauthorized` (401), `forbidden` (403), `not_found` (404),
`conflict` (409), `internal_error` (500).

| Method | Path                                      | Auth                            | Notes                                                                                                                                                                                                                                                                  |
| ------ | ----------------------------------------- | ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| POST   | `/auth/signup`                            | none                            | 201 `{token, refresh_token, user}`; 400 `invalid_input`, 409 `conflict` (username/email taken)                                                                                                                                                                         |
| POST   | `/auth/login`                             | none                            | 200 `{token, refresh_token, user}`; 401 `unauthorized` (identical message for bad email or bad password — no user enumeration)                                                                                                                                         |
| POST   | `/auth/refresh`                           | refresh token (body), no Bearer | `{refresh_token}` → 200 `{token, refresh_token, user}`; rotates (old token revoked, new one issued in the same session family); 401 on a bogus/expired/already-rotated-out token — replaying an already-rotated-out token also revokes every other token in its family |
| POST   | `/auth/logout`                            | Bearer JWT                      | `{refresh_token}` → 200 `{"status":"logged_out"}`; revokes the presented token's whole session family; a missing/unknown/already-revoked token is a no-op 200, not an error                                                                                            |
| GET    | `/users/{user_id}`                        | Bearer JWT                      | 200 user; 400 (bad UUID), 404                                                                                                                                                                                                                                          |
| POST   | `/rooms`                                  | Bearer JWT                      | `{"type":"channel","name","description"}` → 201; `{"type":"direct","peer_user_id"}` → 201 if new, 200 if it already existed (deduped per user pair); `{"type":"group","name","description","member_ids":[...]}` → 201, ≥2 `member_ids` required, always private        |
| GET    | `/rooms`                                  | Bearer JWT                      | rooms the caller actively belongs to                                                                                                                                                                                                                                   |
| GET    | `/rooms/{room_id}`                        | Bearer JWT                      | room + embedded `members[]`; 403 if not an active member and not a system admin (also covers a nonexistent room, so room IDs can't be enumerated)                                                                                                                      |
| GET    | `/rooms/{room_id}/members`                | Bearer JWT                      | same 403 rule as above                                                                                                                                                                                                                                                 |
| POST   | `/rooms/{room_id}/invite`                 | Bearer JWT, admin only          | `{"user_id"}`; 403 non-admin (every caller on a `direct` room, by design — a DM has no admin), 404 unknown user, 409 already an active member                                                                                                                          |
| POST   | `/rooms/{room_id}/leave`                  | Bearer JWT                      | 200 `{"status":"left"}`; 404 if not currently a member; publishes `user.left` live and runs admin succession if the leaver was the room's last admin                                                                                                                   |
| PATCH  | `/rooms/{room_id}/members/{user_id}/role` | Bearer JWT, admin only          | `{"role":"admin"\|"member"}` → 200; idempotent; publishes `member.role_changed` live; 404 non-member target, 409 demoting the room's last admin                                                                                                                        |
| DELETE | `/rooms/{room_id}/members/{user_id}`      | Bearer JWT, admin only          | 200 `{"status":"removed"}` — kicks the target; publishes `user.left` live; 400 on self-removal (use `.../leave`), 404 non-member target                                                                                                                                |
| GET    | `/rooms/{room_id}/messages`               | Bearer JWT                      | `?limit=50&before=<created_at>` keyset pagination, newest-first; each message embeds `read_by[]` and `reactions[]` (grouped by emoji); 403 if not a member                                                                                                             |
| POST   | `/rooms/{room_id}/messages`               | Bearer JWT                      | `{"content","message_type"?}` → 201; REST fallback send used when the WebSocket is down; optional `Idempotency-Key` header, 409 on reuse within 5 minutes                                                                                                              |
| PATCH  | `/messages/{message_id}`                  | Bearer JWT                      | `{"content"}` → 200 `{id, room_id, content, edited_at}`; **author-only, no admin override**; publishes `message.edited` live over WebSocket; 404 if nonexistent/already deleted, 403 if not the author                                                                 |
| DELETE | `/messages/{message_id}`                  | Bearer JWT                      | 200 `{"status":"deleted"}`; author, room admin, or system admin; soft-delete (`deleted_at` set, `content` nulled in the API response, never removed from the row); 404 if already deleted                                                                              |
| GET    | `/admin/rooms`                            | Bearer JWT, system admin only   | `?limit=&offset=` → every room in the system regardless of the caller's own membership; 403 if not a system admin                                                                                                                                                      |
| GET    | `/admin/presence`                         | Bearer JWT, system admin only   | every registered user's current presence status, defaulting `offline`; 403 if not a system admin                                                                                                                                                                       |
| POST   | `/messages/{message_id}/reactions`        | Bearer JWT                      | `{"emoji"}` toggles: 201 `added` / 200 `removed`; publishes `message.reaction` live over WebSocket; 404 if the message is nonexistent or deleted                                                                                                                       |
| GET    | `/health`                                 | none                            | `{"postgres":"ok","redis":"ok"}`, 503 if either dependency is down                                                                                                                                                                                                     |

## WebSocket API

Connect: `GET /ws?token=<JWT>` — the token is validated **before** the
upgrade (invalid/missing → HTTP 401, standard error envelope). `Origin` is
checked against `CORS_ALLOWED_ORIGINS`; clients that send no `Origin` header
(native apps, `websocat`, curl) are allowed.

### Client → server

```jsonc
{ "type": "room.join",       "room_id": "<uuid>" }
{ "type": "room.leave",      "room_id": "<uuid>" }
{ "type": "message.send",    "room_id": "<uuid>", "content": "Hello!", "message_type": "text", "client_id": "<uuid>" }
{ "type": "typing.start",    "room_id": "<uuid>" }
{ "type": "typing.stop",     "room_id": "<uuid>" }
{ "type": "message.read",    "message_id": "<uuid>" }
{ "type": "presence.update", "status": "online" | "away" | "offline" }
```

### Server → client

```jsonc
{ "type": "message.new", "message": {
    "id": "<uuid>", "room_id": "<uuid>",
    "user": { "id": "<uuid>", "username": "john_doe", "avatar_url": "..." },
    "content": "Hello!", "created_at": "2026-07-05T15:30:00Z", "read_by": [],
    "client_id": "<uuid, echoed only when message.send carried one>" } }

{ "type": "user.joined",         "user": { "id": "<uuid>", "username": "john_doe" }, "room_id": "<uuid>" }
{ "type": "user.left",           "user_id": "<uuid>", "room_id": "<uuid>" }
{ "type": "user.typing",         "user_id": "<uuid>", "room_id": "<uuid>", "is_typing": true }
{ "type": "user.status_changed", "user_id": "<uuid>", "status": "online", "last_seen_at": "2026-07-05T15:35:00Z" }
{ "type": "message.read_by",     "message_id": "<uuid>", "read_by_user_id": "<uuid>" }
{ "type": "message.reaction",    "message_id": "<uuid>", "user_id": "<uuid>", "emoji": "👍", "action": "added" | "removed" }
{ "type": "message.edited",      "id": "<uuid>", "room_id": "<uuid>", "content": "edited text", "edited_at": "2026-07-10T15:35:00Z" }
{ "type": "member.role_changed", "room_id": "<uuid>", "user_id": "<uuid>", "role": "admin" | "member" }
{ "type": "error",               "code": "...", "message": "..." }
```

**Notes for client authors:**

- `client_id` on `message.send` is an opaque, client-generated nonce the
  server never interprets — it's echoed back verbatim on the resulting
  `message.new` (omitted if unsent) so the sender can reconcile its own
  optimistic UI against the broadcast, which carries the real database ID.
  Other clients ignore an id they don't recognize.
- A dropped connection (crash, network loss, tab closed uncleanly)
  synthesizes `user.left` for every room it had joined, same as an explicit
  `room.leave` — detected via a 60s read-deadline if the close was never
  seen.
- `message.read`/reactions carry no `room_id`; resolve the room from the
  message itself if you need it (see `message.read_by`/`message.reaction`).
- Reactions are REST-only to send (`POST .../reactions`), but broadcast live
  over the socket on success — there's no `message.*reaction*` client→server
  event.
- `room.join`, `message.send`, and `typing.start` gate on active room
  membership (`error{code:"forbidden"}` otherwise); `room.leave` and
  `typing.stop` are always allowed.

## Testing

```bash
go test ./...          # unit tests, colocated *_test.go
go test ./... -race    # how the suite is verified; integration tests spin up
                        # real Postgres+Redis testcontainers and skip
                        # gracefully if Docker isn't available
golangci-lint run       # internal/testutil.NewStore(t) gives each integration
                        # test a fresh, isolated Postgres+Redis pair
```

`cmd/api` integration tests build the real router (`newRouter`, the same
function `main` uses) and drive real WebSocket clients over `httptest`, so
the hub/broker/auth stack is exercised end-to-end, not mocked.

## Deployment

See [DEPLOYMENT.md](DEPLOYMENT.md) for the full Render (backend) + Vercel
(frontend) deploy walkthrough, local prod-compose rehearsal, and the
production-readiness checklist (secrets, HTTPS/WSS, connection limits,
backups, rate limiting, graceful shutdown).

## Known limitations / v2

- File uploads was considered and decided against, not merely deferred —
  ConvoyChat stays a pure text/reaction/read-receipt chat app; the schema's
  readiness for it (`message_type` `image`/`file`, `messages.metadata`
  JSONB) is left in place but unused, same as the still-unused `guest` role.
- A newly-promoted system admin (`./api -promote-admin <email>`) only sees
  the dashboard after logging out and back in — `is_system_admin` comes from
  the login/signup response and isn't live-refetched mid-session. A minor,
  accepted rough edge, not a bug.
- Both the access and refresh JWT/token live in `localStorage`, not an
  httpOnly cookie, so the WebSocket handshake can authenticate via `?token=`.
  This is an accepted, documented tradeoff (XSS-readable tokens) rather than
  an oversight — the refresh token's rotation-with-reuse-detection design
  (see `plan.md`) bounds how long a stolen one stays useful, rather than
  preventing theft outright.

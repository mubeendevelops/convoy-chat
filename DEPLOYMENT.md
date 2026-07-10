# Deploying ConvoyChat

Target stack: **backend on Render** (Dockerfile-based web service + managed
Postgres + managed Redis/Key-Value), **frontend on Vercel**. This document
covers the deploy mechanics — connecting the services, required env vars, and
how to rehearse the whole thing locally before pushing anything live.

The [Production readiness checklist](#production-readiness-checklist) at the
end of this file covers secrets, HTTPS/WSS, connection limits, Redis
persistence, DB backups, graceful shutdown, and rate limiting — the "is it
safe to leave running" pass on top of "how to deploy" above it.

## Backend — Render

Render builds `Dockerfile` directly (root of the repo) — it does not read
`docker-compose.prod.yml`; that file is for local rehearsal / self-host, not
what Render itself runs. Steps:

1. **New Web Service** → connect this repo → environment **Docker** (Render
   auto-detects the root `Dockerfile`, no build command needed).
2. **Provision managed Postgres** (Render Postgres) and a **managed
   Redis-compatible store** (named "Redis" or "Key Value" depending on your
   dashboard — Render has renamed this offering before, so check what's
   current in yours) in the same region as the web service, for lowest
   latency and no egress cost. Use the **internal** connection strings Render
   gives you for both — they're only reachable from your other Render
   services, which is what you want.
3. **Health Check Path**: `/health` (matches `GET /health` — see CLAUDE.md's
   REST endpoints table; returns 503 if either dependency is down, so Render
   won't route traffic to an instance that can't reach its own DB/cache).
4. **Pre-Deploy Command**: `./api -migrate` — runs the same built image in
   migration-only mode (see `cmd/api/migrate.go`) before the new version
   receives traffic. This is the init-step migration strategy (see CLAUDE.md
   /  plan.md Phase 16 for the rationale); Render runs it once per deploy,
   using the exact image about to go live, so a bad migration fails the
   deploy instead of a boot loop across every server instance.
5. **Environment variables** (Render dashboard, or an Environment Group
   shared with the Pre-Deploy Command):

   | Var | Value |
   |---|---|
   | `DATABASE_URL` | Render Postgres' internal connection string. If it doesn't already include one, append `?sslmode=require`. |
   | `REDIS_URL` | Render's managed Redis/Key-Value internal connection string. |
   | `JWT_SECRET` | A real 32+ char secret — `openssl rand -base64 48`. Never reuse the dev value. |
   | `JWT_TTL` | `15m` (or omit — that's the default). Refresh tokens (Phase 3) cover staying signed in beyond this; see the secrets-rotation note below. |
   | `APP_ENV` | `production` (switches `cmd/api/logging.go` to the JSON slog handler). |
   | `CORS_ALLOWED_ORIGINS` | Your Vercel production domain, e.g. `https://convoychat.vercel.app`. Comma-separate if you need more than one (see the Vercel preview-deployments note below). |

   `PORT` is injected by Render automatically — `internal/config` already
   reads it from the environment with an 8080 fallback, so nothing to set.

Render's own health checks and log stream cover the rest; there's no
Dockerfile `HEALTHCHECK` equivalent needed on Render specifically (that
instruction is for `docker-compose.prod.yml` / plain `docker run`).

**Granting the first system admin** (Phase 3, post-v1): there's no REST
endpoint for this by design — sign up a real account through the deployed
app first, then run the same built image in one-shot admin-grant mode via
Render's **Shell** tab (or a one-off Job, if your plan has them):
`./api -promote-admin you@example.com`. It needs `DATABASE_URL`/`REDIS_URL`
in its environment same as the server itself, connects to both, and exits —
no traffic is served. Log out and back into the web app afterward; the
`is_system_admin` flag comes from the login response, not a live mid-session
refetch.

## Frontend — Vercel

1. **New Project** → import this repo → set **Root Directory** to
   `frontend` (this is a monorepo — easy to miss, and the build fails
   immediately without it).
2. Framework preset: Next.js (auto-detected).
3. **Environment variables** (Vercel project settings):

   | Var | Value |
   |---|---|
   | `NEXT_PUBLIC_API_URL` | `https://<your-render-service>.onrender.com` (no trailing slash — matches the dev convention in `frontend/.env.example`) |
   | `NEXT_PUBLIC_WS_URL` | `wss://<your-render-service>.onrender.com/ws` — **`wss://`, not `ws://`**: Render terminates TLS, and a browser on an `https://` page will block a plain `ws://` connection as mixed content. |

4. `npm run build` (`next build`) is what Vercel runs — verified locally
   clean as part of this phase (see plan.md Phase 16).

**Known limitation (flagged, not fixed here):** Vercel preview deployments
get a random `*.vercel.app` URL per branch/PR, which won't be in
`CORS_ALLOWED_ORIGINS` unless added — previews can't reach the backend
(REST calls fail CORS preflight, WS handshake gets the WS `originChecker`'s
403) until their exact preview URL is added to the backend's env var. Fine
for a single production frontend origin; revisit (e.g. a wildcard
subdomain check in `originChecker`/CORS config) if preview-environment API
access becomes a real workflow need.

## Local rehearsal — `docker-compose.prod.yml`

Builds and runs backend + Postgres + Redis exactly as they'd run in any
Docker-based environment (self-host, Railway, Fly, or just a pre-push smoke
test) — see CLAUDE.md's Commands section for the exact command sequence.
Not what Render runs (Render builds the Dockerfile directly against its own
managed Postgres/Redis), but the closest thing to it you can run on your own
machine, and it's how the init-step migration strategy above was verified
end-to-end (health check, signup, login, full test suite + lint, all against
the real container image) before ever touching Render.

## Production readiness checklist

Checked items are already true today, verified against the current code as
of this pass (2026-07-09) — not aspirational. Unchecked items are real gaps
or platform settings you need to make a call on before real user data is on
the line; each one says exactly what to do about it.

### Secrets management

- [x] `.env` and `.env.prod` are gitignored and have never been committed
      (`.gitignore` blocks `.env`/`.env.*`, allow-listing only
      `.env.example`/`.env.prod.example`; confirmed clean via `git
      ls-files`) — only the templates are in version control.
- [x] No secret is hardcoded anywhere in source — `JWT_SECRET`,
      `DATABASE_URL`, `REDIS_URL` are all read from the environment
      (`internal/config`), and the server refuses to boot without them.
- [ ] Generate a real `JWT_SECRET` for production (`openssl rand -base64
      48`) and set it only in Render's environment variables / an
      Environment Group — never the `.env.example` placeholder. Set it once
      per environment (staging vs. production should not share a secret).
- [ ] For `docker-compose.prod.yml` specifically: fill real
      `POSTGRES_PASSWORD` / `REDIS_PASSWORD` / `JWT_SECRET` into `.env.prod`
      before running anything beyond a local smoke test (the example file
      ships placeholder values, same spirit as the dev `.env.example`).
- [x] Know your rotation story: as of Phase 3 (refresh tokens), rotating
      `JWT_SECRET` invalidates every *access* token immediately, but each
      client transparently gets a new one via its still-valid refresh token
      (`POST /auth/refresh`, `lib/api.ts`'s 401-retry interceptor) — no
      forced re-login. What rotating `JWT_SECRET` does *not* do is revoke
      refresh tokens themselves (they're opaque, hashed, stored in
      Postgres — independent of the JWT secret); if the incident requires
      killing every session outright, that needs a bulk
      `UPDATE refresh_tokens SET revoked_at = NOW() WHERE revoked_at IS
      NULL` (no endpoint for this yet — a real gap if "kill all sessions"
      is ever needed under incident pressure, not just "rotate the
      secret").

### HTTPS / WSS

- [x] Both platform edges already terminate TLS for you — Render for the
      backend, Vercel for the frontend. The Go server never sees or
      handles raw TLS; nothing in-app needs to change for this.
- [ ] Confirm `NEXT_PUBLIC_WS_URL` is `wss://`, not `ws://`, in the Vercel
      production environment (see Frontend — Vercel above) — a browser on
      an `https://` page silently blocks a plain `ws://` connection as
      mixed content, and the failure mode looks like "the socket never
      connects," not an obvious error.
- [ ] Confirm `CORS_ALLOWED_ORIGINS` lists the exact `https://` production
      domain. The same list gates both REST CORS (`cors.Handler` in
      `cmd/api/router.go`) and the WebSocket handshake's `Origin` check
      (`originChecker` in `internal/websocket/server.go`) — one env var,
      two enforcement points, so getting it right here covers both.

### Connection limits

- [ ] `internal/store/postgres.go`'s pgxpool has no explicit `MaxConns` set
      — it takes pgx's built-in default (a small number derived from CPU
      count). Before running more than one backend instance (see
      Multi-server QA in plan.md), set `pool_max_conns` explicitly — either
      as a `DATABASE_URL` query param, which `pgxpool.ParseConfig` reads
      directly (`...?sslmode=require&pool_max_conns=10`), or via
      `pgxpool.Config.MaxConns` in code. **Total connections = replica
      count × pool size per replica** — easy to blow through a smaller
      Postgres plan's connection cap once you scale out horizontally,
      which is the whole point of this app's multi-server design. Check
      your specific Render Postgres plan's actual cap in its dashboard
      before picking a number.
- [ ] Same shape of risk on `internal/store/redis.go`'s client (no explicit
      `PoolSize`, so it takes go-redis's default, also CPU-derived) — lower
      stakes here since this app's Redis load is light (presence keys,
      Pub/Sub, idempotency keys), but worth a glance at your Key Value
      plan's connection cap if you scale to several instances.
- [x] `ReadHeaderTimeout: 5s` is already set on the `http.Server`
      (`cmd/api/main.go`) — bounds a slow/stalled client from holding a
      connection open indefinitely.

### Redis persistence

- [x] **Not actually needed for correctness in this app, by design** —
      worth confirming *why* rather than defaulting to "just turn it on."
      Everything Redis holds is ephemeral on purpose: presence keys
      (`presence:conns:*` / `presence:status:*`) are TTL'd and self-heal
      within one 15s heartbeat cycle, idempotency keys (`idempotency:message:*`)
      guard only a 5-minute retry window, and Pub/Sub traffic is never
      persisted by Redis regardless of settings. Postgres is the only
      durable store. A Redis restart with zero persistence never loses a
      message, room, or user — worst case, every user's presence resets to
      "not yet heard from" for a few seconds, and a client retrying within
      seconds of the restart could theoretically double-send (narrow,
      low-stakes).
- [ ] So: Render's free-tier Key Value (no persistence at all) is
      architecturally fine for this app specifically. If you still want
      it, paid plans default persistence on with a choice of
      Journal+Snapshot (loses ≤1s of writes on restart), Snapshot-only
      (loses everything since the last snapshot), or Off — pick it for a
      smoother restart experience (no presence blip), not for durability
      this app doesn't need from Redis in the first place.

### DB backups

- [ ] Render's **free** Postgres tier has no automatic backups at all —
      fine for a throwaway demo, not once real user accounts/messages
      exist.
- [ ] Paid plans back up continuously: **Hobby** gives a 3-day
      point-in-time-recovery window, **Pro and above** 7 days; logical
      backups are retained 7 days regardless of plan tier. Pick at least
      Hobby before real users sign up.
- [ ] Know the restore mechanic before you need it under pressure: PITR
      spins up a *new* database instance reflecting the chosen point in
      time, so you can validate it before cutting anything over — it does
      not rewrite your live instance in place.
- [ ] Optional extra redundancy: `pg_dump` against the external connection
      string, scripted to somewhere off-Render (e.g. S3), if you want a
      copy outside Render's own backup system too.

### Graceful shutdown

- [x] Already implemented and verified (Phase 1, re-verified Phase 16):
      `cmd/api/main.go` cancels a context on SIGINT/SIGTERM, calls
      `http.Server.Shutdown` with a 10s timeout — in-flight REST requests
      finish, new ones are refused — then closes the Postgres pool and
      Redis client via `defer st.Close()`. The WS Hub/Broker/presence
      goroutines stop on the same context (`websocket.Server.Run(ctx)`).
      Test this against the built binary, not `go run` — `go run` doesn't
      forward signals to its child process (see README).
- [ ] **Known gap, worth knowing rather than assuming away:** shutdown
      does not send a clean close frame to connected WebSocket clients
      first. `Hub.Run`'s own doc comment says as much — "in-flight
      connections are ... torn down by their own pumps as the process
      exits" — because `http.Server.Shutdown` explicitly does not wait for
      hijacked connections (what an upgraded WebSocket becomes) the way it
      waits for ordinary HTTP requests. In practice this is low-stakes: a
      deploy looks like a dropped connection to a connected client, and
      the frontend's existing reconnect-with-backoff
      (`hooks/useWebSocket.tsx`) already treats that as a normal case —
      but every connected user gets one reconnect blip per deploy, not a
      silent handoff. Render's own rollout sequencing (new instance
      healthy on `/health` before the old one stops taking traffic) bounds
      how bad this is; there's no window where a request is dropped on
      the floor, just the WS-reconnect blip.

### Rate limiting (auth + message endpoints)

- [ ] **Not implemented — a real gap, flagged rather than glossed over.**
      `cmd/api/router.go` has no rate-limiting middleware today (no code,
      no dependency pulled in for it), so `POST /api/v1/auth/signup`,
      `POST /api/v1/auth/login`, and `POST
      /api/v1/rooms/{room_id}/messages` are all unprotected —
      brute-force/credential-stuffing risk on the first two, spam risk on
      the third.
- [ ] Recommended fix: [`github.com/go-chi/httprate`](https://github.com/go-chi/httprate)
      — same maintainer family as `go-chi/chi` and `go-chi/cors`, already
      in this stack, no new pattern to learn. Apply it narrowly rather
      than router-wide:
      - Wrap `/auth/signup` and `/auth/login` with something like
        `httprate.LimitByIP(5, time.Minute)` — brute-force protection
        matters most here.
      - Wrap `POST /rooms/{room_id}/messages` — the REST *fallback* send
        path only; normal sends go over the WebSocket as `message.send`
        and never hit this route — with a looser limit keyed by the
        authenticated user ID rather than IP.
- [ ] The WebSocket send path (`message.send`) has no per-message rate
      limit either. The Hub's existing backpressure (drop-on-full-buffer,
      see CLAUDE.md's Hub concurrency conventions) protects the *server*
      from being overwhelmed, but doesn't stop one client from spamming a
      room with messages. Out of scope for a "basic" pass — worth
      revisiting if abuse turns out to be a real problem.

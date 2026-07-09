# Deploying ConvoyChat

Target stack: **backend on Render** (Dockerfile-based web service + managed
Postgres + managed Redis/Key-Value), **frontend on Vercel**. This document
covers the deploy mechanics — connecting the services, required env vars, and
how to rehearse the whole thing locally before pushing anything live.

Phase 17 (see plan.md) adds the production-readiness checklist on top of
this: secrets rotation, HTTPS/WSS confirmation, rate limiting, DB backups,
Redis persistence, connection limits. This file covers *how to deploy*; that
pass covers *is it safe to leave running*.

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
   | `JWT_TTL` | `24h` (or omit — that's the default). |
   | `APP_ENV` | `production` (switches `cmd/api/logging.go` to the JSON slog handler). |
   | `CORS_ALLOWED_ORIGINS` | Your Vercel production domain, e.g. `https://convoychat.vercel.app`. Comma-separate if you need more than one (see the Vercel preview-deployments note below). |

   `PORT` is injected by Render automatically — `internal/config` already
   reads it from the environment with an 8080 fallback, so nothing to set.

Render's own health checks and log stream cover the rest; there's no
Dockerfile `HEALTHCHECK` equivalent needed on Render specifically (that
instruction is for `docker-compose.prod.yml` / plain `docker run`).

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

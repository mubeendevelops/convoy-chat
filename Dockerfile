# syntax=docker/dockerfile:1

# ConvoyChat backend — multi-stage build. Produces a single small image that
# serves the API by default; the same image doubles as the migration-on-deploy
# init step via `-migrate` (see cmd/api/migrate.go and CLAUDE.md's
# migration-on-deploy strategy) so there's exactly one artifact to build,
# scan, and deploy — never a second image just for running migrations.

# ---- deps: cached separately so `go mod download` only reruns when go.mod/
# go.sum actually change, not on every source edit -----------------------
FROM golang:1.25.4-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# ---- build ---------------------------------------------------------------
FROM deps AS build
WORKDIR /src
COPY cmd ./cmd
COPY internal ./internal
# CGO_ENABLED=0: every dependency here (pgx, go-redis, bcrypt, gorilla/
# websocket, jwt) is pure Go, so a static binary needs no libc from the final
# image. -trimpath + -s -w drop local build paths and debug symbols, which
# are dead weight in a shipped image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- final -----------------------------------------------------------------
# Plain alpine (not scratch/distroless): the final image still needs a shell
# user for the non-root account and a way to self-check /health from
# docker-compose.prod.yml's healthcheck — alpine ships wget via busybox
# already, so the only added package is ca-certificates (needed to verify
# TLS certs when DATABASE_URL/REDIS_URL point at a managed Postgres/Redis
# provider, e.g. Render's, over TLS). Costs a few MB over distroless in
# exchange for not needing a second Go binary just to health-check the first.
FROM alpine:3.22 AS final
RUN apk add --no-cache ca-certificates \
    && addgroup -S convoy \
    && adduser -S -G convoy -H -h /app convoy

WORKDIR /app
COPY --from=build --chown=convoy:convoy /out/api ./api
COPY --chown=convoy:convoy migrations ./migrations

USER convoy
EXPOSE 8080

# Platform-native health checks (Render's dashboard health-check path, Fly's
# fly.toml, Railway's settings) don't read this — it's for docker-compose.prod.yml
# and anyone running the image directly with plain `docker run`.
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["./api"]

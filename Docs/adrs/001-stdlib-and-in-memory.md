# ADR 001 — Stdlib HTTP and in-memory store

## Status

Accepted (amended 2026-08-28: Docker is packaging only)

## Context

`fake-pix-provider` is a portfolio demo of a synthetic PIX PSP: create a charge,
simulate a terminal event, and POST the AcmePay-signed webhook. The goal is to
show Go concurrency, HMAC, and retry classification — not to operate
infrastructure.

A router (Gin / Fiber / Chi) or Postgres would pull the v1 recorte toward a
product rather than a readable stdlib service.

## Decision

- Go 1.23 **stdlib only**: `net/http` ServeMux (method+path patterns),
  `crypto/hmac`, `sync.Mutex`. No third-party HTTP framework, ORM, or queue.
- Charges live in an in-memory `MemoryStore`. Process exit drops all data.
- Clock, `http.Client`, and retry sleep are injected so HMAC window and retry
  tests do not wait on wall-clock.
- **Docker (fase 3) is packaging**, not a stack change: multi-stage image +
  compose so wallet stacks can `up` the binary. The process inside the
  container is the same stdlib server and `MemoryStore`. Adding Docker does
  **not** authorize Gin, Postgres, Redis, or an outbox (those stay closed
  until a later ADR / fase 4).

## Consequences

- Restart (process or container) is a full reset; there is no outbox and no
  durable delivery log. `GET /v1/charges/by-payment/{id}` 404s after restart.
- `simulate` can claim an event once under the mutex; the race loser returns
  `already_simulated` and must not POST again.
- Tests use `httptest` and `go test ./...`. CI is `gofmt -l` plus that suite.
  An optional `docker build` job only proves the image compiles.
- Wallet APIs call this process over HTTP. When this process runs in Docker,
  `callback_url` must be a name the **container** can resolve (Sail
  `laravel.test`, Nest `api` / `host.docker.internal`) — not `localhost` on
  the operator's host.

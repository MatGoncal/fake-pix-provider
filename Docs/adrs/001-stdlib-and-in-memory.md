# ADR 001 — Stdlib HTTP and in-memory store

## Status

Accepted (amended 2026-08-28: Docker is packaging only; amended 2026-08-28:
Postgres + outbox for demo, MemoryStore for tests)

## Context

`fake-pix-provider` is a portfolio demo of a synthetic PIX PSP: create a charge,
simulate a terminal event, and POST the AcmePay-signed webhook. The goal is to
show Go concurrency, HMAC, and retry classification — not to operate
infrastructure.

A router (Gin / Fiber / Chi) would pull the v1 recorte toward a product rather
than a readable stdlib service. Fase 3 still used `MemoryStore` inside Docker:
restart dropped charges and in-flight webhooks. Fase 4 needs durability for
the demo without turning this into an ORM/queue product.

## Decision

- Go 1.23 **stdlib HTTP**: `net/http` ServeMux (method+path patterns),
  `crypto/hmac`. No third-party HTTP framework. Clock, `http.Client`, and
  retry sleep stay injected so HMAC window and retry tests do not wait on
  wall-clock.
- **Two stores**:
  - `MemoryStore` + `sync.Mutex` — default when `DATABASE_URL` is unset.
    Unit tests (`go test` + `httptest`) always use this. Process exit drops
    data.
  - `PostgresStore` — demo / Docker when `DATABASE_URL` is set. `database/sql`
    plus **one** Postgres driver (`lib/pq`). Schema is `CREATE TABLE IF
    NOT EXISTS` on boot. No ORM.
- **Outbox (fase 4)**: `ClaimSimulate` marks the charge terminal and inserts
  `outbox_events` in the **same transaction**. An in-process ticker poller
  POSTs the webhook (same retry classification as `internal/deliver`). No
  Redis, no external queue. Replay `already_simulated` does not enqueue again.
- **Docker (fase 3) is packaging**: multi-stage image + compose. Adding Docker
  does not authorize Gin or Redis. Postgres in compose is the fase 4 demo
  store, not a license to add frameworks.

## Consequences

- Restart of the Go container with Postgres: `GET /v1/charges/by-payment/{id}`
  still 200; `simulate` on the old id still works; an undelivered outbox row
  is retried on boot.
- Without `DATABASE_URL`, restart is still a full reset (tests / `go run`
  without compose).
- `simulate` can claim an event once (mutex or `SELECT FOR UPDATE`); the race
  loser returns `already_simulated` and must not POST again.
- Tests: default suite is `httptest` + `MemoryStore`. Postgres tests skip
  unless `TEST_DATABASE_URL` is set. CI `gofmt -l` + `go test`; optional
  `docker build`; fase 4 CI also runs a `postgres` service.
- Wallet APIs call this process over HTTP. `callback_url` must be a name the
  **container** can resolve. Duplicate webhook delivery after restart is
  handled by the wallet inbox unique `(provider, event_id)`.

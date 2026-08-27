# ADR 001 — Stdlib HTTP and in-memory store

## Status

Accepted

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

## Consequences

- Restart is a full reset; there is no outbox and no durable delivery log.
- `simulate` can claim an event once under the mutex; the race loser returns
  `already_simulated` and must not POST again.
- Tests use `httptest` and `go test ./...`. CI is `gofmt -l` plus that suite.
- Plugging the callback into Laravel/Nest is out of v1 (curl / `httptest` only).

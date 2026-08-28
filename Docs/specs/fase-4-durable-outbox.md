# Fase 4 — Durable store + outbox

## Contexto / Objetivo

Fase 3 packaged the binary; charges still live in `MemoryStore`. A container
restart 404s `GET /v1/charges/by-payment/{id}` and the wallet payment stays
`PENDING` with no charge to `simulate`. After `ClaimSimulate`, a process kill
mid-POST also drops the webhook: the in-process goroutine + retries never
resume.

This phase adds `PostgresStore` (demo / Docker) and an **outbox in the same
transaction as simulate**. Unit tests keep `MemoryStore` + `httptest`. Stdlib
`net/http` stays; one new dependency: `database/sql` + a Postgres driver.
No Redis, no external queue — the poller runs in this process.

Wallet APIs (Laravel / Nest) do **not** change: inbox `webhook_events` is
already unique on `(provider, event_id)`. Duplicate delivery after restart is
`200` / `1042` — expected.

## Endpoints (se aplicável)

No new partner/PSP routes. Existing surface must survive restart:

| Método | Rota | Auth | Descrição |
|--------|------|------|-----------|
| POST | `/v1/charges` | optional API key | UPSERT by `payment_id`; row still there after restart |
| GET | `/v1/charges/by-payment/{payment_id}` | optional API key | **200** after Go restart (was 404 with MemoryStore) |
| POST | `/v1/charges/{id}/simulate` | optional API key | Same TX: mark terminal + insert `outbox_events`. Replay `already_simulated` does **not** insert again |
| GET | `/health` | none | Liveness (unchanged; does not probe Postgres) |

## Request / Response

Unchanged from fases 1–2. Webhook JSON and HMAC `t=,v1=` unchanged. The
poller **re-signs** on each attempt so a delayed retry after restart still
passes the wallet timestamp window; `event_id` is stable.

## Fluxo (passo a passo)

1. If `DATABASE_URL` is set, `cmd/provider` opens `PostgresStore`, runs
   `CREATE TABLE IF NOT EXISTS` for `charges` and `outbox_events`, starts the
   in-process outbox poller, and **disables** the simulate goroutine.
   If unset, behavior is fase 3: `MemoryStore` + inline goroutine (tests).
2. `POST /v1/charges` → `CreateOrGet`: `INSERT ... ON CONFLICT (payment_id) DO NOTHING`
   then `SELECT` the winner. 201 / 200 unchanged.
3. `POST .../simulate` winner: **one Postgres transaction** updates the charge
   to `PAID`/`EXPIRED`/`FAILED` (`event_id`, `event_type`,
   `last_delivery_status=pending`) **and** inserts `outbox_events` (payload,
   `event_id`, `callback_url`, `attempts=0`, `next_attempt_at=now`).
   Replay (`event_id` already set) returns 200 `already_simulated` and does
   not insert a second outbox row.
4. Poller ticker: `SELECT ... FOR UPDATE SKIP LOCKED` where `delivered_at IS NULL`
   and `attempts < 5` and `next_attempt_at <= now`. One HTTP POST per row per
   tick (retry classification matches `internal/deliver`: 2xx stop; 4xx except
   429 permanent; 429 / 5xx / network retry). Max 5 attempts. Updates
   `charges.last_delivery_status`. Success sets `delivered_at`.
5. Kill the process mid-POST: on boot the poller re-sends the same payload /
   `event_id`. Wallet unique inbox accepts the duplicate.
6. Compose: Postgres **only for Go** (`fake_pix` DB, host port **5435** so it
   does not collide with Sail `5432` / Nest `5433` / Nest test `5434`).
   Laravel/Nest `fake-pix` services get `DATABASE_URL` pointing at that sibling.
7. CI `go test` without `TEST_DATABASE_URL` is MemoryStore + httptest (skip
   Postgres tests). CI gains a `postgres` service and sets `TEST_DATABASE_URL`.

## Códigos de erro

Unchanged HTTP mapping. Store/DB errors on create/get/simulate → **500**
`{"error":"internal"}` (not a new partner code).

| Código | Situação |
|--------|----------|
| 200 | Replay create / `already_simulated` (no new outbox row) |
| 201 | First create (row persisted) |
| 202 | Simulate claimed; delivery is the poller (or goroutine on MemoryStore) |
| 404 | Unknown charge / payment_id |
| 500 | Postgres unreachable / TX failure |

## Critérios de aceite

- [x] `DATABASE_URL` set → `PostgresStore` + poller; unset → `MemoryStore` + goroutine
- [x] Restart **before** simulate: `GET by-payment` still 200; simulate on the old id still works; wallet stays `PENDING` until simulate
- [x] Restart **after** simulate, webhook not yet delivered: poller delivers; payment `PAID` once (wallet inbox unique)
- [x] `ClaimSimulate` + outbox insert are the same TX; `already_simulated` does not insert again
- [x] Poller: same retry classification as today; max 5; updates `last_delivery_status`
- [x] Unit tests still use `MemoryStore` + httptest; no Docker required for that suite
- [x] Postgres tests behind `TEST_DATABASE_URL` (skip if unset); CI runs them with a `postgres` service
- [x] ADR 001 amended; `AGENTS.md` Do NOT no longer forbids Postgres / outbox; still forbids Gin / Redis / real PSP
- [x] Stdlib HTTP (no Gin); one Postgres driver via `database/sql`; no Redis / external queue
- [x] Laravel/Nest application code unchanged; compose passes `DATABASE_URL` into `fake-pix`

## Testes obrigatórios

- [x] Existing `go test ./...` (MemoryStore + httptest) green without Postgres
- [x] Restart: insert charge → new store (new connection) → `GetByPaymentID` finds it
- [x] Outbox: callback `httptest` down → 5xx → row still pending; then 200 → `delivered_at` set
- [x] Replay `ClaimSimulate` does not insert a second outbox row
- [x] Smoke README: restart Go, by-payment still finds, simulate pays (wallet inbox)

## Migrations

Auto-DDL on boot (`CREATE TABLE IF NOT EXISTS`), not a migration tool:

1. `charges` — PK `id`, unique `payment_id`, amount `bigint`, currency `char(3)`, nullable `event_id` / `event_type` / `last_delivery_status`
2. `outbox_events` — unique `event_id`, `payload`, `callback_url`, `attempts`, `next_attempt_at`, `delivered_at`, FK `charge_id`

## Variáveis de ambiente novas

| Var | Default | Descrição |
|-----|---------|-----------|
| `DATABASE_URL` | empty (`MemoryStore`) | Postgres DSN; when set, durable store + poller |
| `TEST_DATABASE_URL` | empty (skip PG tests) | CI / local Postgres test DSN |
| `OUTBOX_POLL_INTERVAL` | `200ms` | Poller ticker (demo-fast) |

Reuse fase 0 `PORT`, `WEBHOOK_SECRET`, `FAKE_PIX_API_KEY`. Compose host DB
port: `FORWARD_FAKE_PIX_DB_PORT` default `5435`.

## Dependências / Rollback

- Dependências: Postgres 18 (compose). Wallet stacks wire `DATABASE_URL` on
  the existing `fake-pix` service. Driver: `github.com/lib/pq` via
  `database/sql`.
- Rollback: unset `DATABASE_URL` (MemoryStore); drop compose Postgres
  service; restore ADR “no Postgres”.
- Out of scope: Gin, Redis, exposing `provider_charge_id` on partner JSON,
  EMV fallback, unifying Laravel+Nest compose, making wallet create without
  header idempotent.

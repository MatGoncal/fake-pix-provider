# AGENTS.md — fake-pix-provider

> Master index for humans and AI agents. Read this **before** any implementation.

## Project summary

Synthetic PIX PSP (Go) for the AcmePay portfolio. This service **is** the
provider: it creates a fake charge (EMV QR) and delivers a signed webhook to a
caller-supplied `callback_url`. It is **not** the partner API
(`POST /v1/payments`). HMAC + webhook JSON copy
`POST /v1/webhooks/payment` from the AcmePay contract.

Domain: personal skill `payments-domain`.

## Stack

| Layer | Choice |
|-------|--------|
| Runtime | Go 1.23 |
| HTTP | stdlib `net/http` (no Gin / Fiber / Chi) |
| Store | In-memory + `sync.Mutex` (process death = data loss) |
| Crypto | stdlib `crypto/hmac` SHA-256, header `t=<unix>,v1=<hex>` |
| Tests | `go test` + `httptest` |
| Lint | `gofmt -l` |

## Module map

| Package | Responsibility | Doc |
|---------|----------------|-----|
| `cmd/provider` | Process entrypoint | `Docs/specs/fase-0-bootstrap.md` |
| `internal/sign` | HMAC `t,v1` sign + verify | `Docs/specs/fase-1-charges-webhooks.md` |
| `internal/deliver` | HTTP POST, retry classification | `Docs/specs/fase-1-charges-webhooks.md` |
| `internal/store` | MemoryStore + CreateOrGet by `payment_id` | `Docs/specs/fase-1-charges-webhooks.md`, `Docs/specs/fase-2-idempotent-create.md` |
| `internal/httpapi` | mux + handlers (201 create / 200 replay) | `Docs/specs/fase-1-charges-webhooks.md`, `Docs/specs/fase-2-idempotent-create.md` |

## Entrypoints

| Path | Notes |
|------|-------|
| `GET /health` | Liveness |
| `POST /v1/charges` | Create `PENDING` charge + synthetic QR (**201** first; **200** replay by `payment_id`) |
| `GET /v1/charges/{id}` | Charge detail (`last_delivery_status` after simulate) |
| `GET /v1/charges/by-payment/{payment_id}` | Lookup by wallet `payment_id` (404 if missing) |
| `POST /v1/charges/{id}/simulate` | `paid` / `expired` / `failed` → async signed webhook |

## Quick lookup

| Want to understand… | See |
|---------------------|-----|
| Fase 0 bootstrap | `Docs/specs/fase-0-bootstrap.md` |
| Fase 1 charges + webhooks | `Docs/specs/fase-1-charges-webhooks.md` |
| Fase 2 idempotent create | `Docs/specs/fase-2-idempotent-create.md` |
| HMAC algorithm (Next twin) | `checkout-portal-next/lib/webhook-signature.ts` |
| AcmePay webhook contract | `pix-wallet-api/Docs/specs/API_CONTRACT.md` |
| ADRs | `Docs/adrs/` |

## Agent workflow (mandatory)

```
1. Read AGENTS.md
2. Read Docs/specs/<fase>.md
3. Implement in internal/* with injected clock / http.Client / sleep
4. go test ./... && test -z "$(gofmt -l .)"
5. If behavior changed → update the phase spec
6. PR with checklist below
```

**Spec without test does not close. Code without updating the spec does not close.**

## Build phases

| Fase | Scope | Doc |
|------|-------|-----|
| 0 | Spec-driven bootstrap + `go.mod` + CI | `Docs/specs/fase-0-bootstrap.md` |
| 1 | Charges + simulate + signed webhook delivery | `Docs/specs/fase-1-charges-webhooks.md` |
| 2 | Idempotent create by `payment_id` (200 replay / 201 create) | `Docs/specs/fase-2-idempotent-create.md` |

## Do NOT

- Use `float`/`float64` for money — integer minor units only
- Add Gin, Fiber, Chi, or another HTTP router
- Add Postgres, Redis, Docker, or an outbox
- Call a real PSP
- Change `pix-wallet-api`, `payment-api-nest`, Vue, or Next from this repo
  (wallet APIs call this process; wiring lives there)
- Copy StarsPay production code or secrets
- Invent a partner-API shape (`/v1/payments`) — this is the PSP surface

## Naming

- Packages: short nouns (`sign`, `deliver`, `store`, `httpapi`)
- Amounts: `int64` minor units; currency ISO 4217 `BRL`
- Webhook `provider` JSON field: `fake_pix`
- Signature header: `X-AcmePay-Signature`

## PR checklist

- [ ] Spec in `Docs/specs/` updated (acceptance criteria checked)
- [ ] `go test ./...` green
- [ ] `gofmt -l` empty
- [ ] No floats for money
- [ ] Retries reuse the same `event_id`
- [ ] Commits small and English

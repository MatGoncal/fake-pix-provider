# fake-pix-provider

Synthetic PIX PSP in Go. This process **is** the provider: it creates a fake
EMV charge and, on `simulate`, POSTs the canonical AcmePay webhook
(`provider=fake_pix`) with Stripe-style `t=<unix>,v1=<hex>` HMAC.

It is **not** the partner API (`POST /v1/payments`). Callers invent `payment_id`
(the UUID AcmePay expects on `POST /v1/webhooks/payment`). Stdlib HTTP.
Docker / compose uses **Postgres + an in-process outbox** so restart keeps
charges and retries undelivered webhooks. `go test` and `go run` without
`DATABASE_URL` still use the in-memory store.

Fictional portfolio project. No real PSP.

Synthetic PIX PSP in Go. This process **is** the provider: it creates a fake
EMV charge and, on `simulate`, POSTs the canonical AcmePay webhook
(`provider=fake_pix`) with Stripe-style `t=<unix>,v1=<hex>` HMAC.

It is **not** the partner API (`POST /v1/payments`). Callers invent `payment_id`
(the UUID AcmePay expects on `POST /v1/webhooks/payment`). Stdlib HTTP;
in-memory store — **container restart drops all charges**.

Fictional portfolio project. No real PSP.

## Architecture

```mermaid
flowchart LR
  Wallet["Laravel / Nest POST /v1/payments"] -->|"POST /v1/charges"| Go["fake-pix-provider"]
  Go -->|"201 QR synthetic"| Wallet
  Demo["curl / README"] -->|"GET by-payment + simulate"| Go
  Go -->|"POST signed t,v1"| Callback["API POST /v1/webhooks/payment"]
```

| Route | Effect |
|-------|--------|
| `GET /health` | Liveness (Docker HEALTHCHECK) |
| `POST /v1/charges` | Create `PENDING` charge + fake EMV QR |
| `GET /v1/charges/{id}` | Detail (`last_delivery_status` after simulate) |
| `GET /v1/charges/by-payment/{payment_id}` | Lookup by wallet `payment_id` (404 if missing) |
| `POST /v1/charges/{id}/simulate` | `paid` / `expired` / `failed` → async signed webhook |

HMAC is identical to AcmePay / `checkout-portal-next/lib/webhook-signature.ts`:
`HMAC-SHA256("${t}.${raw_body}", WEBHOOK_SECRET)` → header
`X-AcmePay-Signature: t=<unix>,v1=<hex>`. Shared secret default:
`dev-webhook-secret`.

## Quickstart (Docker)

No Go toolchain on the host. Wallet stacks (`pix-wallet-api` Sail,
`payment-api-nest` compose) already start this image; this file is the
isolated path.

```bash
docker compose up -d
curl -s http://localhost:8080/health
```

When this process runs in Docker, `callback_url` must be a hostname the
**container** can resolve (`laravel.test`, `api`, or `host.docker.internal`),
not `http://localhost/...` on the operator's machine.

Compose also starts Postgres on host `:5435`. Restart `fake-pix` after create:
`GET /v1/charges/by-payment/{id}` still 200. Restart after simulate: the
outbox poller re-POSTs the same `event_id`.

Create a charge (point `callback_url` at anything that accepts POST — tests use
`httptest`):

```bash
curl -s -X POST http://localhost:8080/v1/charges \
  -H "Authorization: Bearer fake-pix-demo" \
  -H "Content-Type: application/json" \
  -d '{
    "amount": 1500,
    "currency": "BRL",
    "payment_id": "550e8400-e29b-41d4-a716-446655440000",
    "callback_url": "http://host.docker.internal:9999/v1/webhooks/payment"
  }'
```

### Wallet demo (create → by-payment → simulate)

With Sail (`sail up -d`) or Nest (`docker compose up -d` in
`payment-api-nest`) the PSP is already on `:8080`. Create the payment on the
**wallet**, look up the charge without scraping logs, then simulate.

Laravel callback is `http://laravel.test/v1/webhooks/payment`. Nest on the host
uses `http://host.docker.internal:3001/v1/webhooks/payment`.

```bash
# 1. Create on the wallet — copy `id` from the 201 body
curl -s -X POST http://localhost/v1/payments \
  -H "Authorization: Bearer acmepay_demo_key_change_me" \
  -H "Content-Type: application/json" \
  -d '{"amount":1500,"currency":"BRL","external_id":"demo-1"}'

# 2. Lookup charge id by that payment UUID
curl -s http://localhost:8080/v1/charges/by-payment/<payment_id> \
  -H "Authorization: Bearer fake-pix-demo"

# 3. Simulate paid (async 202). Go POSTs HMAC t,v1 to callback_url.
curl -s -X POST http://localhost:8080/v1/charges/<charge_id>/simulate \
  -H "Authorization: Bearer fake-pix-demo" \
  -H "Content-Type: application/json" \
  -d '{"type":"payment.paid"}'
```

A second simulate returns **200** `already_simulated` and does not POST again.
The wallet queue worker must be running so the job can mark `PAID`
(`./vendor/bin/sail artisan queue:work` on Laravel).

```bash
# Charge still exists after a PSP restart
docker compose restart fake-pix
curl -s http://localhost:8080/v1/charges/by-payment/550e8400-e29b-41d4-a716-446655440000 \
  -H "Authorization: Bearer fake-pix-demo"
```

Inbound auth also accepts `X-Api-Key: fake-pix-demo`. Amounts are integer minor
units (`1500` = R$ 15,00). Never floats.

`go run ./cmd/provider` still works for hacking on the binary; it is not the
demo path.

## Quality gates

```bash
go test ./...
test -z "$(gofmt -l .)"
docker build -t fake-pix-provider:local .
```

Postgres tests skip unless `TEST_DATABASE_URL` is set (CI always sets it).

## Docs

See [AGENTS.md](AGENTS.md) for the module map and agent workflow.
Phase spec: [Docs/specs/fase-4-durable-outbox.md](Docs/specs/fase-4-durable-outbox.md).
ADR: [Docs/adrs/001-stdlib-and-in-memory.md](Docs/adrs/001-stdlib-and-in-memory.md).

# Fase 1 — Charges + signed webhook delivery

## Contexto / Objetivo

This process **is** the PSP: create a synthetic PIX charge and, on `simulate`,
POST the canonical AcmePay webhook (`provider=fake_pix`) with Stripe-style
`t=,v1=` HMAC. Clock, `http.Client`, and sleep/backoff are injected so HMAC
window and retry tests do not wait on wall-clock.

This is **not** the partner API. Callers invent `payment_id` (UUID the AcmePay
wallet will expect on `POST /v1/webhooks/payment`).

## Endpoints

| Método | Rota | Auth | Descrição |
|--------|------|------|-----------|
| GET | `/health` | none | Liveness |
| POST | `/v1/charges` | optional API key | Create `PENDING` charge + fake EMV QR |
| GET | `/v1/charges/{id}` | optional API key | Charge detail |
| GET | `/v1/charges/by-payment/{payment_id}` | optional API key | Lookup by wallet `payment_id` (404 if missing) |
| POST | `/v1/charges/{id}/simulate` | optional API key | `paid` / `expired` / `failed` → async webhook |

Inbound auth: if `FAKE_PIX_API_KEY` is set, require `Authorization: Bearer` or
`X-Api-Key`. If unset, routes are open (local `httptest` / curl).

## Request / Response

**POST /v1/charges**

```json
{
  "amount": 1500,
  "currency": "BRL",
  "payment_id": "550e8400-e29b-41d4-a716-446655440000",
  "callback_url": "http://127.0.0.1:9999/v1/webhooks/payment"
}
```

`amount` integer minor units, `currency` `BRL`. **201**:

```json
{
  "id": "<charge id>",
  "status": "PENDING",
  "qr_code": "00020126ACMEPAY.FAKE.PIX...",
  "copy_paste": "00020126ACMEPAY.FAKE.PIX...",
  "provider_tx_id": "pix_tx_..."
}
```

**POST /v1/charges/{id}/simulate**

```json
{ "type": "payment.paid" }
```

**202** `{ "event_id": "evt_...", "delivery": "pending" }`. Delivery is async.
A second `simulate` on the same charge returns **200**
`{ "event_id": "...", "status": "already_simulated" }` and does **not** POST
again.

Canonical webhook body (`payment.paid` — `data.amount` / `currency` required by
the AcmePay contract):

```json
{
  "event_id": "evt_...",
  "provider": "fake_pix",
  "type": "payment.paid",
  "payment_id": "<from create>",
  "occurred_at": "<RFC3339>",
  "data": {
    "provider_tx_id": "...",
    "amount": 1500,
    "currency": "BRL"
  }
}
```

`payment.expired` / `payment.failed` may send `data: {}`. Header:
`X-AcmePay-Signature: t=<unix>,v1=<hex>` with
`HMAC-SHA256("${t}.${raw_body}", WEBHOOK_SECRET)` (hex), identical to
`checkout-portal-next/lib/webhook-signature.ts`.

GET charge after simulate exposes `last_delivery_status`.

**GET /v1/charges/by-payment/{payment_id}**

Same JSON as `GET /v1/charges/{id}`. Lets a README demo find the charge id
after `POST /v1/payments` on Laravel/Nest without scraping logs. Unknown
`payment_id` → **404**. Charge id is not persisted on the wallet APIs.

## Fluxo (passo a passo)

1. `POST /v1/charges` stores a `PENDING` charge (memory + mutex, indexed by
   `payment_id`) and returns QR.
2. Wallet demo: `POST /v1/payments` on Laravel/Nest → `GET /v1/charges/by-payment/{payment_id}`
   → `POST .../simulate` → signed webhook on the API.
3. `POST .../simulate` claims the charge event **once**; loser of the race gets
   `already_simulated`.
4. Winner signs the raw JSON (`internal/sign`) and enqueues `internal/deliver`.
5. Deliver POSTs the **same** body (same `event_id`) up to 5 times:
   - 2xx → stop
   - 4xx except 429 → permanent, no retry
   - 429, 5xx, network → retry with backoff `[50ms, 150ms, 450ms, 1350ms]`
     (injectable in tests)

## Códigos de erro

Provider surface (not AcmePay partner codes):

| HTTP | Situação |
|------|----------|
| 400 | Invalid amount / currency / type / JSON |
| 401 | Inbound API key missing or wrong (only when env is set) |
| 404 | Unknown charge id or unknown `payment_id` |
| 200 | `already_simulated` on a claimed charge |

## Critérios de aceite

- [x] `internal/sign`: golden vector `t=1710000000` + fixed body → stable `v1`
- [x] `internal/sign`: verify uses timing-safe compare; window reject
- [x] `internal/deliver`: httptest 500 then 200 → 2 requests, same `event_id`, stop on 2xx
- [x] `internal/deliver`: httptest 400 → 1 request, no retry
- [x] `internal/deliver`: 429 and network errors retry; backoff/sleep injectable
- [x] `POST /v1/charges` → 201 QR in `00020126ACMEPAY.FAKE.PIX...` style; integer amount
- [x] Optional inbound API key (Bearer or `X-Api-Key`)
- [x] `simulate paid` → callback receives valid `t=,v1=` and JSON `amount` integer
- [x] Two parallel `simulate` on the same charge → **one** POST to the callback
- [x] GET charge shows `last_delivery_status` after async delivery
- [x] `GET /v1/charges/by-payment/{payment_id}` returns the charge; unknown id → 404

## Testes obrigatórios

- [x] Unit — HMAC golden + timing-safe mismatch + tolerance window
- [x] Unit — deliver retry classification via `httptest` (no long sleep)
- [x] Integração — create + simulate `paid` against `httptest` callback
- [x] Concorrência — two parallel `simulate` → single delivery
- [x] Integração — create then `GET .../by-payment/{payment_id}`; missing → 404

## Variáveis de ambiente

| Var | Default | Descrição |
|-----|---------|-----------|
| `FAKE_PIX_API_KEY` | `fake-pix-demo` | Optional inbound auth |
| `WEBHOOK_SECRET` | `dev-webhook-secret` | Outbound HMAC (same as AcmePay) |
| `WEBHOOK_TOLERANCE_SECONDS` | `300` | Verify window |
| `PORT` | `8080` | Listen port |

## Dependências / Rollback

- Dependências: Go 1.23 stdlib only (`net/http`, `crypto/hmac`, `sync`).
- Rollback: in-memory store; restart drops all charges.

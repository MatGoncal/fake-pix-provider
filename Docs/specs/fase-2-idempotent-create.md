# Fase 2 — Idempotent POST /v1/charges by payment_id

## Contexto / Objetivo

Wallet create retries can POST the same `payment_id` twice (timeout after 201,
failed persist, parallel `Idempotency-Key`). Today `Create` overwrites
`byPaymentID` and mints a second charge. This phase makes create **CreateOrGet**:
if a charge already exists for that `payment_id`, return it; do not insert
another.

This is **not** the partner API. Callers still invent `payment_id` (the AcmePay
wallet UUID). ADR 001 intact: stdlib HTTP, in-memory mutex store, no Postgres,
no Docker, no outbox.

## Endpoints

Partner-facing routes are unchanged. HTTP status on create is the only change:

| Método | Rota | Auth | Descrição |
|--------|------|------|-----------|
| POST | `/v1/charges` | optional API key | **201** first create; **200** replay of the existing charge for that `payment_id` |

Request / response JSON shape is unchanged from fase 1 (same `id`, QR,
`provider_tx_id` on the **Go** payload — that field is the PSP settlement id,
not the wallet column).

## Request / Response

**POST /v1/charges** body unchanged:

```json
{
  "amount": 1500,
  "currency": "BRL",
  "payment_id": "550e8400-e29b-41d4-a716-446655440000",
  "callback_url": "http://127.0.0.1:9999/v1/webhooks/payment"
}
```

First call → **201** + charge. Second call with the same `payment_id` → **200**
+ the **same** `id` / QR (ignore a different amount/callback on replay; the
stored charge wins). Unknown `payment_id` on GET `by-payment` remains **404**.

Wallet APIs accept both 200 and 201 (documented there). This process does not
change `simulate` or webhook delivery.

## Fluxo (passo a passo)

1. Validate amount / currency / `payment_id` UUID / callback as today.
2. `MemoryStore.CreateOrGet`: under the mutex, if `byPaymentID` already maps
   this `payment_id`, return that charge and `created=false`.
3. Otherwise insert the new charge, index `payment_id` → charge id,
   `created=true`.
4. Handler writes **201** when created, **200** on replay.
5. `GET /v1/charges/by-payment/{payment_id}` still returns the single charge.

## Códigos de erro

Provider surface (not AcmePay partner codes):

| HTTP | Situação |
|------|----------|
| 201 | First create for this `payment_id` |
| 200 | Replay — charge already exists for this `payment_id` |
| 400 | Invalid amount / currency / type / JSON |
| 401 | Inbound API key missing or wrong (only when env is set) |
| 404 | Unknown charge id or unknown `payment_id` |

## Critérios de aceite

- [x] Two POSTs with the same `payment_id` → same charge `id`; one item in the store
- [x] First POST **201**, replay **200**, same JSON `id` / QR
- [x] Parallel POSTs with the same `payment_id` still yield a single charge
- [x] `GET .../by-payment/{payment_id}` returns that one charge
- [x] No Postgres / Docker / outbox; ADR 001 intact
- [x] `gofmt`; `go test ./...`

## Testes obrigatórios

- [x] Store — `CreateOrGet` twice with the same `payment_id` → same id, `created` true then false
- [x] HTTP — two POSTs same `payment_id` → 201 then 200, same `id`
- [x] Store — concurrent `CreateOrGet` same `payment_id` → one winner insert

## Variáveis de ambiente

None new. Same as fase 1.

## Dependências / Rollback

- Dependências: Go 1.23 stdlib only (`net/http`, `sync`).
- Rollback: restore unconditional `Create` + always 201 (wallet retries mint duplicate charges).
- Out of scope: persisting charges, exposing this status on the partner API, outbox.

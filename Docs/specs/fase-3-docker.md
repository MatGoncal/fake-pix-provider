# Fase 3 — Docker packaging (in-memory store unchanged)

## Contexto / Objetivo

Local demo currently requires a Go toolchain on the host (`go run ./cmd/provider`)
and wallet APIs reach it via `host.docker.internal:8080`. This phase packages
the same binary so Sail / Nest compose can run the PSP as a sibling container.

Docker is **packaging only**. ADR 001 still holds: stdlib `net/http`,
`MemoryStore`, no Gin, no Postgres, no outbox. Restart of the container still
drops all charges (fase 4 / wallet fase 12 will persist). `GET /health` already
exists; it becomes the compose healthcheck.

## Endpoints (se aplicável)

No new routes. Existing liveness is the probe:

| Método | Rota | Auth | Descrição |
|--------|------|------|-----------|
| GET | `/health` | none | `{"status":"ok"}` — Docker HEALTHCHECK / compose `service_healthy` |

## Request / Response

Unchanged from fases 1–2. `GET /health` remains:

```json
{"status":"ok"}
```

## Fluxo (passo a passo)

1. Multi-stage `Dockerfile`: build with `golang:1.23`, run on Alpine (wget for
   HEALTHCHECK). `EXPOSE 8080`. `CGO_ENABLED=0`. Listen `:{PORT}` as today
   (`0.0.0.0`).
2. `compose.yaml` in this repo runs only `fake-pix` (useful in isolation).
   Publish `8080:8080`. Env: `PORT`, `WEBHOOK_SECRET`, `FAKE_PIX_API_KEY`.
   `extra_hosts: host.docker.internal:host-gateway` so a callback to a process
   on the host still works.
3. Wallet stacks (Laravel Sail / Nest compose) **build from this directory**
   (`context: ../fake-pix-provider` in the local portfolio layout). They do not
   vendor a second copy of the source.
4. CI `go test` / `gofmt` stay on the host runner with httptest — **no** Docker
   required for unit tests. An optional `docker build` job fails the PR if the
   image does not compile.
5. Document: in-memory store → `docker compose restart fake-pix` still 404s
   `GET /v1/charges/by-payment/{id}` for charges created before the restart.

## Códigos de erro

Unchanged provider surface. Packaging does not map new HTTP codes.

## Critérios de aceite

- [x] `Dockerfile` multi-stage (`golang:1.23` → Alpine), `EXPOSE 8080`, HEALTHCHECK on `GET /health`
- [x] `docker compose up -d` (this repo) listens on `:8080` without `go run`
- [x] `GET /health` → 200 `{"status":"ok"}`
- [x] Store remains `MemoryStore`; restart still drops charges (documented in README + ADR)
- [x] No Postgres, Redis, Gin, or outbox
- [x] `go test ./...` and `gofmt` still pass without Docker
- [x] Optional CI job `docker build` (does not replace `go test`)
- [x] `AGENTS.md` Do NOT no longer forbids Docker; still forbids Postgres / outbox
- [x] ADR 001 amended: Docker is packaging, not a store/HTTP-framework change

## Testes obrigatórios

- [x] Existing `go test ./...` (httptest + MemoryStore) green — no new suite that needs a daemon
- [x] `docker build` succeeds locally / in CI
- [x] Smoke (wallet README): create → by-payment → simulate without `go run`

## Migrations

None. In-memory only.

## Variáveis de ambiente novas

None. Reuse fase 0:

| Var | Default | Descrição |
|-----|---------|-----------|
| `FAKE_PIX_API_KEY` | `fake-pix-demo` | Inbound auth |
| `WEBHOOK_SECRET` | `dev-webhook-secret` | HMAC (must match the wallet) |
| `PORT` | `8080` | Listen port inside the container |

## Dependências / Rollback

- Dependências: Docker Engine. Wallet wiring lives in pix-wallet-api /
  payment-api-nest fase 11 (callback URL must be a DNS name the **container**
  can reach — not `localhost` on the host).
- Rollback: delete `Dockerfile` / `compose.yaml`; restore `go run` in READMEs.
- Out of scope: durable store, outbox, unifying Laravel+Nest compose, publishing
  a registry image (local `../fake-pix-provider` context is enough).

# Fase 0 — Bootstrap spec-driven + Go module

## Contexto / Objetivo

Scaffold `fake-pix-provider` as its own Go module: AGENTS.md, phase specs, env
example, and CI. No charge/simulate HTTP yet — that is Fase 1. Stdlib only;
in-memory store comes with the HTTP surface.

## Critérios de aceite

- [x] `AGENTS.md` with stack, module map, workflow, PR checklist
- [x] `go.mod` module `github.com/MatGoncal/fake-pix-provider`, Go 1.23
- [x] `Docs/specs/fase-0-bootstrap.md` + `fase-1-charges-webhooks.md`
- [x] `.env.example` documents `FAKE_PIX_API_KEY`, `WEBHOOK_SECRET`, `PORT`
- [x] CI: `gofmt -l` + `go test ./...` on push/PR
- [x] `cmd/provider/main.go` compiles (`go run ./cmd/provider`)
- [x] README + ADR `001-stdlib-and-in-memory` (Fase 1 docs follow-up)

## Testes

- [x] `go test ./...` is the CI entry (packages with tests land in Fase 1)

## Variáveis de ambiente

| Var | Default | Descrição |
|-----|---------|-----------|
| `FAKE_PIX_API_KEY` | `fake-pix-demo` | Optional inbound auth; empty disables the check |
| `WEBHOOK_SECRET` | `dev-webhook-secret` | HMAC secret (same default as AcmePay) |
| `WEBHOOK_TOLERANCE_SECONDS` | `300` | Verify window for tests / later HTTP |
| `PORT` | `8080` | Listen port |

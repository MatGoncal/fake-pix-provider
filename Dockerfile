# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/provider ./cmd/provider

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -H -u 65532 nonroot
COPY --from=builder /out/provider /usr/local/bin/provider
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/usr/local/bin/provider"]

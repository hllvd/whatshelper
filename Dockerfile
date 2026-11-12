# Estágio de compilação
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Instala certificados para chamadas HTTPS
RUN apk add --no-cache ca-certificates

# Copia arquivos de dependência primeiro para aproveitar cache de camadas
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

# Copia todo o código fonte
COPY . .

# Compilação estática dos binários (sem CGO, SQLite puro em Go)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /bin/brain ./cmd/brain && \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /bin/connectors ./cmd/connectors

# Estágio final enxuto
FROM alpine:3.21

RUN apk add --no-cache ca-certificates curl tzdata && \
    addgroup -S brain && adduser -S -G brain brain

WORKDIR /app

COPY --from=builder /bin/brain /app/brain
COPY --from=builder /bin/connectors /app/connectors
COPY --from=builder /src/packs /app/packs
COPY --from=builder /src/tenants /app/tenants

RUN mkdir -p /data && chown brain:brain /data

USER brain

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/brain"]

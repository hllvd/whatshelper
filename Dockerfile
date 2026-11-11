# Estágio de compilação
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Instala certificados para chamadas HTTPS
RUN apk add --no-cache ca-certificates

# Copia arquivos de dependência primeiro para aproveitar cache de camadas
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

# Copia todo o código fonte
COPY . .

# Compilação estática do binário
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /bin/brain ./cmd/brain

# Estágio final enxuto
FROM alpine:3.21

RUN apk add --no-cache ca-certificates curl tzdata && \
    addgroup -S brain && adduser -S -G brain brain

WORKDIR /app

COPY --from=builder /bin/brain /app/brain
COPY --from=builder /src/packs /app/packs
COPY --from=builder /src/tenants /app/tenants

USER brain

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/brain"]

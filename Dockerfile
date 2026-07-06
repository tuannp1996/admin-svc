# --- Build stage ---
FROM golang:1.26.3-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /admin-svc ./cmd/main.go

# --- Runtime stage ---
FROM alpine:3.20

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    curl \
    docker-cli \
    nodejs \
    npm \
    bash

RUN npm install -g pm2

WORKDIR /app

COPY --from=builder /admin-svc .
COPY config.yaml .

# Run as non-root
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser

ENTRYPOINT ["/app/admin-svc"]
CMD ["-config", "/app/config.yaml"]

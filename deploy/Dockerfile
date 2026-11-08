# =============================================================================
# Project Cerebro — Production Dockerfile
# Multi-stage build: Go builder → distroless non-root final image
# =============================================================================

# ── Stage 1: Build Web Frontend (Next.js export) ─────────────────────────────
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ── Stage 2: Module cache ─────────────────────────────────────────────────────
FROM golang:1.25-alpine AS deps
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

# ── Stage 3: Build Go Binary ──────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Copy static Next.js export into web adapter embed dir
COPY --from=web-builder /web/dist ./internal/adapter/web/dist

# CGO_ENABLED=0 for a fully static binary (works in distroless).
RUN CGO_ENABLED=0 \
    go build \
    -ldflags="-s -w -extldflags=-static" \
    -o /cerebro \
    ./cmd/cerebro

# ── Stage 4: Distroless runtime ───────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# Copy binary
COPY --from=builder /cerebro /cerebro

USER 65532:65532

EXPOSE 8080
EXPOSE 9090

ENTRYPOINT ["/cerebro"]
CMD ["run", "--demo", "--config-dir", "/configs"]

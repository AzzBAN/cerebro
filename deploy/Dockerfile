# =============================================================================
# Project Cerebro — Production Dockerfile
# Multi-stage build: Go builder → distroless non-root final image
# =============================================================================

# ── Stage 1: Module cache ─────────────────────────────────────────────────────
FROM golang:1.25-alpine AS deps
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

# ── Stage 2: Build ────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 for a fully static binary (works in distroless).
# -ldflags "-s -w" strips debug info and DWARF tables.
# Use -tags metrics to enable Prometheus instrumentation.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w -extldflags=-static" \
    -o /cerebro \
    ./cmd/cerebro

# ── Stage 3: Distroless runtime ───────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# Copy the binary and config directory structure.
COPY --from=builder /cerebro /cerebro

# ca-certificates are bundled in distroless/static; no extra COPY needed.

# Non-root user is the default in the nonroot variant.
# UID 65532 = nonroot, GID 65532 = nonroot.
USER 65532:65532

EXPOSE 9090

ENTRYPOINT ["/cerebro"]
CMD ["run", "--paper", "--config-dir", "/configs"]

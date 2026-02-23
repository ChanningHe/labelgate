# Stage 1: Build dashboard frontend (runs on host arch — output is platform-independent)
FROM --platform=$BUILDPLATFORM node:22-alpine AS dashboard-builder

WORKDIR /app/dashboard

# Install dependencies first for better caching
COPY dashboard/package.json dashboard/yarn.lock ./
RUN yarn install --frozen-lockfile

# Build frontend assets
COPY dashboard/ .
RUN yarn build

# Stage 2: Build Go binary (runs on host arch — cross-compiles via GOARCH)
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=""

WORKDIR /app

# Install ca-certificates for HTTPS (needed at build time for go mod download)
RUN apk add --no-cache git ca-certificates

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Copy built dashboard assets into the embed directory
COPY --from=dashboard-builder /app/dashboard/static ./dashboard/static

# Build the binary (pure Go, no CGO needed — modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X github.com/channinghe/labelgate/internal/version.Version=${VERSION} -X github.com/channinghe/labelgate/internal/version.Commit=${COMMIT} -X github.com/channinghe/labelgate/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /app/labelgate ./cmd/labelgate

# Stage 3: Runtime
FROM alpine:3.21@sha256:c3f8e73fdb79deaebaa2037150150191b9dcbfba68b4a46d70103204c53f4709

WORKDIR /app

# Install ca-certificates for HTTPS and tzdata for timezone support
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' labelgate

# Copy binary from builder
COPY --from=builder /app/labelgate /app/labelgate

# Create directories for data and config
RUN mkdir -p /app/config && \
    chown -R labelgate:labelgate /app/config

# Note: No config file copied — use environment variables for configuration
# If you need a config file, mount it at /etc/labelgate/labelgate.yaml

# Switch to non-root user
USER labelgate

# Expose ports
# 8080: API Server + Dashboard
# 8081: Agent Server (WebSocket)
EXPOSE 8080 8081

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/api/health || exit 1

# Default command
ENTRYPOINT ["/app/labelgate"]

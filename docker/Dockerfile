# Self-contained multi-stage build for local development.
# For CI/CD, use docker/ci.Dockerfile with pre-built binaries instead.
#
# Usage:
#   docker build -f docker/Dockerfile .
#   docker build -f docker/Dockerfile --build-arg BASE_IMAGE=gcr.io/distroless/static-debian13:debug-nonroot@sha256:b80e889821affbb581c2743235fe72fca378182a64e7e6313c36aa9a60f6f244 .

# Stage 1: Build dashboard frontend (runs on host arch — output is platform-independent)
FROM --platform=$BUILDPLATFORM node:22-alpine AS dashboard-builder

WORKDIR /app/dashboard

COPY dashboard/package.json dashboard/yarn.lock ./
RUN yarn install --frozen-lockfile

COPY dashboard/ .
RUN yarn build

# Stage 2: Build Go binary (runs on host arch — cross-compiles via GOARCH)
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=""

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=dashboard-builder /app/dashboard/static ./dashboard/static

# Pure Go, no CGO needed (modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X github.com/channinghe/labelgate/internal/version.Version=${VERSION} -X github.com/channinghe/labelgate/internal/version.Commit=${COMMIT} -X github.com/channinghe/labelgate/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /app/labelgate ./cmd/labelgate

# Stage 3: Runtime
# Default: distroless nonroot (production)
# Full:    pass --build-arg BASE_IMAGE=gcr.io/distroless/static-debian13:debug-nonroot@sha256:b80e889821affbb581c2743235fe72fca378182a64e7e6313c36aa9a60f6f244
ARG BASE_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:01e550fdb7ab79ee7be5ff440a563a58f1fd000ad9e0c532e65c3d23f917f1c5
FROM ${BASE_IMAGE}

WORKDIR /app

COPY --from=builder /app/labelgate /app/labelgate

USER nonroot

# 8080: API Server + Dashboard
# 8081: Agent Server (WebSocket)
EXPOSE 8080 8081

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/labelgate", "healthcheck"]

ENTRYPOINT ["/app/labelgate"]

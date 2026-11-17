# Self-contained multi-stage build for local development.
# For CI/CD, use docker/ci.Dockerfile with pre-built binaries instead.
#
# Usage:
#   docker build -f docker/Dockerfile --build-arg VERSION=v0.3.0 --build-arg COMMIT=$(git rev-parse HEAD) --build-arg BUILD_DATE=$(git log -1 --format=%cI) .
#   docker build -f docker/Dockerfile --build-arg BASE_IMAGE=gcr.io/distroless/static-debian13:debug-nonroot@sha256:7df9fac2f5d3927260696af33fc2391da26366825838a47337088ed78f854a0c .

# Runtime base image. Must be declared before the first FROM so the final
# stage's FROM can use it.
# Default: distroless nonroot (production)
# Full:    pass --build-arg BASE_IMAGE=gcr.io/distroless/static-debian13:debug-nonroot@sha256:7df9fac2f5d3927260696af33fc2391da26366825838a47337088ed78f854a0c
ARG BASE_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

# Stage 1: Build dashboard frontend (runs on host arch — output is platform-independent)
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5 AS dashboard-builder

WORKDIR /app/dashboard

COPY dashboard/package.json dashboard/yarn.lock ./
RUN yarn install --frozen-lockfile

COPY dashboard/ .
RUN yarn build

# Stage 2: Build Go binary (runs on host arch — cross-compiles via GOARCH)
# Keep the Go minor in step with the go directive in go.mod.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS builder

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=""
# Pass the commit date for reproducible builds: --build-arg BUILD_DATE=$(git log -1 --format=%cI)
ARG BUILD_DATE=unknown

WORKDIR /app

# hadolint ignore=DL3018
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=dashboard-builder /app/dashboard/static ./dashboard/static

# Pure Go, no CGO needed (modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w -X github.com/channinghe/labelgate/internal/version.Version=${VERSION} -X github.com/channinghe/labelgate/internal/version.Commit=${COMMIT} -X github.com/channinghe/labelgate/internal/version.Date=${BUILD_DATE}" \
    -o /app/labelgate ./cmd/labelgate

# Stage 3: Runtime (base image set by BASE_IMAGE at the top of this file)
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

# CI runtime Dockerfile — packages pre-built binaries only, no compilation.
# Expects labelgate-linux-{amd64,arm64} in the build context.
#
# Used by CI/CD workflows; for local development use docker/Dockerfile instead.

ARG BASE_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5

# Data directory skeleton (distroless has no shell to create it at runtime)
FROM --platform=$BUILDPLATFORM busybox:1.38@sha256:a46257e06c82ad85242d0d38077d0a52d9896389ca60a1a59d3463db586e5fa4 AS data
RUN mkdir -p /skel

FROM ${BASE_IMAGE}

ARG TARGETARCH

WORKDIR /app

COPY --chmod=755 labelgate-linux-${TARGETARCH} /app/labelgate
# Default db.path is /app/config/labelgate.db. Owning the directory as the
# runtime user (65532) lets Docker named volumes inherit writable ownership.
COPY --from=data --chown=65532:65532 /skel /app/config

USER nonroot

# 28111: API Server + Dashboard
# 28112: Agent Server (WebSocket)
EXPOSE 28111 28112

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/labelgate", "healthcheck"]

ENTRYPOINT ["/app/labelgate"]

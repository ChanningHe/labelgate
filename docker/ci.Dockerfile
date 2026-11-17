# CI runtime Dockerfile — packages pre-built binaries only, no compilation.
# Expects labelgate-linux-{amd64,arm64} in the build context.
#
# Used by CI/CD workflows; for local development use docker/Dockerfile instead.

ARG BASE_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

# Data directory skeleton (distroless has no shell to create it at runtime)
FROM --platform=$BUILDPLATFORM busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec AS data
RUN mkdir -p /skel

FROM ${BASE_IMAGE}

ARG TARGETARCH

WORKDIR /app

COPY --chmod=755 labelgate-linux-${TARGETARCH} /app/labelgate
# Default db.path is /app/config/labelgate.db. Owning the directory as the
# runtime user (65532) lets Docker named volumes inherit writable ownership.
COPY --from=data --chown=65532:65532 /skel /app/config

USER nonroot

# 8080: API Server + Dashboard
# 8081: Agent Server (WebSocket)
EXPOSE 8080 8081

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/labelgate", "healthcheck"]

ENTRYPOINT ["/app/labelgate"]

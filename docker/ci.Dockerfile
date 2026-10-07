# CI runtime Dockerfile — packages pre-built binaries only, no compilation.
# Expects labelgate-linux-{amd64,arm64} in the build context.
#
# Used by CI/CD workflows; for local development use docker/Dockerfile instead.

ARG BASE_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
FROM ${BASE_IMAGE}

ARG TARGETARCH

WORKDIR /app

COPY --chmod=755 labelgate-linux-${TARGETARCH} /app/labelgate

USER nonroot

# 8080: API Server + Dashboard
# 8081: Agent Server (WebSocket)
EXPOSE 8080 8081

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/labelgate", "healthcheck"]

ENTRYPOINT ["/app/labelgate"]

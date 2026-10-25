# Labelgate build recipes

# Default recipe: build everything
default: build

# Build the full binary (dashboard + Go)
build: dashboard-build go-build

# Build dashboard frontend
dashboard-build:
    cd dashboard && yarn install --frozen-lockfile && yarn build

# Build Go binary with version injection
go-build:
    #!/usr/bin/env sh
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "")
    DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    go build -ldflags "-s -w -X github.com/channinghe/labelgate/internal/version.Version=${VERSION} -X github.com/channinghe/labelgate/internal/version.Commit=${COMMIT} -X github.com/channinghe/labelgate/internal/version.Date=${DATE}" -o bin/labelgate ./cmd/labelgate

# Run Go backend in dev mode
dev-api *ARGS:
    go run ./cmd/labelgate {{ARGS}}

# Run dashboard frontend dev server
dev-ui:
    cd dashboard && yarn dev

# Run both backend and frontend concurrently (requires config)
dev CONFIG="dev/secrets/config.yaml":
    @echo "Start backend and frontend separately:"
    @echo "  Terminal 1: just dev-api --config {{CONFIG}}"
    @echo "  Terminal 2: just dev-ui"

# Type check Go code
check:
    go build ./...
    go vet ./...

# Type check frontend
check-ui:
    cd dashboard && npx tsc --noEmit

# Run all tests
test:
    go test ./...

# Run tests with coverage
test-cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
    rm -rf bin/ dashboard/static/* coverage.out coverage.html
    @echo '<html><body>Run yarn build to generate dashboard assets</body></html>' > dashboard/static/index.html

# Tag and push a release (triggers the release workflow): just release 0.2.0
release VERSION:
    #!/usr/bin/env sh
    set -eu
    TAG="v{{VERSION}}"
    echo "{{VERSION}}" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "error: version must be X.Y.Z, got '{{VERSION}}'" >&2; exit 1; }
    [ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || { echo "error: must release from main" >&2; exit 1; }
    [ -z "$(git status --porcelain)" ] || { echo "error: working tree is dirty" >&2; exit 1; }
    git fetch --quiet --tags origin main
    [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || { echo "error: main is not in sync with origin/main" >&2; exit 1; }
    if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then echo "error: tag ${TAG} already exists" >&2; exit 1; fi
    git tag -a "${TAG}" -m "${TAG}"
    git push origin "${TAG}"
    echo "Released ${TAG}"

# Labelgate build recipes

# Pinned Go tools, run via `go run`. Keep in step with .github/workflows/test.yml.
# renovate: datasource=go depName=honnef.co/go/tools
staticcheck_version := "v0.8.2"
# renovate: datasource=go depName=golang.org/x/vuln
govulncheck_version := "v1.9.0"

# Default recipe: build everything
default: build

# Build the full binary (dashboard + Go)
build: dashboard-build go-build

# Build dashboard frontend
dashboard-build:
    cd dashboard && yarn install --frozen-lockfile && yarn build

# Create the placeholder dashboard/embed.go needs when the frontend has not been built
[private]
placeholder:
    #!/usr/bin/env sh
    set -eu
    if [ ! -f dashboard/static/index.html ]; then
        mkdir -p dashboard/static
        echo '<html><body>Dashboard not built</body></html>' > dashboard/static/index.html
    fi

# Build Go binary with version injection
go-build: placeholder
    #!/usr/bin/env sh
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "")
    DATE=$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
    go build -trimpath -ldflags "-s -w -X github.com/channinghe/labelgate/internal/version.Version=${VERSION} -X github.com/channinghe/labelgate/internal/version.Commit=${COMMIT} -X github.com/channinghe/labelgate/internal/version.Date=${DATE}" -o bin/labelgate ./cmd/labelgate

# Run Go backend in dev mode
dev-api *ARGS: placeholder
    go run ./cmd/labelgate {{ARGS}}

# Run dashboard frontend dev server
dev-ui:
    cd dashboard && yarn dev

# Run both backend and frontend concurrently (requires config)
dev CONFIG="dev/secrets/config.yaml":
    @echo "Start backend and frontend separately:"
    @echo "  Terminal 1: just dev-api --config {{CONFIG}}"
    @echo "  Terminal 2: just dev-ui"

# Fail if any Go file (tracked or new) is not gofmt-formatted
fmt-check:
    #!/usr/bin/env sh
    set -eu
    unformatted=$(git ls-files -z --cached --others --exclude-standard '*.go' | xargs -0 gofmt -l)
    if [ -n "$unformatted" ]; then
        echo "error: not gofmt-formatted (run gofmt -w):" >&2
        echo "$unformatted" >&2
        exit 1
    fi

# Check Go code: formatting, build, vet
check: placeholder fmt-check
    go build ./...
    go vet ./...

# Static analysis with staticcheck
lint: placeholder
    go run honnef.co/go/tools/cmd/staticcheck@{{staticcheck_version}} ./...

# Scan dependencies and stdlib for known reachable vulnerabilities
vuln: placeholder
    go run golang.org/x/vuln/cmd/govulncheck@{{govulncheck_version}} ./...

# Type check frontend
check-ui:
    cd dashboard && npx tsc --noEmit

# Run all tests (race detector on, as in CI)
test: placeholder
    go test -race ./...

# Run tests with coverage
test-cover: placeholder
    go test -race -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
    rm -rf bin/ dashboard/static/* coverage.out coverage.html
    @echo '<html><body>Run yarn build to generate dashboard assets</body></html>' > dashboard/static/index.html

# Tag and push a release (triggers the release workflow): just release 0.3.0 (or 0.3.0-rc1 for a pre-release)
release VERSION:
    #!/usr/bin/env sh
    set -eu
    TAG="v{{VERSION}}"
    echo "{{VERSION}}" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || { echo "error: version must be X.Y.Z or X.Y.Z-suffix, got '{{VERSION}}'" >&2; exit 1; }
    [ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || { echo "error: must release from main" >&2; exit 1; }
    [ -z "$(git status --porcelain)" ] || { echo "error: working tree is dirty" >&2; exit 1; }
    git fetch --quiet --tags origin main
    [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || { echo "error: main is not in sync with origin/main" >&2; exit 1; }
    if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then echo "error: tag ${TAG} already exists" >&2; exit 1; fi
    git tag -a "${TAG}" -m "${TAG}"
    git push origin "${TAG}"
    echo "Released ${TAG}"

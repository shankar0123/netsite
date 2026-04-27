# NetSite Makefile
#
# What: developer-facing build targets for the NetSite CLI and (later)
# the ns-controlplane and ns-pop binaries.
#
# How: each target is a thin wrapper around the equivalent `go` command
# with consistent ldflag injection. The version metadata in
# pkg/version/version.go is populated at build time by `-X` flags, so
# `./ns version` reflects real values for tagged releases and clear
# sentinels for local builds.
#
# Why: a single Makefile keeps the build path identical across local
# development, CI, and release tagging. Anything CI does should be
# expressible as `make <target>`.

.PHONY: build test test-short test-integration lint vet fmt run-controlplane run-pop clean help

# Version inputs. Override on the command line for tagged releases:
#   make build VERSION=v0.0.1
VERSION    ?= dev
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X github.com/shankar0123/netsite/pkg/version.Version=$(VERSION) \
           -X github.com/shankar0123/netsite/pkg/version.Commit=$(COMMIT) \
           -X github.com/shankar0123/netsite/pkg/version.BuildDate=$(BUILD_DATE)

help: ## Print this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build all binaries with version metadata injected.
	go build -ldflags "$(LDFLAGS)" -o ns ./cmd/ns
	go build -ldflags "$(LDFLAGS)" -o ns-controlplane ./cmd/ns-controlplane
	go build -ldflags "$(LDFLAGS)" -o ns-pop ./cmd/ns-pop

test: ## Run the test suite with the race detector and coverage.
	go test -race -coverprofile=coverage.out ./...

test-short: ## Run unit tests only, no race detector. Fast feedback loop.
	go test -short ./...

test-integration: ## Run integration tests (-tags integration). Requires Docker for testcontainers.
	go test -race -tags integration ./...

vet: ## Run go vet.
	go vet ./...

fmt: ## Run gofmt -s -w on all Go files (run before commit).
	gofmt -s -w .

lint: ## Run golangci-lint (must be installed locally).
	golangci-lint run --timeout=5m

run-controlplane: build ## Run ns-controlplane against the local compose stack.
	NETSITE_CONTROLPLANE_DB_URL="$${NETSITE_CONTROLPLANE_DB_URL:-postgres://netsite:netsite@localhost:5432/netsite?sslmode=disable}" \
	NETSITE_CONTROLPLANE_CH_URL="$${NETSITE_CONTROLPLANE_CH_URL:-clickhouse://netsite:netsite@localhost:9000/netsite}" \
	NETSITE_CONTROLPLANE_NATS_URL="$${NETSITE_CONTROLPLANE_NATS_URL:-nats://localhost:4222}" \
	NETSITE_CONTROLPLANE_HTTP_ADDR="$${NETSITE_CONTROLPLANE_HTTP_ADDR:-:8080}" \
	NETSITE_OTEL_OTLP_ENDPOINT="$${NETSITE_OTEL_OTLP_ENDPOINT:-localhost:4317}" \
	NETSITE_OTEL_INSECURE="$${NETSITE_OTEL_INSECURE:-true}" \
	./ns-controlplane

run-pop: build ## Run ns-pop with NETSITE_POP_CONFIG pointing at a YAML.
	NETSITE_POP_CONFIG="$${NETSITE_POP_CONFIG:-deploy/compose/pop.example.yaml}" \
	NETSITE_OTEL_OTLP_ENDPOINT="$${NETSITE_OTEL_OTLP_ENDPOINT:-localhost:4317}" \
	NETSITE_OTEL_INSECURE="$${NETSITE_OTEL_INSECURE:-true}" \
	./ns-pop

clean: ## Remove build artifacts.
	rm -f ns ns-controlplane ns-pop coverage.out coverage.html

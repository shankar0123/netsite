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

build: ## Build the ns CLI with version metadata injected.
	go build -ldflags "$(LDFLAGS)" -o ns ./cmd/ns

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

run-controlplane: ## Run ns-controlplane (not yet implemented; Task 0.12).
	@echo "ns-controlplane not yet implemented (Task 0.12). See PROJECT_STATE.md."
	@exit 1

run-pop: ## Run ns-pop (not yet implemented; Task 0.17).
	@echo "ns-pop not yet implemented (Task 0.17). See PROJECT_STATE.md."
	@exit 1

clean: ## Remove build artifacts.
	rm -f ns coverage.out coverage.html

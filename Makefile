# Single entry point for developers and CI.
# Tool versions are pinned here; bump them to the latest stable release (see docs/adr/0002-dependencies.md).

GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION   := v1.9.0
GORELEASER_VERSION    := v2.18.3

# Build tools with the same toolchain the project uses; otherwise `go run tool@version`
# may pick the (older) toolchain the tool itself requires and fail on newer code.
TOOL_ENV := GOTOOLCHAIN=$(shell go env GOVERSION)

GOLANGCI_LINT := $(TOOL_ENV) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := $(TOOL_ENV) go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
GORELEASER    := $(TOOL_ENV) go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

BINARY  := kagegami
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.DEFAULT_GOAL := help
.PHONY: help build test lint vuln fmt tidy-check snapshot dev

## Show available targets
help: 
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## Build a static binary into ./kagegami
build: 
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/$(BINARY)

## Run tests with the race detector and write coverage.out
test: 
	go test -race -count=1 -coverprofile=coverage.out ./...

## Run golangci-lint
lint: 
	$(GOLANGCI_LINT) run

## Scan dependencies and code for known vulnerabilities
vuln: 
	$(GOVULNCHECK) ./...

## Format code (gofumpt + goimports via golangci-lint)
fmt: 
	$(GOLANGCI_LINT) fmt

## Fail if go mod tidy changes go.mod or go.sum
tidy-check: 
	go mod tidy
	git diff --exit-code -- go.mod go.sum

## Build a local release snapshot with GoReleaser
snapshot: 
	$(GORELEASER) release --snapshot --clean
	
## Run the watcher from source
dev: 
	go run ./cmd/$(BINARY) watch

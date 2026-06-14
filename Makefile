SHELL       := /usr/bin/env bash
MODULE      := github.com/autobrr/harbrr
BINARY      := harbrr
BIN_DIR     := bin
PKG         := ./...

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

.DEFAULT_GOAL := help

## help: list targets
.PHONY: help
help:
	@grep -E '^##' $(MAKEFILE_LIST) | sed -E 's/## //'

## build: compile the binary to bin/harbrr
.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) ./cmd/harbrr

## backend: alias for build
.PHONY: backend
backend: build

## test: run the full suite with the race detector (always -race -count=1)
.PHONY: test
test:
	go test -race -count=1 $(PKG)

## test-short: run tests without the race detector (faster inner loop)
.PHONY: test-short
test-short:
	go test -count=1 $(PKG)

## test-openapi: validate the embedded management-API OpenAPI spec + handler drift
.PHONY: test-openapi
test-openapi:
	go test -race -count=1 ./internal/web/swagger/... ./internal/web/api/...

## vet: go vet
.PHONY: vet
vet:
	go vet $(PKG)

## lint: run golangci-lint
.PHONY: lint
lint:
	golangci-lint run

## lint-fix: run golangci-lint with --fix
.PHONY: lint-fix
lint-fix:
	golangci-lint run --fix

## lint-json: write lint-report.json
.PHONY: lint-json
lint-json:
	golangci-lint run --output.json.path lint-report.json || true

## fmt: format with the configured formatters (gofumpt + goimports)
.PHONY: fmt
fmt:
	golangci-lint fmt

## tidy: go mod tidy
.PHONY: tidy
tidy:
	go mod tidy

## precommit: fmt + lint + test (run before final on any code change)
.PHONY: precommit
precommit: fmt lint test

## ci: the checks CI enforces
.PHONY: ci
ci: vet lint test build

## docker: build the container image
.PHONY: docker
docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(BINARY):dev .

## vendor-defs: refresh the embedded Jackett definition snapshot
.PHONY: vendor-defs
vendor-defs:
	./scripts/vendor-definitions.sh

## tools: install dev tools (gofumpt, goimports, golangci-lint)
.PHONY: tools
tools:
	go install mvdan.cc/gofumpt@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

## clean: remove build artifacts
.PHONY: clean
clean:
	rm -rf $(BIN_DIR) lint-report.json

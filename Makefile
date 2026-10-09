GO ?= go
BINARY ?= bin/codex-bridge
PREFIX ?= /usr/local
DOWNLOAD_DIR ?= bin/downloads
VERSION ?= dev
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: help tidy frontend build build-all bridge-downloads portable-package test benchmark-formal-proof doc-lint run-hub run-bridge install clean docker

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

tidy: ## Sync go.mod / go.sum
	$(GO) mod tidy

frontend: ## Build the web UI into internal/web/static (requires Node 20+)
	cd frontend && npm ci && npm run build

build: ## Build the Go binary (assumes frontend is already built)
	CGO_ENABLED=0 $(GO) build -ldflags "-s -w" -o $(BINARY) .

build-all: frontend build ## Build the frontend then the Go binary

bridge-downloads: ## Build Linux and macOS amd64/arm64 endpoint binaries sequentially
	mkdir -p "$(DOWNLOAD_DIR)"
	@for platform in linux darwin; do for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=$$platform GOARCH=$$arch $(GO) build -p 1 \
			-ldflags "-s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" \
			-o "$(DOWNLOAD_DIR)/codex-bridge-$$platform-$$arch" . || exit $$?; \
	done; done

portable-package: ## Build a self-contained Linux amd64 tar.gz under dist/
	./scripts/build-portable-package.sh

test: ## Run the Go test suite
	$(GO) test ./...

benchmark-formal-proof: ## Run offline Coq and Isabelle proof benchmarks
	./scripts/run-formal-proof-benchmarks.sh --all

doc-lint: ## Validate docs, env references, and code anchors
	./scripts/check-docs.sh

run-hub: ## Run the Hub from source
	$(GO) run . hub

run-bridge: ## Run the Bridge from source
	$(GO) run . bridge

install: build ## Install the binary to $(PREFIX)/bin (use sudo if needed)
	install -m 0755 $(BINARY) $(PREFIX)/bin/codex-bridge

docker: ## Build the container image (tag: codex-bridge:local)
	docker build -t codex-bridge:local .

clean: ## Remove build artifacts
	rm -rf bin dist

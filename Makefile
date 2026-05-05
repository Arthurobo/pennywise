.PHONY: help build run dev test lint sqlc tailwind clean

VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/Arthurobo/pennywise/internal/cli.version=$(VERSION) \
	-X github.com/Arthurobo/pennywise/internal/cli.commit=$(COMMIT) \
	-X github.com/Arthurobo/pennywise/internal/cli.buildDate=$(DATE)

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: tailwind ## Build the binary for the current platform
	go build -trimpath -ldflags "$(LDFLAGS)" -o pennywise ./cmd/pennywise

run: build ## Build and run
	./pennywise

dev: tailwind ## Run in development mode (templates reload from disk)
	PENNYWISE_ENV=development go run ./cmd/pennywise

test: ## Run all tests with race detector
	go test -race -count=1 ./...

lint: ## Run linters
	golangci-lint run

sqlc: ## Regenerate sqlc code
	sqlc generate

tailwind: ## Build the CSS bundle
	bash scripts/tailwind.sh

clean: ## Remove build artifacts
	rm -f pennywise pennywise.exe
	rm -rf dist/
	rm -f internal/static/css/output.css

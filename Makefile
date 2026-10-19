.PHONY: build clean run install test test-race vet lint fmt tidy bench ci check \
        cover cover-html cover-check e2e docker release tools print-mcp-config e2e conformance

# --- Configuration ---
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

MODULE      := github.com/yourorg/deepseek-mcp
LDFLAGS     := -s -w \
               -X main.version=$(GIT_TAG) \
               -X main.commit=$(GIT_COMMIT) \
               -X main.buildTime=$(BUILD_TIME)

BIN         := bin/deepseek-mcp
PKG         := ./cmd/deepseek-mcp
IMAGE       ?= ghcr.io/yourorg/deepseek-mcp:$(GIT_TAG)
COVERAGE    := coverage.out
COVER_MIN   ?= 50

# --- Build ---

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

clean:
	rm -rf bin/ dist/ $(COVERAGE) coverage.html

install:
	CGO_ENABLED=0 go install -trimpath -ldflags="$(LDFLAGS)" $(PKG)

run: build
	./$(BIN)

# --- Quality ---

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w -s .

tidy:
	go mod tidy

lint:
	@command -v golangci-lint >/dev/null || { \
		echo "golangci-lint not installed: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	}
	golangci-lint run ./...

bench:
	go test -bench=. -benchmem ./...

ci: vet test-race lint

check: build test

# --- Coverage ---

cover:
	go test -race -covermode=atomic -coverprofile=$(COVERAGE) ./...
	@go tool cover -func=$(COVERAGE) | tail -1

cover-html: cover
	@go tool cover -html=$(COVERAGE) -o coverage.html
	@echo "→ coverage.html"

cover-check: cover
	@total=$$(go tool cover -func=$(COVERAGE) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	whole=$${total%.*}; \
	if [ "$$whole" -lt "$(COVER_MIN)" ]; then \
		echo "coverage $$total% < threshold $(COVER_MIN)%"; exit 1; \
	fi; \
	echo "coverage $$total% >= threshold $(COVER_MIN)%"

# --- Integration / E2E ---

# E2E — бьют по реальному API, стоят денег. Запускать вручную.
e2e: build
	@if [ -z "$$DEEPSEEK_API_KEY" ]; then \
		echo "DEEPSEEK_API_KEY not set"; exit 1; \
	fi
	DEEPSEEK_E2E=1 go test -v -count=1 ./test/e2e/...

# Conformance — wire-level MCP-протокол, без сети и без внешних CLI.
conformance: build
	go test -v -count=1 ./test/conformance/...

# --- Distribution ---

docker:
	docker build -t $(IMAGE) .

release:
	@command -v goreleaser >/dev/null || { \
		echo "goreleaser not installed: go install github.com/goreleaser/goreleaser@latest"; \
		exit 1; \
	}
	goreleaser release --clean

# --- Developer helpers ---

tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/goreleaser/goreleaser@latest

# Печатает JSON-фрагмент для Claude Desktop / subconscious-core.
print-mcp-config: build
	./$(BIN) --print-mcp-config

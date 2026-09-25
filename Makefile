BINARY  := celadon
PKG     := ./...
# The toolchain's own gofmt: the one on PATH may predate the go directive,
# and an older gofmt cannot parse the generic methods Go 1.27 allows.
GOFMT   := $(shell go env GOROOT)/bin/gofmt
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/x-chunk/celadon/internal/version.Version=$(VERSION) \
	-X github.com/x-chunk/celadon/internal/version.Commit=$(COMMIT) \
	-X github.com/x-chunk/celadon/internal/version.Date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build bin/celadon with the version stamped in
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/celadon

.PHONY: install
install: ## Install celadon into $GOBIN
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/celadon

.PHONY: run
run: ## Run celadon (pass arguments with ARGS="...")
	go run ./cmd/celadon $(ARGS)

.PHONY: test
test: ## Run the tests with the race detector
	go test -race $(PKG)

.PHONY: cover
cover: ## Run the tests and open the coverage report
	go test -coverprofile=coverage.out $(PKG)
	go tool cover -html=coverage.out

.PHONY: fmt
fmt: ## Format the code
	$(GOFMT) -w .

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum
	go mod tidy

.PHONY: check
check: ## Verify formatting, tidiness, vet and tests, as CI does
	@test -z "$$($(GOFMT) -l .)" || { $(GOFMT) -l .; echo "run make fmt"; exit 1; }
	go mod tidy -diff
	go vet $(PKG)
	go test -race $(PKG)

.PHONY: docs
docs: ## Generate man pages and shell completions into generated/
	go run ./cmd/gendocs -out generated

.PHONY: snapshot
snapshot: ## Build a local release snapshot with GoReleaser
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist generated coverage.out

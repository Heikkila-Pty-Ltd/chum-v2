# CHUM v2 Makefile

BINARY := chum
GO := go

.PHONY: build test vet clean

build: ## Build chum binary
	$(GO) build -o $(BINARY) ./cmd/chum/

test: ## Run all tests
	$(GO) test ./...

vet: ## Run go vet
	$(GO) vet ./...

clean: ## Remove build artifacts
	rm -f $(BINARY)

check: build test vet ## Run all checks

help: ## Display help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

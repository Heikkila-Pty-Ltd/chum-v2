# CHUM v2 Makefile

BINARY := chum
GO := go

.PHONY: build test vet fmt fmt-check quality check clean

build: ## Build chum binary
	$(GO) build -o $(BINARY) ./cmd/chum/

test: ## Run all tests
	$(GO) test ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format Go source files in place
	gofmt -w $$(git ls-files '*.go')

fmt-check: ## Fail if any Go files are not gofmt-formatted
	@unformatted="$$(gofmt -l $$(git ls-files '*.go'))"; \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted Go files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

quality: fmt-check vet test build ## Run mandatory quality gates

clean: ## Remove build artifacts
	rm -f $(BINARY)

check: quality ## Alias for quality gates

help: ## Display help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

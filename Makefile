BINARY := bin/kboba
PKG    := ./cmd/kboba

.PHONY: build test lint run clean

build: ## Build the kboba binary
	go build -o $(BINARY) $(PKG)

test: ## Run unit tests
	go test ./...

lint: ## go vet, plus golangci-lint when installed
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else echo "golangci-lint not installed, skipping"; fi

run: build ## Run against the current kubeconfig context
	./$(BINARY)

clean:
	rm -rf bin

BINARY := bin/kboba
PKG    := ./cmd/kboba

# The kind cluster gets its own kubeconfig under .kind/, so your
# ~/.kube/config is never touched and no real cluster is ever targeted.
KIND_CLUSTER     := kboba
KIND_CONTEXT     := kind-$(KIND_CLUSTER)
KIND_DIR         := .kind
ADMIN_KUBECONFIG := $(KIND_DIR)/admin.kubeconfig
RO_KUBECONFIG    := $(KIND_DIR)/readonly.kubeconfig
KUBECTL          := kubectl --kubeconfig $(ADMIN_KUBECONFIG) --context $(KIND_CONTEXT)

.PHONY: build test lint run clean kind-up kind-kubeconfig run-kind kind-down help

build: ## Build the kboba binary
	go build -o $(BINARY) $(PKG)

test: ## Run unit tests
	go test ./...

lint: ## go vet, plus golangci-lint when installed
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else echo "golangci-lint not installed, skipping"; fi

run: build ## Run against your current kubeconfig context
	./$(BINARY)

clean: ## Remove build output
	rm -rf bin

kind-up: ## Create the local kind cluster with sample pods and read-only RBAC
	@mkdir -p $(KIND_DIR)
	@kind get clusters 2>/dev/null | grep -qx $(KIND_CLUSTER) || \
		kind create cluster --name $(KIND_CLUSTER) --kubeconfig $(ADMIN_KUBECONFIG)
	@test -f $(ADMIN_KUBECONFIG) || kind export kubeconfig --name $(KIND_CLUSTER) --kubeconfig $(ADMIN_KUBECONFIG)
	$(KUBECTL) apply -f hack/kind/demo.yaml -f hack/kind/rbac.yaml

kind-kubeconfig: kind-up ## Write a read-only kubeconfig to .kind/readonly.kubeconfig
	hack/kind/readonly-kubeconfig.sh $(ADMIN_KUBECONFIG) $(KIND_CONTEXT) > $(RO_KUBECONFIG)
	@echo "wrote $(RO_KUBECONFIG) (contexts: kboba-readonly, kboba-limited, kboba-unreachable)"

run-kind: build kind-kubeconfig ## Run kboba on the kind cluster as a read-only ServiceAccount
	./$(BINARY) --kubeconfig $(RO_KUBECONFIG)

kind-down: ## Delete the kind cluster
	kind delete cluster --name $(KIND_CLUSTER) --kubeconfig $(ADMIN_KUBECONFIG)
	rm -rf $(KIND_DIR)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

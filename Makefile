GO ?= go
GINKGO ?= $(GO) run github.com/onsi/ginkgo/v2/ginkgo

# Default settings
PLATFORM ?= any
NAMESPACE ?= llm-conformance-test
KUBECONFIG ?= $(HOME)/.kube/config
REPORT_DIR ?= reports
TESTCASE_DIR ?= configs/testcases
PROFILE ?=
TESTCASE ?=
LABELS ?=
STORAGE_CLASS ?=

# Build flags
GINKGO_FLAGS ?= -v --fail-fast
TEST_FLAGS = -platform=$(PLATFORM) -namespace=$(NAMESPACE) -kubeconfig=$(KUBECONFIG) \
             -report-dir=$(REPORT_DIR) -testcase-dir=$(TESTCASE_DIR)

ifdef STORAGE_CLASS
  TEST_FLAGS += -storage-class=$(STORAGE_CLASS)
endif

ifdef PROFILE
  TEST_FLAGS += -profile=$(PROFILE)
endif
ifdef TESTCASE
  TEST_FLAGS += -testcase=$(TESTCASE)
endif
ifdef LABELS
  TEST_FLAGS += -labels=$(LABELS)
endif

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-25s\033[0m %s\n", $$1, $$2}'

.PHONY: deps
deps: ## Install Go dependencies
	$(GO) mod tidy
	$(GO) mod download

.PHONY: build
build: deps ## Build and verify compilation
	$(GO) build ./...

.PHONY: lint
lint: ## Run linters
	$(GO) vet ./...

# ─── Test Profiles ────────────────────────────────────────────────

.PHONY: test-smoke
test-smoke: ## Run smoke tests (framework validation, no cluster needed)
	$(GO) test ./tests/smoke/ $(GINKGO_FLAGS) -- $(TEST_FLAGS)

.PHONY: test-conformance
test-conformance: ## Run full conformance suite with a profile
	$(GO) test ./tests/ $(GINKGO_FLAGS) -timeout 6h -- $(TEST_FLAGS)

.PHONY: test-profile-smoke
test-profile-smoke: ## Run the smoke profile (CPU-only, quick validation)
	$(MAKE) test-conformance PROFILE=configs/profiles/smoke.yaml

.PHONY: test-profile-cpu
test-profile-cpu: ## Run all CPU test cases
	$(MAKE) test-conformance PROFILE=configs/profiles/cpu-full.yaml

.PHONY: test-profile-gpu
test-profile-gpu: ## Run single-node GPU test cases
	$(MAKE) test-conformance PROFILE=configs/profiles/single-node-gpu.yaml

.PHONY: test-profile-deepseek
test-profile-deepseek: ## Run DeepSeek MoE test cases
	$(MAKE) test-conformance PROFILE=configs/profiles/deepseek.yaml

.PHONY: test-profile-cache
test-profile-cache: ## Run cache-aware routing test cases
	$(MAKE) test-conformance PROFILE=configs/profiles/cache-aware.yaml

.PHONY: test-profile-full
test-profile-full: ## Run ALL test cases (full conformance)
	$(MAKE) test-conformance PROFILE=configs/profiles/full.yaml

# ─── Single Test Case ────────────────────────────────────────────

.PHONY: test-single
test-single: ## Run a single test case (usage: make test-single TESTCASE=opt-125m-cpu)
ifndef TESTCASE
	$(error TESTCASE is required. Usage: make test-single TESTCASE=opt-125m-cpu)
endif
	$(MAKE) test-conformance TESTCASE=$(TESTCASE)

# ─── By Label ────────────────────────────────────────────────────

.PHONY: test-by-label
test-by-label: ## Run tests by label (usage: make test-by-label LABELS=cpu,smoke)
ifndef LABELS
	$(error LABELS is required. Usage: make test-by-label LABELS=cpu,smoke)
endif
	$(MAKE) test-conformance LABELS=$(LABELS)

# ─── Platform-Specific ──────────────────────────────────────────

.PHONY: test-ocp
test-ocp: ## Run conformance on OpenShift
	$(MAKE) test-conformance PLATFORM=ocp

.PHONY: test-aks
test-aks: ## Run conformance on AKS
	$(MAKE) test-conformance PLATFORM=aks

.PHONY: test-gks
test-gks: ## Run conformance on GKS
	$(MAKE) test-conformance PLATFORM=gks

# ─── Failure Tests ───────────────────────────────────────────────

.PHONY: test-failure
test-failure: ## Run failure scenario tests
	$(GO) test ./tests/failure/ $(GINKGO_FLAGS) -- -platform=$(PLATFORM) -namespace=$(NAMESPACE) -kubeconfig=$(KUBECONFIG)

# ─── All Tests ───────────────────────────────────────────────────

.PHONY: test-all
test-all: test-smoke test-conformance test-failure ## Run all test suites

# ─── Utilities ───────────────────────────────────────────────────

.PHONY: clean
clean: ## Remove generated reports
	rm -rf $(REPORT_DIR)/*.json

.PHONY: list-profiles
list-profiles: ## List available test profiles
	@echo "Available profiles:"
	@for f in configs/profiles/*.yaml; do \
		name=$$(grep '^name:' $$f | head -1 | awk '{print $$2}'); \
		desc=$$(grep '^description:' $$f | head -1 | sed 's/^description: *"//;s/"$$//'); \
		printf "  \033[36m%-20s\033[0m %s\n" "$$name" "$$desc"; \
	done

.PHONY: list-testcases
list-testcases: ## List available test cases
	@echo "Available test cases:"
	@for f in configs/testcases/*.yaml; do \
		name=$$(grep '^name:' $$f | head -1 | awk '{print $$2}'); \
		cat=$$(grep '  category:' $$f | head -1 | awk '{print $$2}'); \
		printf "  \033[36m%-40s\033[0m [%s]\n" "$$name" "$$cat"; \
	done

.PHONY: list-labels
list-labels: ## List all unique labels across test cases
	@echo "Available labels:"
	@grep -h '  - ' configs/testcases/*.yaml | sort -u | sed 's/  - /  /'

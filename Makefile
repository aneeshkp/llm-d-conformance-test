GO ?= go

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
MODE ?= deploy
ENDPOINT ?=
MODEL_SOURCE ?= pvc
NO_CLEANUP ?=

# Build flags
GO_TEST_FLAGS ?= -v
GINKGO_FLAGS ?= --ginkgo.v --ginkgo.fail-fast --ginkgo.silence-skips
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
ifneq ($(MODE),deploy)
  TEST_FLAGS += -mode=$(MODE)
endif
ifneq ($(MODEL_SOURCE),pvc)
  TEST_FLAGS += -model-source=$(MODEL_SOURCE)
endif
ifdef ENDPOINT
  TEST_FLAGS += -endpoint=$(ENDPOINT)
endif
ifdef NO_CLEANUP
  TEST_FLAGS += -no-cleanup
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

# ─── Framework Validation ────────────────────────────────────────

.PHONY: test-smoke
test-smoke: ## Run smoke tests (framework validation, no cluster needed)
	$(GO) test ./tests/smoke/ $(GO_TEST_FLAGS) -timeout 30m -args $(GINKGO_FLAGS)

# ─── Single Test Case ────────────────────────────────────────────

.PHONY: test-single
test-single: ## Run a single test case (usage: make test-single TESTCASE=qwen2-7b-gpu)
ifndef TESTCASE
	$(error TESTCASE is required. Usage: make test-single TESTCASE=qwen2-7b-gpu)
endif
	$(MAKE) test-conformance TESTCASE=$(TESTCASE)

# ─── Conformance Suite ───────────────────────────────────────────

.PHONY: test-conformance
test-conformance: ## Run conformance suite with a profile or flags
	$(GO) test ./tests/ $(GO_TEST_FLAGS) -timeout 6h -args $(GINKGO_FLAGS) $(TEST_FLAGS)

# ─── Test Profiles ───────────────────────────────────────────────

.PHONY: test-profile-smoke
test-profile-smoke: ## Quick smoke test (single GPU model)
	$(MAKE) test-conformance PROFILE=configs/profiles/smoke.yaml

.PHONY: test-profile-gpu
test-profile-gpu: ## Run single-node GPU test cases (Qwen2.5-7B)
	$(MAKE) test-conformance PROFILE=configs/profiles/single-node-gpu.yaml

.PHONY: test-profile-deepseek
test-profile-deepseek: ## Run DeepSeek MoE test cases (multi-node, EP/DP)
	$(MAKE) test-conformance PROFILE=configs/profiles/deepseek.yaml

.PHONY: test-profile-cache
test-profile-cache: ## Run cache-aware routing test cases
	$(MAKE) test-conformance PROFILE=configs/profiles/cache-aware.yaml

.PHONY: test-profile-full
test-profile-full: ## Run ALL GPU test cases (full conformance)
	$(MAKE) test-conformance PROFILE=configs/profiles/full.yaml

# ─── Model Caching ───────────────────────────────────────────────

.PHONY: cache-models
cache-models: ## Pre-download all models to PVCs (run before tests to speed them up)
	$(MAKE) test-conformance MODE=cache

.PHONY: cache-model
cache-model: ## Pre-download a single model (usage: make cache-model TESTCASE=qwen2-7b-gpu)
ifndef TESTCASE
	$(error TESTCASE is required. Usage: make cache-model TESTCASE=qwen2-7b-gpu)
endif
	$(MAKE) test-conformance MODE=cache TESTCASE=$(TESTCASE)

# ─── Discover (validate existing deployment) ─────────────────────

.PHONY: test-discover
test-discover: ## Validate existing deployment (usage: make test-discover ENDPOINT=http://svc:8000 TESTCASE=qwen2-7b-gpu)
ifndef ENDPOINT
	$(error ENDPOINT is required. Usage: make test-discover ENDPOINT=http://svc:8000 TESTCASE=qwen2-7b-gpu)
endif
ifndef TESTCASE
	$(error TESTCASE is required. Usage: make test-discover ENDPOINT=http://svc:8000 TESTCASE=qwen2-7b-gpu)
endif
	$(MAKE) test-conformance MODE=discover ENDPOINT=$(ENDPOINT) TESTCASE=$(TESTCASE)

# ─── By Label ────────────────────────────────────────────────────

.PHONY: test-by-label
test-by-label: ## Run tests by label (usage: make test-by-label LABELS=gpu,smoke)
ifndef LABELS
	$(error LABELS is required. Usage: make test-by-label LABELS=gpu,smoke)
endif
	$(MAKE) test-conformance LABELS=$(LABELS)

# ─── Platform-Specific ───────────────────────────────────────────

.PHONY: test-ocp
test-ocp: ## Run conformance on OpenShift
	$(MAKE) test-conformance PLATFORM=ocp

.PHONY: test-aks
test-aks: ## Run conformance on AKS
	$(MAKE) test-conformance PLATFORM=aks

.PHONY: test-gks
test-gks: ## Run conformance on GKS
	$(MAKE) test-conformance PLATFORM=gks

# ─── Utilities ───────────────────────────────────────────────────

.PHONY: delete-testcase
delete-testcase: ## Delete a test case and its manifests (usage: make delete-testcase TESTCASE=my-model)
ifndef TESTCASE
	$(error TESTCASE is required. Usage: make delete-testcase TESTCASE=my-model)
endif
	@echo "Deleting test case: $(TESTCASE)"
	@for f in configs/testcases/$(TESTCASE).yaml deploy/manifests/hf/$(TESTCASE).yaml deploy/manifests/pvc/$(TESTCASE).yaml; do \
		if [ -f "$$f" ]; then \
			echo "  rm $$f"; \
			rm "$$f"; \
		else \
			echo "  $$f (not found, skipping)"; \
		fi; \
	done
	@echo "Done. Remember to remove $(TESTCASE) from any profiles in configs/profiles/"

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
list-testcases: ## List available test cases grouped by category
	@echo ""
	@for category in single-node-gpu multi-node-gpu cache-aware deepseek; do \
		case $$category in \
			single-node-gpu) header="Single-node GPU" ;; \
			multi-node-gpu)  header="Multi-node GPU (P/D)" ;; \
			cache-aware)     header="Cache-aware routing" ;; \
			deepseek)        header="DeepSeek MoE (8+ GPUs)" ;; \
		esac; \
		printf '  \033[1m%s:\033[0m\n' "$$header"; \
		for f in configs/testcases/*.yaml; do \
			cat=$$(grep '  category:' $$f | head -1 | awk '{print $$2}'); \
			if [ "$$cat" = "$$category" ]; then \
				name=$$(grep '^name:' $$f | head -1 | awk '{print $$2}'); \
				desc=$$(grep '^description:' $$f | head -1 | sed 's/^description: *"//;s/"$$//'); \
				printf "    \033[36m%-35s\033[0m %s\n" "$$name" "$$desc"; \
			fi; \
		done; \
		echo ""; \
	done

.PHONY: list-models
list-models: ## List models, HF and PVC URIs for all test cases
	@echo ""
	@printf "  \033[36m%-38s %-12s %-50s %s\033[0m\n" "TEST CASE" "MODE" "HF MANIFEST URI" "PVC MANIFEST URI"
	@echo "  $(shell printf '%.0s─' {1..150})"
	@for f in configs/testcases/*.yaml; do \
		name=$$(grep '^name:' $$f | head -1 | awk '{print $$2}'); \
		manifest=$$(grep 'manifestPath:' $$f | head -1 | awk '{print $$2}'); \
		hf_uri="—"; pvc_uri="—"; \
		hf_manifest="deploy/manifests/hf/$$manifest"; \
		pvc_manifest="deploy/manifests/pvc/$$manifest"; \
		if [ -f "$$hf_manifest" ]; then \
			hf_uri=$$(grep '  uri:' "$$hf_manifest" | head -1 | awk '{print $$2}'); \
		fi; \
		if [ -f "$$pvc_manifest" ]; then \
			pvc_uri=$$(grep '  uri:' "$$pvc_manifest" | head -1 | awk '{print $$2}'); \
		fi; \
		printf "  %-38s %-12s %-50s %s\n" "$$name" "hf + pvc" "$$hf_uri" "$$pvc_uri"; \
	done
	@echo ""
	@echo "  Pre-cache summary (make cache-models):"
	@echo "  ┌──────────────────────────────────────────────────────────────────────────────────────────┐"
	@printf "  │ %-88s│\n" "PVCs to create:"
	@grep -rh 'uri:' deploy/manifests/pvc/ 2>/dev/null | grep 'pvc://' | awk '{print $$2}' | sed 's|pvc://||' | cut -d/ -f1 | sort -u | while read pvc; do \
		printf "  │   %-86s│\n" "$$pvc"; \
	done
	@printf "  │ %-88s│\n" ""
	@printf "  │ %-88s│\n" "Models to download:"
	@grep -rh '  uri: hf://' deploy/manifests/hf/ 2>/dev/null | sort -u | sed 's/.*uri: //' | while read uri; do \
		printf "  │   %-86s│\n" "$$uri"; \
	done
	@printf "  │ %-88s│\n" ""
	@pvc_count=$$(grep -rh 'uri:' deploy/manifests/pvc/ 2>/dev/null | grep 'pvc://' | awk '{print $$2}' | sed 's|pvc://||' | cut -d/ -f1 | sort -u | wc -l); \
	 dl_count=$$(grep -rh '  uri: hf://' deploy/manifests/hf/ 2>/dev/null | sort -u | wc -l); \
	 printf "  │ %-88s│\n" "Total: $$pvc_count PVCs, $$dl_count model downloads"
	@echo "  └──────────────────────────────────────────────────────────────────────────────────────────┘"
	@echo ""
	@echo "  Usage:"
	@echo "    make cache-models                              # download all models to PVCs"
	@echo "    make test-single TESTCASE=qwen2-7b-gpu         # default: uses pvc/ manifests"
	@echo "    make test-single TESTCASE=qwen2-7b-gpu MODEL_SOURCE=hf  # uses hf/ manifests"
	@echo ""

.PHONY: list-labels
list-labels: ## List all unique labels across test cases
	@echo "Available labels:"
	@grep -h '  - ' configs/testcases/*.yaml | sort -u | sed 's/  - /  /'

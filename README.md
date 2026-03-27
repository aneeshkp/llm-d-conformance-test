# LLM-D Conformance Test Framework

Automated end-to-end test framework for validating LLM inference deployments on Kubernetes (OpenShift, AKS, CKS) using the `LLMInferenceService` CRD from [KServe](https://github.com/opendatahub-io/kserve).

Built with **Go**, **Ginkgo + Gomega**, and driven entirely by YAML configs.

For the full test plan with detailed test case descriptions, environment matrix, pass/fail criteria, and model caching strategy, see [docs/test-plan.md](docs/test-plan.md).

## Prerequisites

- Go 1.23+
- Access to a Kubernetes/OpenShift cluster with:
  - `LLMInferenceService` CRD installed ([deployment guide](https://github.com/opendatahub-io/rhaii-on-xks/blob/main/docs/deploying-llm-d-on-managed-kubernetes.md))
  - KServe controller running
  - Gateway API configured
- `kubectl` (or `oc` for OpenShift) configured and pointing to your cluster
- GPU nodes available (for GPU test cases) or CPU-only nodes (for smoke/CPU tests)

## Quick Start

```bash
# 1. Clone the repo
git clone https://github.com/aneeshkp/llm-d-conformance-test.git
cd llm-d-conformance-test

# 2. Install dependencies
make deps

# 3. Verify the framework works (no cluster needed)
make test-smoke

# 4. Run CPU-only tests on your cluster
make test-profile-smoke PLATFORM=ocp KUBECONFIG=~/.kube/config

# 5. Run full conformance
make test-profile-full PLATFORM=aks NAMESPACE=llm-test
```

## Project Structure

```
├── framework/                 # Core framework code
│   ├── config/                # Config types, YAML loader, profile/label filtering
│   ├── deployer/              # K8s deployer (kubectl/oc) with OCP/AKS/CKS support
│   ├── client/                # OpenAI-compatible LLM API client
│   ├── model/                 # Model download and PVC-based caching
│   ├── reporter/              # JSON test report generator
│   ├── retry/                 # Retry with timeout/interval utilities
│   └── cleanup/               # Resource cleanup manager
├── tests/                     # Ginkgo test suites
│   ├── suite_test.go          # Main test runner with CLI flags
│   ├── conformance_test.go    # Full lifecycle tests (deploy → validate → cleanup)
│   ├── smoke/                 # Framework validation (no cluster needed)
│   ├── happy_path/            # Happy-path tests per category
│   └── failure/               # Negative/failure scenario tests
├── deploy/manifests/          # LLMInferenceService YAML manifests
├── configs/
│   ├── testcases/             # Individual test case definitions
│   └── profiles/              # Named test profiles
├── docs/
│   └── test-plan.md           # Full test plan document
├── reports/                   # JSON reports (generated, gitignored)
└── Makefile                   # All commands documented via `make help`
```

## Running Tests

### By Profile

```bash
make test-profile-smoke      # CPU-only, quick validation
make test-profile-cpu        # All CPU test cases
make test-profile-gpu        # Single-node GPU (Qwen2.5-7B)
make test-profile-deepseek   # DeepSeek MoE (8 GPUs + RDMA)
make test-profile-cache      # KV cache-aware routing
make test-profile-full       # All 10 test cases
```

### By Single Test Case

```bash
make test-single TESTCASE=opt-125m-cpu
make test-single TESTCASE=qwen2-7b-gpu
make test-single TESTCASE=deepseek-r1-dp-ep-ht
```

### By Label

```bash
make test-by-label LABELS=cpu
make test-by-label LABELS=gpu,deepseek
make test-by-label LABELS=pd              # prefill/decode tests
make test-by-label LABELS=smoke
```

### Platform-Specific

```bash
make test-profile-smoke PLATFORM=ocp      # OpenShift (auto-uses oc)
make test-profile-cpu   PLATFORM=aks      # Azure Kubernetes Service
make test-profile-full  PLATFORM=cks      # CoreWeave Kubernetes
```

### Using `go test` Directly

```bash
go test ./tests/ -v -count=1 -- \
  -profile=configs/profiles/smoke.yaml \
  -platform=ocp \
  -namespace=llm-test \
  -kubeconfig=~/.kube/config

go test ./tests/ -v -count=1 -- -testcase=opt-125m-cpu
go test ./tests/ -v -count=1 -- -labels=cpu,smoke
go test ./tests/smoke/ -v -count=1          # no cluster needed
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `PLATFORM` | `any` | `ocp`, `aks`, `cks`, `any` |
| `NAMESPACE` | `llm-conformance-test` | Target K8s namespace |
| `KUBECONFIG` | `$KUBECONFIG` | Path to kubeconfig |
| `REPORT_DIR` | `reports` | JSON report output directory |
| `STORAGE_CLASS` | (cluster default) | StorageClass for model cache PVCs |
| `TESTCASE` | (none) | Run a single test case by name |
| `LABELS` | (none) | Comma-separated label filter |

## Adding New Test Cases

1. Add a manifest in `deploy/manifests/your-model.yaml`
2. Create a test case config in `configs/testcases/your-model.yaml` (see existing configs for examples)
3. Add the test case name to a profile in `configs/profiles/`, or run directly:
   ```bash
   make test-single TESTCASE=your-model
   ```

No code changes needed. See the [test plan](docs/test-plan.md) for the full config schema.

## CI/CD Integration

### GitHub Actions

```yaml
jobs:
  conformance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - name: Framework smoke test
        run: make test-smoke
      - name: Run conformance
        run: make test-profile-smoke PLATFORM=aks KUBECONFIG=${{ secrets.KUBECONFIG_PATH }}
      - uses: actions/upload-artifact@v4
        with:
          name: test-reports
          path: reports/*.json
```

## Useful Commands

```bash
make help              # Show all available targets
make list-profiles     # List test profiles
make list-testcases    # List test cases
make list-labels       # List available labels
make build             # Verify compilation
make lint              # Run go vet
make clean             # Remove generated reports
```

# LLM-D Conformance Test Framework

Automated end-to-end test framework for validating LLM inference deployments on Kubernetes (OpenShift, AKS, GKS) using the `LLMInferenceService` CRD from [KServe](https://github.com/opendatahub-io/kserve).

Built with **Go**, **Ginkgo + Gomega**, and driven entirely by YAML configs.

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
│   ├── deployer/              # K8s deployer (kubectl/oc) with OCP/AKS/GKS support
│   ├── client/                # OpenAI-compatible LLM API client
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
├── reports/                   # JSON reports (generated, gitignored)
└── Makefile                   # All commands documented via `make help`
```

## Test Profiles

Profiles group test cases for different scenarios. List them with `make list-profiles`.

| Profile | Description | GPU Required |
|---------|-------------|:---:|
| `smoke` | Quick validation with CPU-only models | No |
| `cpu-full` | All CPU test cases (OPT-125M variants + simulator) | No |
| `single-node-gpu` | Qwen2.5-7B on single GPU nodes | Yes (1 GPU) |
| `cache-aware` | KV cache-aware routing (sim + GPU) | Partial |
| `deepseek` | DeepSeek MoE with data/expert parallelism | Yes (8 GPUs + RDMA) |
| `full` | All 10 test cases | Yes |

## Test Cases

List them with `make list-testcases`. Each test case is a YAML file in `configs/testcases/`.

| Test Case | Category | Model | GPUs |
|-----------|----------|-------|:----:|
| `opt-125m-cpu` | cpu | facebook/opt-125m | 0 |
| `opt-125m-cpu-no-scheduler` | cpu | facebook/opt-125m | 0 |
| `opt-125m-cpu-pd` | cpu | facebook/opt-125m (prefill/decode) | 0 |
| `cache-aware-sim-cpu` | cache-aware | llm-d-inference-sim | 0 |
| `cache-aware-qwen2-7b-gpu` | cache-aware | Qwen2.5-7B-Instruct | 1 |
| `qwen2-7b-gpu` | single-node-gpu | Qwen2.5-7B-Instruct | 1 |
| `qwen2-7b-gpu-no-scheduler` | single-node-gpu | Qwen2.5-7B-Instruct | 1 |
| `qwen2-7b-gpu-pd` | single-node-gpu | Qwen2.5-7B-Instruct (PD + RDMA) | 1 |
| `deepseek-r1-dp-ep-ht` | deepseek | DeepSeek-R1-0528 | 8 |
| `deepseek-coder-v2-dp-ep-naive` | deepseek | DeepSeek-Coder-V2-Lite | 8 |

## Running Tests

### By Profile

```bash
# Smoke test (no GPU needed)
make test-profile-smoke

# All CPU tests
make test-profile-cpu

# GPU tests
make test-profile-gpu

# DeepSeek MoE tests
make test-profile-deepseek

# Cache-aware routing tests
make test-profile-cache

# Full conformance
make test-profile-full
```

### By Single Test Case

```bash
make test-single TESTCASE=opt-125m-cpu
make test-single TESTCASE=qwen2-7b-gpu
make test-single TESTCASE=deepseek-r1-dp-ep-ht
```

### By Label

```bash
# Run all CPU tests
make test-by-label LABELS=cpu

# Run all tests with prefill/decode
make test-by-label LABELS=pd

# Run smoke-labeled tests
make test-by-label LABELS=smoke

# Combine labels
make test-by-label LABELS=gpu,cache-aware
```

Available labels: `cpu`, `gpu`, `smoke`, `simple`, `pd`, `prefill-decode`, `rdma`, `moe`, `dp-ep`, `deepseek`, `cache-aware`, `kv-cache`, `simulator`, `qwen2`, `opt-125m`, `no-scheduler`, `single-node-gpu`, `multi-node`, `naive`.

### Platform-Specific

```bash
# OpenShift (auto-uses `oc` CLI if available)
make test-profile-smoke PLATFORM=ocp

# Azure Kubernetes Service
make test-profile-gpu PLATFORM=aks

# Google Kubernetes Service
make test-profile-cpu PLATFORM=gks
```

### Common Flags

| Flag | Default | Description |
|------|---------|-------------|
| `PLATFORM` | `any` | Target platform: `ocp`, `aks`, `gks`, `any` |
| `NAMESPACE` | `llm-conformance-test` | Kubernetes namespace for deployments |
| `KUBECONFIG` | `$KUBECONFIG` or `~/.kube/config` | Path to kubeconfig |
| `REPORT_DIR` | `reports` | Output directory for JSON reports |

### Using `go test` Directly

```bash
# Run conformance with flags
go test ./tests/ -v -count=1 -- \
  -profile=configs/profiles/smoke.yaml \
  -platform=ocp \
  -namespace=llm-test \
  -kubeconfig=~/.kube/config \
  -report-dir=reports

# Run a single test case
go test ./tests/ -v -count=1 -- -testcase=opt-125m-cpu

# Run by labels
go test ./tests/ -v -count=1 -- -labels=cpu,smoke

# Run smoke tests (framework validation, no cluster)
go test ./tests/smoke/ -v -count=1

# Run failure scenario tests
go test ./tests/failure/ -v -count=1
```

## Test Lifecycle & Pass/Fail Criteria

Each test case goes through **8 phases** in order. A failure at any phase stops the test for that case, records the failure with the exact phase and error, cleans up, and moves to the next test case.

| Phase | Name | What It Does | Pass Criteria | Fail Criteria |
|:-----:|------|--------------|---------------|---------------|
| 1 | **PREP** | Download model to PVC (if `cache.enabled=true`) | Download Job `.status.succeeded=1` | Job fails or times out |
| 2 | **PREREQ** | Check `LLMInferenceService` CRD exists | CRD found on cluster | CRD missing (cluster not set up) |
| 3 | **DEPLOY** | `kubectl apply` the manifest | kubectl exits 0 | Manifest not found, invalid YAML, API error |
| 4 | **READY** | Wait for `llmisvc .status.ready=True` | Status becomes True within `readyTimeout` | Timeout (OOM, scheduling failure, image pull error) |
| 5 | **HEALTH** | `GET /health` on vLLM endpoint | HTTP 200 within `validation.timeout` | Non-200 or connection refused after retries |
| 6 | **MODEL** | `GET /v1/models`, verify model name | Model name appears in response | Warning only (non-blocking) |
| 7 | **INFERENCE** | `POST /v1/chat/completions` with test prompts | Non-empty `choices[].message.content` | Empty response or HTTP error (tries `/v1/completions` as fallback) |
| 8 | **CLEANUP** | `kubectl delete` manifest, wait for pod termination | Resources removed | Warning only (non-blocking) |

### Model Download & Caching (Phase 1: PREP)

Models are **downloaded once into a PVC** and reused across test runs. This avoids:
- Repeated multi-GB downloads during pod startup (Qwen2.5-7B is ~15GB, DeepSeek-R1 is ~600GB)
- Timeout failures caused by slow HuggingFace downloads inside vLLM pods
- Wasted bandwidth on every test run

**How it works:**

1. The framework creates a PVC (e.g., `model-cache-qwen-qwen2-5-7b-instruct`)
2. A Kubernetes Job downloads the model from HuggingFace into the PVC using `huggingface_hub`
3. The framework waits for the Job to succeed (`.status.succeeded=1`)
4. If the PVC already exists with a completed download Job, it skips re-download
5. The LLMInferenceService manifest uses `pvc://` URI to mount the cached model

**Configure per test case:**

```yaml
model:
  name: Qwen/Qwen2.5-7B-Instruct
  uri: hf://Qwen/Qwen2.5-7B-Instruct
  cache:
    enabled: true        # download to PVC before deploying
    storageSize: 100Gi   # PVC size (auto-sized if omitted)
    keepPVC: true        # keep PVC across runs for reuse
    timeout: 60m         # download timeout
    storageClass: ""     # uses cluster default if empty
```

**Storage classes by platform:**

| Platform | Default StorageClass | Notes |
|----------|---------------------|-------|
| **OCP** | `gp3-csi` (AWS), `managed-premium` (Azure), `standard-csi` | Auto-detected, or set via `-storage-class` flag |
| **AKS** | `managed-csi` (Azure Disk), `azurefile-csi` | Use `managed-csi` for single-node, `azurefile-csi` for ReadWriteMany |
| **GKS** | `standard-rwo` (pd-balanced), `premium-rwo` (pd-ssd) | Use `premium-rwo` for faster model loading |

Override at runtime:
```bash
make test-profile-gpu PLATFORM=aks STORAGE_CLASS=managed-csi
```

**Disable caching** (let vLLM download at startup — slower but simpler):
```yaml
model:
  cache:
    enabled: false
```

## JSON Reports

After each run, a JSON report is written to `reports/`. Example:

```json
{
  "suite": "llm-d-conformance",
  "profile": "smoke",
  "platform": "ocp",
  "startTime": "2026-03-27T10:00:00Z",
  "endTime": "2026-03-27T10:05:30Z",
  "duration": "5m30s",
  "environment": {
    "platform": "ocp",
    "kubernetesVersion": "v1.28.0",
    "namespace": "llm-test"
  },
  "results": [
    {
      "name": "opt-125m-cpu",
      "category": "cpu",
      "status": "pass",
      "duration": "2m15s",
      "logs": ["Namespace ready", "Applied manifest", "Health check passed", "Inference OK"],
      "model": {
        "name": "facebook/opt-125m",
        "uri": "hf://facebook/opt-125m",
        "category": "cpu",
        "containerImage": "public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.17.1"
      }
    }
  ],
  "summary": {
    "total": 2,
    "passed": 2,
    "failed": 0,
    "skipped": 0
  }
}
```

## Adding New Test Cases

1. Add a manifest in `deploy/manifests/your-model.yaml`
2. Create a test case config in `configs/testcases/your-model.yaml`:
   ```yaml
   name: your-model
   description: "Description of what this tests"
   labels:
     - gpu
     - your-category
   model:
     name: org/model-name
     uri: hf://org/model-name
     displayName: your-model-k8s-name
     category: your-category
   deployment:
     manifestPath: deploy/manifests/your-model.yaml
     replicas: 1
     readyTimeout: 10m
     resources:
       cpu: "4"
       memory: 32Gi
       gpus: 1
       rdma: false
   validation:
     healthEndpoint: /health
     healthPort: 8000
     healthScheme: HTTPS
     inferenceCheck: true
     testPrompts:
       - "Hello, world"
     expectedCodes: [200]
     timeout: 2m
     retryAttempts: 3
     retryInterval: 15s
   cleanup: true
   ```
3. Add the test case name to a profile in `configs/profiles/`, or run it directly:
   ```bash
   make test-single TESTCASE=your-model
   ```

No code changes needed.

## Adding a New Backend

The framework uses `LLMInferenceService` CRDs which abstract the backend. To add a new inference backend (e.g., TGI, Triton):

1. Create a new manifest following the CRD spec
2. Create a test case config pointing to it
3. The existing deploy/health/inference validation pipeline works for any OpenAI-compatible backend

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

### Prow

```yaml
- name: llm-d-conformance
  decorate: true
  spec:
    containers:
    - image: golang:1.23
      command: ["make", "test-profile-full"]
      env:
      - name: PLATFORM
        value: ocp
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

# LLM-D Conformance Test Framework

Automated end-to-end conformance tests for [llm-d](https://github.com/llm-d) inference deployments on Kubernetes using the `LLMInferenceService` CRD.

The test framework creates `LLMInferenceService` CRs on your cluster and validates the full lifecycle — the llm-d/KServe operator handles everything (vLLM image, pods, routing). Each test case is a proper Ginkgo spec with its own description and labels.

Built with **Go 1.24**, **Ginkgo v2**, and driven entirely by YAML configs. Zero code changes needed to add new models or test cases.

## Prerequisites

- Go 1.24+
- Access to a Kubernetes cluster with:
  - llm-d / KServe operator installed (`LLMInferenceService` CRD)
  - Gateway API configured
  - GPU nodes available
- `kubectl` (or `oc` for OpenShift)

## Quick Start

```bash
# 1. Clone and install dependencies
git clone https://github.com/aneeshkp/llm-d-conformance-test.git
cd llm-d-conformance-test
make deps

# 2. Verify the framework works (no cluster needed)
make test-smoke

# 3. Pre-cache a model to PVC (one-time, speeds up tests)
export KUBECONFIG=~/.kube/my-cluster
make cache-model TESTCASE=qwen2-7b-gpu

# 4. Run a single test
make test-single TESTCASE=qwen2-7b-gpu

# 5. Run full conformance
make test-profile-full
```

## Test Cases

Each test case is a Ginkgo spec with labels for filtering:

| Test Case | Model | Features | Labels |
|-----------|-------|----------|--------|
| `qwen2-7b-gpu` | Qwen2.5-7B-Instruct | Scheduler, 1 GPU | `gpu, smoke, single-node-gpu` |
| `qwen2-7b-gpu-no-scheduler` | Qwen2.5-7B-Instruct | No scheduler, K8s native routing | `gpu, single-node-gpu, no-scheduler` |
| `qwen2-7b-gpu-tp2` | Qwen2.5-7B-Instruct | Tensor parallelism (TP=2), model split across 2 GPUs | `gpu, single-node-gpu, tensor-parallel` |
| `qwen2-7b-gpu-pd` | Qwen2.5-7B-Instruct | Prefill/Decode disaggregation, RDMA | `gpu, multi-node-gpu, prefill-decode` |
| `qwen2-7b-gpu-pd-tcp` | Qwen2.5-7B-Instruct | Prefill/Decode over TCP (no RDMA) | `gpu, multi-node-gpu, prefill-decode, tcp` |
| `cache-aware-qwen2-7b-gpu` | Qwen2.5-7B-Instruct | Prefix KV cache routing | `gpu, cache-aware, kv-cache` |
| `deepseek-coder-v2-dp-ep-naive` | DeepSeek-Coder-V2 | DP=16, EP (naive), 8 GPUs | `gpu, deepseek, moe, dp-ep` |
| `deepseek-r1-dp-ep-ht` | DeepSeek-R1 | DP=32, EP (high-throughput), 8 GPUs, RDMA | `gpu, deepseek, moe, multi-node` |

## Test Lifecycle (8 Phases)

Each test case runs through these phases. During the READY wait, the framework shows detailed status of all sub-resources (PVC, Service, HTTPRoute, InferencePool, Pods):

1. **PREP** — Download model to PVC via KServe storage initializer (auto-detected from cluster)
2. **PREREQ** — Verify `LLMInferenceService` CRD exists
3. **DEPLOY** — `kubectl apply` the LLMInferenceService manifest
4. **READY** — Wait for `.status.ready=True` with live status dashboard:
   ```
   ─── [qwen2-7b-instruct-single] MinimumReplicasUnavailable (elapsed=33s, remaining=14m27s) ───
     LLMIsvc:        READY=       REASON=MinimumReplicasUnavailable
     URL:            http://20.7.109.216/llm-conformance-test/qwen2-7b-instruct-single
     PVC:            model-cache-qwen-qwen2-5-7b-instruct Bound 100Gi
     Service:        qwen2-7b-instruct-single-kserve-workload-svc ClusterIP=10.0.x.x
     HTTPRoute:      qwen2-7b-instruct-single-kserve-route
     InferencePool:  qwen2-7b-instruct-single
     Pod:            qwen2-7b-instruct-single-kserve-xxx Running ready=true restarts=0
   ```
5. **HEALTH** — `GET /health` on the inference endpoint
6. **MODEL** — `GET /v1/models`, verify model name listed
7. **INFERENCE** — `POST /v1/chat/completions` with test prompts
8. **CLEANUP** — Delete the LLMInferenceService

## Model Sources

Each test case has two manifest variants — PVC (pre-cached, default) and HF (download at startup):

```bash
make list-models    # see all models, PVC and HF URIs
```

| Mode | How it works | When to use |
|------|-------------|-------------|
| `pvc` (default) | Pre-download model to PVC, deploy with `pvc://` URI | Fast startup, recommended |
| `hf` | Deploy with `hf://` URI, vLLM downloads at pod startup | No PVC needed, slower |

```bash
# Pre-cache models (one-time)
make cache-models                                    # all models
make cache-model TESTCASE=qwen2-7b-gpu              # single model

# Run with PVC (default, fast)
make test-single TESTCASE=qwen2-7b-gpu

# Run with HuggingFace (no PVC needed)
make test-single TESTCASE=qwen2-7b-gpu MODEL_SOURCE=hf
```

The storage initializer image is auto-detected from the cluster's KServe `inferenceservice-config` configmap.

## Running Tests

### Single Test Case

```bash
make test-single TESTCASE=qwen2-7b-gpu
make test-single TESTCASE=deepseek-r1-dp-ep-ht
```

### By Profile

```bash
make test-profile-smoke      # Quick smoke test (single GPU model)
make test-profile-gpu        # Single-node GPU tests (Qwen2.5-7B)
make test-profile-deepseek   # DeepSeek MoE (multi-node, 8 GPUs)
make test-profile-cache      # Cache-aware routing tests
make test-profile-full       # All test cases
```

### By Label

```bash
make test-by-label LABELS=gpu
make test-by-label LABELS=deepseek
make test-by-label LABELS=smoke
```

### Discover Mode (validate existing deployment)

Skip deploy/cleanup and validate an already-running service:

```bash
make test-discover ENDPOINT=http://my-llm-svc:8000 TESTCASE=qwen2-7b-gpu
```

### Platform-Specific

```bash
make test-profile-full PLATFORM=ocp    # OpenShift (uses oc)
make test-profile-full PLATFORM=aks    # Azure Kubernetes Service
make test-profile-full PLATFORM=gks    # Google Kubernetes Service
```

## Configuration

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `PLATFORM` | `any` | `ocp`, `aks`, `gks`, `any` |
| `NAMESPACE` | `llm-conformance-test` | Target K8s namespace |
| `KUBECONFIG` | `$KUBECONFIG` | Path to kubeconfig |
| `STORAGE_CLASS` | cluster default | StorageClass for model cache PVCs (e.g., `azurefile` for RWX) |
| `MODEL_SOURCE` | `pvc` | `pvc` (pre-cached) or `hf` (HuggingFace direct) |
| `MODE` | `deploy` | `deploy`, `discover` (validate existing), or `cache` (download only) |
| `ENDPOINT` | — | Service URL for discover mode |

### Project Structure

```
├── framework/                 # Core framework code
│   ├── config/                # Config types, YAML loader, filtering
│   ├── deployer/              # K8s deployer (kubectl/oc) with status dashboard
│   ├── client/                # OpenAI-compatible API client
│   ├── model/                 # Model download via KServe storage initializer
│   ├── reporter/              # JSON report generator
│   ├── retry/                 # Retry utilities
│   └── cleanup/               # Resource cleanup
├── tests/
│   ├── conformance_test.go    # Ginkgo specs — one per test case with labels
│   ├── suite_test.go          # Ginkgo suite + CLI flags
│   └── smoke/                 # Framework validation (no cluster)
├── deploy/manifests/
│   ├── hf/                    # Manifests with hf:// URIs (HuggingFace)
│   └── pvc/                   # Manifests with pvc:// URIs (pre-cached)
├── configs/
│   ├── testcases/             # Test case definitions (YAML)
│   └── profiles/              # Named test profiles
├── docs/
│   └── adding-test-cases.md   # Guide for adding new models
└── reports/                   # JSON reports (generated)
```

## Adding New Test Cases

Adding a new model requires **zero code changes** — just 3 YAML files:

1. `deploy/manifests/hf/<name>.yaml` — LLMInferenceService with `hf://` URI
2. `deploy/manifests/pvc/<name>.yaml` — LLMInferenceService with `pvc://` URI
3. `configs/testcases/<name>.yaml` — Test case config (labels, timeouts, prompts)

See the full guide: **[docs/adding-test-cases.md](docs/adding-test-cases.md)**

To remove a test case:
```bash
make delete-testcase TESTCASE=my-model
```

## Useful Commands

```bash
make help              # Show all targets
make list-testcases    # List available test cases
make list-models       # Show models with HF and PVC URIs
make list-profiles     # List test profiles
make list-labels       # List available labels
make test-smoke        # Framework validation (no cluster)
make cache-models      # Pre-download all models to PVCs
make clean             # Remove generated reports
```

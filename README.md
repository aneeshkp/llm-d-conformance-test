# LLM-D Conformance Test Framework

Automated end-to-end conformance tests for [llm-d](https://github.com/llm-d) inference deployments on Kubernetes using the `LLMInferenceService` CRD.

The test framework creates `LLMInferenceService` CRs on your cluster and validates the full lifecycle — the llm-d/KServe operator handles everything (vLLM image, pods, routing). Each test case is a proper Ginkgo spec with its own description and labels.

Built with **Go 1.24**, **Ginkgo v2**, and driven entirely by YAML configs. Zero code changes needed to add new test cases.

Manifests are maintained in a separate repo: [llm-d-conformance-manifests](https://github.com/aneeshkp/llm-d-conformance-manifests) with branches per release (`3.4-ea1`, `3.4-ea2`).

## Prerequisites

- Go 1.24+
- Access to a Kubernetes cluster with:
  - llm-d / KServe operator installed (`LLMInferenceService` CRD)
  - Gateway API configured
  - GPU nodes available
- `kubectl`

## Quick Start

```bash
# 1. Clone and install dependencies
git clone https://github.com/aneeshkp/llm-d-conformance-test.git
cd llm-d-conformance-test
make deps

# 2. Clone manifests (must match your cluster version)
make setup MANIFEST_REF=3.4-ea1    # EA1 clusters (most common)
make setup MANIFEST_REF=3.4-ea2    # EA2 clusters

# 3. Verify the framework works (no cluster needed)
make test-smoke

# 4. Run a quick smoke test
export KUBECONFIG=~/.kube/my-cluster
make test TESTCASE=single-gpu-smoke

# 5. Run all conformance tests
make test-profile-all
```

## Test Cases

```bash
make testcases
```

| Test Case | Default Model | Features |
|-----------|--------------|----------|
| `single-gpu-smoke` | Qwen/Qwen3-0.6B | 1 GPU, fast CI/CD smoke test |
| `single-gpu` | Qwen/Qwen3-0.6B | 1 GPU with scheduler + metrics |
| `single-gpu-no-scheduler` | Qwen/Qwen3-0.6B | 1 GPU, K8s native routing |
| `cache-aware` | Qwen/Qwen3-0.6B | Prefix KV cache-aware routing, 2 replicas |
| `pd` | Qwen/Qwen3-0.6B | P/D disaggregation (2 prefill + 1 decode) |
| `moe` | DeepSeek-R1 | MoE DP/EP, 8 GPUs, RDMA/RoCE |

Override the default model:

```bash
make test TESTCASE=single-gpu MODEL=Qwen/Qwen2.5-7B-Instruct
```

## Profiles

```bash
make profiles
```

| Profile | Make target | Test cases |
|---------|------------|------------|
| `smoke` | `make test-profile-smoke` | single-gpu-smoke |
| `all` | `make test-profile-all` | single-gpu, no-scheduler, cache-aware, pd |
| `cache-aware` | `make test-profile-cache` | cache-aware |
| `pd` | `make test-profile-pd` | pd |
| `moe` | `make test-profile-moe` | moe (requires 8 GPUs + RDMA) |

## Manifest Repo

Manifests live in a separate repo: [llm-d-conformance-manifests](https://github.com/aneeshkp/llm-d-conformance-manifests)

**You must match the branch to your cluster's CRD version** — EA1 manifests will not work on EA2 clusters and vice versa. See the [manifest repo README](https://github.com/aneeshkp/llm-d-conformance-manifests) for details on EA1 vs EA2 differences.

```bash
make setup MANIFEST_REF=3.4-ea1    # EA1 cluster (most common)
make setup MANIFEST_REF=3.4-ea2    # EA2 cluster
make setup                          # clone main (latest)
make delete-manifests               # remove cloned manifests
```

## Test Lifecycle (9 Phases)

Each test case runs through these phases:

1. **PREP** — Download model to PVC via KServe storage initializer
2. **PREREQ** — Verify `LLMInferenceService` CRD exists
3. **DEPLOY** — `kubectl apply` the manifest (URI patched based on MODEL_SOURCE)
4. **Sub-resources** — Validate Service, HTTPRoute, Gateway programmed, InferencePool, Pods
5. **READY** — Wait for `.status.conditions[Ready]=True` with live status
6. **HEALTH** — `GET /health`
7. **INFERENCE** — `POST /v1/chat/completions` with test prompts
8. **METRICS** — Scrape vLLM + EPP `/metrics`, validate prefix cache / P/D / scheduler
9. **CLEANUP** — Delete the LLMInferenceService

### Metrics Validation (Phase 8)

Each metric check is an individual Ginkgo spec:

| Test Type | Metrics Checked |
|-----------|----------------|
| **Cache-aware** | `vllm:prefix_cache_queries > 0`, `vllm:prefix_cache_hits > 0`, hit rate, `gpu_cache_usage`, EPP `prefix_indexer_size` |
| **P/D** | `vllm:prompt_tokens_total`, `vllm:generation_tokens_total`, `request_success`, NIXL transfers (warning if absent) |
| **Scheduler** | `scheduler_e2e_duration`, `request_total`, `request_error_total = 0`, `ready_pods` |

## Model Sources

One manifest per test case — the framework patches the URI based on `MODEL_SOURCE`:

| Mode | How it works | When to use |
|------|-------------|-------------|
| `hf` (default) | Deploy with `hf://` URI, vLLM downloads at pod startup | No PVC needed, simplest |
| `pvc` | Pre-download model to PVC, deploy with `pvc://` URI | Fast startup, recommended for repeated runs |

```bash
# Run with HuggingFace (default)
make test TESTCASE=single-gpu

# Pre-cache a model to PVC (one-time)
make cache-model TESTCASE=single-gpu

# Run with PVC
make test TESTCASE=single-gpu MODEL_SOURCE=pvc

# Cache with custom storage class and size
make cache-model TESTCASE=single-gpu STORAGE_CLASS=azurefile-rwx STORAGE_SIZE=50Gi
```

## Discover Mode

Validate an already-running LLMInferenceService — skips deploy and cleanup:

```bash
make test TESTCASE=single-gpu DISCOVER=true NAMESPACE=my-ns
make test-profile-all DISCOVER=true NAMESPACE=my-ns
```

## Mock Mode

Test the full framework lifecycle **without GPUs or real models**. The deployer replaces the vLLM container in each manifest with a lightweight mock image that serves the OpenAI-compatible API.

```bash
# Run with mock image (no GPU required)
make test TESTCASE=single-gpu MOCK=ghcr.io/aneeshkp/vllm-mock:latest

# Works with any test case
make test TESTCASE=pd MOCK=ghcr.io/aneeshkp/vllm-mock:latest

# Combine with other flags
make test-profile-all MOCK=ghcr.io/aneeshkp/vllm-mock:latest NO_CLEANUP=1
```

What mock mode does:
- Replaces the `main` container in `spec.template` and `spec.prefill.template` with the mock image
- Removes GPU resource requests/limits (uses minimal CPU/memory instead)
- Skips the metrics scraping phase (mock doesn't expose real vLLM metrics)
- Scheduler containers are **not** replaced — they run as normal

This is useful for:
- CI/CD pipelines on clusters without GPUs
- Testing framework changes without waiting for model downloads
- Validating manifest structure and KServe operator behavior

## Configuration

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `TESTCASE` | — | Test case name (e.g., `single-gpu`) |
| `MODEL` | — | Override model (e.g., `Qwen/Qwen2.5-7B-Instruct`) |
| `MODEL_SOURCE` | `hf` | `hf` (HuggingFace direct) or `pvc` (pre-cached) |
| `MANIFEST_REF` | `main` | Manifest repo branch (e.g., `3.4-ea1`, `3.4-ea2`) |
| `MOCK` | — | Mock vLLM image (e.g., `ghcr.io/aneeshkp/vllm-mock:latest`) |
| `PULL_SECRET` | auto-detect | Pull secret name to copy into namespace (skipped on OCP) |
| `NO_CLEANUP` | — | Set to `1` to keep resources after test |
| `DISCOVER` | — | Set to `true` to validate existing deployment (skip deploy/cleanup) |
| `STORAGE_CLASS` | cluster default | StorageClass for PVCs |
| `STORAGE_SIZE` | from test case config | Override PVC storage size (e.g., `50Gi`) |
| `NAMESPACE` | `llm-conformance-test` | Target K8s namespace |
| `KUBECONFIG` | `$KUBECONFIG` | Path to kubeconfig |

### Project Structure

```
├── framework/                 # Core framework code
│   ├── config/                # Config types, YAML loader, filtering
│   ├── deployer/              # K8s deployer with URI patching + status dashboard
│   ├── client/                # OpenAI-compatible API client
│   ├── metrics/               # Prometheus metrics scraper + validation
│   ├── model/                 # Model download via KServe storage initializer
│   ├── reporter/              # JSON + HTML report generator
│   ├── retry/                 # Retry utilities
│   └── cleanup/               # Resource cleanup
├── tests/
│   ├── conformance_test.go    # Ginkgo specs — one per test case
│   ├── suite_test.go          # Ginkgo suite + CLI flags
│   └── smoke/                 # Framework validation (no cluster)
├── deploy/manifests/          # Cloned from manifest repo (gitignored)
├── configs/
│   ├── testcases/             # Test case definitions (YAML)
│   └── profiles/              # Named test profiles
├── .github/workflows/         # CI pipeline (lint, vet, build, smoke tests)
├── docs/
│   └── adding-test-cases.md   # Guide for adding new test cases
└── reports/                   # JSON + HTML reports (generated)
```

## Adding New Test Cases

Adding a new test case requires **zero code changes** — just 2 files:

1. **Manifest** — Add `<name>.yaml` to the [manifest repo](https://github.com/aneeshkp/llm-d-conformance-manifests) on the appropriate branch
2. **Test case config** — Add `configs/testcases/<name>.yaml` with timeouts, prompts, default model

The framework patches the manifest URI based on `MODEL_SOURCE` at runtime.

See [docs/adding-test-cases.md](docs/adding-test-cases.md) for details.

## Useful Commands

```bash
make help              # Show all targets, flags, and examples
make setup             # Clone manifest repo
make testcases    # List test cases (shows manifest version)
make profiles     # List profiles with their test cases
make test-smoke        # Framework validation (no cluster needed)
make cache-models      # Pre-download all models to PVCs
make clear-manifests   # Remove cloned manifests
make clean             # Remove generated reports
```

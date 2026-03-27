# LLM-D Conformance Test Plan

**Version:** 1.0
**Date:** 2026-03-27
**Author:** LLM-D QE Team
**Status:** Draft

---

## 1. Overview

### 1.1 Purpose

This document describes the conformance test plan for LLM-D — a Kubernetes-native platform for deploying large language model (LLM) inference services. The test framework validates that LLM inference deployments work correctly across multiple Kubernetes platforms (OpenShift, AKS, CKS), model architectures (dense, MoE), and deployment patterns (single-node, multi-node, prefill/decode disaggregation, KV cache-aware routing).

### 1.2 Scope

The tests cover:
- Deployment of `LLMInferenceService` custom resources via KServe
- Model download and PVC-based caching
- vLLM inference backend health and readiness
- OpenAI-compatible API validation (`/health`, `/v1/models`, `/v1/chat/completions`)
- EPP scheduler routing and KV cache-aware scheduling
- Prefill/Decode disaggregation with RDMA-based KV transfer
- Multi-node MoE inference with data/expert parallelism
- Resource cleanup and redeployability

### 1.3 Out of Scope

- Performance benchmarking (latency, throughput, tokens/sec)
- Model quality evaluation (accuracy, perplexity)
- Security testing (mTLS validation, RBAC)
- Upgrade testing between LLM-D versions
- Load testing / stress testing
- **Networking and cluster validation** (RDMA connectivity, SR-IOV, GPU device plugin, CRD installation) — handled by [rhaii-cluster-validation](https://github.com/opendatahub-io/rhaii-cluster-validation). That tool should be run first to verify the cluster is healthy before running conformance tests.

### 1.4 References

| Document | URL |
|----------|-----|
| LLM inference service samples | https://github.com/opendatahub-io/kserve/tree/master/docs/samples/llmisvc |
| LLM-D deployment guide | https://github.com/opendatahub-io/rhaii-on-xks/blob/main/docs/deploying-llm-d-on-managed-kubernetes.md |
| KServe LLMInferenceService CRD | https://github.com/opendatahub-io/kserve |
| Cluster validation tool | https://github.com/opendatahub-io/rhaii-cluster-validation |
| Test framework repo | https://github.com/aneeshkp/llm-d-conformance-test |

---

## 2. Test Environments

### 2.1 Available Clusters

| Environment Name | Platform | Infrastructure | RDMA Networking | GPUs | Storage |
|-----------------|----------|---------------|-----------------|------|---------|
| coreweave-waldorf | CKS | Bare-metal | InfiniBand | H100/H200 | Local NVMe |
| AKS cluster | AKS | VMs | TCP (no RDMA) | H100/A100 | managed-csi |
| PSAP IBM Cloud | OCP | VMs | RoCE | 4x8xH100 (32 GPUs) | gp3-csi |
| PSAP B200 Cluster | OCP | Bare-metal | RoCE | 2xB200 | standard-csi |

### 2.2 Environment Capabilities

| Capability | coreweave-waldorf | AKS | PSAP IBM Cloud | PSAP B200 |
|-----------|:---:|:---:|:---:|:---:|
| CPU-only workloads | Yes | Yes | Yes | Yes |
| Single GPU (1x) | Yes | Yes | Yes | Yes |
| Multi-GPU (8x) | Yes | Yes | Yes | Yes |
| RDMA / RoCE | Yes (IB) | No | Yes (RoCE) | Yes (RoCE) |
| NVLink (intra-node P2P) | Yes | No | Yes | Yes |
| SR-IOV networking | Yes | No | Yes | Yes |
| LeaderWorkerSet (LWS) | Yes | Yes | Yes | Yes |
| KServe controller | Yes | Yes | Yes | Yes |
| Gateway API | Yes | Yes | Yes | Yes |

### 2.3 Environment-to-Test Compatibility Matrix

| Test Case | coreweave-waldorf (CKS) | AKS | PSAP IBM Cloud (OCP) | PSAP B200 (OCP) |
|-----------|:---:|:---:|:---:|:---:|
| opt-125m-cpu | Yes | Yes | Yes | Yes |
| opt-125m-cpu-no-scheduler | Yes | Yes | Yes | Yes |
| opt-125m-cpu-pd | Yes | Yes | Yes | Yes |
| cache-aware-sim-cpu | Yes | Yes | Yes | Yes |
| cache-aware-qwen2-7b-gpu | Yes | Yes | Yes | Yes |
| qwen2-7b-gpu | Yes | Yes | Yes | Yes |
| qwen2-7b-gpu-no-scheduler | Yes | Yes | Yes | Yes |
| qwen2-7b-gpu-pd | Yes | No (no RDMA) | Yes (RoCE) | Yes (RoCE) |
| deepseek-r1-dp-ep-ht | Yes (IB) | No (no RDMA) | Yes (RoCE) | Yes (RoCE) |
| deepseek-coder-v2-dp-ep-naive | Yes | Yes (TCP/naive) | Yes | Yes |

---

## 3. Test Lifecycle

Each test case goes through 8 phases in order. A failure at any phase stops the test, records the error with the exact phase, cleans up, and moves to the next test case.

### 3.1 Phase Diagram

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│  PREP    │───>│ PREREQ   │───>│  DEPLOY  │───>│  READY   │
│ Download │    │ CRD check│    │ kubectl  │    │ Wait for │
│ model    │    │          │    │ apply    │    │ ready    │
└──────────┘    └──────────┘    └──────────┘    └──────────┘
                                                     │
┌──────────┐    ┌──────────┐    ┌──────────┐         │
│ CLEANUP  │<───│INFERENCE │<───│  MODEL   │<───┌────▼─────┐
│ Delete   │    │ Test     │    │ Verify   │    │  HEALTH  │
│ resources│    │ prompts  │    │ /v1/model│    │  /health │
└──────────┘    └──────────┘    └──────────┘    └──────────┘
```

### 3.2 Phase Details

| Phase | Name | Description | Pass Criteria | Fail Criteria | Timeout |
|:-----:|------|-------------|---------------|---------------|---------|
| 1 | **PREP** | Download model weights to a PVC via a Kubernetes Job. Checks if PVC already exists and download was completed previously — if so, skips download. | Download Job reports `.status.succeeded=1` | Job `.status.failed > 0` or timeout exceeded | 15m (CPU) to 120m (large MoE) |
| 2 | **PREREQ** | Verify the `LLMInferenceService` CRD (`llminferenceservices.serving.kserve.io`) is installed on the cluster. | `kubectl get crd` returns the CRD | CRD not found | 10s |
| 3 | **DEPLOY** | Apply the LLMInferenceService YAML manifest to the target namespace using `kubectl apply`. | `kubectl apply` exits with code 0 | Manifest file not found, invalid YAML, API server rejects the resource | 30s |
| 4 | **READY** | Poll `kubectl get llmisvc <name> -o jsonpath={.status.ready}` until it returns `True`. Also checks pod phase as a fallback. | `.status.ready` equals `True` | Timeout exceeded (common causes: image pull failure, insufficient GPU resources, OOM, model loading failure) | 5m (CPU) to 90m (large MoE) |
| 5 | **HEALTH** | Send `GET /health` to the vLLM endpoint with retries. Uses the service URL from `.status.url` on the LLMInferenceService. | HTTP 200 response | Non-200 status code or connection refused after all retries | 2m with 10-15s retry interval |
| 6 | **MODEL** | Send `GET /v1/models` and verify the expected model name appears in the response list. | Model name found in `data[].id` | Warning only — does not fail the test (some simulator configs may not list models) | 30s |
| 7 | **INFERENCE** | Send `POST /v1/chat/completions` with each configured test prompt. Falls back to `POST /v1/completions` if chat API fails. | Response contains non-empty `choices[].message.content` (chat) or `choices[].text` (completions) | Empty response body, empty choices array, or HTTP error on both API endpoints | 2m per prompt |
| 8 | **CLEANUP** | Delete the LLMInferenceService manifest using `kubectl delete`. Wait for all associated pods to terminate. PVC is retained for reuse if `cache.keepPVC=true`. | Resources deleted, pods terminated | Warning only — cleanup failures do not fail the test | 2m |

---

## 4. Test Cases

### 4.1 Category: CPU Models (No GPU Required)

These tests validate the core LLM-D deployment pipeline without GPU hardware. They use the lightweight `facebook/opt-125m` model (~250MB) running on vLLM's CPU backend. Suitable for all environments.

---

#### TC-CPU-001: opt-125m-cpu

**Description:** Basic end-to-end deployment of a CPU-only LLM inference service with the EPP scheduler enabled.

| Field | Value |
|-------|-------|
| **Model** | `facebook/opt-125m` (~250MB) |
| **Container image** | `public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.17.1` |
| **Replicas** | 1 |
| **Scheduler** | EPP (default) |
| **GPUs** | 0 |
| **RDMA** | No |
| **Ready timeout** | 5 minutes |
| **Labels** | `cpu`, `smoke`, `opt-125m`, `simple` |
| **Environments** | All (AKS, OCP, CKS) |

**What it validates:**
- LLMInferenceService CRD creates pods correctly
- vLLM starts on CPU with `VLLM_CPU_KVCACHE_SPACE=1`
- Model downloads from HuggingFace (or mounts from cached PVC)
- `/health` returns HTTP 200
- `/v1/chat/completions` returns non-empty response
- EPP scheduler pod starts (`2/2` ready) and routes requests

**Test prompts:**
1. "What is 2+2?"
2. "Hello world"

---

#### TC-CPU-002: opt-125m-cpu-no-scheduler

**Description:** CPU deployment without the EPP scheduler. Requests are routed by native Kubernetes Service load balancing instead.

| Field | Value |
|-------|-------|
| **Model** | `facebook/opt-125m` |
| **Replicas** | 1 |
| **Scheduler** | None (K8s native routing) |
| **GPUs** | 0 |
| **Ready timeout** | 5 minutes |
| **Labels** | `cpu`, `opt-125m`, `no-scheduler` |
| **Environments** | All |

**What it validates:**
- LLM-D works without the EPP scheduler component
- The `router.route` only configuration (no `router.scheduler`, no `router.gateway`) creates a functional service
- vLLM serves requests through standard Kubernetes service routing

**What it proves differently from TC-CPU-001:**
- The scheduler is not a hard dependency — LLM-D can fall back to basic K8s routing

**Test prompts:**
1. "What is Kubernetes?"

---

#### TC-CPU-003: opt-125m-cpu-pd

**Description:** Prefill/Decode disaggregation on CPU — separates prompt processing (prefill) and token generation (decode) into different pod pools.

| Field | Value |
|-------|-------|
| **Model** | `facebook/opt-125m` |
| **Replicas** | 1 decode + 1 prefill |
| **Scheduler** | EPP |
| **GPUs** | 0 |
| **Ready timeout** | 8 minutes |
| **Labels** | `cpu`, `opt-125m`, `prefill-decode`, `pd` |
| **Environments** | All |

**What it validates:**
- The `spec.prefill` section of the CRD creates a separate prefill pod pool
- Both decode and prefill pods become healthy independently
- KServe correctly routes prefill requests to prefill pods and decode requests to decode pods
- The complete prefill-decode lifecycle works end-to-end

**Why it matters:**
PD disaggregation is critical for production latency. Prefill is compute-heavy (processes the entire input prompt at once), while decode is memory-heavy (generates output tokens one at a time). Separating them allows independent scaling — e.g., 2 prefill replicas for parallel prompt processing + 1 decode replica.

**Test prompts:**
1. "Explain LLM inference in one sentence."

---

### 4.2 Category: Single-Node GPU Models

These tests deploy `Qwen/Qwen2.5-7B-Instruct` (~15GB), a production-quality 7B parameter instruct-tuned model, on single GPU nodes. Each replica requires 1 NVIDIA GPU.

---

#### TC-GPU-001: qwen2-7b-gpu

**Description:** Standard GPU inference with EPP scheduler — the most common production deployment pattern.

| Field | Value |
|-------|-------|
| **Model** | `Qwen/Qwen2.5-7B-Instruct` (~15GB) |
| **Replicas** | 3 (for load balancing validation) |
| **Scheduler** | EPP (default) |
| **GPUs** | 1 per replica (3 total) |
| **RDMA** | No |
| **Ready timeout** | 15 minutes |
| **Model cache** | PVC, 100Gi, downloaded via HuggingFace |
| **Labels** | `gpu`, `single-node-gpu`, `qwen2` |
| **Environments** | PSAP IBM Cloud, PSAP B200, CoreWeave |

**What it validates:**
- GPU allocation via `nvidia.com/gpu` resource requests works correctly
- vLLM loads a 7B model into GPU VRAM
- HTTPS health endpoint works (mTLS enabled by default via cert-manager)
- EPP scheduler distributes requests across 3 replicas
- Chat completions return coherent, contextually relevant responses

**Test prompts:**
1. "What is Kubernetes?"
2. "Explain containerization."

---

#### TC-GPU-002: qwen2-7b-gpu-no-scheduler

**Description:** GPU inference without EPP scheduler — validates the fallback path where Kubernetes native service handles routing.

| Field | Value |
|-------|-------|
| **Model** | `Qwen/Qwen2.5-7B-Instruct` |
| **Replicas** | 3 |
| **Scheduler** | None |
| **GPUs** | 1 per replica |
| **Ready timeout** | 15 minutes |
| **Labels** | `gpu`, `single-node-gpu`, `qwen2`, `no-scheduler` |
| **Environments** | PSAP IBM Cloud, PSAP B200, CoreWeave |

**What it validates:**
- Same GPU/model validation as TC-GPU-001
- Routing handled by Kubernetes Service instead of EPP
- Verifies no hard dependency on the scheduler for GPU workloads

**Test prompts:**
1. "What is Kubernetes?"

---

#### TC-GPU-003: qwen2-7b-gpu-pd

**Description:** Prefill/Decode disaggregation on GPU with RDMA-based KV cache transfer between prefill and decode pods.

| Field | Value |
|-------|-------|
| **Model** | `Qwen/Qwen2.5-7B-Instruct` |
| **Replicas** | 1 decode + 2 prefill |
| **Scheduler** | EPP |
| **GPUs** | 1 per pod (3 total) |
| **RDMA** | Yes (`rdma/roce_gdr`, `roce-p2` network attachment) |
| **Ready timeout** | 15 minutes |
| **Labels** | `gpu`, `single-node-gpu`, `qwen2`, `prefill-decode`, `pd`, `rdma` |
| **Environments** | PSAP IBM Cloud (RoCE), PSAP B200 (RoCE), CoreWeave (IB) |

**What it validates:**
- RDMA network attachment annotation (`k8s.v1.cni.cncf.io/networks: roce-p2`)
- NixlConnector for KV cache transfer: `--kv_transfer_config '{"kv_connector":"NixlConnector","kv_role":"kv_both"}'`
- `VLLM_NIXL_SIDE_CHANNEL_HOST` set to pod IP for KV transfer coordination
- UCX transport configuration: `UCX_TLS=rc,sm,self,cuda_copy,cuda_ipc`
- Prefill pod pool scales independently (2 replicas for higher prefill throughput)
- Both pod pools (decode + prefill) become healthy and serve inference

**Why it matters:**
This is the high-performance production pattern. Prefill pods process prompts in parallel, then transfer the computed KV cache to decode pods over RDMA (bypassing CPU/network stack), avoiding re-computation. This significantly reduces time-to-first-token for long prompts.

**Test prompts:**
1. "Explain prefill decode disaggregation."

---

### 4.3 Category: Cache-Aware Routing

These tests validate **precise prefix KV cache routing** — an advanced scheduling feature where the EPP scheduler routes requests to replicas that already have relevant KV cache entries, avoiding redundant computation for shared prompt prefixes.

---

#### TC-CACHE-001: cache-aware-sim-cpu

**Description:** KV cache-aware routing using the llm-d-inference-sim simulator. No GPU required.

| Field | Value |
|-------|-------|
| **Model** | `opt-125m` (simulated) |
| **Container image** | `ghcr.io/llm-d/llm-d-inference-sim:v0.5.1` |
| **API version** | `serving.kserve.io/v1alpha2` (newer CRD version) |
| **Replicas** | 2 (needed to test routing between instances) |
| **Scheduler** | EPP with custom plugins |
| **GPUs** | 0 |
| **Ready timeout** | 5 minutes |
| **Labels** | `cpu`, `cache-aware`, `kv-cache`, `simulator`, `smoke` |
| **Environments** | All (no GPU needed) |

**What it validates:**
- EPP scheduler with `precise-prefix-cache-scorer` plugin
- ZMQ-based KV cache event publishing on `tcp://*:5557`
- Block-based token hashing: `blockSize=16`, `hashSeed=42`
- Weighted scheduling profile:
  - `queue-scorer` (weight 2) — balances queue depth across replicas
  - `kv-cache-utilization-scorer` (weight 2) — considers cache utilization
  - `precise-prefix-cache-scorer` (weight 3) — highest weight, routes to replica with cached prefix
  - `max-score-picker` — selects the replica with the highest combined score
- The simulator correctly publishes KV cache events for the scheduler to consume

**Why it matters:**
This is a lightweight way to validate the entire KV cache routing pipeline (event publishing, scoring, routing) without GPU hardware. The simulator responds with random tokens but correctly publishes KV cache events, making it ideal for CI environments.

**Test prompts:**
1. "Hello, how are you?"

---

#### TC-CACHE-002: cache-aware-qwen2-7b-gpu

**Description:** KV cache-aware routing with a real GPU model (Qwen2.5-7B) and actual vLLM prefix caching.

| Field | Value |
|-------|-------|
| **Model** | `Qwen/Qwen2.5-7B-Instruct` |
| **Replicas** | 2 |
| **GPUs** | 1 per replica |
| **Scheduler** | EPP with prefix cache scorer |
| **Ready timeout** | 15 minutes |
| **Labels** | `gpu`, `cache-aware`, `kv-cache`, `qwen2` |
| **Environments** | PSAP IBM Cloud, PSAP B200, CoreWeave |

**What it validates:**
- vLLM prefix caching with SHA256 hash: `--prefix-caching-hash-algo sha256`
- Block size 64 (must match between vLLM `--block-size 64` and scheduler `blockSize: 64`)
- NixlConnector for KV cache transfer between replicas
- ZMQ-based KV cache event publishing with topic: `kv@${POD_IP}@Qwen/Qwen2.5-7B-Instruct`
- `PYTHONHASHSEED=42` consistency between vLLM pods and EPP scheduler (required for matching prefix hashes)
- Real inference with KV cache hits on repeated prompt prefixes

**Test prompts:**
1. "What is KV cache routing?"
2. "Explain prefix caching in LLM inference."

---

### 4.4 Category: DeepSeek MoE (Mixture of Experts)

These tests deploy large Mixture of Experts models that require multi-node GPU clusters with data parallelism (DP) and expert parallelism (EP). They are the most demanding tests in the suite.

---

#### TC-MOE-001: deepseek-r1-dp-ep-ht

**Description:** DeepSeek-R1-0528 (671B MoE) with high-throughput DP/EP using RDMA/RoCE and DeepEP all-to-all backend.

| Field | Value |
|-------|-------|
| **Model** | `deepseek-ai/DeepSeek-R1-0528` (~600GB) |
| **Model source** | PVC (`pvc://llm-test-pvc-deepseek`) — must be pre-populated |
| **Replicas** | 1 head + workers (via LeaderWorkerSet) |
| **GPUs** | 8 per pod (`nvidia.com/gpu: 8`) |
| **RDMA** | Yes (`rdma/roce_gdr: 1`, `k8s.v1.cni.cncf.io/networks: roce-p2`) |
| **Parallelism** | data=32, dataLocal=8, expert=true, tensor=1 |
| **All-to-all backend** | `deepep_high_throughput` |
| **Ready timeout** | 90 minutes |
| **Labels** | `gpu`, `deepseek`, `moe`, `dp-ep`, `rdma`, `multi-node` |
| **Environments** | CoreWeave (IB), PSAP IBM Cloud (RoCE), PSAP B200 (RoCE) |

**Resource requirements per pod:**

| Resource | Request | Limit |
|----------|---------|-------|
| CPU | 64 | 128 |
| Memory | 256Gi | 512Gi |
| Ephemeral storage | 800Gi | 800Gi |
| GPU | 8 | 8 |
| RDMA | 1 | 1 |

**What it validates:**
- Multi-node MoE inference with expert parallelism (different experts on different GPUs)
- LeaderWorkerSet (LWS) creates head + worker pod topology
- NVSHMEM configuration for GPU-to-GPU RDMA:
  - `NVSHMEM_REMOTE_TRANSPORT=ibgda` (InfiniBand GDR-Accelerated)
  - `NVSHMEM_BOOTSTRAP_TWO_STAGE=1`
  - `NVSHMEM_IBGDA_NIC_HANDLER=gpu`
- NCCL configuration for inter-node communication:
  - `NCCL_IB_GID_INDEX=3`
  - `NCCL_SOCKET_IFNAME=net1`
  - `NCCL_IB_TIMEOUT=100`
- UCX transport: `UCX_TLS=rc,sm,self,cuda_copy,cuda_ipc`
- GDRCopy enabled: `NVIDIA_GDRCOPY=enabled`
- vLLM flags: `--gpu-memory-utilization 0.95 --max-model-len 8192 --enforce-eager`
- Worker pods mirror the head pod configuration

**Why it matters:**
This tests the most complex deployment pattern in LLM-D — a 671B parameter MoE model distributed across multiple 8-GPU nodes using expert parallelism. Different experts run on different GPUs across nodes and communicate via RDMA for the all-to-all dispatch/combine operations. This is the target deployment for production DeepSeek-R1 inference.

**Test prompts:**
1. "What is expert parallelism in MoE models?"

---

#### TC-MOE-002: deepseek-coder-v2-dp-ep-naive

**Description:** DeepSeek-Coder-V2-Lite with naive (TCP-based) all-to-all backend — validates DP/EP on clusters without RDMA networking.

| Field | Value |
|-------|-------|
| **Model** | `deepseek-ai/DeepSeek-Coder-V2-Lite-Instruct` (~30GB) |
| **Model source** | HuggingFace (`hf://`) with PVC caching (200Gi) |
| **Replicas** | 1 head + workers |
| **GPUs** | 8 per pod |
| **RDMA** | No (TCP via eth0/gVNIC) |
| **Parallelism** | data=16, dataLocal=8, expert=true, tensor=1 |
| **All-to-all backend** | `naive` (TCP-based) |
| **Ready timeout** | 90 minutes |
| **Labels** | `gpu`, `deepseek`, `moe`, `dp-ep`, `naive` |
| **Environments** | CoreWeave, PSAP IBM Cloud, PSAP B200 |

**What it validates:**
- MoE inference without RDMA hardware, using TCP-based communication:
  - `NCCL_IB_DISABLE=1` (disable InfiniBand/RDMA)
  - `NCCL_NET_GDR_LEVEL=0` (disable GPUDirect over network)
  - `NCCL_P2P_LEVEL=NVL` (use NVLink for intra-node P2P)
  - `NCCL_SOCKET_IFNAME=eth0` (use TCP network interface)
- NVSHMEM over UCX with TCP: `UCX_TLS=tcp,sm,self,cuda_copy,cuda_ipc`
- Reduced NCCL buffer size for memory pressure: `NCCL_BUFFSIZE=2097152` (2MB)
- Reduced socket threads to avoid allocation errors: `NCCL_NSOCKS_PERTHREAD=2`, `NCCL_SOCKET_NTHREADS=2`

**Why it matters:**
Not all clusters have RDMA networking (e.g., AKS VMs with gVNIC, or cloud VMs without SR-IOV). This test validates that MoE models can still run using TCP-based all-to-all communication — the "naive" backend. Performance is lower than RDMA, but it provides a functional fallback path for environments without specialized networking hardware.

**Test prompts:**
1. "Write a hello world function in Python."

---

## 5. Test Profiles

Profiles group test cases for specific scenarios and environments.

| Profile | Test Cases | GPU | RDMA | Estimated Duration | Recommended Environment |
|---------|-----------|:---:|:---:|:---:|---|
| `smoke` | TC-CPU-001, TC-CACHE-001 | No | No | ~10 min | Any |
| `cpu-full` | TC-CPU-001, TC-CPU-002, TC-CPU-003, TC-CACHE-001 | No | No | ~20 min | Any |
| `single-node-gpu` | TC-GPU-001, TC-GPU-002, TC-GPU-003, TC-CACHE-002 | Yes (1) | Partial | ~60 min | PSAP IBM Cloud, CoreWeave |
| `cache-aware` | TC-CACHE-001, TC-CACHE-002 | Partial | No | ~25 min | PSAP IBM Cloud, CoreWeave |
| `deepseek` | TC-MOE-001, TC-MOE-002 | Yes (8) | Partial | ~3 hours | PSAP IBM Cloud, CoreWeave |
| `full` | All 10 test cases | Yes | Yes | ~6 hours | CoreWeave, PSAP IBM Cloud |

---

## 6. Model Download & Caching Strategy

### 6.1 Problem

LLM models range from 250MB (OPT-125M) to 600GB+ (DeepSeek-R1). Downloading them at pod startup via `hf://` URIs causes:
- Test timeouts (vLLM health check fails while model is still downloading)
- Wasted bandwidth (same model re-downloaded on every test run)
- Non-deterministic test durations

### 6.2 Solution: PVC-Based Model Cache

Models are downloaded into PersistentVolumeClaims (PVCs) as a **prep phase** before deployment. The PVC persists across test runs.

```
┌─────────────────────────────────────────────────┐
│ Phase 1: PREP                                    │
│                                                  │
│  1. Check: does PVC "model-cache-xxx" exist?     │
│     ├── YES + download Job completed → SKIP      │
│     └── NO → Continue                            │
│                                                  │
│  2. Create PVC with storageSize from config       │
│                                                  │
│  3. Create download Job:                         │
│     - Image: python:3.11-slim                    │
│     - Runs: huggingface_hub.snapshot_download()  │
│     - Mounts PVC at /model-cache                 │
│                                                  │
│  4. Wait for Job .status.succeeded=1             │
│     - Polls every 30s                            │
│     - Timeout: config.model.cache.timeout        │
│                                                  │
│  5. PASS → Continue to Phase 2: DEPLOY           │
│     FAIL → Record error, skip remaining phases   │
└─────────────────────────────────────────────────┘
```

### 6.3 Storage Classes by Platform

| Platform | Default StorageClass | Recommended for Model Cache | Access Mode |
|----------|---------------------|---------------------------|-------------|
| OCP (AWS) | `gp3-csi` | `gp3-csi` | ReadWriteOnce |
| OCP (Azure) | `managed-premium` | `managed-premium` | ReadWriteOnce |
| AKS | `managed-csi` | `managed-csi` | ReadWriteOnce |
| CKS (CoreWeave) | Cluster-specific | Local NVMe or distributed | ReadWriteOnce |

### 6.4 Model Sizes

| Model | Size on Disk | Recommended PVC | Download Time (est.) |
|-------|-------------|----------------|---------------------|
| facebook/opt-125m | ~250MB | 5Gi | ~1 min |
| Qwen/Qwen2.5-7B-Instruct | ~15GB | 100Gi | ~15 min |
| deepseek-ai/DeepSeek-Coder-V2-Lite-Instruct | ~30GB | 200Gi | ~30 min |
| deepseek-ai/DeepSeek-R1-0528 | ~600GB | 500Gi | Pre-populated (manual) |

---

## 7. Reporting

### 7.1 JSON Report Structure

Each test run produces a JSON report in `reports/` with:

| Field | Description |
|-------|-------------|
| `suite` | Always `llm-d-conformance` |
| `profile` | Profile name (e.g., `smoke`, `full`) |
| `platform` | Target platform (`ocp`, `aks`, `cks`) |
| `startTime` / `endTime` | ISO 8601 timestamps |
| `duration` | Total execution time |
| `environment.platform` | Detected platform |
| `environment.kubernetesVersion` | Kubelet version |
| `environment.namespace` | Target namespace |
| `results[]` | Array of per-test-case results |
| `results[].name` | Test case name |
| `results[].status` | `pass`, `fail`, or `skip` |
| `results[].error` | Error message with phase prefix (e.g., `[DEPLOY] kubectl apply failed: ...`) |
| `results[].logs` | Phase-by-phase log entries (e.g., `PREP PASSED: model cached in PVC ...`) |
| `results[].model` | Model name, URI, category, container image |
| `summary.total` / `passed` / `failed` / `skipped` | Aggregate counts |

### 7.2 Example Report Output

```
[PASS] opt-125m-cpu (2m15s)
  PREP PASSED: model cached in PVC model-cache-facebook-opt-125m (45s)
  PREREQ PASSED: LLMInferenceService CRD found
  DEPLOY PASSED: manifest applied in 1.2s
  READY PASSED: llmisvc status.ready=True
  HEALTH PASSED: /health returned 200 OK
  MODEL PASSED: facebook/opt-125m found in /v1/models
  INFERENCE[0] PASSED via /v1/chat/completions (tokens=42)
  INFERENCE[1] PASSED via /v1/chat/completions (tokens=38)
  CLEANUP PASSED: all resources removed

[FAIL] qwen2-7b-gpu (15m02s)
  PREP PASSED: model cached in PVC model-cache-qwen-qwen2-5-7b-instruct (12m)
  PREREQ PASSED: LLMInferenceService CRD found
  DEPLOY PASSED: manifest applied in 1.5s
  FAILED at phase READY: Service did not become ready: wait-ready-qwen2-7b-instruct-single
    timed out after 60 attempts: llmisvc qwen2-7b-instruct-single not ready yet
    (status: "", pods: Pending)
```

---

## 8. Execution Instructions

### 8.1 Quick Start

```bash
git clone https://github.com/aneeshkp/llm-d-conformance-test.git
cd llm-d-conformance-test
make deps
make test-smoke              # no cluster needed
```

### 8.2 Run by Profile

```bash
make test-profile-smoke      PLATFORM=ocp KUBECONFIG=~/.kube/config
make test-profile-cpu        PLATFORM=aks
make test-profile-gpu        PLATFORM=ocp
make test-profile-deepseek   PLATFORM=cks
make test-profile-full       PLATFORM=ocp
```

### 8.3 Run Single Test Case

```bash
make test-single TESTCASE=opt-125m-cpu
make test-single TESTCASE=deepseek-r1-dp-ep-ht PLATFORM=ocp
```

### 8.4 Run by Label

```bash
make test-by-label LABELS=cpu
make test-by-label LABELS=gpu,rdma
make test-by-label LABELS=moe,deepseek
```

### 8.5 Flags Reference

| Flag | Default | Description |
|------|---------|-------------|
| `PLATFORM` | `any` | `ocp`, `aks`, `cks`, `any` |
| `NAMESPACE` | `llm-conformance-test` | Target K8s namespace |
| `KUBECONFIG` | `$KUBECONFIG` | Path to kubeconfig |
| `REPORT_DIR` | `reports` | JSON report output directory |
| `STORAGE_CLASS` | (cluster default) | StorageClass for model cache PVCs |
| `TESTCASE` | (none) | Run a single test case by name |
| `LABELS` | (none) | Comma-separated label filter |
| `PROFILE` | (none) | Path to profile YAML |

---

## 9. Risk & Mitigation

| Risk | Impact | Mitigation |
|------|--------|------------|
| Model download timeout | PREP phase fails | Use PVC caching (`cache.enabled=true`), increase `cache.timeout` |
| Insufficient GPU resources | READY phase fails (pods Pending) | Check node GPU availability before running GPU profiles |
| RDMA not available | PD tests fail | Run naive backend tests (`deepseek-coder-v2-dp-ep-naive`) instead |
| Image pull failure | READY phase fails | Ensure pull secrets are configured in the namespace |
| CRD not installed | PREREQ phase fails | Follow the [deployment guide](https://github.com/opendatahub-io/rhaii-on-xks/blob/main/docs/deploying-llm-d-on-managed-kubernetes.md) first |
| StorageClass not available | PREP phase fails | Set `STORAGE_CLASS` flag to match available class |

---

## 10. Appendix

### 10.1 Label Reference

| Label | Meaning | Test Cases |
|-------|---------|------------|
| `cpu` | No GPU required | TC-CPU-001/002/003, TC-CACHE-001 |
| `gpu` | Requires NVIDIA GPU | TC-GPU-001/002/003, TC-CACHE-002, TC-MOE-001/002 |
| `smoke` | Quick validation | TC-CPU-001, TC-CACHE-001 |
| `pd` / `prefill-decode` | Uses PD disaggregation | TC-CPU-003, TC-GPU-003 |
| `rdma` | Requires RDMA networking | TC-GPU-003, TC-MOE-001 |
| `cache-aware` / `kv-cache` | KV cache routing | TC-CACHE-001/002 |
| `deepseek` / `moe` | DeepSeek MoE models | TC-MOE-001/002 |
| `no-scheduler` | No EPP scheduler | TC-CPU-002, TC-GPU-002 |
| `multi-node` | Multi-node deployment | TC-MOE-001 |
| `naive` | TCP-based all-to-all (no RDMA) | TC-MOE-002 |
| `simulator` | Uses inference simulator | TC-CACHE-001 |

### 10.2 CRD API Versions

| API Version | Used By |
|-------------|---------|
| `serving.kserve.io/v1alpha1` | All test cases except TC-CACHE-001 |
| `serving.kserve.io/v1alpha2` | TC-CACHE-001 (simulator with inline scheduler config) |

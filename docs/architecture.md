# LLM-D Conformance Test Framework — Architecture Guide

This document provides a comprehensive overview of the test framework architecture, test case topologies, deployment modes, and component interactions. All diagrams use Mermaid syntax.

---

## Table of Contents

- [Framework Overview](#framework-overview)
- [9-Phase Test Lifecycle](#9-phase-test-lifecycle)
- [Package Architecture](#package-architecture)
- [Test Case Topologies](#test-case-topologies)
  - [1. Single-GPU Smoke](#1-single-gpu-smoke)
  - [2. Single-GPU](#2-single-gpu)
  - [3. Single-GPU No-Scheduler](#3-single-gpu-no-scheduler)
  - [4. Cache-Aware](#4-cache-aware)
  - [5. Prefill/Decode (P/D)](#5-prefilldecode-pd)
  - [6. P/D + Cache-Aware](#6-pd--cache-aware)
  - [7. Mixture of Experts (MoE)](#7-mixture-of-experts-moe)
  - [8. Multi-Pool](#8-multi-pool)
- [Deployment Modes](#deployment-modes)
  - [Mock Mode](#mock-mode)
  - [Model Sources](#model-sources)
  - [Discover Mode](#discover-mode)
- [Platform Differences](#platform-differences)
- [Manifest Patching Pipeline](#manifest-patching-pipeline)
- [Metrics Validation Matrix](#metrics-validation-matrix)
- [Suggested Improvements](#suggested-improvements)

---

## Framework Overview

```mermaid
graph TB
    subgraph "User Interface"
        MK[Makefile Targets]
        CLI[CLI Flags]
    end

    subgraph "Configuration"
        TC[Test Cases<br/>configs/testcases/*.yaml]
        PR[Profiles<br/>configs/profiles/*.yaml]
        MF[Manifests<br/>deploy/manifests/*.yaml]
    end

    subgraph "Test Runner"
        ST[suite_test.go<br/>Flag parsing]
        CT[conformance_test.go<br/>9-phase lifecycle]
    end

    subgraph "Framework Packages"
        CFG[config<br/>Load & resolve]
        DEP[deployer<br/>Deploy & manage]
        CL[client<br/>OpenAI API]
        MT[metrics<br/>Prometheus scrape]
        DL[model<br/>PVC download]
        CLN[cleanup<br/>Resource tracking]
        RPT[reporter<br/>JSON & HTML]
        RTY[retry<br/>Polling utils]
    end

    subgraph "Kubernetes Cluster"
        CRD[LLMInferenceService CRD]
        POD[vLLM Pods]
        SCH[Scheduler / EPP]
        SVC[Service + HTTPRoute]
        GW[Gateway]
        IP[InferencePool]
    end

    MK --> CLI --> ST --> CT
    ST --> CFG
    CFG --> TC & PR
    CT --> DEP & CL & MT & DL & CLN & RPT
    DEP --> MF
    DEP --> CRD & POD & SVC
    CL --> GW
    MT --> POD & SCH
    DL --> POD
    CLN --> DEP
```

---

## 9-Phase Test Lifecycle

Each test case follows this ordered sequence. A failure in any phase skips all subsequent phases.

```mermaid
flowchart TD
    START([Test Case Start]) --> P1

    P1[Phase 1: PREP<br/>Download model to PVC]
    P1 -->|"skip if hf:// or discover"| P2

    P2[Phase 2: PREREQ<br/>Check CRD installed]
    P2 -->|"skip if discover"| P3

    P3[Phase 3: DEPLOY<br/>Patch manifest + kubectl apply]
    P3 -->|"skip if discover"| P4

    P4[Phase 4: READY<br/>Wait for all sub-resources]
    P4 --> P4a & P4b & P4c & P4d & P4e & P4f

    P4a[4a: Service exists]
    P4b[4b: HTTPRoute exists]
    P4c[4c: Gateway Programmed=True]
    P4d[4d: HTTPRoute Accepted=True]
    P4e[4e: InferencePool created<br/>skip if no scheduler]
    P4f[4f: Pods Running<br/>no CrashLoopBackOff]

    P4a & P4b & P4c & P4d & P4e & P4f --> P5

    P5[Phase 5: MODEL FILES<br/>Check /mnt/models<br/>config.json + weights]
    P5 -->|"skip if mock or no init container"| P6

    P6[Phase 6: HEALTH + MODEL<br/>GET /health → 200<br/>GET /v1/models → list]
    P6 --> P7

    P7[Phase 7: INFERENCE<br/>POST /v1/chat/completions<br/>Validate non-empty response]
    P7 --> P7b

    P7b{Multi-pool<br/>enabled?}
    P7b -->|yes| P7c[Phase 7b: MULTI-POOL<br/>Route to each pool endpoint]
    P7b -->|no| P8
    P7c --> P8

    P8{Metrics<br/>enabled?}
    P8 -->|yes| P8a[Phase 8: METRICS<br/>Scrape vLLM + EPP<br/>Validate counters]
    P8 -->|no| P9
    P8 -->|"mock mode"| P9
    P8a --> P9

    P9[Phase 9: CLEANUP<br/>kubectl delete manifest<br/>Wait for pod termination]
    P9 -->|"skip if NO_CLEANUP"| DONE

    DONE([Test Case End])

    style P3 fill:#e1f5fe
    style P7 fill:#e8f5e9
    style P8a fill:#fff3e0
    style P9 fill:#fce4ec
```

### Phase Details

| Phase | Timeout | Skip Conditions |
|-------|---------|----------------|
| 1. PREP | 90m (configurable) | `hf://` source, discover mode, cache mode |
| 2. PREREQ | 30s | discover mode |
| 3. DEPLOY | 2m | discover mode |
| 4. READY | 10-90m (per test case) | discover mode |
| 5. MODEL FILES | 30s | mock mode, no storage-initializer |
| 6. HEALTH | 2m (retries) | — |
| 7. INFERENCE | 2m per prompt | — |
| 7b. MULTI-POOL | 2m per pool | only if `multiPool.enabled` |
| 8. METRICS | 2m | mock mode, `metricsCheck.enabled=false` |
| 9. CLEANUP | 2m | `NO_CLEANUP=1`, discover mode |

---

## Package Architecture

```mermaid
classDiagram
    class config {
        +LoadProfile(path) TestProfile
        +LoadTestCase(path) TestCase
        +ResolveProfileTestCases() []TestCase
        +FilterTestCasesByNames()
    }

    class deployer {
        +Deploy(ctx, tc) DeployResult
        +WaitForReady(ctx, tc)
        +Cleanup(ctx, tc)
        +GetServiceEndpoint(ctx, tc) string
        +GetPlatformInfo(ctx) map
        +GetVLLMVersion(ctx) string
        -patchManifest(ctx, path, tc) string
        -ensurePullSecrets(ctx, path, ns)
        -findSecret(ctx, names, namespaces)
        -copySecret(ctx, src, srcNS, dest, destNS)
    }

    class client {
        +HealthCheck(ctx) error
        +Completions(ctx, req) Response
        +ChatCompletions(ctx, req) Response
        +ListModels(ctx) ModelsResponse
    }

    class metrics {
        +ScrapePod(ctx, pod, port) ScrapeResult
        +ScrapeVLLMPods(ctx, name) []ScrapeResult
        +ScrapeEPPPods(ctx, name) []ScrapeResult
        +ValidateCacheAwareMetrics()
        +ValidatePDMetrics()
        +ValidateSchedulerMetrics()
    }

    class model {
        +DownloadModel(ctx, tc) CacheResult
        +Cleanup(ctx, tc)
        +ResolveSnapshotPath() string
    }

    class cleanup {
        +Track(tc)
        +CleanupAll(ctx) []error
        +CleanupOne(ctx, name) error
    }

    class reporter {
        +AddResult(result)
        +Finalize()
        +SetEnvironment(env)
    }

    deployer --> config : uses TestCase
    client --> config : uses TestCase
    metrics --> deployer : kubectl exec
    model --> deployer : kubectl apply
    cleanup --> deployer : Cleanup()
    reporter --> config : uses TestResult
```

---

## Test Case Topologies

### 1. Single-GPU Smoke

**Purpose:** Fast baseline validation — confirms framework + scheduler work.

```mermaid
graph LR
    subgraph "Client"
        U[Test Runner]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway<br/>OSSM/Istio]
        HR[HTTPRoute<br/>single-gpu-smoke]
        SVC[Service<br/>single-gpu-smoke]
        IP[InferencePool]

        subgraph "Pod"
            VLLM[vLLM<br/>Qwen3-0.6B<br/>1 GPU]
            EPP[Scheduler<br/>sidecar]
        end
    end

    U -->|"POST /v1/chat/completions"| GW
    GW --> HR --> SVC --> EPP
    EPP -->|route| VLLM
    VLLM -.->|register| IP

    style VLLM fill:#4caf50,color:#fff
    style EPP fill:#2196f3,color:#fff
```

| Property | Value |
|----------|-------|
| Replicas | 1 |
| GPUs | 1 |
| Scheduler | Yes (EPP sidecar) |
| Metrics | Disabled (smoke) |
| Prompts | 1 chat prompt |
| Ready Timeout | 10m |

---

### 2. Single-GPU

**Purpose:** Full baseline with metrics validation — vLLM + EPP + scheduler metrics.

```mermaid
graph LR
    subgraph "Client"
        U[Test Runner]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway]
        HR[HTTPRoute]
        SVC[Service<br/>single-gpu]
        IP[InferencePool]

        subgraph "Pod"
            VLLM["vLLM<br/>Qwen3-0.6B<br/>1 GPU<br/>📊 /metrics:8000"]
            EPP["Scheduler EPP<br/>📊 /metrics:9090"]
        end
    end

    U -->|"POST /v1/chat/completions"| GW
    GW --> HR --> SVC --> EPP
    EPP -->|route| VLLM
    VLLM -.->|register| IP

    U -.->|"scrape metrics"| VLLM
    U -.->|"scrape metrics"| EPP

    style VLLM fill:#4caf50,color:#fff
    style EPP fill:#2196f3,color:#fff
```

| Property | Value |
|----------|-------|
| Replicas | 1 |
| GPUs | 1 |
| Scheduler | Yes |
| Metrics | vLLM ✓  EPP ✓  Scheduler ✓ |
| Prompts | 2 chat prompts |

---

### 3. Single-GPU No-Scheduler

**Purpose:** Validates K8s native load balancing without LLM-D scheduler. No InferencePool.

```mermaid
graph LR
    subgraph "Client"
        U[Test Runner]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway]
        HR[HTTPRoute]
        SVC["Service<br/>single-gpu-no-scheduler<br/>Round-Robin LB"]

        VLLM1[vLLM Pod 1<br/>1 GPU]
        VLLM2[vLLM Pod 2<br/>1 GPU]
        VLLM3[vLLM Pod 3<br/>1 GPU]
    end

    U -->|"POST /v1/chat/completions"| GW
    GW --> HR --> SVC
    SVC -->|random| VLLM1
    SVC -->|random| VLLM2
    SVC -->|random| VLLM3

    style VLLM1 fill:#4caf50,color:#fff
    style VLLM2 fill:#4caf50,color:#fff
    style VLLM3 fill:#4caf50,color:#fff
    style SVC fill:#ff9800,color:#fff
```

| Property | Value |
|----------|-------|
| Replicas | 3 |
| GPUs | 3 total |
| Scheduler | **No** — K8s Service round-robin |
| InferencePool | **No** |
| Metrics | Disabled |

---

### 4. Cache-Aware

**Purpose:** Validates prefix KV cache-aware routing — scheduler routes requests with same system prefix to the same pod for cache reuse.

```mermaid
graph TB
    subgraph "Client"
        U["Test Runner<br/>3 requests with<br/>same system prefix<br/>(5s delay between)"]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway]
        HR[HTTPRoute]
        SVC[Service<br/>cache-aware]
        IP[InferencePool]

        subgraph "Scheduler EPP"
            PCS["precise-prefix-cache-scorer<br/>weight=3 (highest)"]
            QS[queue-scorer]
            KVS[kv-cache-utilization-scorer]
            MSP[max-score-picker]
            PI["Prefix Indexer<br/>pod→prefix mapping"]
        end

        subgraph "Pod A"
            VLLM_A["vLLM Replica A<br/>1 GPU<br/>prefix-cache enabled<br/>block-size=16"]
        end

        subgraph "Pod B"
            VLLM_B["vLLM Replica B<br/>1 GPU<br/>prefix-cache enabled<br/>block-size=16"]
        end
    end

    U -->|"req 1: system+Q1"| GW --> HR --> SVC --> PCS
    PCS --> QS --> KVS --> MSP
    MSP -->|"route to A (random first time)"| VLLM_A

    VLLM_A -->|"ZMQ: prefix cached on A"| PI
    PI -->|"req 2: same prefix → route to A"| VLLM_A
    PI -.->|"different prefix → route to B"| VLLM_B

    VLLM_A -.-> IP
    VLLM_B -.-> IP

    style VLLM_A fill:#4caf50,color:#fff
    style VLLM_B fill:#4caf50,color:#fff
    style PCS fill:#9c27b0,color:#fff
    style PI fill:#ff9800,color:#fff
```

**Cache-Aware Request Flow:**

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Scheduler EPP
    participant PI as Prefix Indexer
    participant A as vLLM Pod A
    participant B as vLLM Pod B

    C->>S: Request 1 (system prefix + Q1)
    S->>PI: Lookup prefix hash
    PI-->>S: No match (first time)
    S->>A: Route to Pod A (random)
    A->>A: Compute prefix KV cache
    A->>PI: ZMQ: "prefix X cached on Pod A"
    A-->>C: Response 1

    Note over C: 5 second delay (indexer sync)

    C->>S: Request 2 (same prefix + Q2)
    S->>PI: Lookup prefix hash
    PI-->>S: Match! Pod A has this prefix
    S->>A: Route to Pod A (cache hit)
    A->>A: Reuse cached KV (skip recompute)
    A-->>C: Response 2 (faster)

    C->>S: Request 3 (same prefix + Q3)
    S->>PI: Lookup prefix hash
    PI-->>S: Match! Pod A
    S->>A: Route to Pod A (cache hit)
    A-->>C: Response 3 (faster)
```

| Property | Value |
|----------|-------|
| Replicas | 2 |
| GPUs | 2 total |
| Scheduler | Yes — prefix-cache-scorer (weight=3) |
| Key Config | `block-size=16`, `hash-algo=sha256` |
| ZMQ | vLLM → EPP (prefix cache events) |
| Prompts | 3 chat prompts, same system prefix, 5s delay |
| Metrics | vLLM ✓  EPP ✓  Scheduler ✓  PrefixCache ✓ |

---

### 5. Prefill/Decode (P/D)

**Purpose:** Validates P/D disaggregation — prefill pods compute KV cache, transfer via NIXL to decode pod for token generation.

```mermaid
graph TB
    subgraph "Client"
        U[Test Runner]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway]
        HR[HTTPRoute]
        SVC[Service<br/>pd]
        IP[InferencePool]

        subgraph "Decode Pod (1 replica)"
            DECODE["vLLM Decode<br/>1 GPU<br/>Token generation<br/>NIXL receiver"]
            EPP["Scheduler EPP"]
        end

        subgraph "Prefill Pod A"
            PF_A["vLLM Prefill A<br/>1 GPU<br/>Prompt processing<br/>NIXL sender"]
        end

        subgraph "Prefill Pod B"
            PF_B["vLLM Prefill B<br/>1 GPU<br/>Prompt processing<br/>NIXL sender"]
        end
    end

    U -->|"POST /v1/chat/completions"| GW
    GW --> HR --> SVC --> EPP

    EPP -->|"route prompt to prefill"| PF_A
    EPP -.->|"or"| PF_B

    PF_A ==>|"NIXL/UCX/TCP<br/>KV cache transfer"| DECODE
    PF_B ==>|"NIXL/UCX/TCP<br/>KV cache transfer"| DECODE

    DECODE -->|"generated tokens"| U

    DECODE -.-> IP
    PF_A -.-> IP
    PF_B -.-> IP

    style DECODE fill:#e91e63,color:#fff
    style PF_A fill:#4caf50,color:#fff
    style PF_B fill:#4caf50,color:#fff
    style EPP fill:#2196f3,color:#fff
```

**P/D Request Flow:**

```mermaid
sequenceDiagram
    participant C as Client
    participant D as Decode Pod
    participant S as Scheduler EPP
    participant P as Prefill Pod A

    C->>D: POST /v1/chat/completions
    D->>S: Route prompt
    S->>P: Forward to Prefill A
    P->>P: Tokenize + compute KV cache
    P->>D: NIXL: Transfer KV cache (TCP/UCX)
    D->>D: Continue token generation
    D-->>C: Stream response tokens
```

| Property | Value |
|----------|-------|
| Decode Replicas | 1 |
| Prefill Replicas | 2 |
| GPUs | 3 total (1 decode + 2 prefill) |
| KV Transfer | NIXL over TCP/UCX |
| Scheduler | Yes — routes prompts to prefill pods |
| Metrics | vLLM ✓  PD ✓  NIXL ✓  Scheduler ✓ |

---

### 6. P/D + Cache-Aware

**Purpose:** Most complex topology — combines P/D disaggregation with prefix cache-aware routing. Scheduler routes to the prefill pod that already has the prefix cached.

```mermaid
graph TB
    subgraph "Client"
        U["Test Runner<br/>3 chat prompts<br/>same system prefix"]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway]
        SVC[Service]

        subgraph "Decode Pod"
            DECODE["vLLM Decode<br/>1 GPU<br/>NIXL receiver"]
            EPP["Scheduler EPP<br/>prefix-cache-scorer"]
            PI["Prefix Indexer"]
        end

        subgraph "Prefill Pod A"
            PF_A["vLLM Prefill A<br/>1 GPU<br/>prefix-cache ON<br/>NIXL sender"]
        end

        subgraph "Prefill Pod B"
            PF_B["vLLM Prefill B<br/>1 GPU<br/>prefix-cache ON<br/>NIXL sender"]
        end
    end

    U -->|requests| GW --> SVC --> EPP

    EPP -->|"route to prefill<br/>(cache-aware)"| PF_A
    EPP -.->|"or"| PF_B

    PF_A ==>|"NIXL: KV cache"| DECODE
    PF_B ==>|"NIXL: KV cache"| DECODE

    PF_A -->|"ZMQ: prefix cached"| PI
    PF_B -->|"ZMQ: prefix cached"| PI
    PI -->|"lookup: which pod<br/>has this prefix?"| EPP

    DECODE -->|response| U

    style DECODE fill:#e91e63,color:#fff
    style PF_A fill:#4caf50,color:#fff
    style PF_B fill:#4caf50,color:#fff
    style EPP fill:#2196f3,color:#fff
    style PI fill:#ff9800,color:#fff
```

**Combined Flow:**

```mermaid
sequenceDiagram
    participant C as Client
    participant EPP as Scheduler + Prefix Indexer
    participant PA as Prefill Pod A
    participant D as Decode Pod

    C->>EPP: Request 1 (prefix + Q1)
    EPP->>PA: Route to Prefill A (random first time)
    PA->>PA: Compute prefix KV cache
    PA->>D: NIXL: transfer KV cache
    PA->>EPP: ZMQ: "prefix X on Pod A"
    D-->>C: Response 1

    Note over C: 5s delay

    C->>EPP: Request 2 (same prefix + Q2)
    Note over EPP: Prefix indexer → Pod A has prefix
    EPP->>PA: Route to Prefill A (cache hit!)
    PA->>PA: Reuse prefix KV (fast)
    PA->>D: NIXL: transfer KV cache
    D-->>C: Response 2 (faster)
```

| Property | Value |
|----------|-------|
| Decode Replicas | 1 |
| Prefill Replicas | 2 |
| GPUs | 3 total |
| KV Transfer | NIXL over TCP/UCX |
| Prefix Cache | Yes — on prefill pods |
| ZMQ Events | Prefill → EPP (cache state) |
| Metrics | **All**: vLLM, PD, NIXL, EPP, Scheduler, PrefixCache |

---

### 7. Mixture of Experts (MoE)

**Purpose:** Validates distributed MoE inference with expert/tensor parallelism across multi-GPU nodes connected via RoCE/InfiniBand.

```mermaid
graph TB
    subgraph "Client"
        U[Test Runner]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway]
        SVC[Service<br/>moe]
        IP[InferencePool]

        subgraph "Main Pod (8 GPUs)"
            VLLM_M["vLLM Main<br/>8× GPU<br/>Expert subset"]
            EPP[Scheduler EPP]
        end

        subgraph "Worker Pod 1 (8 GPUs)"
            W1["vLLM Worker<br/>8× GPU<br/>Expert subset"]
        end

        subgraph "Worker Pod N (8 GPUs)"
            WN["vLLM Worker<br/>8× GPU<br/>Expert subset"]
        end

        NET["RoCE / InfiniBand<br/>NCCL all-reduce<br/>GPU Direct RDMA"]
    end

    U -->|request| GW --> SVC --> EPP --> VLLM_M

    VLLM_M <-->|"NCCL collective ops"| NET
    W1 <-->|"NCCL collective ops"| NET
    WN <-->|"NCCL collective ops"| NET

    VLLM_M -.-> IP

    style VLLM_M fill:#9c27b0,color:#fff
    style W1 fill:#673ab7,color:#fff
    style WN fill:#673ab7,color:#fff
    style NET fill:#ff5722,color:#fff
```

| Property | Value |
|----------|-------|
| Model | DeepSeek-R1-0528 |
| GPUs | 8 per pod (main + N workers) |
| Parallelism | data=32, expert=true, tensor=1 |
| Network | RoCE/IB (NCCL, NVSHMEM, UCX) |
| Storage | 500Gi PVC |
| Ready Timeout | 90m |
| Metrics | vLLM ✓  Scheduler ✓ |

---

### 8. Multi-Pool

**Purpose:** Validates multiple independent InferencePools sharing a single Gateway — tests VirtualService merge (OSSM-12585 fix).

```mermaid
graph TB
    subgraph "Client"
        U[Test Runner]
    end

    subgraph "Kubernetes Cluster"
        GW[Gateway<br/>shared]
        VS["VirtualService<br/>merges both pools"]

        subgraph "Pool A"
            HR_A[HTTPRoute A]
            SVC_A[Service A]
            IP_A[InferencePool A]
            subgraph "Pod A"
                VLLM_A[vLLM<br/>1 GPU]
                EPP_A[Scheduler A]
            end
        end

        subgraph "Pool B"
            HR_B[HTTPRoute B]
            SVC_B[Service B]
            IP_B[InferencePool B]
            subgraph "Pod B"
                VLLM_B[vLLM<br/>1 GPU]
                EPP_B[Scheduler B]
            end
        end
    end

    U -->|"/multi-pool-a/..."| GW
    U -->|"/multi-pool-b/..."| GW
    GW --> VS
    VS -->|"prefix: /multi-pool-a/"| HR_A --> SVC_A --> EPP_A --> VLLM_A
    VS -->|"prefix: /multi-pool-b/"| HR_B --> SVC_B --> EPP_B --> VLLM_B
    VLLM_A -.-> IP_A
    VLLM_B -.-> IP_B

    style VLLM_A fill:#4caf50,color:#fff
    style VLLM_B fill:#ff9800,color:#fff
    style EPP_A fill:#2196f3,color:#fff
    style EPP_B fill:#2196f3,color:#fff
    style VS fill:#9c27b0,color:#fff
```

| Property | Value |
|----------|-------|
| Pools | 2 (multi-pool-a, multi-pool-b) |
| GPUs | 2 total (1 per pool) |
| Schedulers | 2 (independent per pool) |
| Routing | VirtualService prefix-based |
| Validates | OSSM-12585 multi-pool merge |

---

## Deployment Modes

### Mock Mode

Replaces vLLM containers with a lightweight mock server — no GPU, no model download. The scheduler, EPP, and InferencePool remain real.

```mermaid
graph LR
    subgraph "Normal Mode"
        N_VLLM["vLLM Container<br/>GPU required<br/>Model download<br/>Real inference"]
    end

    subgraph "Mock Mode (MOCK=image)"
        M_VLLM["Mock Container<br/>CPU only (100m)<br/>No model download<br/>Fake /v1/* endpoints"]
        M_SCH["Scheduler EPP<br/>REAL (unchanged)"]
        M_IP["InferencePool<br/>REAL (unchanged)"]
    end

    N_VLLM -->|"replaced by"| M_VLLM
    M_VLLM --- M_SCH --- M_IP
```

**What changes in mock mode:**

| Component | Normal | Mock |
|-----------|--------|------|
| vLLM image | `vllm/vllm:latest` | Mock image |
| GPU | Required | Not needed |
| CPU/Memory | 4 CPU, 32Gi | 100m CPU, 64Mi |
| storageInitializer | Downloads model | Disabled |
| Scheduler/EPP | Real | **Real** (unchanged) |
| InferencePool | Real | **Real** (unchanged) |
| Phase 5 (model files) | Checked | Skipped |
| Phase 8 (metrics) | Scraped | **Skipped** |
| Prefill pods (P/D) | Real vLLM | **Mock** (both replaced) |

**Usage:**
```bash
make test TESTCASE=single-gpu MOCK=ghcr.io/aneeshkp/vllm-mock:latest
make test-profile-all MOCK=ghcr.io/aneeshkp/vllm-mock:latest  # all topologies
```

---

### Model Sources

```mermaid
flowchart TD
    MS{MODEL_SOURCE?}

    MS -->|"hf (default)"| HF["hf:// Direct Download<br/>vLLM downloads at startup<br/>via storage-initializer"]

    MS -->|"pvc"| PVC["pvc:// Pre-cached<br/>Download to PVC first<br/>via make cache-model"]

    MS -->|"pvc-snapshot"| SNAP["pvc-snapshot://<br/>HF cache layout<br/>snapshot_download()"]

    HF --> HF_FLOW["Pod starts → init container<br/>downloads from HuggingFace<br/>→ /mnt/models/ModelName/"]

    PVC --> PVC_FLOW["1. make cache-model<br/>2. Job downloads to PVC<br/>3. URI: pvc://pvc-name/ModelName<br/>4. Pod mounts PVC directly"]

    SNAP --> SNAP_FLOW["1. Download with HF cache<br/>2. models--Org--Model/snapshots/hash/<br/>3. URI: pvc://pvc-name"]

    style HF fill:#4caf50,color:#fff
    style PVC fill:#2196f3,color:#fff
    style SNAP fill:#9c27b0,color:#fff
```

---

### Discover Mode

Validates an existing deployment without deploying or cleaning up.

```mermaid
flowchart LR
    subgraph "Phases SKIPPED"
        P1[1. PREP]
        P2[2. PREREQ]
        P3[3. DEPLOY]
        P4[4. READY]
        P5[5. MODEL FILES]
    end

    subgraph "Phases EXECUTED"
        P6[6. HEALTH ✓]
        P6b[6b. MODEL LIST ✓]
        P7[7. INFERENCE ✓]
        P8[8. METRICS ✓]
    end

    P1 ~~~ P6

    style P1 fill:#ccc
    style P2 fill:#ccc
    style P3 fill:#ccc
    style P4 fill:#ccc
    style P5 fill:#ccc
    style P6 fill:#4caf50,color:#fff
    style P6b fill:#4caf50,color:#fff
    style P7 fill:#4caf50,color:#fff
    style P8 fill:#4caf50,color:#fff
```

**Usage:**
```bash
make test TESTCASE=single-gpu DISCOVER=true NAMESPACE=my-existing-ns
```

---

## Platform Differences

| Feature | OCP | AKS | GKS |
|---------|-----|-----|-----|
| CLI tool | `oc` (preferred) | `kubectl` | `kubectl` |
| Pull secrets | Global (skipped) | Copied from source NS | Copied from source NS |
| Auth disable | Annotation injection | Annotation injection | Annotation injection |
| Gateway discovery | `opendatahub`, `istio-system` | `kserve`, `istio-system` | `kserve`, `istio-system` |
| Storage default | RWO | Azure Disk (RWO) | Persistent Disk (RWO) |
| Multi-replica PVC | RWX via NFS | `azurefile-csi-premium` | Custom StorageClass |

---

## Manifest Patching Pipeline

Every manifest goes through this transformation pipeline before `kubectl apply`:

```mermaid
flowchart TD
    MF[Original Manifest<br/>deploy/manifests/*.yaml]

    MF --> A{DisableAuth?}
    A -->|yes| A1["Inject annotation:<br/>security.opendatahub.io/<br/>enable-auth: false"]
    A -->|no| B

    A1 --> B{Model URI<br/>override?}
    B -->|yes| B1["Patch spec.model.uri<br/>and spec.model.name"]
    B -->|no| C

    B1 --> C{PullSecret<br/>override?}
    C -->|yes| C1["Patch imagePullSecrets<br/>name entries"]
    C -->|no| D

    C1 --> D{Mock mode?}
    D -->|yes| D1["Disable storageInitializer"]
    D1 --> D2["Replace vLLM containers<br/>with mock image<br/>(skip scheduler containers)"]
    D -->|no| E

    D2 --> E[Patched Manifest<br/>temp file]
    E --> F["kubectl apply -n ns -f patched.yaml"]
    F --> G["Delete temp file"]

    style MF fill:#e3f2fd
    style E fill:#fff3e0
    style F fill:#e8f5e9
```

---

## Metrics Validation Matrix

Which metrics are checked per test case topology:

| Metric | Smoke | Single | No-Sched | Cache | P/D | P/D+Cache | MoE | Multi |
|--------|:-----:|:------:|:--------:|:-----:|:---:|:---------:|:---:|:-----:|
| `vllm:request_success_total` | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `vllm:prompt_tokens_total` | — | ✓ | — | — | ✓ | ✓ | ✓ | ✓ |
| `vllm:generation_tokens_total` | — | ✓ | — | — | ✓ | ✓ | ✓ | ✓ |
| `vllm:gpu_cache_usage_perc` | — | ✓ | — | ✓ | — | ✓ | — | ✓ |
| `vllm:prefix_cache_queries` | — | — | — | ✓ | — | ✓ | — | — |
| `vllm:prefix_cache_hits` | — | — | — | ✓ | — | ✓ | — | — |
| `prefix_cache_hit_rate` | — | — | — | ✓ | — | ✓ | — | — |
| `nixl:kv_transfer_count` | — | — | — | — | ✓ | ✓ | — | — |
| `nixl:kv_transfer_failures` | — | — | — | — | ✓ | ✓ | — | — |
| `scheduler_e2e_duration` | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `request_total` | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `request_error_total` | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `pool_ready_pods` | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| `prefix_indexer_size` | — | — | — | ✓ | — | ✓ | — | — |

---

## Suggested Improvements

### High Priority

| # | Improvement | Impact |
|---|-------------|--------|
| 1 | **Increase deployer test coverage** (19% → 60%+) | Catch manifest patching edge cases |
| 2 | **Pre-flight `kubectl apply --dry-run=client`** | Fail fast on invalid manifests |
| 3 | **Per-phase configurable timeouts** in test case YAML | Different clusters need different timeouts |
| 4 | **Labeled break in loops** and better error context | Easier debugging |

### Medium Priority

| # | Improvement | Impact |
|---|-------------|--------|
| 5 | **Test parallelism** with per-test namespaces | 2h → 30m runtime |
| 6 | **Separate download timeout** from ready timeout | Large models need 2h+ download |
| 7 | **Pre-flight cluster checks** (node capacity, GPU availability) | Fail fast on capacity issues |
| 8 | **Extract magic numbers** (ports, labels, timeouts) to constants | Maintainability |

### Low Priority

| # | Improvement | Impact |
|---|-------------|--------|
| 9 | **Plugin system** for custom validators | Extensibility |
| 10 | **Performance tracking** (latency trends over time) | Regression detection |
| 11 | **Manifest versioning** (v1, v2 patching strategies) | Backwards compatibility |
| 12 | **Per-test namespace isolation** | Prevent state leakage |

---

## Quick Reference

### Common Commands

```bash
# Setup
make setup                              # Clone manifests (main branch)
make setup MANIFEST_REF=3.4-ea2         # Clone specific branch
make manifests                          # List available branches

# Run tests
make test TESTCASE=single-gpu           # Single test case
make test TESTCASE=single-gpu,pd        # Multiple test cases
make test-profile-all                   # All conformance tests

# Mock mode (no GPU)
make test TESTCASE=single-gpu MOCK=ghcr.io/aneeshkp/vllm-mock:latest
make test-profile-all MOCK=ghcr.io/aneeshkp/vllm-mock:latest

# Validate existing deployment
make test TESTCASE=single-gpu DISCOVER=true NAMESPACE=my-ns

# Cache model to PVC
make cache-model TESTCASE=single-gpu STORAGE_CLASS=azurefile-rwx

# Debug (keep resources)
make test TESTCASE=pd NO_CLEANUP=1

# Unit tests
make unittest

# Discovery
make profiles                           # List test profiles
make testcases                          # List test cases
make models                             # List models
```

### File Layout

```
├── configs/
│   ├── testcases/          # 8 test case definitions
│   │   ├── single-gpu-smoke.yaml
│   │   ├── single-gpu.yaml
│   │   ├── single-gpu-no-scheduler.yaml
│   │   ├── cache-aware.yaml
│   │   ├── pd.yaml
│   │   ├── pd-cache-aware.yaml
│   │   ├── moe.yaml
│   │   └── multi-pool.yaml
│   └── profiles/           # 5 test profiles
│       ├── smoke.yaml
│       ├── all.yaml
│       ├── cache-aware.yaml
│       ├── pd.yaml
│       └── deepseek.yaml
├── deploy/manifests/       # Cloned from manifest repo (gitignored)
├── framework/
│   ├── cleanup/            # Resource lifecycle tracking
│   ├── client/             # OpenAI-compatible API client
│   ├── config/             # YAML config loading (95% coverage)
│   ├── deployer/           # K8s deployment management (31% coverage)
│   ├── metrics/            # Prometheus scraping (71% coverage)
│   ├── model/              # Model download & PVC caching
│   ├── reporter/           # JSON & HTML reporting
│   └── retry/              # Polling utilities
├── tests/
│   ├── conformance_test.go # 9-phase test lifecycle (1400+ lines)
│   ├── suite_test.go       # Ginkgo suite + CLI flags
│   └── smoke/              # Framework validation (no cluster)
├── docs/
│   ├── architecture.md     # This document
│   ├── adding-test-cases.md
│   └── disconnected-ocp-setup.md
└── Makefile                # All targets and flag mappings
```

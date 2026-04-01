# Adding New Models and Test Cases

Adding a new model or test case requires **zero code changes** — just 3 YAML files.

## Overview

The framework auto-discovers test cases from `configs/testcases/`. Each test case references a manifest in `deploy/manifests/`. Two manifest variants exist for every test case:

```
deploy/manifests/
├── hf/                    # vLLM downloads from HuggingFace at pod startup
│   └── <name>.yaml        #   uri: hf://org/model-name
└── pvc/                   # Pre-cached model in a PVC (faster startup)
    └── <name>.yaml        #   uri: pvc://<pvc-name>/<model-subpath>
```

## Step-by-Step

### 1. Create the HF manifest

Create `deploy/manifests/hf/<name>.yaml` with your `LLMInferenceService` spec.

The `uri` field should use the `hf://` scheme pointing to the HuggingFace model repo.

```yaml
apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: my-new-model
spec:
  model:
    uri: hf://org/model-name
    name: org/model-name
  replicas: 1
  router:
    scheduler: {}
    route: {}
    gateway: {}
  template:
    containers:
      - name: main
        resources:
          limits:
            cpu: "4"
            memory: 32Gi
            nvidia.com/gpu: "1"
          requests:
            cpu: "2"
            memory: 16Gi
            nvidia.com/gpu: "1"
        livenessProbe:
          httpGet:
            path: /health
            port: 8000
            scheme: HTTPS
          initialDelaySeconds: 120
          periodSeconds: 30
          timeoutSeconds: 30
          failureThreshold: 5
```

Customize the manifest based on what you're testing:

| Feature | What to add |
|---------|-------------|
| No scheduler | Remove `scheduler: {}` from `router` |
| Prefill/Decode | Add `prefill:` section with its own `template` |
| Cache-aware routing | Add scheduler `config.inline` with prefix-cache plugins |
| Data/Expert parallelism | Add `parallelism:` section |
| RDMA/RoCE | Add network annotations and `rdma/roce_gdr` resources |
| Multiple GPUs | Set `nvidia.com/gpu: "8"` in resources |

See existing manifests in `deploy/manifests/hf/` for examples of each pattern.

### 2. Create the PVC manifest

Create `deploy/manifests/pvc/<name>.yaml` — same as the HF manifest but with a `pvc://` URI.

The PVC name is auto-generated from the model name by the framework. To find what it will be:

```bash
# Model name: org/model-name
# PVC name:   model-cache-org-model-name
# URI:        pvc://model-cache-org-model-name/model-name
```

The naming convention is:
- Take the model name (e.g., `Qwen/Qwen2.5-7B-Instruct`)
- Lowercase, replace `/`, `_`, `.` with `-`
- Truncate to 50 chars
- Prefix with `model-cache-`
- Append `/<model-name-after-slash>` for the subpath

Example for `Qwen/Qwen2.5-7B-Instruct`:
```
PVC name: model-cache-qwen-qwen2-5-7b-instruct
URI:      pvc://model-cache-qwen-qwen2-5-7b-instruct/Qwen2.5-7B-Instruct
```

Quick way to create the PVC manifest:
```bash
cp deploy/manifests/hf/my-new-model.yaml deploy/manifests/pvc/my-new-model.yaml
# Edit the uri field:
#   Before: uri: hf://org/model-name
#   After:  uri: pvc://model-cache-org-model-name/model-name
```

The PVC manifest can also have other differences (e.g., volume mounts, storage config) depending on your setup. See the [KServe PVC storage docs](https://github.com/kserve/kserve/tree/master/docs/samples/storage/pvc-init) for reference.

### 3. Create the test case config

Create `configs/testcases/<name>.yaml`:

```yaml
name: my-new-model
description: "Short description of what this tests"
labels:
  - gpu
  - smoke                  # add to smoke profile for quick validation
  - single-node-gpu        # category label
model:
  name: org/model-name
  uri: hf://org/model-name
  displayName: my-new-model
  category: single-node-gpu
  cache:
    enabled: true           # set to true for PVC caching
    storageSize: 100Gi      # PVC size
    keepPVC: true           # reuse PVC across test runs
    timeout: 60m            # download timeout
deployment:
  manifestPath: my-new-model.yaml   # just the filename, not the full path
  replicas: 1
  readyTimeout: 15m         # how long to wait for READY=True
  resources:
    cpu: "4"
    memory: 32Gi
    gpus: 1
    rdma: false
validation:
  healthEndpoint: /health
  healthPort: 8000
  healthScheme: HTTPS        # KServe injects TLS certs
  inferenceCheck: true       # send test prompts
  testPrompts:
    - "What is Kubernetes?"
    - "Hello, how are you?"
  expectedCodes: [200]
  timeout: 2m               # health/inference timeout
  retryAttempts: 3
  retryInterval: 15s
cleanup: true                # delete LLMInferenceService after test
```

#### Test case config fields

| Field | Description |
|-------|-------------|
| `name` | Unique test case name (used with `TESTCASE=`) |
| `labels` | For filtering with `LABELS=` |
| `model.name` | HuggingFace model ID (e.g., `Qwen/Qwen2.5-7B-Instruct`) |
| `model.uri` | Base model URI (`hf://` or `pvc://`) |
| `model.cache.enabled` | If true, PREP phase downloads model to PVC |
| `model.cache.storageSize` | PVC storage request |
| `deployment.manifestPath` | Manifest filename (looked up in `deploy/manifests/{hf,pvc}/`) |
| `deployment.readyTimeout` | Max wait for `LLMInferenceService` to become READY |
| `validation.healthScheme` | `HTTP` or `HTTPS` (KServe uses HTTPS by default) |
| `validation.inferenceCheck` | Whether to send test prompts after health check passes |
| `validation.testPrompts` | Prompts to send via `/v1/chat/completions` |
| `cleanup` | Delete resources after test completes |

### 4. (Optional) Add to a profile

Add the test case name to a profile in `configs/profiles/`:

```yaml
# configs/profiles/full.yaml
testCases:
  - qwen2-7b-gpu
  - my-new-model          # add here
```

Or create a new profile:

```yaml
# configs/profiles/my-profile.yaml
name: my-profile
description: "My custom test profile"
platform: any
testCases:
  - my-new-model
parallel: false
timeout: 60m
```

## Running your new test case

```bash
# Verify it's discovered
make list-testcases
make list-models

# Pre-cache the model (one-time)
make cache-model TESTCASE=my-new-model

# Run with PVC (default, fast)
make test-single TESTCASE=my-new-model

# Run with HuggingFace (no PVC needed)
make test-single TESTCASE=my-new-model MODEL_SOURCE=hf

# Run via profile
make test-conformance PROFILE=configs/profiles/my-profile.yaml
```

## Common patterns

### Single-node GPU (simplest)

See: `deploy/manifests/hf/qwen2-7b-gpu.yaml`

### No scheduler (route-only, no EPP)

See: `deploy/manifests/hf/qwen2-7b-gpu-no-scheduler.yaml`

Difference: `router` only has `route: {}`, no `scheduler: {}`.

### Prefill/Decode disaggregation

See: `deploy/manifests/hf/qwen2-7b-gpu-pd.yaml`

Adds a `prefill:` section with separate replicas, resources, and RDMA config.

### Cache-aware routing (prefix KV cache)

See: `deploy/manifests/hf/cache-aware-qwen2-7b-gpu.yaml`

Adds scheduler plugins (`precise-prefix-cache-scorer`, `queue-scorer`, etc.) in `router.scheduler.config.inline`.

### Multi-node MoE with data/expert parallelism

See: `deploy/manifests/hf/deepseek-coder-v2-dp-ep-naive.yaml`

Adds `parallelism:` section and `worker:` template with NCCL/NVSHMEM env vars.

### Pre-existing PVC (manually created)

See: `deploy/manifests/hf/deepseek-r1-dp-ep-ht.yaml`

Uses `uri: pvc://llm-test-pvc-deepseek` directly. The PVC must exist before running the test. Set `cache.enabled: true` in the test case config so the framework verifies the PVC exists.

## File checklist

| File | Required | Purpose |
|------|----------|---------|
| `deploy/manifests/hf/<name>.yaml` | Yes | LLMInferenceService with `hf://` URI |
| `deploy/manifests/pvc/<name>.yaml` | Yes | LLMInferenceService with `pvc://` URI |
| `configs/testcases/<name>.yaml` | Yes | Test case config (labels, timeouts, prompts) |
| `configs/profiles/<profile>.yaml` | Optional | Group test cases into a named profile |

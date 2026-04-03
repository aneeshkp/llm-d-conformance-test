# Adding New Test Cases

Adding a new test case requires **zero code changes** — just 2 files.

## Overview

The framework auto-discovers test cases from `configs/testcases/`. Each test case references a manifest by filename. Manifests are maintained in a separate repo ([llm-d-conformance-manifests](https://github.com/aneeshkp/llm-d-conformance-manifests)) and cloned locally via `make setup`.

```
configs/testcases/<name>.yaml     ← test case config (this repo)
deploy/manifests/<name>.yaml      ← manifest (cloned from manifest repo)
```

The framework patches the manifest's `uri:` and `name:` fields at runtime based on `MODEL_SOURCE` and `MODEL` flags. You only need one manifest per test case.

## Step-by-Step

### 1. Create the manifest

Add `<name>.yaml` to the manifest repo on the appropriate branch (e.g., `3.4-ea2`):

```yaml
apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: my-new-test
spec:
  model:
    uri: hf://Qwen/Qwen3-0.6B
    name: Qwen/Qwen3-0.6B
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

Customize based on what you're testing:

| Feature | What to add |
|---------|-------------|
| No scheduler | Remove `scheduler: {}` from `router` |
| Prefill/Decode | Add `prefill:` section with separate `template` |
| Cache-aware routing | Add scheduler `template` with `--config-text` for prefix-cache plugins |
| Data/Expert parallelism | Add `parallelism:` section |
| RDMA/RoCE | Add network annotations and `rdma/roce_gdr` resources |
| Multiple GPUs | Set `nvidia.com/gpu: "8"` in resources |

Push to the manifest repo, then run `make setup` to pull the new manifest.

### 2. Create the test case config

Create `configs/testcases/<name>.yaml`:

```yaml
name: my-new-test
description: "Short description of what this tests"
model:
  name: Qwen/Qwen3-0.6B
  uri: hf://Qwen/Qwen3-0.6B
  displayName: my-new-test
  category: single-node-gpu
  cache:
    enabled: true
    storageSize: 10Gi
    keepPVC: true
    timeout: 15m
deployment:
  manifestPath: my-new-test.yaml    # filename in deploy/manifests/
  replicas: 1
  readyTimeout: 15m
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
    - "What is Kubernetes?"
  expectedCodes: [200]
  timeout: 2m
  retryAttempts: 3
  retryInterval: 15s
cleanup: true
```

#### Key fields

| Field | Description |
|-------|-------------|
| `name` | Unique test case name (used with `TESTCASE=`) |
| `model.name` | HuggingFace model ID — can be overridden with `MODEL=` |
| `model.uri` | Base model URI (framework patches this based on `MODEL_SOURCE`) |
| `model.cache.storageSize` | PVC size — can be overridden with `STORAGE_SIZE=` |
| `deployment.manifestPath` | Manifest filename (looked up in `deploy/manifests/`) |
| `deployment.readyTimeout` | Max wait for `LLMInferenceService` to become READY |
| `validation.healthScheme` | `HTTP` or `HTTPS` (KServe uses HTTPS by default) |
| `validation.chatPrompts` | Structured system+user prompts (for cache-aware tests) |
| `validation.metricsCheck` | Enable metrics validation (vLLM, EPP, scheduler) |
| `cleanup` | Delete resources after test completes |

#### Adding metrics validation

```yaml
validation:
  metricsCheck:
    enabled: true
    checkVLLM: true          # vLLM prompt/gen tokens, request success
    checkEPP: true           # EPP scheduler metrics
    checkPrefixCache: true   # prefix cache queries/hits (cache-aware)
    checkScheduler: true     # scheduler e2e, routing errors
    checkPD: true            # P/D prompt/gen tokens, preemptions
    checkNIXL: true          # NIXL KV transfer count/failures
```

#### Adding cache-aware chat prompts

```yaml
validation:
  chatPrompts:
    - system: "Long shared system prefix..."
      user: "First question"
    - system: "Long shared system prefix..."
      user: "Second question (triggers prefix cache hit)"
```

### 3. (Optional) Add to a profile

Add the test case name to a profile in `configs/profiles/`:

```yaml
# configs/profiles/full.yaml
testCases:
  - single-gpu
  - my-new-test          # add here
```

## PVC naming

When using `MODEL_SOURCE=pvc`, the PVC name is derived from the model name:

```
Model:    Qwen/Qwen3-0.6B
PVC:      model-cache-qwen-qwen3-0-6b
URI:      pvc://model-cache-qwen-qwen3-0-6b/Qwen3-0.6B
```

Convention: lowercase, replace `/`, `_`, `.` with `-`, prefix `model-cache-`, truncate to 50 chars.

Test cases sharing the same model share the same PVC — cache once, reuse across tests.

## Running your new test case

```bash
# Re-clone manifests after adding to manifest repo
make setup

# Verify it's discovered
make testcases

# Run with HuggingFace (default)
make test TESTCASE=my-new-test

# Run with a different model
make test TESTCASE=my-new-test MODEL=Qwen/Qwen2.5-7B-Instruct

# Pre-cache model, then run with PVC
make cache-model TESTCASE=my-new-test
make test TESTCASE=my-new-test MODEL_SOURCE=pvc
```

## File checklist

| File | Where | Required |
|------|-------|----------|
| `<name>.yaml` manifest | Manifest repo (`llm-d-conformance-manifests`) | Yes |
| `configs/testcases/<name>.yaml` | This repo | Yes |
| `configs/profiles/<profile>.yaml` | This repo | Optional |

# Testing on Disconnected/Bare-Metal OCP Clusters

## 1. Fix Gateway

```bash
# Quick fix (may get reverted by operator)
kubectl patch svc openshift-ai-inference-openshift-ai-inference \
  -n openshift-ingress --type merge \
  -p '{"spec":{"type":"ClusterIP"}}'

# Verify: should show Programmed: True
kubectl get gateway -n openshift-ingress
```

## 2. Create Route

Required when running tests from a laptop. Uses `Passthrough` TLS because the KServe gateway serves HTTPS with its own self-signed certs (`enableLLMInferenceServiceTLS: true`).

> **Note:** Use `Passthrough`, not `Edge`. Edge termination causes 502 because the router forwards HTTP to the gateway which expects HTTPS.

```bash
kubectl apply -f - <<'EOF'
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: openshift-ai-inference-route
  namespace: openshift-ingress
spec:
  to:
    kind: Service
    name: openshift-ai-inference-openshift-ai-inference
    weight: 100
  port:
    targetPort: https
  tls:
    termination: Passthrough
  wildcardPolicy: None
EOF

# Get hostname
kubectl get route openshift-ai-inference-route -n openshift-ingress -o jsonpath='{.spec.host}'
```

## 3. Mirror Mock Image

```bash
# Find mirror registry
kubectl get imagedigestmirrorset -o yaml | grep "mirrors:" -A1

# Get credentials
oc extract secret/pull-secret -n openshift-config --to=/tmp/

# Mirror
oc image mirror \
  ghcr.io/aneeshkp/vllm-mock:latest \
  <bastion-host>:8443/models/vllm-mock:latest \
  --insecure=true \
  -a /tmp/.dockerconfigjson
```

## 4. RBAC for Bearer Token

```bash
# Grant access to your user
kubectl create clusterrolebinding htpasswd-llmisvc-access \
  --clusterrole=cluster-admin \
  --user=<your-username>
```

## 5. Run Tests

```bash
make setup MANIFEST_REF=3.4-ea2

TOKEN=$(oc whoami -t)
make test TESTCASE=single-gpu \
  MOCK=<bastion-host>:8443/models/vllm-mock:latest \
  PLATFORM=ocp \
  ENDPOINT=https://<route-hostname> \
  BEARER_TOKEN=$TOKEN

# All tests
make test-profile-all \
  MOCK=<bastion-host>:8443/models/vllm-mock:latest \
  PLATFORM=ocp \
  ENDPOINT=https://<route-hostname> \
  BEARER_TOKEN=$TOKEN
```

## Verify with curl

```bash
TOKEN=$(oc whoami -t)
curl -sk -H "Authorization: Bearer $TOKEN" \
  https://<route-hostname>/<namespace>/<llmisvc-name>/health
```

## Notes

- The framework auto-builds the URL path (`/<namespace>/<llmisvc-name>/...`) from the `ENDPOINT` base URL — no need to include it in the `ENDPOINT` flag.
- The framework auto-disables `storageInitializer` in mock mode so no HuggingFace download is attempted.
- `ImageDigestMirrorSet` is only needed if you reference images by their original public URL. If you pass the full bastion URL directly (e.g., `MOCK=bastion:8443/models/vllm-mock:latest`), no mirror set is needed.
- Using Gateways with ClusterIP services is **not officially supported** on OCP and requires a support exception.

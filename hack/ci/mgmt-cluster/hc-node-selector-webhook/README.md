# hc-node-selector-webhook

MutatingAdmissionWebhook that injects a default `spec.nodeSelector` into HostedCluster CREATE requests when one is not already set.

## Problem

On HyperShift management clusters, HostedCluster control plane (HCP) pods can spill onto undersized worker nodes when `spec.nodeSelector` is not specified at creation time. This causes memory exhaustion, CoreDNS kills, and cascading test failures in CI.

The root cause is that multiple HC creation paths (CI step registry, e2e binary) omit the `--node-selector` flag. Rather than patching each path individually, this webhook intercepts all HostedCluster CREATEs centrally.

See [OCPBUGS-82112](https://issues.redhat.com/browse/OCPBUGS-82112) for the full root cause analysis.

## How it works

1. The webhook intercepts all `CREATE` operations on `hostedclusters.hypershift.openshift.io/v1beta1`
2. If `spec.nodeSelector` is nil or empty, it injects:
   ```json
   {"hypershift.openshift.io/control-plane": "true"}
   ```
3. If `spec.nodeSelector` is already set, the request passes through unchanged
4. `failurePolicy: Ignore` ensures CI is never blocked if the webhook is down

## Deployment

### Prerequisites

- OpenShift cluster with the HyperShift operator installed
- `oc` CLI authenticated with cluster-admin privileges
- The target namespace (`hypershift`) must exist

### Deploy

```bash
oc apply -f manifests/serviceaccount.yaml
oc apply -f manifests/service.yaml
oc apply -f manifests/deployment.yaml
oc apply -f manifests/pdb.yaml

# Wait for the service-CA operator to generate the TLS secret
oc get secret hc-node-selector-webhook-serving-cert -n hypershift

# Wait for pods to be ready
oc rollout status deployment/hc-node-selector-webhook -n hypershift

# Activate the webhook (this starts intercepting HostedCluster CREATEs)
oc apply -f manifests/mutating-webhook-configuration.yaml
```

### Verify

```bash
# Check pods are running
oc get pods -n hypershift -l app=hc-node-selector-webhook

# Check logs
oc logs -n hypershift -l app=hc-node-selector-webhook -f

# Check which HCs have nodeSelector set
oc get hostedclusters -A -o custom-columns='NAME:.metadata.name,NAMESPACE:.metadata.namespace,NODE_SELECTOR:.spec.nodeSelector'

# Check webhook stats (terminal 1: port-forward)
oc port-forward -n hypershift svc/hc-node-selector-webhook 8443:443 &

# Terminal 2: query stats
curl -sk https://127.0.0.1:8443/stats
```

### Remove

```bash
oc delete mutatingwebhookconfiguration hc-node-selector-webhook
oc delete deployment hc-node-selector-webhook -n hypershift
oc delete service hc-node-selector-webhook -n hypershift
oc delete pdb hc-node-selector-webhook -n hypershift
oc delete serviceaccount hc-node-selector-webhook -n hypershift
```

## Configuration

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

Set `LOG_LEVEL=debug` to see full request/response details, body sizes, and non-CREATE passthrough events.

### Endpoints

| Path | Method | Description |
|------|--------|-------------|
| `/mutate-hostedcluster` | POST | Admission webhook endpoint |
| `/healthz` | GET | Liveness probe |
| `/readyz` | GET | Readiness probe |
| `/stats` | GET | JSON counters (mutated, skipped, denied, errors, uptime) |

## Architecture

### Mutation logic

```text
HostedCluster CREATE request
    |
    v
spec.nodeSelector already set?
    |-- yes --> allow, no mutation (action: skipped)
    |-- no, spec exists --> JSON patch: add /spec/nodeSelector (action: mutated)
    |-- no, spec is nil --> JSON patch: add /spec with nodeSelector (action: mutated)
```

### Production hardening

- **2 replicas** with `topologySpreadConstraints` across nodes
- **PodDisruptionBudget** with `minAvailable: 1`
- **Dedicated ServiceAccount** with `automountServiceAccountToken: false`
- **Full securityContext**: `runAsNonRoot`, `readOnlyRootFilesystem`, `drop ALL`, `seccompProfile: RuntimeDefault`
- **TLS cert auto-reload** via `GetCertificate` callback (survives OpenShift service-CA cert rotation)
- **Graceful shutdown** on SIGTERM with 5s drain
- **Structured JSON logging** via `log/slog` with configurable levels
- **Request validation**: method, content-type, body size limit (1 MB), nil-request guard
- **`failurePolicy: Ignore`** with `timeoutSeconds: 5` to prevent CI disruption

### Log format

Every mutation is logged as structured JSON:

```json
{
  "time": "2026-04-27T17:01:24.579Z",
  "level": "INFO",
  "msg": "injected default nodeSelector",
  "uid": "870cee6d-e4bd-4d03-88b4-98eef506b416",
  "name": "76269a10dc0bf73ef65a",
  "namespace": "clusters",
  "operation": "CREATE",
  "user": "system:serviceaccount:hypershift-ops:admin",
  "action": "mutated",
  "spec_was_nil": false,
  "node_selector": {"hypershift.openshift.io/control-plane": "true"},
  "duration_ms": 0
}
```

## Building

```bash
# Run tests
go test -v -race ./...

# Build binary
go build -o webhook .

# Build container image
podman build -t quay.io/hypershift/hc-node-selector-webhook:v0.2.0 -f Containerfile .

# Push
podman push quay.io/hypershift/hc-node-selector-webhook:v0.2.0
```

## Testing with a throwaway CRD

To validate the webhook end-to-end without affecting real HostedClusters:

```bash
# Create a test CRD with the same spec.nodeSelector shape
oc apply -f - <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: nodeplacementtests.test.hypershift.io
spec:
  group: test.hypershift.io
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                nodeSelector:
                  type: object
                  additionalProperties:
                    type: string
  scope: Namespaced
  names:
    plural: nodeplacementtests
    singular: nodeplacementtest
    kind: NodePlacementTest
EOF

# Create a test webhook config targeting the throwaway CRD
oc apply -f - <<'EOF'
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: hc-node-selector-webhook-test
  annotations:
    service.beta.openshift.io/inject-cabundle: "true"
webhooks:
  - name: hc-node-selector-webhook-test.hypershift.svc
    admissionReviewVersions: ["v1"]
    sideEffects: None
    failurePolicy: Fail
    timeoutSeconds: 5
    clientConfig:
      service:
        name: hc-node-selector-webhook
        namespace: hypershift
        path: "/mutate-hostedcluster"
    rules:
      - operations: ["CREATE"]
        apiGroups: ["test.hypershift.io"]
        apiVersions: ["v1"]
        resources: ["nodeplacementtests"]
EOF

# Test: create without nodeSelector (should be injected)
oc apply -f - <<'EOF'
apiVersion: test.hypershift.io/v1
kind: NodePlacementTest
metadata:
  name: test-no-selector
  namespace: default
spec: {}
EOF

# Verify injection
oc get nodeplacementtest test-no-selector -n default -o jsonpath='{.spec.nodeSelector}'
# Expected: {"hypershift.openshift.io/control-plane":"true"}

# Test: create with nodeSelector (should be untouched)
oc apply -f - <<'EOF'
apiVersion: test.hypershift.io/v1
kind: NodePlacementTest
metadata:
  name: test-with-selector
  namespace: default
spec:
  nodeSelector:
    custom-key: custom-value
EOF

# Verify no mutation
oc get nodeplacementtest test-with-selector -n default -o jsonpath='{.spec.nodeSelector}'
# Expected: {"custom-key":"custom-value"}

# Clean up
oc delete nodeplacementtest test-no-selector test-with-selector -n default
oc delete mutatingwebhookconfiguration hc-node-selector-webhook-test
oc delete crd nodeplacementtests.test.hypershift.io
```

# Test Plan: Holmes GPT Investigation for ARO-HCP

## Context

The Holmes investigation feature adds a `POST /admin/v1/hcp{resourceId}/investigate` endpoint to the admin API. Design and implementation details are in `docs/design-holmes-investigation.md` and `docs/implementation-plan-holmes-investigation.md`.

This test plan covers all three phases:
- **Phase 1** (dataplane) — ephemeral pods on management cluster, CSR-signed kubeconfig
- **Phase 2** (serviceplane) — persistent Holmes server on service cluster, in-cluster HTTP call
- **Phase 3** (controlplane) — persistent Holmes server on management cluster, kube API proxy

It follows the testing patterns already established in the ARO-HCP codebase and references what ARO classic tests in [PR #4754](https://github.com/Azure/ARO-RP/pull/4754).

## Testing Layers

### 1. Unit Tests — Config (`admin/server/holmes/config_test.go`)

Following ARO classic's `pkg/util/holmes/config_test.go` pattern:

**`TestNewHolmesConfigFromEnv`** (table-driven, `t.Setenv` for env vars):
- Valid config with all required env vars → success
- Missing `HOLMES_AZURE_OPENAI_API_BASE` → error
- Custom values override defaults (`HOLMES_IMAGE`, `HOLMES_MODEL`, `HOLMES_DEFAULT_TIMEOUT`, `HOLMES_MAX_CONCURRENT`, `HOLMES_AZURE_OPENAI_API_VERSION`, `HOLMES_SERVICE_CLUSTER_ENDPOINT`)
- Invalid integer for `HOLMES_DEFAULT_TIMEOUT` → falls back to default
- Invalid model name (special chars) → validation error
- Zero timeout → validation error
- Zero max concurrent → validation error

**`TestNewHolmesConfig`** (table-driven, mocked Key Vault via mock `KeyVaultSecretClient` interface):
- Reads API base from Key Vault → success
- API base not found in Key Vault → error
- Key Vault returns nil value → error

**`TestHolmesConfigValidate`**:
- All fields valid → no error
- Empty API base → error
- Model with injection chars → error

> **Note**: No API key tests — authentication is via Workload Identity (no keys stored anywhere).

### 2. Unit Tests — Shared CSR Packages

**`internal/certs/generator_test.go`** (migrated from `tooling/hcpctl/pkg/breakglass/certs/generator_test.go`):
- Existing tests: deterministic key gen, CSR gen, PEM encoding, subject building
- Uses fixture-based golden file comparison via `testutil.CompareWithFixture()`

**`internal/certs/diagnostics_test.go`** (new):
- `TestBuildDiagnosticsSubject`: verify CN=`system:aro-diagnostics`, Org=`["system:aro-diagnostics"]`

**`internal/csrminting/minting_test.go`** (migrated from `tooling/hcpctl/pkg/breakglass/minting/`):
- Existing tests for `CreateCSR`, `CreateCSRApproval`, `WaitForCSRApproval`, `WaitForCertificate`, `CleanupCSR`
- Uses fake Kubernetes clients (`k8s.io/client-go/kubernetes/fake`)

### 3. Unit Tests — Rate Limiter (`admin/server/holmes/ratelimit_test.go`)

- Acquire within limit → returns true
- Acquire at capacity → returns false
- Release then acquire → returns true
- Concurrent acquire/release (goroutines with `-race` flag)
- Acquire returns false exactly at max, not before

### 4. Unit Tests — Handler (`admin/server/handlers/hcp/investigate_test.go`)

Following ARO classic's `admin_openshiftcluster_investigate_test.go` and ARO-HCP's `serialconsole_test.go` patterns:

**Table-driven test cases** (using `httptest.NewRecorder`, `httptest.NewRequest`, mock dependencies):

| Test Case | Body | Expected Status | Expected Error |
|-----------|------|----------------|----------------|
| Empty body | `""` | 400 | "request body could not be parsed" |
| Empty question | `{"question":""}` | 400 | "question parameter is required" |
| Question with control chars | `{"question":"what\nis wrong?"}` | 400 | "must not contain control characters" |
| Question too long (>1000) | `{"question":"aaa..."}` | 400 | "must not exceed 1000 characters" |
| Invalid scope | `{"question":"ok","scope":"invalid"}` | 400 | "invalid scope" |
| Holmes not configured | valid body | 500 | "investigation is not configured" |
| Cluster not found | valid body, bad resourceID | 404 | "not found" |
| No ClusterServiceID | valid body | 500 | "no ClusterServiceID" |
| Rate limit exceeded | valid body | 429 | "too many requests" |
| Serviceplane scope | `{"scope":"serviceplane"}` | 200 | Proxied response from persistent Holmes service |
| Controlplane scope | `{"scope":"controlplane"}` | 501 (Phase 3: 200) | "not implemented" (Phase 3: proxied response via mgmt cluster kube API proxy) |
| Valid dataplane request | `{"question":"why pods crash?"}` | 200 | streamed text |

**Mocking approach:**
- `databasetesting.MockResourcesDBClient` for database
- `gomock` for `ocm.ClusterServiceClientSpec` (mock `GetClusterHypershiftDetails`, `GetClusterProvisionShard`)
- Custom `mockFPACredentialRetriever` struct (same pattern as `serialconsole_test.go`)
- Mock `PodManager` interface for the pod lifecycle
- Mock `KubeconfigBuilder` interface for kubeconfig generation
- Context injection: `utils.ContextWithResourceID()`, `utils.ContextWithLogger()`

### 5. Unit Tests — Pod Manager (`admin/server/holmes/pod_test.go`)

Using `k8s.io/client-go/kubernetes/fake`:

- Creates Secret with correct data keys (`config` — kubeconfig YAML only, no Azure credentials)
- Creates ConfigMap with Holmes toolset config
- Creates Pod with correct:
  - Command: `python holmes_cli.py`
  - Args: `ask <question> -n --model=<model> --config=/etc/holmes/config.yaml`
  - Env vars as plain values (`AZURE_AD_TOKEN_AUTH=true`, `AZURE_API_BASE`, `AZURE_API_VERSION`, `KUBECONFIG=/etc/kubeconfig/config`)
  - Pod label: `azure.workload.identity/use: "true"` (triggers WI mutating webhook)
  - Volume mounts (kubeconfig, holmes-config, tmp, holmes-cache)
  - Security context (UID 1000, non-root, no privilege escalation, capabilities dropped)
  - Resource limits (1 CPU / 2Gi memory)
  - `RestartPolicy: Never`, `ActiveDeadlineSeconds`
  - `AutomountServiceAccountToken: true` (required for Workload Identity — projected SA token)
  - `ServiceAccountName: "holmesgpt"`
- Cleanup deletes Pod, Secret, ConfigMap even on error
- Pod failure returns error with reason and message

> **Note**: No Azure credentials in Secret — authentication is via Workload Identity. The pod's projected SA token is exchanged for an Entra ID access token by the Azure SDK.

### 6. Unit Tests — Kubeconfig Builder (`admin/server/holmes/kubeconfig_test.go`)

Using mock `CSRManager` interface:

- Successful kubeconfig generation → valid YAML with embedded cert/key/CA
- CSR creation failure → error propagated
- CSR approval failure → error propagated, CSR cleaned up
- Certificate wait timeout → error, cleanup called
- KAS CA secret not found → error
- Cleanup function deletes CSR and CSR approval

### 7. Integration Tests (`test-integration/admin/artifacts/AdminCRUD/HCP/investigate*/`)

Following existing integration test pattern (ordered artifact directories):

```
investigate/
  00-load-initial-cosmos-state/     # Pre-populate HCP in database
  01-loadClusterService-initial-state/ # Mock CS responses (hypershift details, provision shard)
  02-httpPost-investigate/           # POST /investigate with question
  03-httpPost-investigate-bad-request/ # POST with invalid body → 400
  04-httpPost-investigate-not-found/  # POST for nonexistent HCP → 404
```

These test the full HTTP pipeline including middleware, routing, request parsing, and error formatting.

### 8. E2E Tests (`test/e2e/admin_investigate.go`)

Following existing `admin_api.go` pattern (Ginkgo + Gomega):

```go
It("should investigate a cluster via Holmes", labels.High, labels.DevelopmentOnly, func(ctx context.Context) {
    // 1. Create or use existing HCP cluster
    // 2. POST /admin/v1/hcp{resourceId}/investigate
    //    Body: {"question": "what is the cluster health status?"}
    // 3. Verify response:
    //    - HTTP 200
    //    - Content-Type: text/plain
    //    - Body contains investigation output (non-empty)
    //    - Investigation completes within timeout
    // 4. Verify cleanup:
    //    - No leftover pods in HCP namespace on mgmt cluster
    //    - No leftover secrets/configmaps
    //    - No leftover CSRs
})
```

**Prerequisites for E2E:**
- Azure OpenAI resource provisioned in dev environment (`disableLocalAuth=true`, DataZoneStandard SKU)
- Key Vault secret populated (`holmes-azure-api-base`)
- `holmesgpt` MSI with federated credential on mgmt + svc clusters
- `Cognitive Services OpenAI User` role assigned to holmesgpt MSIs
- HolmesGPT image available (dev: `quay.io/haoran/holmesgpt:latest`, prod: ACR)
- `system:aro-diagnostics` RBAC deployed to test HCP cluster (via ACM policy)
- Test HCP cluster in Running state

**Labels:** `labels.DevelopmentOnly` (requires dev environment with AOAI), `labels.High` priority

### 9. Manual Test Script (`hack/test-holmes-investigate.sh`)

Following ARO classic's `hack/test-holmes-investigate.sh` pattern:

```bash
#!/bin/bash
# Usage: ./hack/test-holmes-investigate.sh <resource-id> [question]
# Requires: admin API running locally or port-forwarded
# Tests investigate endpoint with curl and streams response

RESOURCE_ID="${1:?usage: $0 <resource-id> [question]}"
QUESTION="${2:-what is the cluster health status?}"
ADMIN_API_URL="${ADMIN_API_URL:-https://localhost:8443}"

curl -sk -X POST \
  "${ADMIN_API_URL}/admin/v1/hcp${RESOURCE_ID}/investigate" \
  -H "Content-Type: application/json" \
  -d "{\"question\": \"${QUESTION}\"}" \
  --no-buffer
```

## Test Coverage Targets

| Package | Target | Key Metrics |
|---------|--------|-------------|
| `admin/server/holmes/config.go` | 90%+ | All env vars, validation paths, KV loading |
| `admin/server/holmes/ratelimit.go` | 95%+ | All acquire/release paths |
| `admin/server/holmes/pod.go` | 80%+ | Pod spec, cleanup, error paths |
| `admin/server/holmes/kubeconfig.go` | 80%+ | CSR flow, cleanup, error paths |
| `admin/server/handlers/hcp/investigate.go` | 85%+ | All HTTP status codes, validation, routing |
| `internal/certs/` | 90%+ | Migrated tests + new diagnostics |
| `internal/csrminting/` | 80%+ | Migrated tests |

Run with: `go test -race -cover ./admin/server/holmes/... ./admin/server/handlers/hcp/... ./internal/certs/... ./internal/csrminting/...`

## Phase 2: Serviceplane Tests

### Unit Tests — Holmes Client (`admin/server/holmes/client_test.go`)

- `AskHolmes()` with mock HTTP server returning 200 → response streamed to writer
- `AskHolmes()` with mock HTTP server returning 500 → error with status code and body
- `AskHolmes()` with unreachable endpoint → connection error
- URL construction: trailing slash handling, `/api/chat` appended correctly

### E2E Tests — Serviceplane

```bash
SCOPE=serviceplane demo/test-holmes-investigate.sh "check pod health in aro-hcp namespace"
```

Verify:
- HTTP 200 response
- Response contains analysis of service cluster pods
- Holmes correctly identifies pods in `aro-hcp` and `clusters-service` namespaces
- No HCP database lookup performed (serviceplane doesn't need it)

### Phase 2 Verification Checklist ✅

- [x] Holmes server pod running on service cluster (`kubectl get pods -n aro-holmesgpt`)
- [x] Holmes server responding to health checks (`/healthz`, `/readyz`)
- [x] Serviceplane investigation returns HTTP 200 with meaningful analysis
- [x] Holmes can access pods/logs across service cluster namespaces
- [x] WI auth to Azure OpenAI working (no API key errors)
- [x] ConfigMap mounts correct (main config at `/.holmes/config.yaml`, toolsets at `/etc/holmes/toolsets.yaml`)

---

## Phase 3: Controlplane Tests

### Unit Tests — Controlplane Handler

- `handleControlplane()` constructs correct kube API proxy URL
- `handleControlplane()` uses mgmt REST config TLS transport
- `handleControlplane()` routes to correct management cluster (via provision shard)
- Error when mgmt cluster unreachable → 500 with clear message
- Error when Holmes service not deployed on mgmt cluster → 502/503

### E2E Tests — Controlplane

```bash
SCOPE=controlplane demo/test-holmes-investigate.sh "check HCP control plane health for haowang-e2e"
```

Verify:
- HTTP 200 response
- Response contains HyperShift-specific analysis (HostedCluster, HostedControlPlane, NodePool status)
- Holmes can access HCP namespace resources on management cluster
- Correct management cluster routing (HCP → provision shard → mgmt cluster)

### Phase 3 Verification Checklist ✅

- [x] Holmes server pod running on management cluster (`kubectl get pods -n aro-holmesgpt` on mgmt cluster)
- [x] Holmes server responding to health checks (`/healthz`, `/readyz`)
- [x] Controlplane investigation returns HTTP 200 with meaningful analysis
- [x] Holmes can access HyperShift CRDs (hostedclusters, hostedcontrolplanes, nodepools)
- [x] Holmes can access HCP namespace pods/logs
- [x] Routing works for correct management cluster (via provision shard)
- [x] WI auth to Azure OpenAI working on mgmt cluster
- [x] No SA in deploy-mgmt chart (uses SA from deploy/ chart to avoid Helm ownership conflict)

---

## Verification Checklist — Phase 1 ✅

- [x] All unit tests pass with `-race` flag
- [x] E2E test passes in personal dev environment
- [x] Manual test script works against local RP
- [x] Holmes pod cleaned up after investigation completes
- [x] Holmes pod cleaned up after investigation fails/times out
- [x] Rate limiter correctly rejects at capacity
- [x] Graceful degradation when Holmes not configured (endpoint not registered)
- [x] No secrets leaked in pod logs or HTTP response

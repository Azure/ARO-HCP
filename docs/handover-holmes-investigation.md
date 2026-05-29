# Handover Notes: Holmes GPT Investigation for ARO-HCP

## What This Is

An AI-powered cluster diagnostics feature for ARO-HCP, allowing SREs to ask natural language questions about cluster health via an admin API endpoint. Modeled after [ARO classic's implementation (PR #4754)](https://github.com/Azure/ARO-RP/pull/4754), adapted for the three-cluster HCP architecture.

## Documents to Read First

All in `docs/`:

1. **`design-holmes-investigation.md`** — Architecture, API design, security model, phasing. This is the source of truth for what to build.
2. **`implementation-plan-holmes-investigation.md`** — Step-by-step file lists, code patterns, Go struct definitions. This tells you how to build it.
3. **`test-plan-holmes-investigation.md`** — Unit/integration/E2E test cases with coverage targets.

## Key Design Decisions Already Made

| Decision | Rationale |
|----------|-----------|
| Dataplane ephemeral pod runs on **management cluster**, not service cluster | kube-apiserver is co-located there; solves private cluster connectivity; eliminates need for separate "private clusters" phase |
| **Authentication via native Workload Identity** (not API keys) | HolmesGPT natively supports `DefaultAzureCredential` via `AZURE_AD_TOKEN_AUTH=true`. Both HCP clusters have full WI support. The pod authenticates to Azure OpenAI directly — no API keys stored anywhere. ARO classic (PR #4852) uses a workaround (pre-acquired token as API key); HCP does it properly. |
| Holmes pod/server runs in **`aro-holmesgpt` namespace** on all clusters | WI federated credentials need a fixed `system:serviceaccount:{ns}:{name}` subject. HCP namespaces are dynamic per-cluster. Consistent convention across mgmt and svc clusters. |
| AOAI resource has **`disableLocalAuth=true`** + **custom subdomain** + **DataZoneStandard SKU** | Required for Entra ID token auth. No API keys can be created or used. DataZoneStandard for data residency compliance. |
| Config has **two loading modes**: Key Vault (prod) vs env vars (dev) | Only non-secret config (API base URL, model, timeout). No secrets needed — auth is via WI. |
| `HOLMES_IMAGE` is a **local dev override only** | Production image comes from ACR domain + config.yaml digest: `acrDomain + "/holmesgpt:latest"` |
| Model default: `azure/gpt-5.2`, API version: `2025-04-01-preview` | Matches ARO classic; user confirmed |
| **RBAC deployed via ACM policy** | `aro-diagnostics-rbac.policy.yaml` targets all HCP clusters via `all-hosted-clusters` Placement. Auto-remediated with `remediationAction: enforce`. |
| CSR subject CN: `system:sre-break-glass:aro-diagnostics` | HyperShift CSR approver enforces `system:sre-break-glass:` prefix for the breakglass signer. Original `system:aro-diagnostics` was rejected. |
| KAS root CA: `root-ca` secret, `ca.crt` key | Not `kas-server-crt`/`tls.crt` (which is the server cert, not the CA). |
| **HolmesGPT server mode needs `~/.holmes/config.yaml`** | Without this file, server only loads `ROBUSTA_AI` model. Must contain `model`, `api_base`, `api_version`, `custom_toolsets`. |
| No `HostAliases` needed | Unlike ARO classic, HCP kube-apiserver is reachable from the management cluster without DNS workarounds |
| Three phases, no Phase 4 | Phase 1: dataplane, Phase 2: serviceplane, Phase 3: controlplane. Private clusters work automatically via Phase 1. |
| **Phase 3 reuses `AskHolmes()` via kube API proxy** | No separate `proxy.go` needed. Same HTTP client function works for both in-cluster (svc) and cross-cluster (mgmt via proxy) calls. |

## What's Done

- Design document — reviewed and approved
- Implementation plan — reviewed and approved
- Test plan — reviewed and approved
- **Phase 1 (dataplane) — COMPLETE** — all 10 steps implemented, E2E tested in personal dev environment
- **Phase 2 (serviceplane) — COMPLETE** — persistent Holmes server on service cluster, E2E tested
- **Phase 3 (controlplane) — COMPLETE** — persistent Holmes server on management cluster via kube API proxy, E2E tested
- **Config materialization** — schema, rendered configs, Helm fixture tests pass
- **RBAC deployment** — ACM policy created (`acm/deploy/helm/policies/templates/aro-diagnostics-rbac.policy.yaml`)
- **Workload Identity auth** — fully implemented (no API keys anywhere in the system)
- **Infrastructure** — Azure OpenAI (DataZoneStandard SKU, `disableLocalAuth=true`), holmesgpt MSI + federated credentials on both mgmt and svc clusters, AOAI role assignments

## All Phases Complete

All three investigation scopes are implemented and E2E tested:

| Scope | How it works |
|-------|-------------|
| **dataplane** | Ephemeral pod on mgmt cluster with CSR-signed kubeconfig |
| **serviceplane** | In-cluster HTTP call to persistent Holmes on svc cluster |
| **controlplane** | FPA + kube API proxy to persistent Holmes on mgmt cluster |

**Key Phase 3 implementation note**: The `deploy-mgmt/` chart does not include a `serviceaccount.yaml` — the SA is already owned by the `holmesgpt` Helm release (Phase 1's `deploy/` chart). A duplicate SA causes Helm ownership conflicts. The `deploy-mgmt/` chart uses release name `holmesgpt-server` to avoid conflicts with the existing `holmesgpt` release.

## Codebase Patterns to Follow

| Pattern | Example File | What to Copy |
|---------|-------------|-------------|
| Handler registration | `admin/server/server/admin.go:93-120` | `middlewareMux.Handle(middleware.V1HCPResourcePattern(...))` |
| Handler struct | `admin/server/handlers/hcp/serialconsole.go` | Constructor with deps, `ServeHTTP(w, r) error` signature |
| Breakglass handler | `admin/server/handlers/hcp/breakglass/create.go` | How to get HCP from DB, call Cluster Service, get provision shard |
| CSR generation | `tooling/hcpctl/pkg/breakglass/certs/generator.go` | Functions to extract to `internal/certs/` |
| CSR lifecycle | `tooling/hcpctl/pkg/breakglass/minting/minting.go` | `CSRManager` interface to extract to `internal/csrminting/` |
| Mgmt cluster access | `sessiongate/pkg/mc/kubeconfig.go` | `GetAKSRESTConfig()` for FPA-based mgmt cluster connection |
| Options pattern | `admin/server/cmd/server/options.go` | `RawOptions` → `Validate()` → `Complete()` chain |
| Config from KV | ARO classic `pkg/util/holmes/config.go` | Two loading modes with `azsecrets.Client` for KV |
| Pod spec | ARO classic `pkg/hive/investigate.go` | Exact pod spec to replicate (command, volumes, security context) |
| Handler tests | `admin/server/handlers/hcp/serialconsole_test.go` | Table-driven, `httptest`, mock FPA retriever |
| Handler tests (classic) | ARO classic `pkg/frontend/admin_openshiftcluster_investigate_test.go` | Test cases for all error paths |
| DB mocking | `internal/databasetesting/mock_resources_db_client.go` | In-memory mock for `ResourcesDBClient` |
| E2E tests | `test/e2e/admin_api.go` | Ginkgo/Gomega with `framework.NewTestContext()` |
| Integration tests | `test-integration/admin/artifacts/AdminCRUD/HCP/breakglass/` | Ordered artifact directories |

## Azure OpenAI Authentication

Holmes uses **Entra ID token auth via Workload Identity** — no API keys are stored or used.

**Infrastructure required per management cluster:**

| Resource | Purpose |
|----------|---------|
| Managed identity `holmesgpt` | WI identity for Holmes pods |
| Federated credential | Links `aro-holmesgpt/holmesgpt` SA to the managed identity |
| `Cognitive Services OpenAI User` role | On the AOAI resource, assigned to the holmesgpt identity |
| `aro-holmesgpt` namespace + `holmesgpt` ServiceAccount | On the management cluster |

**AOAI resource requirements:**
- Custom subdomain (required for Entra ID token auth)
- `disableLocalAuth=true` (no API keys)

**How it works at runtime:**
1. Admin API creates an ephemeral Holmes pod in `aro-holmesgpt` namespace on mgmt cluster
2. Pod has `ServiceAccountName: "holmesgpt"` + label `azure.workload.identity/use: "true"`
3. AKS WI webhook injects federated token into the pod
4. HolmesGPT uses `AZURE_AD_TOKEN_AUTH=true` → `DefaultAzureCredential` → WI token → Azure OpenAI

**Config still needed** (non-secret, via config.yaml or env var):
- `HOLMES_AZURE_OPENAI_API_BASE` — AOAI endpoint URL (e.g. `https://arohcp-dev-aoai.openai.azure.com`)

## Config Changes — DONE

**`config/config.yaml`** — added under `adminApi:`:
```yaml
holmes:
  enabled: false
  aoaiName: ""
  azureOpenAIAPIBase: ""
  model: "azure/gpt-5.2"
  azureOpenAIAPIVersion: "2025-04-01-preview"
  defaultTimeout: 600
  maxConcurrent: 20
```

**Dev cloud override** — under `clouds.dev.defaults.adminApi`:
```yaml
holmes:
  enabled: true
  aoaiName: "arohcp-dev-aoai"
  azureOpenAIAPIBase: "https://arohcp-dev-aoai.openai.azure.com"
```

Config schema updated in `config/config.schema.json`.
Rendered configs materialized via `cd config && make materialize`.

## Resolved Questions

1. **RBAC deployment**: ✅ Deployed via ACM policy (`aro-diagnostics-rbac.policy.yaml`) targeting all HCP clusters via the `all-hosted-clusters` Placement. Auto-remediated with `remediationAction: enforce`.
2. **Holmes namespace convention**: ✅ `aro-holmesgpt` as hardcoded convention on all clusters (mgmt + svc).
3. **Management cluster Holmes RBAC (ephemeral pods)**: ✅ Not needed — ephemeral pods use CSR-signed kubeconfigs for HCP access and WI for AOAI access.
4. **Management cluster Holmes RBAC (persistent server, Phase 3)**: ✅ Cluster-wide ClusterRole with HyperShift CRD access. Per-namespace scoping rejected (operational complexity for minimal benefit).
5. **Controlplane routing (which mgmt cluster?)**: ✅ One Holmes per management cluster. Admin API uses `csClient.GetClusterProvisionShard()` (same pattern as dataplane).
6. **Streaming from persistent Holmes**: ✅ `/api/chat` returns complete JSON response. `AskHolmes()` streams with chunked flushing. 600s timeout sufficient.
7. **Azure OpenAI v1 API**: Deferred — using `2025-04-01-preview` for now, matches ARO classic.

## Production Deployment

After code is merged to main:
1. Bump commit SHA in `sdp-pipelines/hcp/Revision.mk`
2. Add MSFT-specific overrides in `sdp-pipelines/hcp/config.clouds-overlay.yaml` (AOAI endpoints, image digests, KV secret names for int/stg/prod)
3. Run ADO pipelines → EV2 rollout
4. STG/PROD require approval from `TM-AzureRedHatOpenshift-HCP-Leads`

See `docs/ev2-deployment.md` and [aka.ms/arohcp-pipelines](https://aka.ms/arohcp-pipelines).

## Risks to Watch

| Risk | Status | Mitigation |
|------|--------|-----------|
| HolmesGPT image may not be in ACR yet | **Active** | Test image at `quay.io/haoran/holmesgpt:latest` (must be `--platform linux/amd64`). Production needs image pushed to ACR. |
| Management cluster may not have ACR pull access for Holmes image | **Active** | Verify ImagePullSecrets are configured |
| CSR signing timeout in prod (>60s) | **Low risk** — E2E tested, signing takes ~10-30s | Make timeout configurable; implement retry |
| Holmes toolset config may need HCP-specific adjustments | **Resolved** — same config works for all scopes | All unwanted toolsets explicitly disabled (including `crossplane/core`) |
| WI federated credential not provisioned | **Resolved** — provisioned in bicep | Holmes config gracefully degrades — endpoint not registered if AOAI unreachable |
| AOAI resource lacks custom subdomain | **Resolved** — enforced in bicep | `disableLocalAuth=true` + custom subdomain required for Entra ID token auth |
| HolmesGPT server requires `~/.holmes/config.yaml` | **Resolved** — mounted via ConfigMap | Without this file, server only loads ROBUSTA_AI model. Discovered during Phase 2 E2E. |

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
| Holmes pod runs in **`aro-holmesgpt` namespace** on mgmt cluster | WI federated credentials need a fixed `system:serviceaccount:{ns}:{name}` subject. HCP namespaces are dynamic per-cluster. The pod accesses the HCP cluster via CSR-signed kubeconfig, so its namespace doesn't affect cluster access. |
| AOAI resource has **`disableLocalAuth=true`** + **custom subdomain** | Required for Entra ID token auth. No API keys can be created or used. |
| Config has **two loading modes**: Key Vault (prod) vs env vars (dev) | Only non-secret config (API base URL, model, timeout). No secrets needed — auth is via WI. |
| `HOLMES_IMAGE` is a **local dev override only** | Production image comes from ACR domain + config.yaml digest: `acrDomain + "/holmesgpt:latest"` |
| Model default: `azure/gpt-5.2`, API version: `2025-04-01-preview` | Matches ARO classic; user confirmed |
| **RBAC deployed via ACM policy** | `aro-diagnostics-rbac.policy.yaml` targets all HCP clusters via `all-hosted-clusters` Placement. Auto-remediated. |
| No `HostAliases` needed | Unlike ARO classic, HCP kube-apiserver is reachable from the management cluster without DNS workarounds |
| Three phases, no Phase 4 | Phase 1: dataplane, Phase 2: serviceplane, Phase 3: controlplane. Private clusters work automatically via Phase 1. |

## What's Done

- Design document — reviewed and approved
- Implementation plan — reviewed and approved
- Test plan — reviewed and approved
- **Phase 1 code implemented** (all 10 steps) — builds and tests pass
- **Config materialization** — schema, rendered configs, Helm fixture tests pass
- **RBAC deployment** — ACM policy created (`acm/deploy/helm/policies/templates/aro-diagnostics-rbac.policy.yaml`)
- **Auth migration to Workload Identity** — in progress (replacing API key auth)

## What Needs to Be Built (Phase 1)

Phase 1 (dataplane) has 10 implementation steps. Follow the implementation plan exactly. Here's the dependency order:

```
Step 1: Extract shared CSR packages (internal/certs/, internal/csrminting/)
    ↓
Step 2: Holmes configuration (admin/server/holmes/config.go)
Step 3: Holmes toolset config (admin/server/holmes/staticresources/holmes-config.yaml)
    ↓
Step 4: Kubeconfig builder (admin/server/holmes/kubeconfig.go) — depends on Step 1
Step 5: Ephemeral pod manager (admin/server/holmes/pod.go) — depends on Steps 2, 3
Step 6: Rate limiter (admin/server/holmes/ratelimit.go)
    ↓
Step 7: Investigation handler (admin/server/handlers/hcp/investigate.go) — depends on Steps 4, 5, 6
    ↓
Step 8: Wire into admin API (admin/server/server/admin.go, cmd/server/options.go)
Step 9: RBAC for customer clusters (staticresources/)
Step 10: Helm chart & config.yaml updates
```

Steps 1-3 can be done in parallel. Steps 4-6 can be done in parallel. Steps 9-10 can be done in parallel with Step 8.

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

## Config Changes Needed

**`config/config.yaml`** — add under `adminApi:`:
```yaml
holmes:
  enabled: false
  model: "azure/gpt-5.2"
  azureOpenAIAPIVersion: "2025-04-01-preview"
  defaultTimeout: 600
  maxConcurrent: 20
```

**Dev cloud override** — under `clouds.dev.defaults.adminApi`:
```yaml
holmes:
  enabled: true
```

After config changes: `cd config && make materialize` and commit rendered output.
Also update `config/config.schema.json` with the new Holmes schema.

## Resolved Questions

1. **RBAC deployment**: ✅ Deployed via ACM policy (`aro-diagnostics-rbac.policy.yaml`) targeting all HCP clusters via the `all-hosted-clusters` Placement. Auto-remediated with `remediationAction: enforce`.
2. **Holmes namespace convention on mgmt cluster**: ✅ `aro-holmesgpt` as hardcoded convention.
3. **Management cluster Holmes RBAC**: ✅ Not needed for Holmes pods — they use CSR-signed kubeconfigs for HCP access and WI for AOAI access.
4. **Azure OpenAI v1 API**: Deferred — using `2025-04-01-preview` for now, matches ARO classic.

## Production Deployment

After code is merged to main:
1. Bump commit SHA in `sdp-pipelines/hcp/Revision.mk`
2. Add MSFT-specific overrides in `sdp-pipelines/hcp/config.clouds-overlay.yaml` (AOAI endpoints, image digests, KV secret names for int/stg/prod)
3. Run ADO pipelines → EV2 rollout
4. STG/PROD require approval from `TM-AzureRedHatOpenshift-HCP-Leads`

See `docs/ev2-deployment.md` and [aka.ms/arohcp-pipelines](https://aka.ms/arohcp-pipelines).

## Risks to Watch

| Risk | Mitigation |
|------|-----------|
| HolmesGPT image may not be in ACR yet | Check with user; may need to push manually for dev. Test image available at `quay.io/haoran/holmesgpt:latest`. |
| Management cluster may not have ACR pull access for Holmes image | Verify ImagePullSecrets are configured |
| CSR signing timeout in prod (>60s) | Make timeout configurable; implement retry |
| Holmes toolset config may need HCP-specific adjustments | Start with ARO classic's config, iterate |
| WI federated credential not provisioned | Holmes config gracefully degrades — endpoint not registered if AOAI unreachable |
| AOAI resource lacks custom subdomain | Entra ID token auth requires custom subdomain; `deploy-holmes-aoai.sh` enforces this |

# Design: Holmes GPT Investigation for ARO-HCP

## 1. Problem Statement

SREs troubleshooting ARO-HCP clusters currently rely on manual investigation across three distinct cluster layers: the service cluster (hosting the RP and Cluster Service), the management cluster (hosting HyperShift control planes), and the customer cluster (the HCP data plane). Each layer requires different access credentials and tooling.

ARO classic recently added Holmes GPT-powered investigation ([Azure/ARO-RP#4754](https://github.com/Azure/ARO-RP/pull/4754)), which allows SREs to ask natural language diagnostic questions and receive automated root cause analysis. ARO-HCP needs equivalent functionality, adapted for its three-cluster architecture.

## 2. Goals

- Enable SREs to investigate cluster issues across all three layers via a single admin API endpoint
- Provide automated, AI-powered root cause analysis using [HolmesGPT](https://holmesgpt.dev/)
- Maintain read-only access with dedicated RBAC (no risk of modifying cluster state)
- Minimize per-request latency where possible

## 3. Non-Goals

- Automated remediation (Holmes provides diagnosis only)
- Persistent investigation history or dashboards

## 4. Background

### 4.1 ARO-HCP Cluster Topology

```
┌─────────────────────────────────────────────────────────────────┐
│ Service Cluster (1 per region)                                  │
│  Admin API, Frontend (RP), Backend, Cluster Service,            │
│  Maestro Server, Sessiongate Controller + Proxy                 │
└───────────────────────────┬─────────────────────────────────────┘
                            │ AKS API + kube API (FPA creds)
                            │ Maestro (MQTT via Event Grid)
┌───────────────────────────▼─────────────────────────────────────┐
│ Management Cluster (N per region)                               │
│  HyperShift Operator, Maestro Agent                             │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ HCP Namespace (per cluster): HostedControlPlane,        │    │
│  │ etcd, kube-apiserver, openshift-apiserver,kas-server-crt│    │
│  └─────────────────────────────────────────────────────────┘    │
└───────────────────────────┬─────────────────────────────────────┘
                            │ kube-apiserver endpoint (public or private)
┌───────────────────────────▼─────────────────────────────────────┐
│ Customer Cluster (HCP data plane, in customer subscription)     │
│  Worker nodes, customer workloads, OpenShift operators          │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 Network Connectivity

| From → To | Reachable? | Mechanism |
|-----------|:---:|-----------|
| Service Cluster → Management Cluster | Yes | FPA credentials → AKS `ListClusterUserCredentials` API → Azure token auth |
| Management Cluster → Customer Cluster API | Yes (always) | kube-apiserver pods run directly on the management cluster |
| Service Cluster → Customer Cluster API | Yes (public only) | Direct HTTPS to HCP kube-apiserver public endpoint |

### 4.3 HolmesGPT Deployment Modes

HolmesGPT supports two deployment modes relevant to our design:

1. **Helm chart (persistent server)**: Deploys a long-running service exposing `POST /api/chat`. Uses in-cluster ServiceAccount for authentication. The Helm chart auto-creates read-only RBAC.

2. **CLI (ephemeral)**: Runs a one-off investigation using a provided kubeconfig. Exits after producing output.

### 4.4 Existing Credential Mechanisms

- **Breakglass sessions** (Sessiongate): CSR-based client certificates signed by HyperShift's CA. Used for SRE access to HCP clusters. CSR signer: `hypershift.openshift.io/{hcpNamespace}.sre-break-glass`.
- **FPA + AKS API**: First-Party Application credentials used to obtain management cluster kubeconfig (pattern: `sessiongate/pkg/mc/kubeconfig.go`).
- **hcpctl breakglass**: CLI tool that generates CSR-signed kubeconfigs with embedded cert/key (pattern: `tooling/hcpctl/pkg/breakglass/`).

### 4.5 How ARO Classic Does It

ARO classic runs an ephemeral Holmes pod on the Hive AKS cluster per investigation. It generates a 1-hour kubeconfig signed by the cluster CA (from the persisted graph) with a dedicated `system:aro-diagnostics` identity. The RP streams pod logs back to the caller.

**Key difference**: ARO classic has direct access to the cluster CA and can sign certificates locally. ARO-HCP uses HyperShift's CSR mechanism — certificates are signed by the HyperShift CSR approver on the management cluster.

## 5. Proposed Design

### 5.1 Hybrid Deployment Model

Deploy Holmes using two models based on the investigation scope:

| Scope | Deployment | Where Pod Runs | Rationale |
|-------|-----------|---------------|-----------|
| **Service Cluster** (`serviceplane`) | Persistent (Helm) | Service cluster | Stable environment; one instance serves all requests; in-cluster SA is sufficient |
| **Management Cluster** (`controlplane`) | Persistent (Helm) | Management cluster | Shared infrastructure; one instance per management cluster serves all HCPs on it; ServiceAccount with HCP namespace read-access |
| **Customer Cluster** (`dataplane`) | Ephemeral (per-request) | Management cluster | kube-apiserver pods run on the management cluster, so the Holmes pod is co-located — works for both public and private HCPs. Requires dedicated CSR-signed credential per cluster. |

**Key design decision**: The dataplane ephemeral pod runs on the **management cluster**, not the service cluster. This is because the customer cluster's kube-apiserver runs as a pod within the HCP namespace on the management cluster. Running the Holmes pod on the same cluster means:
- No external network hop to reach the kube-apiserver
- Private HCPs are accessible (no separate network path needed)
- The admin API creates/monitors the pod remotely via FPA + AKS API REST config

### 5.2 API Design

**Endpoint**: `POST /admin/v1/hcp{resourceId}/investigate`

Registered in the admin API service (service cluster), following existing patterns from breakglass and serial console handlers.

**Request**:
```json
{
  "question": "why are pods in namespace X crashlooping?",
  "scope": "dataplane"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `question` | string | Yes | Natural language diagnostic question. Max 1000 chars, no control characters. |
| `scope` | string | No | `"dataplane"` (default), `"controlplane"`, `"serviceplane"` |

**Response**: `Content-Type: text/plain`, streamed. The response body contains Holmes's investigation output.

**Error Responses**:
- `400 Bad Request` — invalid question or scope
- `404 Not Found` — HCP not found
- `429 Too Many Requests` — concurrent investigation limit reached
- `500 Internal Server Error` — Holmes not configured or infrastructure failure
- `501 Not Implemented` — scope not yet supported

### 5.3 Scope: Service Cluster Investigation (`serviceplane`)

```
Admin API ──POST──► Holmes Service (same cluster)
                    http://holmesgpt-svc.aro-holmesgpt.svc.cluster.local:80/api/chat
                    {"ask": "<question>", "model": "<model>"}
```

**Holmes deployment**: Custom Helm chart (`holmesgpt/deploy-svc/`) deployed to the `aro-holmesgpt` namespace on the service cluster. Runs as a persistent server (`python server.py`) on port 5050 behind a ClusterIP Service (port 80 → 5050).

**Server configuration**: HolmesGPT server mode requires a main config file at `~/.holmes/config.yaml` containing `model`, `api_base`, `api_version`, and `custom_toolsets` reference. Without this file, the server only loads the `ROBUSTA_AI` model (which fails without Robusta credentials). The config is mounted from a ConfigMap with two files:
- `/.holmes/config.yaml` — main config (model, API base, API version, custom_toolsets path)
- `/etc/holmes/toolsets.yaml` — toolset allow/deny config (same as dataplane)

**Access**: In-cluster ServiceAccount (`holmesgpt`) with Workload Identity for Azure OpenAI auth. Helm-managed read-only ClusterRole covering core resources, apps, batch, networking, and metrics.

**What it investigates**: RP frontend/backend pods, Cluster Service, Maestro server, sessiongate, networking between services, pod logs, events.

**Admin API call**: Simple HTTP POST to the in-cluster Service DNS name (`http://holmesgpt-svc.aro-holmesgpt.svc.cluster.local:80/api/chat`). Response streamed back to caller via chunked transfer. No HCP database lookup needed — serviceplane scope investigates the service cluster itself.

### 5.4 Scope: Management Cluster Investigation (`controlplane`)

```
Admin API ──kube API proxy──► Holmes Service (management cluster)
            via FPA + AKS REST config
            /api/v1/namespaces/aro-holmesgpt/services/holmesgpt-svc:80/proxy/api/chat
```

**Holmes deployment**: Custom Helm chart (`holmesgpt/deploy-mgmt/`) deployed to the `aro-holmesgpt` namespace on each management cluster. Same persistent server pattern as the service cluster deployment (Phase 2). Reuses the same ConfigMap structure: main config at `/.holmes/config.yaml` + toolsets at `/etc/holmes/toolsets.yaml`.

**Access**: ServiceAccount (`holmesgpt`) with Workload Identity for Azure OpenAI auth. Read-only access to all namespaces including HCP namespaces. The same instance serves diagnostics for all HCPs on that management cluster.

**RBAC**: ClusterRole granting `get/list/watch` on:
- Core resources (pods, pods/log, events, configmaps, services, nodes, PVCs, PVs, namespaces) across all namespaces including HCP namespaces
- HyperShift CRDs (`hostedclusters`, `hostedcontrolplanes`, `nodepools`, `machinesets`, `machines`) in `hypershift.openshift.io` and `cluster.x-k8s.io` API groups
- CertificateSigningRequests (`certificates.k8s.io`)
- Standard workload resources (deployments, statefulsets, daemonsets, replicasets, jobs, cronjobs)
- Metrics (`metrics.k8s.io`)

**Admin API call**: The admin API already has management cluster access via FPA + AKS API (`mc.GetAKSRESTConfig()`). It reaches Holmes via the Kubernetes API server proxy, reusing the same `AskHolmes()` HTTP client function from Phase 2 with a proxied URL constructed from the mgmt REST config:
```
POST /api/v1/namespaces/aro-holmesgpt/services/holmesgpt-svc:80/proxy/api/chat
```
This requires no additional ingress — the kube-apiserver proxies the request to the Holmes service. The admin API constructs the proxy URL and uses a custom `http.Client` with the mgmt REST config's TLS transport.

**Per-request routing**: The admin API determines which management cluster hosts the HCP by calling `csClient.GetClusterProvisionShard()`, which returns the management cluster ARM resource ID. This is the same routing logic already used by the dataplane handler.

**Key difference from serviceplane**: The serviceplane Holmes is co-located with the admin API (same cluster), so a simple in-cluster DNS URL suffices. The controlplane Holmes requires the FPA + AKS REST config to reach the mgmt cluster, plus kube API proxy to reach the Holmes service within it.

### 5.5 Scope: Customer Cluster Investigation (`dataplane`)

```
Admin API ──creates──► Ephemeral Holmes Pod (management cluster)
  (via FPA + AKS REST config)    with CSR-signed kubeconfig
                                 investigates HCP kube-apiserver
                                 (co-located on same cluster)
```

**Holmes deployment**: Ephemeral pod created on the **management cluster** per investigation request. The admin API interacts with the management cluster remotely via FPA + AKS API REST config.

**Why management cluster**: The customer cluster's kube-apiserver runs as a pod in the HCP namespace on the management cluster. Running the Holmes pod on the same cluster means:
- Direct network access to the kube-apiserver (no external network hop)
- Works for both public and private HCPs
- No DNS resolution issues

**Kubeconfig generation** (CSR mechanism, reusing patterns from `hcpctl` breakglass):
1. Get management cluster REST config (FPA + AKS API)
2. Generate RSA private key (2048-bit)
3. Build CSR with: CN=`system:sre-break-glass:aro-diagnostics`, Org=`["system:aro-diagnostics"]`
4. Submit CSR to management cluster with signer `hypershift.openshift.io/{hcpNamespace}.sre-break-glass`
5. Create `CertificateSigningRequestApproval` CR in HCP namespace → HyperShift approver signs it
6. Wait for signed certificate (watch-based, ~10-30s)
7. Read KAS root CA cert from `root-ca` secret (`ca.crt` key) in HCP namespace on management cluster
8. Build kubeconfig with embedded client cert/key + CA, server = HCP kube-apiserver endpoint
9. Defer cleanup: delete CSR and CSR approval resources

**Pod namespace**: The ephemeral pod runs in the `aro-holmesgpt` namespace on the management cluster, **not** in the HCP namespace. This is because:
- Workload Identity federated credentials require a fixed namespace/ServiceAccount subject (`aro-holmesgpt/holmesgpt`). HCP namespaces are dynamic and per-cluster, which would require a separate federated credential per HCP.
- The pod accesses the HCP cluster via a CSR-signed kubeconfig, so the pod's own namespace does not affect its ability to reach the customer cluster's kube-apiserver.

**Ephemeral pod spec**:
- Holmes container image with `python holmes_cli.py ask` command
- Args: `ask <question> -n --model=<model> --config=/etc/holmes/main-config.yaml`
- `ServiceAccountName: "holmesgpt"` (Workload Identity projected token)
- `AutomountServiceAccountToken: true` (required for Workload Identity)
- Pod label: `azure.workload.identity/use: "true"` (triggers WI mutating webhook)
- Env vars: `AZURE_AD_TOKEN_AUTH=true`, `AZURE_API_BASE`, `AZURE_API_VERSION`, `KUBECONFIG=/etc/kubeconfig/config`, `HOLMES_CONFIG_PATH=/etc/holmes/main-config.yaml`
- Security context: non-root (UID 1000), all capabilities dropped, no privilege escalation
- `ActiveDeadlineSeconds` for timeout enforcement (default: 600 seconds)
- `RestartPolicy: Never`
- Resource limits: 1 CPU / 2Gi memory; requests: 100m CPU / 256Mi memory

**Persistent ConfigMap** (`holmesgpt-dataplane-config`): Deployed once to the management cluster by the `deploy-mgmt` Helm chart. Contains:
- `main-config.yaml` — HolmesGPT main config (model, api_base, api_version, custom_toolsets, custom_skill_paths)
- `toolsets.yaml` — toolset allow/deny config (bash allowlist, enabled toolsets)
- `skill-dataplane.md` — HCP creation troubleshooting skill for the data plane

The ephemeral pod mounts this persistent ConfigMap instead of creating a per-investigation ConfigMap. Only the per-investigation **Secret** (kubeconfig) is created and cleaned up per request.

**Holmes toolset config** (`holmes-config.yaml`):
Defines which toolsets Holmes can use:
- **Enabled**: `kubectl-run`, `kubernetes/core`, `kubernetes/logs`, `kubernetes/live-metrics`, `kubernetes/kube-prometheus-stack`, `bash` (with restricted commands), `skills`
- **Bash allowlist**: `kubectl get`, `kubectl describe`, `kubectl logs`, `kubectl top`, `kubectl cluster-info`, `egrep`
- **Bash denylist**: `kubectl delete`, `kubectl apply`, `kubectl create`, `kubectl exec`, `kubectl patch`, `kubectl scale`, `kubectl drain`, `rm`, `oc`
- **Disabled**: All other toolsets (openshift, robusta, internet, argocd, helm, grafana, datadog, etc.)

**Response handling**: For persistent Holmes (serviceplane/controlplane), the admin API buffers the `/api/chat` JSON response, extracts the `analysis` field, and returns only the analysis text. For ephemeral pods (dataplane), the admin API streams raw CLI output via pod logs.

**Ephemeral Secret**: Contains only the `config` key (kubeconfig YAML with embedded client cert/key + CA). No Azure OpenAI credentials — authentication is via Workload Identity.

**Cleanup**: Deferred deletion of pod, Secret, CSR, and CSR approval on the management cluster.

**Error handling**: All infrastructure errors (FPA auth, REST config, kubeconfig build, Holmes service errors) return `arm.CloudError` with descriptive messages including the root cause, not generic "Internal server error".

**RBAC on customer clusters**: A `system:aro-diagnostics` ClusterRole + ClusterRoleBinding must be deployed to every HCP cluster, providing read-only access to diagnostic resources (pods, logs, nodes, events, deployments, etc.). This mirrors the ARO classic `system:aro-diagnostics` role.

### 5.6 Rate Limiting

Per-admin-API-instance atomic counter (same as ARO classic). Default: 20 concurrent investigations. Reject with HTTP 429 if at capacity. This applies across all scopes.

### 5.7 Configuration

Holmes configuration uses a layered approach following the project's existing patterns:

**Defaults (built into Go code):**

| Config | Default | Notes |
|--------|---------|-------|
| Model | `azure/gpt-5.2` | Matches ARO classic |
| Azure OpenAI API version | `2025-04-01-preview` | Latest preview; evaluate v1 API (`/openai/v1/`) when available |
| Timeout | `600` (seconds) | 10 minutes, matches ARO classic |
| Max concurrent | `20` | Per admin API instance |
| Image | `acrDomain + "/holmesgpt:latest"` | Constructed from ACR domain; overridable via `HOLMES_IMAGE` for local dev |

**Environment variables (overridable):**

| Env Var | Required | Default | Source |
|---------|----------|---------|--------|
| `HOLMES_IMAGE` | No | `{acrDomain}/holmesgpt:latest` | Local dev override only; production uses ACR domain from config |
| `HOLMES_AZURE_OPENAI_API_BASE` | Yes | — | Key Vault via SecretProviderClass (prod) or env var (dev) |
| `HOLMES_AZURE_OPENAI_API_VERSION` | No | `2025-04-01-preview` | config.yaml / env var |
| `HOLMES_MODEL` | No | `azure/gpt-5.2` | config.yaml / env var |
| `HOLMES_DEFAULT_TIMEOUT` | No | `600` | config.yaml / env var |
| `HOLMES_MAX_CONCURRENT` | No | `20` | config.yaml / env var |
| `HOLMES_SERVICE_CLUSTER_ENDPOINT` | Phase 2 | — | config.yaml (in-cluster DNS) |

**Key Vault secrets** (stored in the service Key Vault per region):

| Secret Name | Purpose |
|-------------|---------|
| `holmes-azure-api-base` | Azure OpenAI endpoint URL (e.g. `https://arohcp-aoai.openai.azure.com`) |

**Authentication**: Holmes authenticates to Azure OpenAI via Entra ID using Workload Identity. The `holmesgpt` managed identity on each management cluster is assigned the `Cognitive Services OpenAI User` role on the AOAI resource. The ephemeral pod sets `AZURE_AD_TOKEN_AUTH=true`, which causes the Azure SDK (`DefaultAzureCredential` from the `azure-identity` Python SDK) to acquire tokens using the projected ServiceAccount token. No API keys are stored or used anywhere in the system.

**Config loading modes** (matching ARO classic pattern):
- **Production**: `NewHolmesConfig(ctx, acrDomain, serviceKeyvault)` — reads API base from Key Vault via Azure SDK. No API key needed.
- **Local dev** (`RP_MODE=development`): `NewHolmesConfigFromEnv(acrDomain)` — reads API base, model, timeout, concurrency, and image from environment variables.

Holmes is optional — if the Key Vault secrets are not provisioned, the endpoint is not registered (graceful degradation).

### 5.8 Infrastructure Requirements

The following Azure infrastructure must be provisioned per environment:

| Resource | Scope | How Provisioned |
|----------|-------|-----------------|
| Azure OpenAI resource (DataZoneStandard SKU) | Per region | Bicep template in `dev-infrastructure/`. Custom subdomain, `disableLocalAuth=true`. |
| Azure OpenAI model deployment (`gpt-5.2`) | Per AOAI resource | Bicep template |
| Managed identity (`holmesgpt`) | Per management + service cluster | Created in `mgmt-cluster.bicep` and `svc-cluster.bicep` |
| Federated credential | Links `aro-holmesgpt/holmesgpt` SA to managed identity | Created in `aks-cluster-base.bicep` (automatic) |
| `Cognitive Services OpenAI User` role | On AOAI resource | Assigned to `holmesgpt` managed identity on both clusters |
| FPA `Azure Kubernetes Service Cluster User Role` | On management cluster AKS | `svc-mgmt-aks-permissions.bicep` — required for `listClusterUserCredential` API |
| FPA `Azure Kubernetes Service RBAC Cluster Admin` | On management cluster AKS | `svc-mgmt-aks-permissions.bicep` — required for pod/secret/CSR operations |
| `aro-holmesgpt` namespace + `holmesgpt` ServiceAccount | Per management + service cluster | Created by Holmes pipeline |
| HolmesGPT container image | Per environment | `quay.io/haoran/holmesgpt:latest` (dev) or ACR (prod) |
| `system:aro-diagnostics` RBAC on HCP clusters | Per HCP cluster | ACM policy (`aro-diagnostics-rbac.policy.yaml`) via `all-hosted-clusters` Placement |

**Pipeline deployment**:
- Management cluster: `holmesgpt/pipeline.yaml` deploys SA + persistent server + dataplane ConfigMap
- Service cluster: `holmesgpt/svc-pipeline.yaml` deploys persistent server (registered in `topology.yaml` as `HolmesGPT.Svc`)
- `azureOpenAIAPIBase` is derived from `aoaiName` in `config.yaml`: `https://arohcp-{env}-aoai-{regionShort}.openai.azure.com`

**Production deployment**: Changes deploy via EV2 through ADO pipelines in `sdp-pipelines`. MSFT-specific overrides (image digests, AOAI endpoints, KV names) go in `sdp-pipelines/hcp/config.clouds-overlay.yaml`. STG/PROD require approval from `TM-AzureRedHatOpenshift-HCP-Leads`.

## 6. Custom Skills for HCP Creation Troubleshooting

HolmesGPT supports custom skills — step-by-step troubleshooting guides that Holmes follows when investigating issues. Each skill is a `SKILL.md` file with YAML frontmatter and markdown body, loaded via `custom_skill_paths` in the main config.

### Scope-Specific Skills

Each scope's Holmes instance has only its own skill to avoid confusion:

| Scope | Skill | What it checks |
|-------|-------|---------------|
| `serviceplane` | `hcp-creation-serviceplane` | Frontend/backend pods + logs, Cluster Service state, Maestro server, operation status |
| `controlplane` | `hcp-creation-controlplane` | HostedCluster/HostedControlPlane conditions, control plane pods (etcd, kube-apiserver), NodePool, CAPI machines, HyperShift operator |
| `dataplane` | `hcp-creation-dataplane` | Node status, ClusterOperators, ClusterVersion, pod health, storage, networking |

### Skill Deployment

- **Service cluster**: `holmesgpt-skills` ConfigMap with serviceplane skill, mounted at `/etc/holmes/skills/`
- **Management cluster (persistent server)**: `holmesgpt-skills` ConfigMap with controlplane skill
- **Management cluster (ephemeral pods)**: `holmesgpt-dataplane-config` ConfigMap with dataplane skill

### Skill Discovery

Skills are configured via `custom_skill_paths: ["/etc/holmes/skills/"]` in the HolmesGPT main config. Holmes scans the directory for `SKILL.md` files and matches them to user questions based on the skill's `description` field.

## 7. Shared Code: CSR and Certificate Utilities

The CSR generation and certificate utility code currently lives in `tooling/hcpctl/pkg/breakglass/certs/` and `minting/`. To enable reuse by the admin API without importing from `tooling/`, these packages will be extracted to shared `internal/` packages:

| Current Location | New Location | Contents |
|------------------|-------------|----------|
| `tooling/hcpctl/pkg/breakglass/certs/generator.go` | `internal/certs/generator.go` | `GeneratePrivateKey()`, `GenerateCSR()`, `EncodePrivateKey()`, `BuildSubject()` |
| `tooling/hcpctl/pkg/breakglass/minting/minting.go` | `internal/csrminting/minting.go` | `CSRManager` interface, `CreateCSR()`, `CreateCSRApproval()`, `WaitForCertificate()`, `CleanupCSR()` |

New addition: `BuildDiagnosticsSubject()` returning CN=`system:sre-break-glass:aro-diagnostics`, Org=`["system:aro-diagnostics"]`.

`tooling/hcpctl/` imports will be updated to point to the new shared packages.

## 8. Security Considerations

| Concern | Mitigation |
|---------|-----------|
| **Customer cluster access** | Dedicated `system:aro-diagnostics` identity with read-only RBAC. No `system:masters` group. Short-lived CSR-signed certificates (~15 min). |
| **Azure OpenAI authentication** | Entra ID tokens via Workload Identity. No API keys stored anywhere. `disableLocalAuth=true` enforced on AOAI resource. Token scope limited to `cognitiveservices.azure.com`. |
| **Holmes pod privileges** | Non-root (UID 1000), all capabilities dropped, resource-limited. `AutomountServiceAccountToken: true` is required for Workload Identity, but the projected token scope is limited to Azure Cognitive Services. |
| **Bash command restriction** | Holmes toolset config enforces allowlist (read-only kubectl) and denylist (delete/apply/exec/rm). |
| **Rate limiting** | Per-instance atomic counter prevents resource exhaustion. |
| **Audit trail** | Admin API sits behind Geneva Actions with MISE auth. Client principal tracked in audit logs. |
| **Management cluster access** | FPA credentials scoped to specific tenant. AKS API requires proper Azure RBAC. |
| **Persistent Holmes services** | Helm-managed ServiceAccount with read-only ClusterRole. No write permissions. |

## 9. Phasing

| Phase | Scope | Status | Description |
|-------|-------|--------|-------------|
| **Phase 1** | `dataplane` | **Complete** | Admin API endpoint + ephemeral pods on management cluster + CSR kubeconfig + `system:aro-diagnostics` RBAC via ACM policy. E2E tested. |
| **Phase 2** | `serviceplane` | **Complete** | Persistent Holmes server on service cluster (`holmesgpt/deploy-svc/`). Admin API calls in-cluster Holmes via `AskHolmes()`. E2E tested. |
| **Phase 3** | `controlplane` | **Complete** | Persistent Holmes server on management cluster (`holmesgpt/deploy-mgmt/`). Admin API reaches Holmes via FPA + AKS REST config + kube API proxy. Reuses `AskHolmesWithClient()` with proxied URL. |

Note: Private HCP clusters are supported out of the box by running the ephemeral pod on the management cluster (where the kube-apiserver is co-located). No separate phase is needed.

## 10. Alternatives Considered

### 9.1 All-Ephemeral Pods
Run ephemeral Holmes pods for every scope, not just dataplane. Rejected because:
- Pod startup latency (image pull + CSR signing) adds 30-60s per request
- Service/management clusters are stable — persistent services are simpler and faster

### 9.2 All-Persistent Services
Deploy persistent Holmes on all three cluster types including customer clusters. Rejected because:
- Cannot deploy persistent workloads on customer infrastructure
- Each HCP needs unique credentials — would require per-cluster Holmes deployments

### 9.3 Single Merged Kubeconfig
Give one Holmes pod a merged kubeconfig with contexts for all three clusters. Rejected because:
- Holmes operates on one kubeconfig context at a time
- Long-lived credentials for customer clusters are less secure
- Single pod can't have both in-cluster SA access and external kubeconfigs cleanly

### 9.4 Breakglass Session Proxy for Kubeconfig
Use the sessiongate proxy mechanism instead of CSR. Rejected because:
- Breakglass kubeconfigs use exec-based auth (kubelogin) — not available in Holmes container
- Would require modifying the Holmes image or building custom kubeconfigs from session credentials
- CSR mechanism is more direct and produces embedded-cert kubeconfigs

### 9.5 Ephemeral Pod on Service Cluster (for dataplane)
Run the dataplane investigation pod on the service cluster instead of the management cluster. Rejected because:
- Service cluster can only reach public HCP API servers — private clusters would require a separate Phase 4 for network path establishment
- Running on the management cluster provides co-located access to the kube-apiserver (which runs as a pod in the HCP namespace), eliminating all connectivity concerns

## 11. Open Questions

1. **Holmes on management cluster routing**: How does the admin API know the Holmes service namespace on the management cluster? Should it be a convention (e.g., `aro-holmes`) or configurable per management cluster?
   - **Resolved**: `aro-holmesgpt` is used as a fixed convention on all clusters (management and service). This namespace hosts the ephemeral investigation pods (dataplane), the persistent Holmes server (serviceplane/controlplane), and the `holmesgpt` ServiceAccount bound to Workload Identity.

2. **HCP RBAC deployment**: How should the `system:aro-diagnostics` ClusterRole/ClusterRoleBinding be deployed to HCP clusters? Options: HyperShift operator, Cluster Service provisioning step, or a dedicated operator.
   - **Resolved**: Deployed via ACM policy (`aro-diagnostics-rbac.policy.yaml`) targeting all HCP clusters via the `all-hosted-clusters` Placement. This ensures the RBAC is consistently applied to every HCP cluster without requiring changes to the HyperShift operator or Cluster Service.

3. **Management cluster Holmes RBAC**: The persistent Holmes on the management cluster needs access to all HCP namespaces. A ClusterRole works, but should access be scoped per-namespace (more secure) or cluster-wide (simpler)?
   - **Resolved**: Cluster-wide ClusterRole. Per-namespace scoping would require dynamic RoleBinding creation for each new HCP, adding operational complexity for minimal security benefit — Holmes is already restricted to read-only operations via RBAC and bash allowlist/denylist. The ClusterRole includes HyperShift CRDs (`hostedclusters`, `hostedcontrolplanes`, `nodepools`) in addition to standard Kubernetes resources.

4. **Streaming from persistent Holmes**: The `/api/chat` endpoint may not support streaming yet (docs say "Streaming APIs are forthcoming"). If responses take minutes, the admin API HTTP connection must stay open. Timeout and long-polling considerations?
   - **Resolved (Phase 2 E2E testing)**: The `/api/chat` endpoint returns a complete JSON response (not streamed). The `AskHolmes()` client streams the response body to the HTTP writer with chunked flushing, which works well for responses that take 30-120 seconds. The admin API's HTTP timeout (600s default) is sufficient. No special long-polling needed.

5. **Which management cluster?**: When `scope=controlplane`, the admin API needs to route to the correct management cluster's Holmes. The provision shard gives the management cluster ARM resource ID. Is one Holmes per management cluster sufficient, or do we need per-HCP routing?
   - **Resolved**: One Holmes per management cluster is sufficient. The admin API uses `csClient.GetClusterProvisionShard()` to get the management cluster ARM resource ID (same pattern as the dataplane handler), then reaches the Holmes service via kube API proxy on that specific management cluster.

6. **Azure OpenAI API version strategy**: Azure now offers a v1 API (`/openai/v1/`) that removes the need for `api-version` parameter. Should we adopt v1 or stay with the versioned preview API (`2025-04-01-preview`)?
   - Deferred — using `2025-04-01-preview` for now, matches ARO classic.

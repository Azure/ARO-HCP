# Implementation Plan: Holmes GPT Investigation for ARO-HCP

> Companion to [design-holmes-investigation.md](./design-holmes-investigation.md). This document covers the step-by-step implementation details for each phase. Each phase is independently shippable.

## Phase 1: Dataplane Investigation (Ephemeral Pods on Management Cluster)

Phase 1 delivers the highest-value scope — investigating customer cluster issues. The ephemeral Holmes pod runs on the **management cluster** (where the HCP kube-apiserver is co-located), and the admin API orchestrates it remotely via FPA + AKS API.

### Step 1: Extract Shared CSR Packages

**Goal**: Move CSR certificate and minting code from `tooling/hcpctl/` to `internal/` so both `hcpctl` and `admin` can import it.

**Files to create:**

| File | Contents |
|------|----------|
| `internal/certs/generator.go` | Copy of `tooling/hcpctl/pkg/breakglass/certs/generator.go` — functions: `GeneratePrivateKey()`, `GenerateCSR()`, `EncodePrivateKey()`, `BuildSubject()` |
| `internal/certs/generator_test.go` | Copy of corresponding tests |
| `internal/certs/diagnostics.go` | New function: `BuildDiagnosticsSubject() pkix.Name` returning CN=`system:aro-diagnostics`, Org=`["system:aro-diagnostics"]` |
| `internal/certs/diagnostics_test.go` | Tests for `BuildDiagnosticsSubject()` |
| `internal/csrminting/minting.go` | Copy of `tooling/hcpctl/pkg/breakglass/minting/minting.go` — `CSRManager` interface and `DefaultManager` implementation |
| `internal/csrminting/minting_test.go` | Copy of corresponding tests |

**Files to modify:**

| File | Change |
|------|--------|
| `tooling/hcpctl/pkg/breakglass/certs/generator.go` | Replace with thin wrapper that re-exports from `internal/certs` |
| `tooling/hcpctl/pkg/breakglass/minting/minting.go` | Replace with thin wrapper that re-exports from `internal/csrminting` |
| `tooling/hcpctl/pkg/breakglass/execute.go` | Update imports to use `internal/certs` and `internal/csrminting` |
| `go.work` | Ensure `internal` module includes new packages (already in workspace) |

**Key consideration**: The minting package currently imports `tooling/hcpctl/pkg/common` (for `Scheme()`) and `tooling/hcpctl/pkg/utils` (for `SanitizeUsername()`). The minting code should be refactored to accept a `client.Client` and `kubernetes.Interface` directly (dependency injection) instead of constructing them internally from `rest.Config`. This keeps the shared package self-contained and lets callers provide cluster-specific clients.

**Refactored `CSRManager` constructor:**
```go
// In internal/csrminting/minting.go
func NewDefaultManager(kubeClient kubernetes.Interface, ctrlClient client.Client) *DefaultManager {
    return &DefaultManager{kubeClient: kubeClient, ctrlClient: ctrlClient}
}
```

---

### Step 2: Holmes Configuration

**Goal**: Define the configuration struct and loading logic for Holmes, with two modes matching [ARO classic's pattern](https://github.com/Azure/ARO-RP/blob/main/pkg/util/holmes/config.go).

**Files to create:**

| File | Contents |
|------|----------|
| `admin/server/holmes/config.go` | `HolmesConfig` struct, `NewHolmesConfig()` (prod), `NewHolmesConfigFromEnv()` (dev), validation |
| `admin/server/holmes/config_test.go` | Tests for config loading, validation, defaults |

**`HolmesConfig` struct:**
```go
type HolmesConfig struct {
    Image                       string // default: acrDomain + "/holmesgpt:latest"; override via HOLMES_IMAGE
    AzureOpenAIAPIBase          string // from Key Vault (prod) or HOLMES_AZURE_OPENAI_API_BASE (dev)
    AzureOpenAIAPIVersion       string // default: "2025-04-01-preview"
    Model                       string // default: "azure/gpt-5.2"
    DefaultTimeout              int    // seconds, default: 600
    MaxConcurrentInvestigations int    // default: 20
}
```

> **Note**: Authentication is handled by the Holmes pod via Workload Identity (not the admin API). The admin API does not need Azure OpenAI credentials — it only needs the endpoint URL and model configuration to pass as plain env vars to the ephemeral pod.

**Two loading modes** (no credential parameter needed):

```go
// Production: config from env vars and Key Vault (non-secret values only)
func NewHolmesConfig(ctx context.Context, acrDomain string, kvClient azsecrets.Client) (*HolmesConfig, error)

// Local development: all config from environment variables
func NewHolmesConfigFromEnv(acrDomain string) (*HolmesConfig, error)
```

**Validation** (`Validate()` checks: API base URL required, model name regex, positive timeout/concurrency):
```go
var modelPattern = regexp.MustCompile(`^[a-zA-Z0-9/.:_-]+$`)

func (c *HolmesConfig) Validate() error
```

**`HOLMES_IMAGE` behavior**: Defaults to `version.HolmesImage(acrDomain)` (i.e. `{acrDomain}/holmesgpt:latest`). Can be overridden via `HOLMES_IMAGE` env var for local testing. Not stored in config.yaml.

**Files to create (image version constant):**

| File | Contents |
|------|----------|
| Add to existing `pkg/util/version/` or similar | `func HolmesImage(acrDomain string) string` returning `acrDomain + "/holmesgpt:latest"` |

---

### Step 3: Holmes Toolset Config

**Goal**: Embed the Holmes toolset configuration that controls which tools Holmes can use and which bash commands are allowed/denied.

**Files to create:**

| File | Contents |
|------|----------|
| `admin/server/holmes/staticresources/holmes-config.yaml` | Toolset config (adapted from ARO classic) |

**Content** (embedded via `//go:embed`):
```yaml
toolsets:
  kubectl-run:
    enabled: true
  kubernetes/kube-prometheus-stack:
    enabled: true
  kubernetes/logs:
    enabled: true
  kubernetes/core:
    enabled: true
  kubernetes/live-metrics:
    enabled: true
  bash:
    enabled: true
    config:
      builtin_allowlist: extended
      allow:
        - "kubectl get"
        - "kubectl describe"
        - "kubectl logs"
        - "kubectl top"
        - "kubectl cluster-info"
        - "kubectl explain"
        - "kubectl api-resources"
        - "kubectl version"
        - "egrep"
      deny:
        - "kubectl delete"
        - "kubectl apply"
        - "kubectl create"
        - "kubectl edit"
        - "kubectl exec"
        - "kubectl patch"
        - "kubectl scale"
        - "kubectl drain"
        - "kubectl cordon"
        - "kubectl taint"
        - "kubectl debug"
        - "rm"
        - "oc"
  # All other toolsets disabled
  core_investigation:
    enabled: false
  openshift/core:
    enabled: false
  # ... (see ARO classic for full list)
```

This is security-critical — it prevents Holmes from running destructive commands.

---

### Step 4: Kubeconfig Builder

**Goal**: Build a self-contained kubeconfig for diagnosing customer clusters using the CSR mechanism. All operations happen on the management cluster.

**Files to create:**

| File | Contents |
|------|----------|
| `admin/server/holmes/kubeconfig.go` | `KubeconfigBuilder` struct, `BuildDataplaneKubeconfig()` method |
| `admin/server/holmes/kubeconfig_test.go` | Tests with mocked `CSRManager` |

**`KubeconfigBuilder` dependencies:**
- `fpa.FirstPartyApplicationTokenCredentialRetriever` — to get management cluster credentials
- `csrminting.CSRManager` — to create/approve CSRs (injected as interface)

**`BuildDataplaneKubeconfig()` flow:**
1. Get management cluster REST config via `mc.GetAKSRESTConfig(ctx, mgmtClusterResourceID, tokenCredential)`
2. Create Kubernetes clients from REST config
3. Create `CSRManager` from those clients
4. Generate RSA private key (2048-bit) via `certs.GeneratePrivateKey(2048)`
5. Build subject via `certs.BuildDiagnosticsSubject()`
6. Generate CSR PEM via `certs.GenerateCSR(privateKey, subject)`
7. Submit CSR via `csrManager.CreateCSR(ctx, csrPEM, clusterID, "aro-diagnostics", hcpNamespace)`
8. Create approval via `csrManager.CreateCSRApproval(ctx, csrName, hcpNamespace, clusterID, "aro-diagnostics")`
9. Wait for certificate via `csrManager.WaitForCertificate(ctx, csrName, 60*time.Second)`
10. Read KAS root CA cert from secret `root-ca` (`ca.crt` key) in HCP namespace on management cluster
11. Get HCP kube-apiserver endpoint from Cluster Service `GetClusterHypershiftDetails()`
12. Assemble kubeconfig YAML with embedded client cert/key + CA + server URL
13. Return cleanup function that deletes CSR and CSR approval

**Returns**: `[]byte` (kubeconfig YAML), cleanup function `func()`, and error.

---

### Step 5: Ephemeral Pod Manager

**Goal**: Create, monitor, stream logs from, and clean up ephemeral Holmes pods on the **management cluster**.

**Files to create:**

| File | Contents |
|------|----------|
| `admin/server/holmes/pod.go` | `PodManager` struct with `RunInvestigation()` method |
| `admin/server/holmes/pod_test.go` | Tests with fake Kubernetes client |

**Key difference from ARO classic**: The admin API creates pods **remotely** on the management cluster via FPA + AKS API REST config, not locally on its own cluster.

**Pod namespace**: The Holmes pod runs in the `aro-holmesgpt` namespace (not the HCP namespace). Workload Identity requires a fixed namespace for federated credential binding — the managed identity's federated credential is bound to the `holmesgpt` ServiceAccount in the `aro-holmesgpt` namespace.

**`RunInvestigation()` signature** accepts separate `podNamespace` (always `aro-holmesgpt`) and `hcpNamespace` (the target HCP namespace for the kubeconfig) parameters:
```go
func (pm *PodManager) RunInvestigation(ctx context.Context, w http.ResponseWriter,
    mgmtKubeClient kubernetes.Interface, podNamespace string, hcpNamespace string,
    question string, kubeconfigYAML []byte) error
```

**`PodManager` responsibilities:**

1. **Create investigation Secret** (on mgmt cluster, in `podNamespace`): Contains kubeconfig only (`"config": kubeconfigYAML`)
2. **Create investigation ConfigMap** (on mgmt cluster, in `podNamespace`): Holmes toolset config (`holmes-config.yaml`)
3. **Create investigation Pod** (on mgmt cluster, in `podNamespace`): Holmes container with `ask` CLI command, using Workload Identity for Azure OpenAI auth
4. **Wait for pod running**: Poll until pod phase is Running/Succeeded/Failed
5. **Stream pod logs**: `Follow: true` log stream from mgmt cluster, written to `http.ResponseWriter` with chunked flushing
6. **Cleanup**: Deferred deletion of pod, Secret, ConfigMap on the management cluster

**Pod spec** (uses Workload Identity for Azure OpenAI auth):
```go
pod := &corev1.Pod{
    ObjectMeta: metav1.ObjectMeta{
        Name:      "holmes-investigate-" + id,
        Namespace: podNamespace, // always "aro-holmesgpt"
        Labels: map[string]string{
            "azure.workload.identity/use": "true",
        },
    },
    Spec: corev1.PodSpec{
        ServiceAccountName:           "holmesgpt",
        AutomountServiceAccountToken: ptr(true), // WI needs the projected service account token
        ActiveDeadlineSeconds:        ptr(int64(holmesConfig.DefaultTimeout)),
        RestartPolicy:                corev1.RestartPolicyNever,
        SecurityContext: &corev1.PodSecurityContext{
            RunAsUser:  ptr(int64(1000)),
            RunAsGroup: ptr(int64(1000)),
            FSGroup:    ptr(int64(1000)),
        },
        Containers: []corev1.Container{{
            Name:            "holmes",
            Image:           holmesConfig.Image,
            ImagePullPolicy: corev1.PullAlways,
            Command:         []string{"python", "holmes_cli.py"},
            Args:            []string{"ask", question, "-n", "--model=" + holmesConfig.Model, "--config=/etc/holmes/config.yaml"},
            Env: []corev1.EnvVar{
                {Name: "AZURE_AD_TOKEN_AUTH", Value: "true"},
                {Name: "AZURE_API_BASE", Value: holmesConfig.AzureOpenAIAPIBase},
                {Name: "AZURE_API_VERSION", Value: holmesConfig.AzureOpenAIAPIVersion},
                {Name: "KUBECONFIG", Value: "/etc/kubeconfig/config"},
            },
            VolumeMounts: []corev1.VolumeMount{
                {Name: "kubeconfig", MountPath: "/etc/kubeconfig", ReadOnly: true},
                {Name: "holmes-config", MountPath: "/etc/holmes/config.yaml", SubPath: "config.yaml", ReadOnly: true},
                {Name: "tmp", MountPath: "/tmp"},
                {Name: "holmes-cache", MountPath: "/.holmes"},
            },
            SecurityContext: &corev1.SecurityContext{
                RunAsNonRoot:             ptr(true),
                AllowPrivilegeEscalation: ptr(false),
                Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
            },
            Resources: corev1.ResourceRequirements{
                Requests: corev1.ResourceList{
                    corev1.ResourceCPU:    resource.MustParse("100m"),
                    corev1.ResourceMemory: resource.MustParse("256Mi"),
                },
                Limits: corev1.ResourceList{
                    corev1.ResourceCPU:    resource.MustParse("1"),
                    corev1.ResourceMemory: resource.MustParse("2Gi"),
                },
            },
        }},
        Volumes: []corev1.Volume{
            {Name: "kubeconfig", VolumeSource: corev1.VolumeSource{
                Secret: &corev1.SecretVolumeSource{
                    SecretName: secretName,
                    Items:      []corev1.KeyToPath{{Key: "config", Path: "config"}},
                },
            }},
            {Name: "holmes-config", VolumeSource: corev1.VolumeSource{
                ConfigMap: &corev1.ConfigMapVolumeSource{
                    LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
                },
            }},
            {Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
            {Name: "holmes-cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
        },
    },
}
```

**Investigation Secret data** (kubeconfig only — no Azure OpenAI credentials):
```go
secret := &corev1.Secret{
    ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: podNamespace},
    Data: map[string][]byte{
        "config": kubeconfigYAML,
    },
}
```

**Log streaming** (from management cluster, via remote kube client):
```go
req := mgmtKubeClient.CoreV1().Pods(podNamespace).GetLogs(podName, &corev1.PodLogOptions{Follow: true})
stream, err := req.Stream(ctx)
buf := make([]byte, 4096)
for {
    n, readErr := stream.Read(buf)
    if n > 0 {
        w.Write(buf[:n])
        if flusher, ok := w.(http.Flusher); ok {
            flusher.Flush()
        }
    }
    if readErr == io.EOF { break }
    if readErr != nil { return readErr }
}
```

---

### Step 6: Rate Limiter

**Goal**: Atomic counter to limit concurrent investigations.

**Files to create:**

| File | Contents |
|------|----------|
| `admin/server/holmes/ratelimit.go` | `ConcurrencyLimiter` struct with `Acquire()` / `Release()` methods |
| `admin/server/holmes/ratelimit_test.go` | Concurrency tests |

**Implementation**: `sync/atomic.Int32` counter. `Acquire()` increments and checks against max; returns false if over limit. `Release()` decrements. Caller pattern:
```go
if !limiter.Acquire() {
    return arm.NewCloudError(http.StatusTooManyRequests, ...)
}
defer limiter.Release()
```

---

### Step 7: Investigation Handler

**Goal**: The HTTP handler that ties everything together.

**Files to create:**

| File | Contents |
|------|----------|
| `admin/server/handlers/hcp/investigate.go` | `HCPInvestigateHandler` struct + `ServeHTTP()` |
| `admin/server/handlers/hcp/investigate_test.go` | Handler tests |

**Handler struct** (follows existing patterns from `serialconsole.go` and `breakglass/create.go`):
```go
type HCPInvestigateHandler struct {
    resourcesDBClient      database.ResourcesDBClient
    clustersServiceClient  ocm.ClusterServiceClientSpec
    fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
    holmesConfig           *holmes.HolmesConfig
    limiter                *holmes.ConcurrencyLimiter
    kubeconfigBuilder      *holmes.KubeconfigBuilder
    podManager             *holmes.PodManager
}
```

**`ServeHTTP()` flow:**
1. Parse and validate request body (`question`, `scope`)
2. Extract resource ID from context (middleware-populated)
3. Check rate limit via `limiter.Acquire()` → HTTP 429 if at capacity
4. Load HCP from database
5. Get `ClusterServiceID`, fetch HyperShift details and provision shard from Cluster Service
6. Get management cluster ARM resource ID from provision shard
7. Route by scope:
   - `"dataplane"`:
     a. Get mgmt cluster REST config via FPA + AKS API
     b. Build kubeconfig via CSR mechanism (on mgmt cluster)
     c. Create Secret + ConfigMap + Pod on mgmt cluster
     d. Stream pod logs back to HTTP response
   - `"serviceplane"` / `"controlplane"`: Return HTTP 501 (Phase 2/3)
8. Set response headers: `Content-Type: text/plain`, `Cache-Control: no-cache`

**Request validation:**
- `question`: required, max 1000 chars, no control characters (regex: `[^\x00-\x1f\x7f]+`)
- `scope`: optional, must be one of `"dataplane"`, `"controlplane"`, `"serviceplane"` (default: `"dataplane"`)

---

### Step 8: Wire Into Admin API

**File to modify:** `admin/server/server/admin.go`

**Changes:**
1. Accept `*holmes.HolmesConfig` as parameter in `NewAdminAPI()` (can be `nil` when Holmes is disabled)
2. If config is non-nil, register the investigate endpoint:
```go
if holmesConfig != nil {
    limiter := holmes.NewConcurrencyLimiter(holmesConfig.MaxConcurrentInvestigations)
    kubeconfigBuilder := holmes.NewKubeconfigBuilder(fpaCredentialRetriever)
    podManager := holmes.NewPodManager(holmesConfig)
    middlewareMux.Handle(
        middleware.V1HCPResourcePattern("POST", "/investigate"),
        hcpMiddleware.HandlerFunc(errorutils.ReportError(
            hcp.NewHCPInvestigateHandler(
                resourcesDBClient, clustersServiceClient,
                fpaCredentialRetriever, holmesConfig,
                limiter, kubeconfigBuilder, podManager,
            ).ServeHTTP,
        )),
    )
}
```

**File to modify:** `admin/server/cmd/server/options.go`

**Changes:**
- Add Holmes config loading in `Complete()`:
  - Production: `holmes.NewHolmesConfig(ctx, acrDomain, kvClient)`
  - Dev: `holmes.NewHolmesConfigFromEnv(acrDomain)`
- Holmes returns `nil` if not configured (graceful degradation — endpoint not registered)
- Pass `holmesConfig` (possibly `nil`) to `NewAdminAPI()`

---

### Step 9: RBAC for Customer Clusters

**Goal**: Deploy `system:aro-diagnostics` ClusterRole + ClusterRoleBinding to every HCP cluster.

**Files to create:**

| File | Contents |
|------|----------|
| `admin/server/holmes/staticresources/clusterrole-diagnostics.yaml` | Read-only ClusterRole |
| `admin/server/holmes/staticresources/clusterrolebinding-diagnostics.yaml` | ClusterRoleBinding |

**ClusterRole** grants `get/list/watch` on: pods, pods/log, events, nodes, services, endpoints, configmaps, PVCs, namespaces, deployments, daemonsets, statefulsets, replicasets, jobs, cronjobs, ingresses, networkpolicies, storageclasses, PVs, machines, machinesets, clusterversions, clusteroperators.

**Deployment mechanism**: Open question — HyperShift operator, Cluster Service provisioning step, or Day 2 configuration.

---

### Step 10: Admin API Helm Chart, Infrastructure & Config Updates

**Files to modify:**

| File | Change |
|------|--------|
| `admin/deploy/templates/admin.deployment.yaml` | Add Holmes env vars (conditionally, when `holmes.enabled`) |
| `config/config.yaml` | Add `adminApi.holmes` section with defaults (no API key references) |
| `config/config.schema.json` | Add Holmes schema |

> **Note**: No `holmes.secretproviderclass.yaml` is needed. The admin API does not hold Azure OpenAI secrets — authentication is handled by the Holmes pod via Workload Identity on the management cluster.

**Infrastructure changes** (Workload Identity for Holmes pods):

| File | Change |
|------|--------|
| `dev-infrastructure/templates/mgmt-cluster.bicep` | Add `holmesgpt` workload identity entry to the management cluster's identity configuration |

The following infrastructure must be provisioned:

1. **Azure OpenAI resource**: Must have a custom subdomain and `disableLocalAuth=true` (forces Entra ID auth, no API keys).
2. **Role assignment**: Grant `Cognitive Services OpenAI User` on the AOAI resource to the `holmesgpt` managed identity.
3. **Namespace + ServiceAccount**: Create the `aro-holmesgpt` namespace and `holmesgpt` ServiceAccount on each management cluster (via pipeline or admin API bootstrap). The ServiceAccount must have a federated credential binding to the managed identity.

**New config.yaml section:**
```yaml
adminApi:
  holmes:
    enabled: false
    model: "azure/gpt-5.2"
    azureOpenAIAPIBase: ""
    azureOpenAIAPIVersion: "2025-04-01-preview"
    defaultTimeout: 600
    maxConcurrent: 20
    serviceClusterEndpoint: ""
```

**Dev cloud override** (under `clouds.dev.defaults`):
```yaml
adminApi:
  holmes:
    enabled: true
```

After changes: `cd config && make materialize` and commit rendered output.

---

### Phase 1 Testing Strategy

| Test Type | Scope | Approach |
|-----------|-------|----------|
| Unit | `internal/certs/` | Existing tests from breakglass (migrated) + `BuildDiagnosticsSubject` |
| Unit | `internal/csrminting/` | Existing tests from breakglass (migrated) |
| Unit | `admin/server/holmes/config.go` | Env var parsing, validation edge cases, KV loading |
| Unit | `admin/server/holmes/ratelimit.go` | Concurrent acquire/release, boundary conditions |
| Unit | `admin/server/handlers/hcp/investigate.go` | Request validation, scope routing, error responses |
| Integration | Kubeconfig builder | Mock `CSRManager` + fake kube client, verify kubeconfig structure |
| Integration | Pod manager | Fake kube client, verify pod spec, log streaming |
| E2E | Full flow | Deploy to personal dev env, run investigation against a test HCP |
| WI verification | Workload Identity | Create a test pod in `aro-holmesgpt` namespace with WI annotations (`azure.workload.identity/use: "true"`, SA `holmesgpt`) and verify it can acquire a token for `cognitiveservices.azure.com` and call the AOAI endpoint. This validates the managed identity, federated credential, and role assignment are correctly configured before testing the full investigation flow. |

---

## Phase 2: Serviceplane Investigation (Persistent Holmes) — COMPLETE

Phase 2 adds investigation of the service cluster itself. Simpler than Phase 1 — calls an already-deployed persistent Holmes service.

### Step 1: Holmes Helm Chart for Service Cluster ✅

**Files created:**

| File | Contents |
|------|----------|
| `holmesgpt/deploy-svc/Chart.yaml` | Chart metadata (`holmesgpt-svc`) |
| `holmesgpt/deploy-svc/templates/deployment.yaml` | Holmes Deployment — persistent server (`python server.py`) on port 5050, WI auth, ConfigMap volumes |
| `holmesgpt/deploy-svc/templates/service.yaml` | ClusterIP Service (`holmesgpt-svc`) port 80 → 5050 |
| `holmesgpt/deploy-svc/templates/serviceaccount.yaml` | ServiceAccount (`holmesgpt`) with WI annotations |
| `holmesgpt/deploy-svc/templates/clusterrole.yaml` | Read-only ClusterRole for standard Kubernetes + metrics resources |
| `holmesgpt/deploy-svc/templates/clusterrolebinding.yaml` | Bind ClusterRole to ServiceAccount |
| `holmesgpt/deploy-svc/templates/configmap.yaml` | Two config files: `config.yaml` (model/api_base/api_version + custom_toolsets) and `toolsets.yaml` (toolset config) |
| `holmesgpt/svc-values.yaml` | Values with WI MSI client ID, image, AOAI config |

**Key learning — HolmesGPT server mode config**: The server needs a main config at `~/.holmes/config.yaml` with `model`, `api_base`, `api_version`, and `custom_toolsets` reference. Without this file, the server only loads the `ROBUSTA_AI` model (which fails without Robusta credentials). The ConfigMap mounts:
- `/.holmes/config.yaml` — main config for model registration
- `/etc/holmes/toolsets.yaml` — toolset allow/deny config

**No SecretProviderClass needed** — Workload Identity handles Azure OpenAI auth.

### Step 2: Service Cluster Infrastructure ✅

**Files modified:**

| File | Change |
|------|--------|
| `dev-infrastructure/templates/svc-cluster.bicep` | Added `holmesgpt` workload identity + AOAI role assignment |

### Step 3: Serviceplane Handler Logic ✅

**Files created:**

| File | Contents |
|------|----------|
| `admin/server/holmes/client.go` | `AskHolmes()` function — HTTP POST to `/api/chat`, streams response to `http.ResponseWriter` |

**Files modified:**

| File | Change |
|------|--------|
| `admin/server/handlers/hcp/investigate.go` | `serviceplane` case calls `AskHolmes()` with in-cluster DNS URL. Skips HCP database lookup — serviceplane scope investigates the service cluster itself. |
| `admin/server/holmes/config.go` | Added `ServiceClusterEndpoint` field (default: `http://holmesgpt-svc.aro-holmesgpt.svc.cluster.local:80`) |

**`serviceplane` handler flow:**
```go
if req.Scope == "serviceplane" {
    return holmes.AskHolmes(ctx, h.holmesConfig.ServiceClusterEndpoint, req.Question, h.holmesConfig.Model, writer)
}
```

---

## Phase 3: Controlplane Investigation (Management Cluster Proxy) — COMPLETE

Phase 3 adds investigation of HCP control planes on management clusters. Based on Phase 2 learnings, this is simpler than originally planned — the Helm chart is nearly identical to Phase 2, and the handler reuses `AskHolmes()` with a proxied URL.

### Step 1: Holmes Helm Chart for Management Cluster ✅

Copied `holmesgpt/deploy-svc/` to `holmesgpt/deploy-mgmt/` with extended RBAC.

**Note:** No `serviceaccount.yaml` in `deploy-mgmt/` — the ServiceAccount is already created by the existing `holmesgpt/deploy/` chart (Phase 1) and owned by the `holmesgpt` Helm release. Adding a duplicate SA causes Helm ownership conflicts.

**Files created:**

| File | Contents |
|------|----------|
| `holmesgpt/deploy-mgmt/Chart.yaml` | Chart metadata (`holmesgpt-mgmt`) |
| `holmesgpt/deploy-mgmt/templates/deployment.yaml` | Same as `deploy-svc` (persistent server on port 5050, WI, ConfigMap volumes) |
| `holmesgpt/deploy-mgmt/templates/service.yaml` | Same as `deploy-svc` (`holmesgpt-svc`, port 80 → 5050) |
| `holmesgpt/deploy-mgmt/templates/clusterrole.yaml` | **Extended** — adds HyperShift CRDs, Cluster API, and `certificates.k8s.io` |
| `holmesgpt/deploy-mgmt/templates/clusterrolebinding.yaml` | Same as `deploy-svc` |
| `holmesgpt/deploy-mgmt/templates/configmap.yaml` | Same as `deploy-svc` (main config + toolsets) |
| `holmesgpt/deploy-mgmt/holmes-config.yaml` | Same toolset config as `deploy-svc` |
| `holmesgpt/mgmt-values.yaml` | Values with mgmt cluster WI MSI client ID, image, AOAI config |

**ClusterRole additions** (beyond what `deploy-svc` has):
```yaml
# HyperShift CRDs
- apiGroups: ["hypershift.openshift.io"]
  resources:
  - hostedclusters
  - hostedcontrolplanes
  - nodepools
  verbs: ["get", "list", "watch"]
# Cluster API
- apiGroups: ["cluster.x-k8s.io"]
  resources:
  - machines
  - machinesets
  - machinedeployments
  verbs: ["get", "list", "watch"]
# CSRs (for diagnostics visibility)
- apiGroups: ["certificates.k8s.io"]
  resources:
  - certificatesigningrequests
  verbs: ["get", "list", "watch"]
```

### Step 2: Management Cluster Pipeline Integration ✅

The mgmt cluster already has a Holmes pipeline step in `holmesgpt/pipeline.yaml` (Phase 1 — deploys SA + namespace). Phase 3 adds the persistent server deployment.

**Files modified:**

| File | Change |
|------|--------|
| `holmesgpt/pipeline.yaml` | Added `deploy-server` Helm step deploying `deploy-mgmt/` chart with release name `holmesgpt-server` |

**Note**: The existing pipeline step deploys `holmesgpt/deploy/` (SA only for ephemeral pods). The new step deploys `holmesgpt/deploy-mgmt/` (persistent server). Both deploy to `aro-holmesgpt` namespace. Separate Helm release names (`holmesgpt` vs `holmesgpt-server`) avoid resource ownership conflicts.

### Step 3: Controlplane Handler Logic ✅

**No separate `proxy.go` needed** — extended `client.go` with `AskHolmesWithClient()`, `ServiceProxyURL()`, and `HTTPClientForRESTConfig()` helper functions.

**Files modified:**

| File | Change |
|------|--------|
| `admin/server/handlers/hcp/investigate.go` | Replaced `controlplane` 501 stub with `handleControlplane()` method |
| `admin/server/holmes/client.go` | Added `AskHolmesWithClient()` (accepts custom `*http.Client`), `ServiceProxyURL()`, `HTTPClientForRESTConfig()` |

**`controlplane` handler flow:**
```go
case "controlplane":
    return h.handleControlplane(writer, request, hcp, req.Question)
```

**`handleControlplane()` implementation:**
1. Get management cluster ARM resource ID via `csClient.GetClusterProvisionShard()` (same as dataplane)
2. Get FPA credential via `fpaCredentialRetriever.RetrieveCredential(tenantId)`
3. Get management cluster REST config via `mc.GetAKSRESTConfig(ctx, mgmtClusterResourceID, credential)`
4. Construct proxied Holmes URL:
   ```go
   proxyURL := fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:80/proxy",
       mgmtRESTConfig.Host, holmes.HolmesNamespace, "holmesgpt-svc")
   ```
5. Create HTTP client with mgmt REST config's TLS transport (`rest.TransportFor(mgmtRESTConfig)`)
6. Call `AskHolmes()` with the proxied URL and custom HTTP client

**Key insight from Phase 2**: The same `AskHolmes()` function works for both in-cluster (serviceplane) and cross-cluster (controlplane) calls. The only difference is the URL and HTTP transport.

**Convention**: Holmes namespace on management clusters is `aro-holmesgpt` (same as service cluster and dataplane ephemeral pods).

---

## File Summary

### New Files

| File | Phase | Status | Purpose |
|------|-------|--------|---------|
| `internal/certs/generator.go` | 1 | ✅ | Shared CSR certificate generation |
| `internal/certs/generator_test.go` | 1 | ✅ | Tests |
| `internal/certs/diagnostics.go` | 1 | ✅ | `BuildDiagnosticsSubject()` (CN=`system:sre-break-glass:aro-diagnostics`) |
| `internal/certs/diagnostics_test.go` | 1 | ✅ | Tests |
| `internal/csrminting/minting.go` | 1 | ✅ | Shared CSR lifecycle management |
| `admin/server/holmes/config.go` | 1 | ✅ | Configuration loading + validation |
| `admin/server/holmes/config_test.go` | 1 | ✅ | Tests |
| `admin/server/holmes/staticresources/holmes-config.yaml` | 1 | ✅ | Holmes toolset config (bash allowlist/denylist) |
| `admin/server/holmes/kubeconfig.go` | 1 | ✅ | Kubeconfig builder for dataplane |
| `admin/server/holmes/pod.go` | 1 | ✅ | Ephemeral pod creation + log streaming (on mgmt cluster) |
| `admin/server/holmes/ratelimit.go` | 1 | ✅ | Concurrency limiter |
| `admin/server/holmes/ratelimit_test.go` | 1 | ✅ | Tests |
| `admin/server/holmes/staticresources/clusterrole-diagnostics.yaml` | 1 | ✅ | RBAC for customer clusters |
| `admin/server/holmes/staticresources/clusterrolebinding-diagnostics.yaml` | 1 | ✅ | RBAC binding |
| `admin/server/handlers/hcp/investigate.go` | 1 | ✅ | HTTP handler |
| `acm/deploy/helm/policies/templates/aro-diagnostics-rbac.policy.yaml` | 1 | ✅ | ACM policy deploying RBAC to HCP clusters |
| `admin/server/holmes/client.go` | 2 | ✅ | `AskHolmes()` HTTP client for persistent Holmes |
| `holmesgpt/deploy-svc/` (entire chart) | 2 | ✅ | Helm chart for service cluster |
| `holmesgpt/svc-values.yaml` | 2 | ✅ | Values for service cluster deployment |
| `holmesgpt/deploy-mgmt/` (entire chart, no SA) | 3 | ✅ | Helm chart for management cluster (copy of deploy-svc with extended RBAC, no SA — owned by deploy/ chart) |
| `holmesgpt/mgmt-values.yaml` | 3 | ✅ | Values for management cluster deployment |

### Modified Files

| File | Phase | Status | Change |
|------|-------|--------|--------|
| `tooling/hcpctl/pkg/breakglass/certs/generator.go` | 1 | ✅ | Re-export from `internal/certs` |
| `tooling/hcpctl/pkg/breakglass/minting/minting.go` | 1 | ✅ | Re-export from `internal/csrminting` |
| `admin/server/server/admin.go` | 1 | ✅ | Register `/investigate` endpoint |
| `admin/server/cmd/server/options.go` | 1 | ✅ | Load Holmes config (non-secret values from KV or env) |
| `admin/deploy/templates/admin.deployment.yaml` | 1 | ✅ | Add Holmes env vars |
| `dev-infrastructure/templates/mgmt-cluster.bicep` | 1 | ✅ | Add `holmesgpt` workload identity entry + AOAI role assignment |
| `dev-infrastructure/templates/svc-cluster.bicep` | 2 | ✅ | Add `holmesgpt` workload identity + AOAI role assignment |
| `config/config.yaml` | 1 | ✅ | Add `adminApi.holmes` section (no API key references) |
| `config/config.schema.json` | 1 | ✅ | Add Holmes schema |
| `holmesgpt/pipeline.yaml` | 3 | ✅ | Added `deploy-server` step for persistent Holmes on mgmt cluster |
| `admin/server/handlers/hcp/investigate.go` | 3 | ✅ | Added `handleControlplane()` — routes via kube API proxy |
| `admin/server/holmes/client.go` | 3 | ✅ | Added `AskHolmesWithClient()`, `ServiceProxyURL()`, `HTTPClientForRESTConfig()` |

---

## Dependencies and Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| HolmesGPT `/api/chat` doesn't support streaming | Long-running requests may timeout | Buffer full response, set generous HTTP timeout, add response timeout header |
| HolmesGPT image not compatible with Azure OpenAI | Feature won't work | Test image compatibility early in Phase 1 |
| CSR signing takes >60s in production | Kubeconfig generation fails | Configurable timeout; retry with backoff |
| `system:aro-diagnostics` RBAC not deployed to HCP | Investigation returns permission errors | Validate RBAC exists before running investigation; return clear error |
| Management cluster image pull | Holmes image may not be pullable on mgmt cluster | Ensure ACR pull secret or ImagePullSecrets configured |
| Remote pod management latency | Admin API ↔ mgmt cluster API calls add latency | Acceptable — same pattern used by breakglass; pod startup dominates |

## Estimated Effort

| Phase | Estimated Effort | Dependencies |
|-------|-----------------|--------------|
| Phase 1 (dataplane) | 3-4 weeks | Azure OpenAI resource provisioned; HolmesGPT image in ACR; RBAC deployment mechanism decided |
| Phase 2 (serviceplane) | 1-2 weeks | Upstream Holmes Helm chart evaluated; service cluster pipeline access |
| Phase 3 (controlplane) | 1-2 weeks | Management cluster pipeline access; namespace convention decided |

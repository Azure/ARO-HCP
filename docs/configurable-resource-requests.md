# Configurable Resource Requests

This is an inventory of implemented resource controls, not a sizing recommendation
or a claim that every observed container can be tuned. Configuration and schema
live in [config/config.yaml](../config/config.yaml) and
[config/config.schema.json](../config/config.schema.json). The shared automation
catalog is [targets.go](../tooling/rightsize-requests/pkg/targets/targets.go).

## Configuration Versus Editability

- **Configurable** means the repository passes a setting to the workload's owning
  chart, installer, controller, or supported upstream API.
- **Editable capability** means an observed identity matches the right-sizing
  catalog. It does not prove that a baseline is configured, measurements are
  complete, the HCP is minimal, or a proposed request fits live limits.
- **Safe to edit** additionally requires the selected CLI mode's baseline,
  identity, eligibility, stale-data, limit, and decrease checks. See the
  [rightsize-requests README](../tooling/rightsize-requests/README.md).

The configurability changes preserve existing default quantities and limits.
Previously absent requirements remain absent or under upstream control; adding a
knob must not silently add requests or limits. The tables identify the source of
the policy rather than prescribing new numbers. Limits are configurable only
where the owning implementation supports them; offline right-sizing never changes
limits.

For ordinary resource blocks, CPU and memory are Kubernetes quantity strings.
`NONE` and `unlimited` are omission sentinels, not zero usage. A request whose
effective baseline is `NONE`, `unlimited`, missing, or nonnumeric is skipped by
offline sizing even if its identity is an editable capability. An owner must
manually seed a reviewed request baseline before automation can size it. Numeric
zero is distinct from omission; the general schema permits it, although individual
installers can have special zero semantics (notably Velero CLI omission).

HSO operator requests, guest KSM requests and its memory limit, and supplied
additional-HCP quantities use positive-quantity schema definitions: no zero,
negative values, `NONE`, or `unlimited`. The additional-HCP editor also parses
Kubernetes quantities and rejects nonpositive values. Optional additional-HCP
CPU/memory fields may be omitted instead of using a sentinel.

## Identity Rules

The inventory below uses `Kind/workload/container`; `D`, `S`, `DS`, and `J` mean
Deployment, StatefulSet, DaemonSet, and Job. `init` explicitly selects an init
container. All other entries select regular containers, regardless of their names
(in particular, certificate refreshers are regular containers).

Role-constrained service mappings recognize only terminal cluster-name suffixes
matching `(?:^|-)(svc(?:-[0-9]+)?|mgmt-[0-9]+|opstool(?:-[0-9]+)?)$`.
For example, `int-uksouth-svc-1`, `pers-region-svc`, `ci00-job-mgmt-1`, and
`prod-region-opstool` qualify; bare `mgmt`, `mgmt-1-extra`, and unknown roles do not
select management paths. `any` below means no catalog role constraint, not a
claim about deployment topology.

Namespaces, workload names, kinds, container names, and init status match exactly
where specified. The only namespace families are `ocm-arohcp*` and `klusterlet-*`,
each requiring a nonempty suffix. Prometheus StatefulSets accept only
`prom-agent-prometheus` and `prom-agent-prometheus-shard-<digits>`; KSM has the two
explicit names listed below. There is no general fuzzy matching. Unknown owners
or roles can conservatively block candidate shared paths, but never authorize
an edit. Shared paths aggregate evidence from all matching workloads and clusters.

## Implemented Config Paths

Every path in these tables is relative to `defaults` (or an environment override
prefix). Append `.requests.cpu` or `.requests.memory` to a resource-block path.
Normal offline input writes only `clouds.dev.defaults.<path>.requests.<resource>`.
Some paths end in `Resources` rather than `.resources`; that spelling matters.

### Existing Services

These original mappings require the exact namespace/container, a resolved owner
kind and nonempty workload, but do not constrain a particular workload name or
cluster role. They are retained alongside the expanded exact-identity catalog.

| Category | Namespace / Container | Resource Block Path | Policy Source |
|---|---|---|---|
| RP | `aro-hcp` / `aro-hcp-backend` | `backend.k8s.resources` | `backend/values.yaml` and deployment chart |
| RP | `aro-hcp` / `aro-hcp-frontend` | `frontend.k8s.resources` | `frontend/values.yaml` and deployment chart |
| Admin | `aro-hcp-admin-api` / `service` | `adminApi.k8s.resources` | `admin/values.yaml` and deployment chart |
| Observability | `aro-hcp-exporter` / `aro-hcp-exporter` | `customExporter.k8s.resources` | `tooling/aro-hcp-exporter/deploy/values.yaml` and chart |
| Cluster Service | `clusters-service` / `clusters-service-server` | `clustersService.k8s.resources` | `cluster-service/values.yaml` and deployment chart |
| Fleet | `fleet` / `fleet-controller` | `fleet.k8s.resources` | `fleet/values.yaml` and deployment chart |
| Kube-applier | `kube-applier` / `kube-applier` | `kubeApplier.k8s.resources` | `kube-applier/values.yaml` and deployment chart |
| Maestro | `maestro` / `maestro-server` | `maestro.server.k8s.resources` | `maestro/server/values.yaml` and deployment chart |
| Management | `mgmt-agent` / `mgmt-agent-controller` | `mgmtAgent.k8s.resources` | `mgmt-agent/values.yaml` and deployment chart |
| SecretSync provider | `secret-sync-controller` / `provider-azure-installer` | `secretSyncController.k8s.resources` | `secret-sync-controller/deploy/values-azure.yaml`, provider container |
| SessionGate | `sessiongate` / `sessiongate-controller` | `sessiongate.k8s.resources` | `sessiongate/values.yaml` and deployment chart |
| Observability | `monitoring` / `kube-events` | `kubeEvents.k8s.resources` | `observability/kube-events` chart |
| Logging | `arobit` / `fluentbit` | `svc.arobit.forwarder.resources`, `mgmt.arobit.forwarder.resources` | Role-specific `observability/arobit/values-{svc,mgmt}.yaml` and forwarder chart |

### Infrastructure And Auxiliary Containers

| Category | Role; Namespace | Exact Identity | Resource Block Path | Policy Source / Default Behavior |
|---|---|---|---|---|
| ACR Pull | any; `acrpull` | `D/acrpull/acrpull-controller` | `acrPull.k8s.resources` | `acrpull/deploy`: existing chart requirements |
| Backup | mgmt; `velero` | `D/velero/velero` | `velero.server.resources` | `velero/deploy`: explicit installer flags preserve existing server requests and absent limits |
| Backup | mgmt; `velero` | `DS/node-agent/node-agent` | `velero.nodeAgent.resources` | Velero installer flags; omitted by default |
| Backup | mgmt; `velero` | `D/velero/oadp-oadp-velero-plugin-for-microsoft-azure-rhel9` (init) | `velero.azurePlugin.resources` | Velero exact init-container kustomize patch; omitted by default |
| Backup | mgmt; `velero` | `D/velero/oadp-oadp-hypershift-velero-plugin-rhel9` (init) | `velero.hypershiftPlugin.resources` | Velero exact init-container kustomize patch; omitted by default |
| Backup installer | mgmt; `velero` | `J/velero-install/generate-manifest` (init) | `velero.installer.generate.resources` | Velero install Job; omitted by default |
| Backup installer | mgmt; `velero` | `J/velero-install/apply-manifest` | `velero.installer.apply.resources` | Velero install Job; omitted by default |
| Recorder | any; `swift-recorder` | `DS/swift-recorder/swift-recorder` | `swiftRecorder.k8s.resources` | `swift-recorder/values.yaml` and DaemonSet; existing requirements |
| Maestro agent | mgmt; `maestro` | `D/maestro-agent/maestro-agent` | `maestro.agent.k8s.resources` | `maestro/agent/values.yaml`; omitted by default |
| Maestro agent | mgmt; `maestro` | `D/maestro-agent/metrics-proxy` | `maestro.agent.metricsProxy.resources` | Maestro agent sidecar; omitted by default |
| Maestro agent | mgmt; `maestro` | `D/maestro-agent/init` (init) | `maestro.agent.init.resources` | Maestro agent init container; omitted by default |
| SecretSync controller | any; `secret-sync-controller` | `D/secrets-store-sync-controller-manager/manager` | `secretSyncController.k8s.controllerResources` | `secret-sync-controller/deploy/values-azure.yaml`, controller container; omitted by default |
| Certificate refresher | svc; `aks-istio-ingress` | `D/frontend-certificate-refresher/init-container-msg-container-init` | `frontend.certificateRefresher.resources` | `frontend/deploy/templates/frontend.secret-refresher.yaml`; omitted by default |
| Certificate refresher | svc; `aks-istio-ingress` | `D/admin-api-certificate-refresher/init-container-msg-container-init` | `adminApi.certificateRefresher.resources` | `admin/deploy/templates/admin.secret-refresher.yaml`; omitted by default |
| Certificate refresher | svc; `aks-istio-ingress` | `D/sessiongate-certificate-refresher/init-container-msg-container-init` | `sessiongate.certificateRefresher.resources` | `sessiongate/deploy/templates/ingress.secret-refresher.yaml`; omitted by default |
| Gateway API ingress | svc; `aks-istio-ingress` | `D/ops-ingress-gateway-istio/istio-proxy` | `svc.opsIngress.gateway.resources` | `istio/deploy/templates/ops-ingress.gateway.yaml`: Gateway `parametersRef` ConfigMap; upstream default unless configured |
| Development DB | svc; `clusters-service` | `D/ocm-cs-db/postgresql` | `clustersService.postgres.containerizedDb.resources` | `cluster-service/deploy/templates/database.deployment.yaml`; omitted by default |
| Cluster Service init | svc; `clusters-service` | `D/clusters-service/clusters-service-init` (init) | `clustersService.initResources` | `cluster-service/deploy/templates/deployment.yaml`; omitted by default |
| Development DB | svc; `maestro` | `D/maestro-db/postgresql` | `maestro.postgres.containerizedDb.resources` | `maestro/server/deploy/templates/pg.deployment.yaml`; omitted by default |
| Maestro migration | svc; `maestro` | `D/maestro/maestro-server-migration` (init) | `maestro.server.migrationResources` | `maestro/server/deploy/templates/maestro.deployment.yaml`; omitted by default |
| Maestro DB wait | svc; `maestro` | `D/maestro/wait-for-db` (init) | `maestro.server.waitForDBResources` | Maestro server deployment; omitted by default |
| Kubelet fixes | mgmt; `kube-system` | `DS/set-kubelet-parameters-for-scale/kubelet-parameters` | `mgmt.kubeletFixes.resources` | `mgmt-fixes/deploy/kubelet-ds`: existing partial requirements |
| Kubelet fixes | mgmt; `kube-system` | `DS/set-kubelet-parameters-for-scale/apply-kubelet-parameters` (init) | `mgmt.kubeletFixes.initResources` | Kubelet fixes init container: existing requirements |
| Storage class hooks | mgmt; `default-sc` | `J/unannotate-default-sc/finalize`; `J/delete-sc-pre-upgrade/delete-sc` | `mgmt.storageClassHooks.resources` | `mgmt-fixes/deploy/default-sc`: shared setting for both Jobs; omitted by default |
| Guest KSM | mgmt; `ocm-arohcp*` | `D/kube-state-metrics-hcp/kube-state-metrics` | `mgmtAgent.guestKSMResources` | `mgmt-agent/pkg/controller/ksmhcp`: existing CPU/memory requests and memory limit; positive quantities only |
| HSO operator | mgmt; `hypershift` | `D/operator/operator` | `hypershift.operatorResources` | `hypershiftoperator` render/patch/apply installer: existing requests only, no limits |

SecretSync's old `k8s.resources` block controls **the Azure provider**, not
`manager`. The controller now has the distinct `k8s.controllerResources` block.
Neither `manager` nor `finalize` may be mapped by container name alone.

### Prometheus

All identities are in namespace `prometheus`. The policy source is
`observability/prometheus/values-{svc,mgmt,opstool}.yaml`, the
`kube-prometheus-stack` values adapter, and `deploy/templates/prometheus.yaml`.
Agent requests/limits retain existing settings; auxiliary resources are omitted
by default. A reloader setting is shared by regular and init containers.

| Role | Exact Identity | Resource Block Path |
|---|---|---|
| svc, opstool | `D/arohcp-monitor-kube-state-metrics/kube-state-metrics`; `D/prometheus-kube-state-metrics/kube-state-metrics` | `svc.prometheus.kubeStateMetrics.resources` |
| mgmt | Same two KSM identities | `mgmt.prometheus.kubeStateMetrics.resources` |
| svc, opstool | `D/prometheus-operator/kube-prometheus-stack` | `svc.prometheus.prometheusOperator.resources` |
| mgmt | `D/prometheus-operator/kube-prometheus-stack` | `mgmt.prometheus.prometheusOperator.resources` |
| svc, opstool | `S/prom-agent-prometheus/config-reloader`; `S/prom-agent-prometheus/init-config-reloader` (init); also numeric shards | `svc.prometheus.prometheusConfigReloader.resources` |
| mgmt | Same regular/init reloader identities and numeric shards | `mgmt.prometheus.prometheusConfigReloader.resources` |
| svc | `S/prom-agent-prometheus/prometheus`; also numeric shards | `svc.prometheus.prometheusSpec.resources` |
| mgmt | `S/prom-agent-prometheus/prometheus`; also numeric shards | `mgmt.prometheus.prometheusSpec.resources` |
| opstool | `S/prom-agent-prometheus/prometheus`; also numeric shards | `svc.prometheus.opstoolResources` |

Opstool shares the service-cluster auxiliary policies, but has a separate agent
resource block. Unknown cluster roles never fall back to service-cluster settings.

### ACM

All mappings require the management role. The owning templates, exact upstream
API selectors, preservation rules, and release-source evidence are documented in
[acm/resources.md](../acm/resources.md).

| Namespace | Exact Identity | Resource Block Path | Policy Source / Default Behavior |
|---|---|---|---|
| `multicluster-engine` | `D/multicluster-engine-operator/backplane-operator` | `acm.resources.backplaneOperator` | MCE chart `resources.backplaneOperator`; existing requirements |
| `multicluster-engine` | `D/grc-policy-addon-controller/manager` | `acm.resources.policyAddonController` | Policy chart `grc.resources.policyAddonController`; existing requests |
| `multicluster-engine` | `D/grc-policy-propagator/governance-policy-propagator` | `acm.resources.policyPropagator` | Policy chart `grc.resources.policyPropagator`; existing requests |
| `multicluster-engine` | `D/klusterlet-addon-controller-v2/klusterlet-addon-controller` | `acm.resources.klusterletAddonController` | Policy chart `cluster-lifecycle.resources.klusterletAddonController`; existing requests |
| `multicluster-engine` | `J/finalize-mce/finalize`; `J/finalize-mce-config/finalize` | `acm.resources.finalize` | Both chart finalize Jobs; shared setting, omitted by default |
| `klusterlet-*` or `open-cluster-management-agent-addon` | `D/config-policy-controller/config-policy-controller` | `acm.resources.configPolicyController` | `addon-hosted-config` AddOnDeploymentConfig; upstream default |
| Same add-on namespaces | `D/governance-policy-framework/governance-policy-framework-addon` | `acm.resources.governancePolicyFramework` | `addon-hosted-config` AddOnDeploymentConfig; upstream default |
| Same add-on namespaces | `D/klusterlet-addon-workmgr/acm-agent` | `acm.resources.workManager` | `addon-hosted-config` AddOnDeploymentConfig and finalize hook attachment; upstream default |
| Same add-on namespaces | `D/hypershift-addon-agent/hypershift-addon-agent` | `acm.resources.hypershiftAddonAgent` | `hypershift-addon-deploy-config` AddOnDeploymentConfig via finalize hook; upstream default |

Add-on selectors are `deployments:<workload>:<container>`, not broad container
matches. An all-sentinel entry emits no override. An active entry replaces the
matched resource requirements, rather than merging omitted quantities with
upstream defaults: verify and explicitly retain any needed upstream limits when
seeding requests. These are shared policies, not independent per-HCP controls.
Setting the HyperShift add-on back to all sentinels does not remove an already
applied override; explicit manual cleanup is required. No operand Deployment is
patched to bypass its owner.

## Minimal HCP Requests

The source is
`hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml`.
Only the `limitClusterSizes=true` branch's `e2e_minimal` class is exposed to the
offline sizing modes. Other classes and the unlimited branch are preserved.

### Seven Literal Entries

Use `--sizing-template` for these existing entries. Each identity maps to its
existing `cpu` and `memory` scalars in `e2e_minimal.resourceRequests`; no new
entry or missing scalar is inserted.

| Kind / Workload / Container | Policy Source |
|---|---|
| `Deployment/kube-apiserver/kube-apiserver` | Existing literal minimal sizing entry |
| `Deployment/openshift-controller-manager/openshift-controller-manager` | Existing literal minimal sizing entry |
| `Deployment/cluster-policy-controller/cluster-policy-controller` | Existing literal minimal sizing entry |
| `Deployment/kube-controller-manager/kube-controller-manager` | Existing literal minimal sizing entry |
| `Deployment/openshift-apiserver/openshift-apiserver` | Existing literal minimal sizing entry |
| `StatefulSet/etcd/etcd` | Existing literal minimal sizing entry |
| `Deployment/ovnkube-control-plane/ovnkube-control-plane` | Existing entry preserved; not proof that CNO consumes the component API |

### Additional HCP Catalog

`hypershift.additionalMinimalResourceRequests` defaults to `{}`. Each stable ID
maps to required `deploymentName` and `containerName` strings, and optional
positive Kubernetes quantity strings `cpu` and `memory`. The exact paths are
`defaults.hypershift.additionalMinimalResourceRequests.<id>.{deploymentName,containerName,cpu,memory}`
or the corresponding `clouds.dev.defaults` paths. These are direct CPU/memory
fields, not `resources.requests` blocks; there are no limit fields.

The schema rejects unknown entry fields and omission sentinels. The CLI further
requires IDs matching `^[a-zA-Z][a-zA-Z0-9_-]*$` and exact audited identities.
Duplicate workload/container pairs, including the original seven, are rejected.
An entry with neither quantity emits nothing; a missing quantity remains under
upstream control and is not automatically seeded.

Every target must produce a valid Kubernetes qualified annotation name:
`resource-request-override.hypershift.openshift.io/<deployment>.<container>`.
The `deployment.container` suffix is limited to 63 characters. The CLI rejects
configured invalid keys and experimental seeds; the experimental runner skips
invalid catalog targets with a diagnostic before planning. The chart rejects
overlong suffixes even if neither quantity is supplied.

The policy source for every row below is the upstream control-plane component
request override, wired through the Helm helper. Its audited allowlist is
[regular-resource-targets.yaml](../hypershiftoperator/deploy/regular-resource-targets.yaml),
mirrored by `AdditionalMinimalTargets()`. There are 51 audited identities, of which
49 have usable annotation keys; no numeric baseline is implied. Route-controller
and csi-snapshot-controller-operator remain in the catalog for audit parity, but
their 69- and 65-character suffixes cannot be configured through the current
override API.

| Kind | Exact Workload (`deploymentName`) | Exact Regular Containers (`containerName`) |
|---|---|---|
| Deployment | `control-plane-operator` | `control-plane-operator` |
| Deployment | `cluster-api` | `manager` |
| Deployment | `capi-provider` | `manager` |
| Deployment | `azure-cloud-controller-manager` | `cloud-controller-manager` |
| Deployment | `catalog-operator` | `catalog-operator`, `konnectivity-proxy-socks5` |
| Deployment | `certified-operators-catalog` | `registry` |
| Deployment | `cluster-autoscaler` | `cluster-autoscaler` |
| Deployment | `cluster-image-registry-operator` | `cluster-image-registry-operator`, `apiserver-token-minter` |
| Deployment | `cluster-network-operator` | `cluster-network-operator`, `client-token-minter`, `konnectivity-proxy-socks5` |
| Deployment | `cluster-node-tuning-operator` | `cluster-node-tuning-operator` |
| Deployment | `cluster-storage-operator` | `cluster-storage-operator` |
| Deployment | `cluster-version-operator` | `cluster-version-operator` |
| Deployment | `community-operators-catalog` | `registry` |
| Deployment | `control-plane-pki-operator` | `control-plane-pki-operator` |
| Deployment | `csi-snapshot-controller-operator` | `csi-snapshot-controller-operator` (unsupported: 65-character annotation suffix) |
| Deployment | `dns-operator` | `dns-operator` |
| StatefulSet | `etcd` | `etcd-defrag`, `etcd-metrics`, `healthz` |
| Deployment | `hosted-cluster-config-operator` | `hosted-cluster-config-operator` |
| Deployment | `ignition-server` | `ignition-server` |
| Deployment | `ignition-server-proxy` | `haproxy` |
| Deployment | `ingress-operator` | `ingress-operator`, `konnectivity-proxy-https` |
| Deployment | `konnectivity-agent` | `konnectivity-agent` |
| Deployment | `kube-apiserver` | `audit-logs`, `azure-kms-provider-active`, `azure-kms-provider-backup`, `azure-workload-identity-webhook`, `bootstrap`, `konnectivity-server` |
| Deployment | `kube-controller-manager` | `availability-prober` |
| Deployment | `kube-scheduler` | `kube-scheduler` |
| Deployment | `kube-storage-version-migrator` | `migrator` |
| Deployment | `machine-approver` | `machine-approver` |
| Deployment | `olm-operator` | `olm-operator`, `konnectivity-proxy-socks5` |
| Deployment | `openshift-apiserver` | `audit-logs`, `kas-readiness-check`, `konnectivity-proxy-https` |
| Deployment | `openshift-route-controller-manager` | `openshift-route-controller-manager` (unsupported: 69-character annotation suffix) |
| Deployment | `packageserver` | `packageserver`, `kas-readiness-check`, `konnectivity-proxy-socks5` |
| Deployment | `redhat-marketplace-catalog` | `registry` |
| Deployment | `redhat-operators-catalog` | `registry` |
| Deployment | `router` | `router` |

Despite the `deploymentName` field name, the component API supports StatefulSet
containers too. The report capability predicate requires a known management role,
an `ocm-arohcp` namespace with a nonempty suffix, the exact kind/workload/container,
and a regular container. The HTML renderer does not load configuration: it marks
these as editable capabilities but display-ineligible/non-actionable until a
baseline is configured, and excludes them from projected savings. Raw JSON
evidence is retained. This catalog-only display does not establish annotation-key
validity; the two overlong targets remain unsupported even if baselines are
supplied.

Verify the active CPO revision and live regular/init placement before configuring
these targets. The upstream API matches names in both container lists and has no
regular/init selector; the audited capability list is not proof that a token
minter or prober has the same role in every release. Guest KSM remains a separate
mgmt-agent control, not an additional HCP entry. See
[HyperShift upstream evidence](../hypershiftoperator/README.md#upstream-evidence).

### Additional HCP Workflow

First manually configure an approved baseline for an audited identity. The
following is **syntax only**: `200m` and `256Mi` are example placeholders, not
validated CPO sizing. Do not apply these numbers on the strength of this example.
Merge a reviewed entry into the existing config, rather than replacing its maps:

```yaml
clouds:
  dev:
    defaults:
      hypershift:
        additionalMinimalResourceRequests:
          controlPlaneOperator:
            deploymentName: control-plane-operator
            containerName: control-plane-operator
            cpu: 200m
            memory: 256Mi
```

From the repository root, preview against explicitly selected reports:

```bash
go run ./tooling/rightsize-requests \
  --input run-a/right-sizing.json --input run-b/right-sizing.json \
  --additional-hcp-config config/config.yaml \
  --namespace-prefix ocm-arohcpci01- \
  --dry-run
```

The mode reads existing effective entries from global and dev defaults in that
file and writes only dev CPU/memory overrides, copying existing identities into
dev when needed. It never creates a new effective target or missing CPU/memory
baseline from report suggestions. An empty map remains empty: there is no safe
automatic insertion. The allowlist is located relative to the config file at
`../hypershiftoperator/deploy/regular-resource-targets.yaml`.

The namespace prefix must match `^ocm-[a-z0-9][a-z0-9-]*-$`, is matched literally,
and requires a nonempty suffix. This CLI mode selects by the explicit prefix and
configured identity, not by management-cluster name; all matching source clusters
participate. The prefix is the user's assertion of environment and minimal size,
not proof of either. Use only representative `e2e_minimal` samples and independently
check live pod limits. Available incomplete CI evidence is not sufficient to apply
actual values. Keep `--dry-run` until these checks are complete.

Maximum suggestions across matching evidence govern shared targets. Ineligible,
init, incomplete, or unknown/wrong-owner evidence blocks the affected configured
resource. Missing reports block reductions; stale checks, the deadband, and
`--allow-decrease` still apply. Neither HCP sizing mode checks pod limits or changes
them. The two HCP modes are mutually exclusive and cannot be combined with explicit
`--config`, `--write-config`, `--source-prefix`, or `--write-prefix` flags.

After an approved write, run `make -C config materialize` and review the config,
rendered output, and fixture diffs. Offline sizing does not render or commit.

## Installer Preservation

HSO operator sizing uses `hypershift install render --outputs=resources
--render-sensitive`, an exact operator Deployment/container kustomize patch, then
server-side apply with field manager `hypershift` and force ownership, matching
upstream installation. Sensitive manifests are not logged. The CRD installer is
unchanged. PriorityClasses are split out and **created only**: `AlreadyExists` is
tolerated, other failures abort, and immutable values are not updated during an
upgrade. They must not be included in the server-side apply stream. See
[hypershiftoperator/README.md](../hypershiftoperator/README.md#resource-requests)
for source and tooling-image evidence. This preserves upstream upgrade semantics;
it is not a claim of live-cluster upgrade validation.

## Blocked Controls

Do not add speculative config keys or direct patches for owner-reconciled
workloads. The following remain outside implemented coverage:

| Category | Blocked Targets | Required Owner / API Change |
|---|---|---|
| ACM backplane operands | `cluster-manager/registration-operator`, `cluster-permission/cluster-permission`, `clusterlifecycle-state-metrics-v2/clusterlifecycle-state-metrics`, `hypershift-addon-manager/hypershift-addon-manager`, `managedcluster-import-controller-v2/managedcluster-import-controller`, `ocm-controller/ocm-controller`, `ocm-webhook/ocm-webhook` | All seven need a shipped ContainerConfig resources API/reconciler or explicit template consumers; current MCE overrides expose name/env, not resources |
| ACM ClusterManager | Shared addon-manager, placement/debug, registration/webhook, work-controller and optional gRPC resources | ClusterManager has a resource API, but backplane does not pass resource configuration into its Go-built object; add a supported MCE input and reconciliation |
| ACM Klusterlet | `klusterlet-agent`, `registration-controller`, `klusterlet-manifestwork-agent`; separately the generated `klusterlet` operator | Klusterlet has a resource API, but shipped KlusterletConfig/import propagation does not; expose it and separately expose the operator chart's Resources |
| Shared HSO ingress | Shared router Deployment, distinct from per-HCP `router/router` above | Change the owning shared-ingress controller/API; component request overrides do not cover it and a Deployment patch would be reconciled away |
| AKS-managed Istio | Legacy managed ingress gateways and `istiod` | No verified supported request API in this implementation; the ops Gateway API control does not apply to them |
| Injection | Arbitrary injected sidecars/init containers | Unsupported as a general policy; explicit audited regular HCP names do not authorize broad injection tuning or automatic init tuning |
| Other HCP operands | External CNO/CSI operands, recovery Jobs, unresolved identities | Need verified ownership, resource API, revision/role evidence and explicit catalog entries |

The ACM limitations and pinned release-source checks are detailed in
[acm/resources.md](../acm/resources.md#blocked-upstream); HCP limitations are in
[hypershiftoperator/README.md](../hypershiftoperator/README.md#upstream-evidence).
Source inspection and local render tests are not live admission/reconciliation
proof. In particular, the ACM source-to-pinned-image identity and full upstream
bundle refresh were not verified by those checks.

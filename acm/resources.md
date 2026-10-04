# Resource Controls

All config paths below are under `acm.resources` and have four string leaves:
`requests.cpu`, `requests.memory`, `limits.cpu`, and `limits.memory`.
`NONE` and `unlimited` omit that quantity. Neither means zero or observed zero
usage. Other values must match an anchored Kubernetes quantity grammar; empty
strings and shell metacharacters fail Helm rendering, including before values
can enter the finalize hook's JSON patch. Numeric zero is preserved.
Config/schema declarations and rendered fixtures are maintained separately from
this ACM chart change.

## Implemented

These defaults preserve the checked-in Deployments. All are in namespace
`multicluster-engine`; match workload AND container, not container alone.

| Config Key | Deployment / Container | Requests CPU / Memory | Limits CPU / Memory | Helm Values Path |
|---|---|---|---|---|
| `backplaneOperator` | `multicluster-engine-operator` / `backplane-operator` | `100m` / `20Mi` | `100m` / `2Gi` | MCE: `resources.backplaneOperator` |
| `policyAddonController` | `grc-policy-addon-controller` / `manager` | `25m` / `64Mi` | `NONE` / `NONE` | MCE config: `policy.grc.resources.policyAddonController` |
| `policyPropagator` | `grc-policy-propagator` / `governance-policy-propagator` | `25m` / `64Mi` | `NONE` / `NONE` | MCE config: `policy.grc.resources.policyPropagator` |
| `klusterletAddonController` | `klusterlet-addon-controller-v2` / `klusterlet-addon-controller` | `50m` / `96Mi` | `NONE` / `NONE` | MCE config: `policy.cluster-lifecycle.resources.klusterletAddonController` |
| `finalize` | Jobs `finalize-mce` AND `finalize-mce-config` / `finalize` | `NONE` / `NONE` | `NONE` / `NONE` | Both charts: `resources.finalize` |

The finalize key intentionally has two collision targets. It does not control
the `finalize` containers in the default-storage-class chart.

Add-on keys have all four defaults `NONE`, and use MCE config Helm values
`resources.<key>`. All-NONE entries are omitted, leaving upstream defaults intact.
An active entry replaces the matched container's resource requirements; it does
not merge missing quantities with upstream defaults. A requests-only override
therefore does not preserve an upstream limit automatically.

| Config Key | Exact `spec.resourceRequirements[].containerID` | AddOnDeploymentConfig |
|---|---|---|
| `configPolicyController` | `deployments:config-policy-controller:config-policy-controller` | `multicluster-engine/addon-hosted-config` |
| `governancePolicyFramework` | `deployments:governance-policy-framework:governance-policy-framework-addon` | `multicluster-engine/addon-hosted-config` |
| `workManager` | `deployments:klusterlet-addon-workmgr:acm-agent` | `multicluster-engine/addon-hosted-config` |
| `hypershiftAddonAgent` | `deployments:hypershift-addon-agent:hypershift-addon-agent` | `multicluster-engine/hypershift-addon-deploy-config` |

These are shared defaults across every managed/hosted namespace using the named
config, not independent per-cluster controls. ManagedClusterAddOn-specific configs
may override the defaults. The policy ClusterManagementAddOns already reference
`addon-hosted-config`; the existing MCE finalize hook attaches work-manager.
HyperShift uses a separate config and is updated by the existing config finalize
hook. The hook patches resourceRequirements only when at least one quantity is
configured; otherwise it omits the field, preserving any existing override while
still patching customizedVariables. An active override replaces the list.
Removing the last configured HyperShift quantity (setting all quantities to
`NONE` or `unlimited`) does NOT roll back a previously applied override. Removing
that override requires explicit manual cleanup of the corresponding
resourceRequirements entry on `multicluster-engine/hypershift-addon-deploy-config`.
Backplane's HyperShift config template
does not manage resourceRequirements; its server-side apply leaves these
user-configured entries intact. No operand Deployment is patched.

Exact selectors exclude metrics proxies, cleanup Pods/Jobs, and unrelated
containers. Do not map `manager`, `finalize`, or `acm-agent` by name alone.

## Generation And Tests

`update-resource-templates.sh` runs after both MCE bundle repackaging and policy
refresh. It restores the helper, MCE defaults, and templated resource blocks,
including the generated MCE finalize Job. Policy values are repo-owned and are
not overwritten by the refresh script. The postprocessor fails if an expected
resource block disappears rather than silently dropping configurability.

Run `make -C acm test-resources` (Helm, Python/PyYAML, yq v4, Perl required).
Tests cover both values adapters, nested charts, all four scalar overrides,
NONE omission, exact add-on selectors, the two finalize targets, refresh
restoration, idempotence, and fail-closed upstream layout changes.

## Blocked Upstream

No config keys or pretend Helm settings are provided for these controls.

**ClusterManager shared resources:** The CRD supports
`spec.resourceRequirement.type: ResourceRequirement` with
`spec.resourceRequirement.resourceRequirements.{requests,limits}.{cpu,memory}`.
However, backplane constructs the object in
`pkg/foundation/cluster_manager.go:ClusterManager`, called by
`controllers/toggle_components.go:ensureClusterManager`, without resource input.
The `installer.multicluster.openshift.io/template-override-configmap` annotation
only supplies values to templates that explicitly consume them; it does not
patch arbitrary objects, and this object is built in Go, not Helm.

Required upstream: add a supported MCE API field or explicit template-override
consumer, pass the validated shared ResourceRequirement into
`foundation.ClusterManager`, reconcile changes/removal, and ship the updated
operator/API. This shared control would collide across addon-manager-controller,
addon webhook, placement-controller AND debug-server, registration controller,
registration webhook AND work webhook (`cluster-manager-webhook` containers),
work controller, and optional gRPC server. It does NOT size the backplane-owned
`cluster-manager` Deployment's `registration-operator` container.

**Seven backplane operands:** MCE's checked-in
`spec.overrides.components[].configOverrides.deployments[].containers[]` exposes
`name` and `env`, not `resources`. Their upstream resource blocks do not consume
templateOverrides. Required upstream: add requests/limits to the ContainerConfig
API and CRD plus the deployment override reconciler, or explicit resource
template-override consumers in each corresponding chart; then ship the operator.

| Deployment / Container | Upstream Chart / Template |
|---|---|
| `cluster-manager` / `registration-operator` | `cluster-manager/templates/cluster-manager.yaml` |
| `cluster-permission` / `cluster-permission` | `cluster-permission/templates/cluster-permission.yaml` |
| `clusterlifecycle-state-metrics-v2` / `clusterlifecycle-state-metrics` | `cluster-lifecycle/templates/metrics-deployment.yaml` |
| `hypershift-addon-manager` / `hypershift-addon-manager` | `hypershift/templates/hypershift-addon-manager-deployment.yaml` |
| `managedcluster-import-controller-v2` / `managedcluster-import-controller` | `server-foundation/templates/managedcluster-import-deployment.yaml` |
| `ocm-controller` / `ocm-controller` | `server-foundation/templates/ocm-controller.yaml` |
| `ocm-webhook` / `ocm-webhook` | `server-foundation/templates/ocm-webhook.yaml` |

**Klusterlet shared agents:** Klusterlet supports the same
`spec.resourceRequirement.{type,resourceRequirements}` API, but the shipped
KlusterletConfig schema has no corresponding resource field. Import's
`pkg/bootstrap/render.go` does not pass one through. Required upstream: add the
field to `config.open-cluster-management.io/v1alpha1` KlusterletConfig, publish
its CRD in backplane, and propagate global/per-cluster config through import's
chart configuration to Klusterlet.spec.resourceRequirement, including updates
and removal. Shared collision targets are `klusterlet-agent`,
`registration-controller`, and `klusterlet-manifestwork-agent` in their respective
singleton/hosted modes. The generated `klusterlet` operator container is a
different control: import must separately expose its chart-level `Resources`
(currently 50m/64Mi requests and 2Gi memory limit). Agent settings cannot size it.

## API Evidence

The local MCE CRD was checked directly. Other CRDs and consumers were inspected
in these upstream release sources:

| Repository | Revision |
|---|---|
| `stolostron/backplane-operator`, `backplane-2.17` | `f9eaf564822e263111a48e331480c20b4fbb24c5` |
| `stolostron/ocm`, `backplane-2.17` | `3656da57e7171b2b8361356eae2f7177e2834ec5` |
| `stolostron/managedcluster-import-controller`, `backplane-2.17` | `cdd62a0f4d9b6ff4f72715b86afba0e0bf14b0bf` |
| `stolostron/governance-policy-addon-controller`, `release-2.16` | `99e33c0e54e15f1b144561507d73d181d7d4d8db` |
| `stolostron/hypershift-addon-operator`, `backplane-2.17` | `80fd5b7c1b5008d320298339bc901ee64e7953cf` |
| `stolostron/multicloud-operators-foundation`, `backplane-2.17` | `ee45f6b15fb11b3985b141153b233fae60a2b9df` |

OCM's `manifests/cluster-manager/hub/crds/0000_02_addon.open-cluster-management.io_addondeploymentconfigs.crd.yaml`
defines v1alpha1 `resourceRequirements`, `containerID`, and Kubernetes resources.
Policy and foundation add-on templates consume exact selectors through the
addon-framework resource requirements values adapter. HyperShift's
`pkg/manager/manager.go` and `pkg/manager/manifests/templates/deployment.yaml`
do likewise. Backplane's ClusterManager and KlusterletConfig CRDs are under
`pkg/templates/crds/{cluster-manager,foundation}`.

These are release-source checks, not live-cluster admission or reconciliation
tests. The pinned backplane image could not be inspected at registry.redhat.io
(`manifest unknown`), so source-to-image identity and a full bundle download/
refresh were not verified in this change. Generation is tested locally against
simulated refreshed charts, without changing image digests.

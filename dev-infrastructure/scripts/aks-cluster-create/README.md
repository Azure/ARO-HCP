# aks-cluster-create

Creates a management cluster's AKS `ManagedCluster` resource and all of its node
pools (system, infra, worker) during initial provisioning. Subsequent runs
reconcile cluster configuration while leaving pool updates to fleet.

It plans pools with the **same** `fleet/pkg/compute` profiles the
[nodepool controller](../../../fleet/docs/nodepool) uses at steady state
(`compute.ResolveDesiredPools`). With the same profile, zones, quota limits, and
SKU metadata, both compute the same desired pool set. The tool covers the
one thing the controller cannot: the initial `ManagedCluster` and its first
system pool, which must exist before any controller can reconcile.

## Why a Go tool (not bicep)

The management pipeline runs this as a `Shell` step (`mgmt-pipeline.yaml`,
step `cluster-create`) rather than an ARM/bicep deployment because pool planning
needs live quota and SKU data and must reuse the controller's allocation logic —
not something bicep can express. The EV2 Shell runner image has no Go toolchain,
so the pipeline `buildStep` compiles the binary ahead of time and ships it in the
step's working directory (same pattern as `scripts/grafana-group-roles`).

## Behavior

Each reconciliation attempt reads the cluster and calls `ensureManagedCluster`
for both creation and updates. It provisions pools only while the cluster carries
the provisioning tag, then finishes with one tag-reconciliation step. Creation
resolves pools first because the cluster requires an inline bootstrap pool;
existing clusters reconcile configuration before attempting pool allocation.

New clusters embed their first system pool inline, then create the remaining
system, infra, and worker pools concurrently. Required tiers must allocate at
least one pool, and a system pool must be available. Optional tier allocation
failures are logged; partial nonzero allocations can be provisioned.

### Idempotency & resume

An interrupted or failed pipeline step can rerun the tool to resume provisioning:

- New clusters carry an `aro-hcp-provisioning=true` **provisioning tag**. Its
  case-insensitive presence, regardless of value, permits pool provisioning.
- Every existing cluster first reconciles its configuration, including during
  provisioning. Resumed runs manage all desired pools through the pool API;
  they never resubmit an inline bootstrap pool.
- `ensurePool` creates missing pools, recovers pools in `Failed` state, and
  leaves other existing pools alone. Existing-pool updates use the pool ETag
  (`If-Match`); new clusters and pools use `If-None-Match: *`.
- `reconcileClusterTags` is the single tag-finalization path: it merges configured
  tags, fills missing observed-capacity baselines, and removes the provisioning
  marker in one conditional tag write. Marker removal requires successful
  provisioning of the cluster and every observed pool, even if baselines exist.
- Once the marker is absent, the tool does not plan or mutate pools. It observes
  pools only if baseline tags need initialization. Complete baselines are never
  recalculated or replaced.

ARM `409`/`412` conflicts restart the entire attempt from a fresh cluster read,
with exponential backoff and jitter, up to six attempts within the command's
deadline. Retries recheck ownership and preserve baselines written concurrently;
they never replay a stale payload. Other failures propagate immediately apart
from the SDK's normal transient HTTP retries. Cancellation interrupts backoff.

A converged cluster with complete baselines needs one cluster GET and no pool API
calls. Configuration updates supply the next cluster snapshot and ETag through
their completed operation response. Successful pool writes require a parent
refresh before tag finalization because they can invalidate the cluster ETag.
Tag helpers never reread the cluster.

Once the tool exits successfully, the nodepool controller owns the cluster's
pools from then on.

## Cluster configuration reconciliation

`buildClusterSpec` in `cluster_spec.go` defines configuration once for both
creation and updates, without layered builders. For a new cluster it initializes
creation-only settings, tags, Kubernetes version, and the bootstrap pool. For an
existing cluster it preserves unowned fields and tags while omitting pools and
version. Metrics normalization, field merging, and sorted changed-field paths
stay in this function; it does not mutate the observed snapshot.

`ensureManagedCluster` submits that spec through one `BeginCreateOrUpdate` and
polling path. Creation uses `If-None-Match: *`; updates use the observed ETag.
For every existing cluster, the tool compares configured fields with the live
resource and skips the cluster PUT when those fields match and provisioning has
succeeded. A failed update is resubmitted even if its desired properties are
already visible. Updates use the observed ETag (`If-Match`); conflicts restart the
lifecycle as described above, while terminal asynchronous failures propagate.

Only configured fields are overlaid. Configuration PUTs carry live tags unchanged;
configured tags are reconciled centrally during finalization, preserving
unconfigured keys and existing fleet capacity tags. Configured lists are authoritative.
AKS-added profile fields and omitted empty metrics allowlists do not cause drift.
The pipeline supplies `OWNING_TEAM_TAG_VALUE` from `monitoring.alertRuleOwningTeamTag`;
it overrides the `owningTeam` entry in `CLUSTER_TAGS`, matching the original Bicep
precedence.

Cluster updates omit agent pool profiles and Kubernetes version. Creation-only
location, DNS prefix, and node resource group retain their live values. Kubernetes
upgrades remain owned by `upgrade-aks-cluster.sh`. The three maintenance schedules
are managed by the post-cluster Bicep deployment through `modules/aks/maintenance.bicep`,
shared with the base cluster template.

The base template's management-cluster properties are retained; Istio and ingress
are service-cluster-only. Pool topology, sizing, zones, and Swift configuration
come from fleet profiles rather than the former Bicep inputs. The shared pool
builder uses `maxSurge=10%`; the former worker/infra `maxUnavailable` input and
explicit initial `count` are not submitted by that builder.

## Capacity baseline adoption

The tool initializes missing `arohcp-capacity-system`, `arohcp-capacity-infra`,
and `arohcp-capacity-worker` tags from the live pool ceilings and SKU metadata.
Values are JSON objects with `vcpus`, `memoryGiB`, and `swiftNICs`. Autoscaled
pools use `maxCount`; static pools use `count`. Swift NIC capacity uses the
configured secondary-NIC tag on Swift-enabled workers.

Initialization requires successful ARM provisioning and complete capacity data.
Existing role tags retain their original spelling and values, even if observed
capacity or the desired plan is smaller. Missing roles are initialized separately.
Malformed baseline tags fail the run. New baselines and provisioning-marker
removal commit atomically; every tag write uses the observed cluster ETag.

The fleet controller requires these baselines before modifying pools and updates
them only after configuration convergence. A fully allocated configuration may
lower the baseline; a partial allocation must preserve it. Initial provisioning,
including retries, does not compare the desired plan against a baseline. The
baseline is established when provisioning finishes. Re-running the tool can
initialize missing baselines on finished clusters without planning or modifying
their pools.

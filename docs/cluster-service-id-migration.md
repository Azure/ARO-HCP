# Cluster Service ID migration

This three-stage migration moves the cluster's `PendingClusterServiceID` and
`ClusterServiceID` to the singleton `ServiceProviderCluster`. Node pool and
external auth IDs are not migrated. The pending ID remains necessary for
idempotent creation and for recovering deletion after an interrupted create.

## Stage 1: old reads, dual writes

All ID mutations replace the cluster and its provider state in one resources
container transaction, within their subscription partition. Both replacements
carry ETags. A missing provider is created in the same batch. Conflicts require
reconciliation from fresh state; neither side may be updated unconditionally.
`CreateServiceProviderCluster` also reconciles existing records from old to new,
including clearing stale IDs. Equal records require no writes. Deleting clusters
with IDs still require provider state; completed cleanup must not recreate it.

Deploy this stage to **all writers of either document**, not just the ID-writing
controllers. Older binaries may replace a provider document and erase unfamiliar
fields. During mixed-version rollout the old representation remains authoritative
and synchronization is convergent, not an instantaneous fleet-wide guarantee.

Before stage 2, retire all pre-stage-1 writers and verify every live cluster has
matching pending and final IDs in provider state. Include in-progress creations
and deletions. Absent provider state is acceptable only after deletion cleanup,
with both old IDs absent. Repeat this check after a reconciliation interval and
verify there are no synchronization errors. PR merge order alone is not this gate.

Backfill also records `ClusterServiceDeletionCompleted` on deleting clusters
whose delete has been dispatched and whose IDs have both been cleared. This
durable evidence survives removal of provider children; dispatch alone is not
completion. Before stage 2, require this marker for every deleting cluster with
no provider, including deletions completed by pre-stage-1 writers. Do not infer
completion from a missing provider or recreate deleted provider state.

## Stage 2: new reads, dual writes

Stage 1 also persists `ServiceProviderClusterInitialized` on every source cluster.
Initial provider creation and this marker use the same ETag-protected Cosmos
batch, copying any legacy IDs in stage 1. All initializer callers (including
admin sizing and deny assignments) refuse to create state after this marker is
set, or after deletion dispatch. Stage-1 synchronization may restore lost state
from the still-authoritative old IDs; stage 2 must fail closed instead. A fresh
cluster may initialize before deletion dispatch, but never resurrect a provider
after cleanup. Verify the initialization marker for every existing cluster before
stage 2; retire all writers that do not preserve it. Stage 2 initializers no longer
copy old IDs: only new, uninitialized clusters may use them. Missing initialized
state requires operator investigation, not a new pending ID.

The cluster-service mismatch inventory leaves missing providers unindexed.
Absence before initialization or after proven deletion cleanup is expected;
missing initialized state or deletion dispatch without completion is reported
through the controller's error/retry path after safe orphan reconciliation.
The live parent lookup still prevents deleting any cluster-service cluster with
an existing parent, even when provider state is missing. Other provider read
errors abort the scan. Inventory diagnostics never initialize or repair state
and do not weaken creation's fail-closed requirement.

Only after that gate, deploy provider-based readers. Remove the old-authoritative
backfill in this stage; otherwise it could restore stale IDs after stage 3 stops
updating the legacy representation. All lifecycle mutations still update both
documents atomically. The provider initializer also handles a delete arriving before
initialization, but stops recreating provider state after CS deletion is dispatched.
Deletion cleanup requires the durable completion marker, not just dispatch or
provider absence. The ID clearer records completion when both provider IDs are
absent after dispatch. Cleanup reads live provider IDs after the live cluster
deletion markers and blocks if either ID remains. Once the provider child is
removed, the surviving completion marker lets parent deletion finish.
Historical diagnostic snapshots may still contain legacy
fields and are distinct from live operational reads.

Before stage 3, deploy stage 2 everywhere and retire all stage-1 reconcilers and
old readers. Verify create/adopt, pending-delete recovery, normal deletion,
credential operations, and child-resource creation against provider state.

## Stage 3: new representation only

Remove the legacy fields and compatibility writes, retaining **both** provider
fields. Old JSON members disappear as cluster documents are replaced; their
presence in historical snapshots is not a live dependency. Stage 2 is the rollback
floor after new-only writes begin. Rolling back to stage 1 or earlier requires a
separate, reviewed reverse migration; stale legacy values are not safe input.

## Verification

Exercise nil/empty IDs, pending-to-final promotion, clearing, repeated backfill,
missing/deleting provider state, and conflicts on both documents. The production
Cosmos batch is atomic, but the in-memory transaction mock executes steps
sequentially without rollback: mock tests alone cannot prove batch rollback.
Run emulator-backed conflict tests before rollout. Use repository `make generate`
and generated-file checks, targeted controller/API tests, and the integration
harness (`make -C test-integration test`). No deployment is performed by this series.

# Customer Workload Impact During etcd Restore

## Context

The HCP recovery controller restores a guest cluster's control plane by deleting the
HCP and HC namespaces, then recreating them from a Velero backup that includes etcd
PV snapshots (`velero.go:82-85`). Customer worker nodes remain running throughout
the restore. This document explains why customer workloads survive the restore and
what transient disruption to expect.

## How the restore works

The Velero backup captures etcd volumes via cloud-provider snapshots
(`SnapshotVolumes(true)`, `SnapshotMoveData(true)`). On restore, `RestorePVs(true)`
recreates the PVCs/PVs from those snapshots (`velero.go:34-41`). The entire HCP
namespace (API server, etcd, kube-controller-manager, etc.) is destroyed and
recreated — there are no surviving API server processes or watch connections on the
server side.

When the new API server starts against the restored etcd, all objects carry
resourceVersion values from the snapshot timestamp. Customer controllers and
kubelets on worker nodes hold watches established against the previous API server
process. Those TCP connections are now dead. On reconnect, clients present a stale
resourceVersion to the new API server.

## Why informers on customer workloads recover

Every Kubernetes controller (including customer-deployed operators on worker nodes)
uses client-go's `Reflector` to maintain watches. The reflector has explicit handling
for stale resourceVersions:

1. The watch request returns HTTP 410 Gone (the API server translates etcd's
   compaction error into this status).

2. `isExpiredError()` catches both `StatusReasonExpired` and `StatusReasonGone`.
   ([reflector.go:1154-1160](https://github.com/kubernetes/client-go/blob/768b463699de8c449b3ec62bf96a265c7acf7a0c/tools/cache/reflector.go#L1154-L1160))

3. The reflector exits the watch loop and calls `list()`, which first attempts a
   LIST at the last-known resourceVersion. If that also returns 410, it sets
   `isLastSyncResourceVersionUnavailable = true` and retries with `RV=""` — a full
   consistent read from etcd.
   ([reflector.go:720-728](https://github.com/kubernetes/client-go/blob/768b463699de8c449b3ec62bf96a265c7acf7a0c/tools/cache/reflector.go#L720-L728))

4. `relistResourceVersion()` returns `""` when the unavailable flag is set,
   triggering a quorum read that rebuilds the informer cache from scratch.
   ([reflector.go:1116-1132](https://github.com/kubernetes/client-go/blob/768b463699de8c449b3ec62bf96a265c7acf7a0c/tools/cache/reflector.go#L1116-L1132))

This is the standard recovery path — it fires routinely during normal etcd
compaction. No customer controller changes are needed.

## Kubelet behavior

Kubelets on worker nodes are also informer-based clients and follow the same
410 → relist path described above. Additional kubelet-specific behaviors:

**Orphan pod termination.** The kubelet's housekeeping loop runs every 2 seconds.
`HandlePodCleanups` compares containers running in CRI against the desired pod set
from the API server. Pods running locally but absent from the API server (created
after the snapshot) are killed with a 1-second grace period.
([kubelet_pods.go:HandlePodCleanups](https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kubelet_pods.go),
[pod_workers.go:SyncKnownPods](https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/pod_workers.go))

**Node lease renewal.** Lease objects in `kube-node-lease` revert to snapshot-time
resourceVersions. The kubelet's next conditional PUT fails with 409 Conflict. The
kubelet re-reads the lease and retries — self-healing.

**Certificate rotation.** If kubelet certificates rotated after the snapshot, the
restored etcd won't have the CSR approval for the current certificate. However, the
certificate is still signed by the same CA. In HyperShift the CA lives in management
cluster secrets, which are restored from the same backup, so they stay in sync.

## Leader election

Customer operators using Lease-based leader election will hit a 409 Conflict on
their next renewal attempt (stale resourceVersion on the cached Lease object). The
leader election code in client-go handles this with a two-phase approach:

1. **Fast path**: optimistic `Update()` with cached resourceVersion — fails with 409.
2. **Slow path**: `Get()` fetches fresh Lease (new resourceVersion), then `Update()`.

`renew()` retries `tryAcquireOrRenew()` every `RetryPeriod` (default 2s) for up to
`RenewDeadline` (default 10s). Brief leader flap, then self-heals.
([leaderelection.go:tryAcquireOrRenew](https://github.com/kubernetes/client-go/blob/master/tools/leaderelection/leaderelection.go),
[leaselock.go](https://github.com/kubernetes/client-go/blob/master/tools/leaderelection/resourcelock/leaselock.go))

## etcd revision bump: why it's not needed here

The etcd documentation describes `--bump-revision` and `--mark-compacted` flags for
`etcdutl snapshot restore`
([etcd.io recovery docs](https://etcd.io/docs/v3.6/op-guide/recovery/#restoring-with-revision-bump)).
These flags force-terminate all existing watches by marking all revisions as
compacted, ensuring no client silently misses events in the gap between snapshot
and restore.

This solves the problem of restoring etcd **in-place under a running API server**
that maintains existing watch connections. In that scenario, a surviving watch might
not receive a 410 if the restored revision happens to be higher than the client's
watch position — the watch silently receives no events for the gap.

The HCP recovery controller sidesteps this entirely. The control plane is destroyed
and recreated (`controller.go:309-314`), so there are no surviving API server
processes or server-side watch connections. Every client (kubelet, customer
controller) must establish a new connection to the new API server, and if its cached
resourceVersion is stale, it gets 410 immediately via the normal reflector path.

## What customers will observe

All of the following are consequences of the time gap between backup and restore
(etcd state divergence), not of any controller or kubelet failure mode:

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| Pod created after snapshot | Running locally but absent from etcd. Kubelet kills it as an orphan within ~2s. | Owning controller (Deployment, StatefulSet) recreates it from the restored spec. |
| Pod deleted after snapshot | Present in etcd but not running. Controllers attempt to schedule it. | Normal scheduling — pod starts on an available node. |
| ConfigMap/Secret changed after snapshot | Reverts to snapshot-time value. Kubelet volume sync (~60s) propagates the old value to running pods. | Customer must re-apply the change. `subPath` mounts do not update regardless of restore. |
| PV provisioned after snapshot | Cloud disk exists but no PV/PVC in etcd. Orphaned cloud resource. | Manual cleanup or re-provisioning required. |
| PV deleted after snapshot | PV/PVC in etcd but cloud disk may be gone. Pod mounts will fail. | Customer must re-provision storage. |
| CRD spec changed after snapshot | Reverts to snapshot-time spec. Customer operator relists and reconciles to old spec. | Customer must re-apply the change. |
| Service endpoints | Revert to snapshot-time state. Endpoints controller re-reconciles as pods report in. kube-proxy relists via 410. | Brief window of stale routing, self-heals. |

## Summary

Preserving customer worker nodes through an etcd PV snapshot restore is survivable.
Every informer-based client (kubelets, customer controllers, operators) recovers
automatically through the client-go reflector's 410 → relist path. Leader election
self-heals through Get+Update retry. The only customer-visible impact is the logical
state divergence between what etcd contains (snapshot-time) and what is actually
running — this is inherent to any point-in-time restore and is not caused by a
failure in any Kubernetes component.

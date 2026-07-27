# Test Failure Analysis: Customer should upgrade and update a nodepool from 4.20.z to 4.21.zLatest

## Root Cause

A race condition between two independent Clusters Service workers caused the HyperShift NodePool `spec.replicas` to be reverted from `3` back to `2`, even though the CS database correctly stored `replicas=3`. The node pool worker and the upgrade policy worker both performed concurrent read-modify-write operations on the same ManifestWork, and the last writer (upgrade policy worker) overwrote the replica change applied by the first writer (node pool worker).

## Summary

The test triggered a single node-pool PATCH at **2026-07-20T21:32:57Z** to both upgrade `npupgrade-4-20` to `4.21.25` and scale replicas to `3`. Clusters Service correctly persisted both changes in its database and dispatched them to two independent workers:

1. The **node pool worker** (`aroHcpPendingUpdateNodePoolProcessor`) processed the `pending_update` state and patched the ManifestWork with the full node pool model (replicas=3, version=4.21.25).
2. The **upgrade policy worker** (`aroHcpNodePoolUpgradePolicyWorker`) independently processed the scheduled upgrade policy and patched the same ManifestWork with only the version change (version=4.21.25, but using a stale base that had replicas=2).

The upgrade policy worker's patch landed ~700ms after the node pool worker's patch, overwriting `spec.replicas` from `3` back to `2`. The HyperShift NodePool never recovered to `replicas=3` because no subsequent reconciliation corrected the ManifestWork, and the RP backend kept the operation in `Updating` with the blocking reason `hypershift NodePool replicas is 2, want 3` until the test timed out.

## Key Timeline

| Time (UTC) | Event | Source |
|---|---|---|
| 21:32:57 | Test triggers PATCH: upgrade to 4.21.25 + replicas=3 | e2e test |
| 21:33:00.907 | Node pool worker picks up `validating_update` state | `aro_hcp_node_pool_worker.go:230` |
| 21:33:01.809 | Node pool worker picks up `pending_update` state | `aro_hcp_node_pool_worker.go:230` |
| 21:33:02.547 | Upgrade policy worker starts processing policy | `aro_hcp_node_pool_upgrade_policy_worker.go:150` |
| 21:33:02.666 | **Node pool worker patches ManifestWork** (replicas=3, version=4.21.25) | `node_pool_cr_helpers.go:93` |
| 21:33:03.372 | **Upgrade policy worker patches ManifestWork** (version=4.21.25, stale replicas=2) | `node_pool_cr_helpers.go:93` |
| 21:33:03.385 | Upgrade policy worker logs success | `aro_hcp_node_pool_upgrade_policy_worker.go:361` |
| 21:33:03.273-03.659 | HyperShift NodePool briefly shows `specReplicas=3` | `kubernetesResourceSnapshots` |
| 21:33:03.938 | HyperShift NodePool reverts to `specReplicas=2` | `kubernetesResourceSnapshots` |
| 21:52:00.725 | CS logs version convergence to 4.21.25 | `aro_hcp_node_pool_status.go` |
| 22:17:57 | Test times out (backend still blocking on replicas mismatch) | e2e test |

## Causal Chain

### 0. Q: Why did this test fail?

**A:** The test failed because the node-pool update step timed out waiting for node pool `npupgrade-4-20` in cluster `np-version-upgrade-cluster-9cldvc` to finish updating after the test triggered a PATCH to upgrade it to `4.21.25` and change replicas to `3`.

#### Proof 1 (log -- error)

```
fail [github.com/Azure/ARO-HCP/test/e2e/nodepool_version_upgrade.go:210]: failed to upgrade node pool npupgrade-4-20 to version 4.21.25
Unexpected error:
    <*fmt.wrapErrors | 0xc0027f6060>:
    failed waiting for nodepool="npupgrade-4-20" in cluster="np-version-upgrade-cluster-9cldvc" resourcegroup="rg-np-version-upgrade-9cldvc-x78hlk" to finish updating, caused by: timeout '45.000000' minutes exceeded during UpdateNodePoolAndWait for nodepool npupgrade-4-20 in cluster np-version-upgrade-cluster-9cldvc in resource group rg-np-version-upgrade-9cldvc-x78hlk, error: context deadline exceeded
    ...
occurred
```

### 1. Q: Why did the RP backend keep the operation in `Updating`?

**A:** Because the HyperShift NodePool's `spec.replicas` was `2` while the desired state was `3`. The backend repeatedly reported `hypershift NodePool replicas is 2, want 3`.

#### Proof 1 (kusto)

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where timestamp between (datetime(2026-07-20T21:32:50Z) .. datetime(2026-07-20T22:18:05Z))
| where resource_group == 'rg-np-version-upgrade-9cldvc-x78hlk'
| where resource_name == 'npupgrade-4-20'
| where log.controller_name == 'operationnodepoolupdate'
| where tostring(log.msg) == 'picked node pool update operation status'
| summarize firstSeen=min(timestamp), lastSeen=max(timestamp), samples=count() by provisioningState=tostring(log.provisioningState), message=tostring(log.message)
| order by firstSeen asc
```

| provisioningState | message | firstSeen | lastSeen | samples |
| --- | --- | --- | --- | --- |
| Updating | [clusterServiceNodePoolStatus] <no_message>; [hypershiftNodePool] hypershift NodePool replicas is 2, want 3 | 2026-07-20T21:32:58.68Z | 2026-07-20T22:17:58.322Z | 225 |

### 2. Q: Why was the HyperShift NodePool's `spec.replicas` stuck at 2?

**A:** Because Clusters Service correctly wrote `replicas=3` to the ManifestWork, but 700ms later the upgrade policy worker overwrote the ManifestWork with a stale version that had `replicas=2`. The HyperShift NodePool briefly showed `specReplicas=3` for ~600ms before reverting to `2`.

#### Proof 1 (kusto -- HyperShift NodePool snapshots showing the revert)

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesResourceSnapshots')
| where timestamp between (datetime(2026-07-20T21:32:50Z) .. datetime(2026-07-20T22:18:05Z))
| where apiVersion == 'hypershift.openshift.io/v1beta1'
| where objectKind == 'NodePool'
| where namespace == 'ocm-arohcpint-2rm44ag6ca53sf0k892iva6rn0nbbmik'
| where name == 'f6i4v1b5u3u5b4f-npupgrade-4-20'
| project observedTime=timestamp, specReplicas=toint(object.spec.replicas), statusReplicas=toint(object.status.replicas)
| order by observedTime asc
| serialize
| extend prevSpec = prev(specReplicas)
| extend prevStatus = prev(statusReplicas)
| extend newGroup = iif(specReplicas != prevSpec or statusReplicas != prevStatus or isnull(prevSpec), 1, 0)
| extend groupId = row_cumsum(newGroup)
| summarize from_time=min(observedTime), to_time=max(observedTime), specReplicas=take_any(specReplicas), statusReplicas=take_any(statusReplicas), observations=count() by groupId
| project-away groupId
| order by from_time asc
```

| from_time | to_time | specReplicas | statusReplicas | observations |
| --- | --- | --- | --- | --- |
| 7/20/2026, 9:33:03.273 PM | 7/20/2026, 9:33:03.659 PM | 3 | 2 | 3 |
| 7/20/2026, 9:33:03.938 PM | 7/20/2026, 9:38:58.458 PM | 2 | 2 | 15 |
| 7/20/2026, 9:38:58.693 PM | 7/20/2026, 9:41:57.889 PM | 2 | 3 | 13 |
| 7/20/2026, 9:41:58.054 PM | 7/20/2026, 9:47:53.198 PM | 2 | 2 | 7 |
| 7/20/2026, 9:47:53.467 PM | 7/20/2026, 9:50:51.162 PM | 2 | 3 | 8 |
| 7/20/2026, 9:50:52.226 PM | 7/20/2026, 9:50:52.783 PM | 2 | 2 | 5 |

The NodePool had `specReplicas=3` for only ~600ms (9:33:03.273 to 9:33:03.938) before being overwritten back to `2`. The subsequent `statusReplicas` fluctuations between 2 and 3 are HyperShift scaling up/down in response to the wrong spec.

#### Proof 2 (kusto -- CS database correctly had replicas=3)

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where timestamp between (datetime(2026-07-20T12:33:00Z) .. datetime(2026-07-21T22:30:05Z))
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'csstatedump'
| where log.msg == 'cluster-service node pool state dump'
| where log.resource_group == 'rg-np-version-upgrade-9cldvc-x78hlk'
| where log.resource_name == 'np-version-upgrade-cluster-9cldvc'
| where log.csNodePool.id =~ 'npupgrade-4-20'
| summarize csNodePool=take_any(log.csNodePool) by timestamp
| sort by timestamp asc
| project observedTime=timestamp, specReplicas=toint(csNodePool.replicas), statusReplicas=toint(csNodePool.status.current_replicas)
| serialize
| extend prevSpec = prev(specReplicas)
| extend prevStatus = prev(statusReplicas)
| extend newGroup = iif(specReplicas != prevSpec or statusReplicas != prevStatus or isnull(prevSpec), 1, 0)
| extend groupId = row_cumsum(newGroup)
| summarize from_time=min(observedTime), to_time=max(observedTime), specReplicas=take_any(specReplicas), statusReplicas=take_any(statusReplicas), observations=count() by groupId
| project-away groupId
| order by from_time asc
```

| from_time | to_time | specReplicas | statusReplicas | observations |
| --- | --- | --- | --- | --- |
| 7/20/2026, 9:24:57.514 PM | 7/20/2026, 9:32:11.178 PM | 2 | 0 | 18 |
| 7/20/2026, 9:32:22.323 PM | 7/20/2026, 9:32:22.323 PM | 2 | 2 | 1 |
| 7/20/2026, 9:33:20.098 PM | 7/20/2026, 10:21:58.915 PM | 3 | 2 | 101 |

The CS database correctly stored `specReplicas=3` from 9:33:20 onwards for the entire test duration. The problem was only in the ManifestWork on the management cluster.

#### Proof 3 (kusto -- ManifestWork never had replicas=3)

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-07-20T21:32:50Z) .. datetime(2026-07-20T22:18:05Z))
| where log contains 'f6i4v1b5u3u5b4f-npupgrade-4-20'
| where log contains 'NodePoolManifestWork for cluster'
| extend rawJson = extract(@"NodePoolManifestWork for cluster '[^']+': (.+)", 1, tostring(log.msg))
| extend mw = parse_json(rawJson)
| extend manifest = mw.spec.workload.manifests[0]
| extend specReplicas = toint(manifest.spec.replicas)
| extend statusReplicas = toint(manifest.status.replicas)
| project observedTime=timestamp, specReplicas, statusReplicas
| order by observedTime asc
| serialize
| extend prevSpec = prev(specReplicas)
| extend prevStatus = prev(statusReplicas)
| extend newGroup = iif(specReplicas != prevSpec or statusReplicas != prevStatus or isnull(prevSpec), 1, 0)
| extend groupId = row_cumsum(newGroup)
| summarize from_time=min(observedTime), to_time=max(observedTime), specReplicas=take_any(specReplicas), statusReplicas=take_any(statusReplicas), observations=count() by groupId
| project-away groupId
| order by from_time asc
```

| from_time | to_time | specReplicas | statusReplicas | observations |
| --- | --- | --- | --- | --- |
| 7/20/2026, 9:33:05.622 PM | 7/20/2026, 10:18:01.813 PM | 2 | 0 | 56 |

The ManifestWork (as observed by the CS status controller) never showed `specReplicas=3` -- the node pool worker's patch was overwritten before the next observation.

### 3. Q: Why did the upgrade policy worker overwrite the replica change?

**A:** Both workers use the same `PatchNodePoolCR` function which performs a read-modify-write on the ManifestWork. The upgrade policy worker read the ManifestWork *before* the node pool worker's patch landed, then wrote back a version with only the version field changed -- but against a base that still had `replicas=2`. This replaced the entire NodePool manifest entry in the ManifestWork.

#### Proof 1 (kusto -- CS logs showing overlapping operations)

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-07-20T21:32:50Z) .. datetime(2026-07-20T21:34:00Z))
| where log has '2rm44ag6ca53sf0k892iva6rn0nbbmik'
| where log has 'Successfully patched node pool CR'
    or log has 'Processing ARO-HCP upgrade policy'
    or log has 'Upgrade started for policy'
    or log has_cs 'processing'
| project timestamp, msg=tostring(log.msg), caller=tostring(log.caller)
| order by timestamp asc
```

| timestamp | msg | caller |
| --- | --- | --- |
| 7/20/2026, 9:33:00.907 PM | processing 'validating_update' node pool 'npupgrade-4-20' for cluster '2rm44ag6ca53sf0k892iva6rn0nbbmik' | azure/aro_hcp_node_pool_worker.go:230 |
| 7/20/2026, 9:33:01.809 PM | processing 'pending_update' node pool 'npupgrade-4-20' for cluster '2rm44ag6ca53sf0k892iva6rn0nbbmik' | azure/aro_hcp_node_pool_worker.go:230 |
| 7/20/2026, 9:33:02.547 PM | Processing ARO-HCP upgrade policy '943ce839-8482-11f1-bb23-12f844e80b9c' for node pool 'npupgrade-4-20' in cluster '2rm44ag6ca53sf0k892iva6rn0nbbmik' | azure/aro_hcp_node_pool_upgrade_policy_worker.go:150 |
| 7/20/2026, 9:33:02.666 PM | Successfully patched node pool CR for node pool 'npupgrade-4-20' cluster '2rm44ag6ca53sf0k892iva6rn0nbbmik' | aro/node_pool_cr_helpers.go:93 |
| 7/20/2026, 9:33:03.372 PM | Successfully patched node pool CR for node pool 'npupgrade-4-20' cluster '2rm44ag6ca53sf0k892iva6rn0nbbmik' | aro/node_pool_cr_helpers.go:93 |
| 7/20/2026, 9:33:03.385 PM | Upgrade started for policy '943ce839-8482-11f1-bb23-12f844e80b9c' node pool 'npupgrade-4-20' cluster '2rm44ag6ca53sf0k892iva6rn0nbbmik' | azure/aro_hcp_node_pool_upgrade_policy_worker.go:361 |

The two `Successfully patched node pool CR` lines at 9:33:02.666 and 9:33:03.372 show both workers patching the same ManifestWork 700ms apart. The second patch (from the upgrade policy worker, confirmed by the immediately following "Upgrade started" log) overwrote the first.

#### Proof 2 (code -- the race mechanism)

Both workers call `PatchNodePoolCR` (`pkg/nodepoolprovisioner/acm/aro/node_pool_cr_helpers.go:24-96`):

```go
func PatchNodePoolCR(ctx context.Context, ...) error {
    // 1. READ: Get current ManifestWork from management cluster
    manifestWork := workv1.ManifestWork{}
    err = client.Get(ctx, key, &manifestWork)

    // 2. MODIFY: DeepCopy and update the NodePool within it
    newManifestWork := manifestWork.DeepCopy()
    err = acm.UpdateNodePool(newManifestWork, cluster, nodePool)

    // 3. WRITE: Patch back using MergeFrom (diff against the read version)
    err = client.Patch(ctx, newManifestWork, clnt.MergeFrom(&manifestWork))
}
```

`acm.UpdateNodePool` (`pkg/acm/manifestwork.go:42-93`) replaces the **entire NodePool manifest entry** in the ManifestWork's `Spec.Workload.Manifests` array:

```go
func UpdateNodePool(manifestWork *workv1.ManifestWork, ...) error {
    // Calls MapNodePoolModel to update fields, then replaces the whole manifest entry
    updatedManifests = append(updatedManifests, workv1.Manifest{
        RawExtension: runtime.RawExtension{Object: updatedNodePool},
    })
    manifestWork.Spec.Workload.Manifests = updatedManifests
}
```

#### Proof 3 (code -- why the upgrade policy worker uses a partial model)

The upgrade policy worker (`cmd/clusters-service/service/azure/aro_hcp_node_pool_upgrade_policy_worker.go:254-258`) constructs a minimal patch model with only `Version` set:

```go
npPatch := &models.NodePool{
    ID:        nodePool.ID,
    ClusterID: nodePool.ClusterID,
    Version:   targetVersion,
}
```

`hypershift.MapNodePoolModel` (`pkg/hypershift/node_pools.go:72+`) only sets fields that are non-nil on the model:

```go
func MapNodePoolModel(in *hsv1beta1.NodePool, ...) {
    if model.Replicas != nil {  // nil for upgrade worker -- replicas NOT set
        in.Spec.Replicas = ptr.To[int32](int32(*model.Replicas))
    }
    if model.Version != nil {   // non-nil for upgrade worker -- version IS set
        releaseImage := buildReleaseImage(model.Version, cluster)
        if releaseImage != "" {
            in.Spec.Release.Image = releaseImage
        }
    }
}
```

Since the upgrade policy worker's model has `Replicas=nil`, `MapNodePoolModel` does not touch replicas -- it preserves whatever was in the **base ManifestWork that was read**. But because the read happened before the node pool worker's patch landed, the base still had `replicas=2`, and that stale value was written back.

### 4. Q: Why was there no recovery?

**A:** The node pool worker only processes node pools in `pending_update` state. After successfully patching (from its perspective), it transitioned the node pool to `updating` state. The upgrade policy worker then overwrote the ManifestWork. No subsequent reconciliation re-reads the CS database's desired replicas and re-applies them to the ManifestWork, so the stale `replicas=2` persisted indefinitely.

## What is Proven vs. Not Proven

### Proven

- The CS database correctly stored `replicas=3` (from the csstatedump logs).
- The HyperShift NodePool briefly showed `specReplicas=3` for ~600ms before reverting to `2`.
- Two `PatchNodePoolCR` calls executed concurrently on the same node pool within 700ms.
- The upgrade policy worker was the second writer (confirmed by the "Upgrade started" log immediately after the second patch).
- The ManifestWork (as observed by the status controller) never showed `replicas=3` in any subsequent observation.

### Not Proven

- The exact Kubernetes resource version of each patch (would require API server audit logs).

## Suggestions

### Fix the race condition (pick one or combine)

1. **Unify the workers**: Have the upgrade policy worker not patch the ManifestWork directly. Instead, it should only update the node pool's target version in the CS database and let the existing node pool worker (which already handles `pending_update`) be the single writer to the ManifestWork. This eliminates the dual-writer problem entirely.

2. **Use optimistic concurrency on the ManifestWork**: Change `PatchNodePoolCR` to use a server-side apply or include the `resourceVersion` in the patch so that a concurrent write results in a conflict error that can be retried with a fresh read. This would cause the second writer to re-read the ManifestWork (now with `replicas=3`) and produce a correct patch.

3. **Have the upgrade policy worker use the full node pool model**: Instead of constructing a minimal `npPatch` with only `Version`, read the full current node pool from the database and pass it to `PatchNodePoolCR`. Then even if it writes second, it would write `replicas=3` because that is what the database has. This is fragile (still relies on the DB being updated first) but would have prevented this specific instance.

4. **Add a reconciliation loop**: Have a periodic worker that compares the ManifestWork's NodePool spec against the CS database's desired state and corrects any drift. This acts as a safety net for any race or transient failure.

### Improve observability

- Add the worker/caller name to the `"Successfully patched node pool CR"` log message so races can be identified without needing to infer from surrounding lines.
- Log the ManifestWork `resourceVersion` before and after patching to detect concurrent modifications.
- Add a metric for ManifestWork patch conflicts / retries once optimistic concurrency is implemented.

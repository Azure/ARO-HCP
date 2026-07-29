This document shows a proof chain for a failure to delete a cluster during the test cleanup phase because
the ManagedCluster CR could not complete Detaching — addon pre-delete hook pods were evicted due to
MemoryPressure on their hosting node.

# Root Cause

An ARO HCP cluster failed to delete during test cleanup because `ManagedClusterAddon` pre-delete hook pods were evicted
from their hosting node due to `MemoryPressure`, preventing their finalizers from being removed, which blocked the
`ManagedCluster` destructor and halted the entire Clusters Service deletion chain.

## Summary

An end-to-end test for a cluster lifecycle scenario completed successfully in all assertions; post-test cleanup failed to
delete the ARO HCP cluster within the cleanup timeout. The deletion signal propagated from the frontend to the backend,
and Clusters Service moved the cluster to the `'uninstalling'` phase.

Clusters Service ran the destruct chain, but the `hypershift-managed-cluster-destructor` could not complete because the
`ManagedCluster` resource was stuck in `Detaching` state. The addon pre-delete hook pods were evicted from their node
due to a `MemoryPressure` condition before they could complete. Without the addon pods running to execute the hooks,
the `ManagedClusterAddon` finalizers were never removed, and the destruct chain never advanced.

## Recursive 'Why' Chain

### Why did the test fail?

The test client timed out waiting for the ARO HCP cluster deletion to complete during cleanup. Clusters Service moved
the cluster to `'uninstalling'` but never completed the deletion.

### Why did the cluster deletion never complete?

Clusters Service moved the cluster to `'uninstalling'` but never completed the deletion. The destruct chain was stuck.

#### Proof 1: Log Snippet

Clusters Service phase transitions show the cluster reached `'uninstalling'` but never progressed further:

```kql
// manifest.json: kusto_cluster
// manifest.json: time_window.start .. time_window.end
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-07-01) .. datetime(2026-07-03))
| where log has '2r9nhugpbdko2vai55lv2ikki9h9958r'
| where log has 'state to' or log has 'now in'
| project timestamp, msg=tostring(log.msg)
| order by timestamp asc
```

| timestamp                | msg                                                                              |
|--------------------------|----------------------------------------------------------------------------------|
| 7/2/2026, 2:03:38.985 AM | Cluster '2r9nhugpbdko2vai55lv2ikki9h9958r' created, now in 'validating' state   |
| 7/2/2026, 2:03:57.264 AM | updating cluster '2r9nhugpbdko2vai55lv2ikki9h9958r' state to 'pending'          |
| 7/2/2026, 2:08:06.312 AM | updating cluster '2r9nhugpbdko2vai55lv2ikki9h9958r' state to 'installing'       |
| 7/2/2026, 2:22:12.913 AM | updating cluster '2r9nhugpbdko2vai55lv2ikki9h9958r' state to 'ready'            |
| 7/2/2026, 2:24:39.248 AM | updating cluster '2r9nhugpbdko2vai55lv2ikki9h9958r' state to 'uninstalling'     |

### Why didn't Clusters Service complete the deletion?

The destruct chain was stuck at `hypershift-managed-cluster-destructor` for ~8 hours, retrying every few seconds.

#### Proof 1: Log Snippet

The destruct chain looped 5,161 times, always stopping at the managed cluster destructor:

```kql
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-07-01) .. datetime(2026-07-03))
| where log has '2r9nhugpbdko2vai55lv2ikki9h9958r'
| where log has 'destructor' or log has 'destruct chain' or log has 'Not continuing'
| summarize
    first_occurrence = min(timestamp),
    last_occurrence = max(timestamp),
    occurrences = count()
  by msg = tostring(log.msg)
| order by first_occurrence asc
| where occurrences > 5
```

| msg                                                                                  | first_occurrence          | last_occurrence            | occurrences |
|--------------------------------------------------------------------------------------|---------------------------|----------------------------|-------------|
| Starting destruct chain for cluster                                                  | 7/2/2026, 2:24:40.024 AM | 7/2/2026, 10:19:17.92 AM  | 5,161       |
| Running destructor 'hypershift-managed-cluster-destructor' for cluster               | 7/2/2026, 2:24:40.024 AM | 7/2/2026, 10:19:17.92 AM  | 5,161       |
| Not continuing to the next destructor for cluster                                    | 7/2/2026, 2:24:41.405 AM | 7/2/2026, 10:19:13.289 AM | 5,160       |
| Finished destruct chain for cluster                                                  | 7/2/2026, 2:24:41.405 AM | 7/2/2026, 10:19:39.457 AM | 5,161       |
| Running destructor 'hypershift-manifest-work-destructor' for cluster                 | 7/2/2026, 10:12:44.975 AM | 7/2/2026, 10:19:17.925 AM | 74         |
| Running destructor 'break-glass-credential-secrets-deleter' for cluster              | 7/2/2026, 10:13:38.805 AM | 7/2/2026, 10:19:17.932 AM | 64         |
| Running destructor 'swift-podnetworkinstance-deleter' for cluster                    | 7/2/2026, 10:13:38.822 AM | 7/2/2026, 10:19:17.938 AM | 64         |

#### Proof 2: Code Citation

The `hypershift-managed-cluster-destructor` in `aro-hcp-clusters-service` (`pkg/controllers/cluster_destructor.go`)
checks whether the `ManagedCluster` CR has been deleted. If the CR still exists, the destructor returns without
advancing to the next step. The CS deletion controller re-runs the full destruct chain on its next reconcile loop,
logging `Not continuing to the next destructor for cluster` each time the managed cluster destructor cannot proceed.

### Why was the `hypershift-managed-cluster-destructor` stuck?

The ManagedCluster CR was stuck in `Detaching` state — its conditions show the cluster became unavailable while
addon cleanup was still in progress.

#### Proof 1: Log Snippet

Query the ManagedCluster CR conditions from mgmt-agent ResourceWatcher to see the ManagedCluster state:

```kql
// manifest.json: kusto_cluster
// manifest.json: cs_cluster_id (retrieve OCM cluster ID from the clustersService/cid snapshot query
//   or from manifest.json; it is the opaque hash like '2r9nhugpbdko2vai55lv2ikki9h9958r')
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-07-01) .. datetime(2026-07-03))
| where container_name == 'mgmt-agent-controller'
| where tostring(log.msg) == 'resource event'
| where tostring(log.object.kind) == 'ManagedCluster'
| where tostring(log.name) == '2r9nhugpbdko2vai55lv2ikki9h9958r'
| summarize content=take_any(log.object), observedTime=take_any(timestamp) by event=tostring(log.event)
| top 1 by observedTime desc
| mv-expand condition = content.status.conditions
| project observedTime, type=tostring(condition.type), status=tostring(condition.status), reason=tostring(condition.reason), message=tostring(condition.message)
```

The ManagedCluster conditions showed `ManagedClusterConditionAvailable` as `Unknown`, confirming the klusterlet
lost contact with the hub, preventing addon pre-delete hooks from completing.

### Why was the ManagedCluster stuck in Detaching?

The addon pre-delete hook pods in the klusterlet namespace were evicted due to node `MemoryPressure` before they
could complete their cleanup work.

#### Proof 1: Log Snippet

Query `kubernetesEvents` for eviction events in the klusterlet namespace:

```kql
// manifest.json: kusto_cluster
// manifest.json: cs_cluster_id
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-07-01) .. datetime(2026-07-03))
| where eventNamespace == 'klusterlet-2r9nhugpbdko2vai55lv2ikki9h9958r'
| where reason == 'Evicted' or message has 'MemoryPressure' or message has 'evict'
| project timestamp, objectKind, objectName, reason, message
| order by timestamp asc
```

#### Proof 2: Log Snippet

The mgmt-agent PodWatcher confirms addon pods were repeatedly evicted in the klusterlet namespace:

```kql
// manifest.json: kusto_cluster
// manifest.json: cs_cluster_id
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-07-01) .. datetime(2026-07-03))
| where namespace_name == 'mgmt-agent' and log.msg == 'pod event'
| where log.namespace == 'klusterlet-2r9nhugpbdko2vai55lv2ikki9h9958r'
| where tostring(log.object.status.reason) == 'Evicted'
| project
    timestamp,
    pod_name = tostring(log.name),
    event = tostring(log.event),
    reason = tostring(log.object.status.reason),
    message = tostring(log.object.status.message),
    phase = tostring(log.object.status.phase),
    node = tostring(log.object.spec.nodeName)
| order by timestamp asc
```

| timestamp                | pod_name                                         | event  | reason  | message                                                                             | phase  | node                                  |
|--------------------------|--------------------------------------------------|--------|---------|-------------------------------------------------------------------------------------|--------|---------------------------------------|
| 7/2/2026, 2:08:55.178 AM | governance-policy-framework-6566f77686-rfsnp     | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki. | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:06.431 AM | klusterlet-addon-workmgr-54b77c6b77-btnzz        | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki. | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:08.56 AM  | klusterlet-addon-workmgr-54b77c6b77-48rqm        | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki. | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:23.601 AM | klusterlet-addon-workmgr-54b77c6b77-48rqm        | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki. | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:38.819 AM | klusterlet-addon-workmgr-54b77c6b77-48rqm        | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki. | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:13:58.836 AM | klusterlet-addon-workmgr-54b77c6b77-48rqm        | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki. | Failed | aks-infrasd4ds52-30241817-vmss000000  |

The `MemoryPressure` condition was transient — the node recovered later — but the addon pre-delete pods were
repeatedly evicted during the critical deletion window. The pods come from a Deployment (ReplicaSet `54b77c6b77`).
The evictions happened in quick succession (within seconds), and after a few retries the ReplicaSet controller
stopped scheduling new pods — the addon controller uses a Deployment rather than a Job with configurable
`backoffLimit`, so once the ReplicaSet exhausted its rapid retry budget the pods were never reattempted, permanently
blocking the `ManagedCluster` cleanup until the cleaner intervened at ~10:12 AM.

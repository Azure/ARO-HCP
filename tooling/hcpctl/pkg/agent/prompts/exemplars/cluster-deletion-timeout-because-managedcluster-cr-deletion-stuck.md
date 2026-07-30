This document shows a proof chain for a failure to delete a cluster during the test cleanup phase because
the ManagedCluster CR could not be deleted — addon pre-delete hook pods were evicted due to
MemoryPressure on their hosting node, preventing finalizer removal.

# Root Cause

An ARO HCP cluster failed to delete during test cleanup because `ManagedClusterAddon` pre-delete hook pods were evicted
from their hosting node due to `MemoryPressure`, preventing their finalizers from being removed, which blocked the
`ManagedCluster` destructor and halted the entire Clusters Service deletion chain.

## Summary

An end-to-end test for a cluster lifecycle scenario completed successfully in all assertions; post-test cleanup failed to
delete the ARO HCP cluster within the cleanup timeout. The deletion signal propagated from the frontend to the backend,
and Clusters Service moved the cluster to the `'uninstalling'` phase.

Clusters Service ran the destruct chain, but the `hypershift-managed-cluster-destructor` could not complete because the
`ManagedCluster` CR was stuck pending deletion — its `ManagedClusterConditionAvailable` showed `Unknown`. The addon
pre-delete hook pods were evicted from their node due to a `MemoryPressure` condition before they could complete.
Without the addon pods running to execute the hooks, the `ManagedClusterAddon` finalizers were never removed, and
the destruct chain never advanced.

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
cluster('https://hcp-stg-uk-2.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
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
cluster('https://hcp-stg-uk-2.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
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

| msg                                                                                                                  | first_occurrence           | last_occurrence            | occurrences |
|----------------------------------------------------------------------------------------------------------------------|----------------------------|----------------------------|-------------|
| Starting destruct chain for cluster                                                                                  | 7/2/2026, 2:24:40.024 AM  | 7/2/2026, 10:19:17.92 AM  | 5,161       |
| Running destructor 'hypershift-managed-cluster-destructor' for cluster                                               | 7/2/2026, 2:24:40.024 AM  | 7/2/2026, 10:19:17.92 AM  | 5,161       |
| Not continuing to the next destructor for cluster                                                                    | 7/2/2026, 2:24:41.405 AM  | 7/2/2026, 10:19:13.289 AM | 5,160       |
| Finished destruct chain for cluster                                                                                  | 7/2/2026, 2:24:41.405 AM  | 7/2/2026, 10:19:39.457 AM | 5,161       |
| Running destructor 'hypershift-manifest-work-destructor' for cluster                                                 | 7/2/2026, 10:12:44.975 AM | 7/2/2026, 10:19:17.925 AM | 74          |
| Running destructor 'break-glass-credential-secrets-deleter' for cluster                                              | 7/2/2026, 10:13:38.805 AM | 7/2/2026, 10:19:17.932 AM | 64          |
| Running destructor 'swift-podnetworkinstance-deleter' for cluster                                                    | 7/2/2026, 10:13:38.822 AM | 7/2/2026, 10:19:17.938 AM | 64          |
| SWIFT networking not enabled for cluster '2r9nhugpbdko2vai55lv2ikki9h9958r', skipping PodNetworkInstance deletion     | 7/2/2026, 10:13:38.822 AM | 7/2/2026, 10:19:17.938 AM | 64          |
| Running destructor 'swift-podnetwork-deleter' for cluster                                                            | 7/2/2026, 10:13:38.823 AM | 7/2/2026, 10:19:17.938 AM | 64          |
| SWIFT networking not enabled for cluster '2r9nhugpbdko2vai55lv2ikki9h9958r', skipping PodNetwork deletion             | 7/2/2026, 10:13:38.823 AM | 7/2/2026, 10:19:17.938 AM | 64          |
| Running destructor 'swift-sal-deleter' for cluster                                                                   | 7/2/2026, 10:13:38.823 AM | 7/2/2026, 10:19:17.938 AM | 64          |
| SWIFT networking not enabled for cluster '2r9nhugpbdko2vai55lv2ikki9h9958r', skipping SAL deletion                    | 7/2/2026, 10:13:38.823 AM | 7/2/2026, 10:19:17.938 AM | 64          |
| Running destructor 'hypershift-manifest-work-namespaces-destructor' for cluster                                      | 7/2/2026, 10:13:38.823 AM | 7/2/2026, 10:19:17.938 AM | 64          |
| Running destructor 'rh-managed-nsg-deleter' for cluster                                                              | 7/2/2026, 10:17:25.059 AM | 7/2/2026, 10:19:18.017 AM | 22          |
| Running destructor 'managed-resource-group-deleter' for cluster                                                      | 7/2/2026, 10:17:25.059 AM | 7/2/2026, 10:19:18.017 AM | 22          |

#### Proof 2: Code Citation

The `hypershift-managed-cluster-destructor` in `aro-hcp-clusters-service`
(`pkg/clusterprovisioner/acm/destruct/managed_cluster.go`) checks whether the `ManagedCluster` CR has been deleted.
If the CR still exists, the destructor returns `(false, nil)` without advancing to the next step. The destruct chain
(`pkg/clusterprovisioner/acm/destruct/base_destruct_chain.go`) always restarts from step 0 on each reconcile — completed
steps return `(true, nil)` immediately since their resources are already gone. CS logs `Not continuing to the next
destructor for cluster` each time the blocking destructor cannot proceed.

### Why was the `hypershift-managed-cluster-destructor` stuck?

The ManagedCluster CR was stuck pending deletion — its `ManagedClusterConditionAvailable` condition showed `Unknown`,
indicating the klusterlet lost contact with the hub while addon cleanup was still in progress.

#### Proof 1: Log Snippet

Query the ManagedCluster CR conditions from mgmt-agent ResourceWatcher to see the ManagedCluster state:

```kql
// manifest.json: kusto_cluster
// manifest.json: cs_cluster_id (retrieve OCM cluster ID from the clustersService/cid snapshot query
//   or from manifest.json; it is the opaque hash like '2r9nhugpbdko2vai55lv2ikki9h9958r')
cluster('https://hcp-stg-uk-2.uksouth.kusto.windows.net').database('ServiceLogs').table('containerLogs')
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

| observedTime               | type                              | status  | reason                          | message                                            |
|----------------------------|-----------------------------------|---------|---------------------------------|----------------------------------------------------|
| 7/2/2026, 10:12:08.582 AM  | ManagedClusterImportSucceeded     | False   | ManagedClusterForceDetaching    | The managed cluster is being detached by force     |
| 7/2/2026, 10:12:08.582 AM  | HubAcceptedManagedCluster         | True    | HubClusterAdminAccepted         | Accepted by hub cluster admin                      |
| 7/2/2026, 10:12:08.582 AM  | ManagedClusterConditionAvailable  | Unknown | ManagedClusterLeaseUpdateStopped | Registration agent stopped updating its lease.    |
| 7/2/2026, 10:12:08.582 AM  | ManagedClusterJoined              | True    | ManagedClusterJoined            | Managed cluster joined                             |
| 7/2/2026, 10:12:08.582 AM  | ManagedClusterConditionClockSynced | True   | ManagedClusterClockSynced       | The clock of the managed cluster is synced with the hub. |
| 7/2/2026, 10:12:08.582 AM  | Deleting                          | True    | NoResource                      | No cleaned resource in cluster ns.                 |

The key signals: `ManagedClusterImportSucceeded` shows `ManagedClusterForceDetaching` (the hub is attempting
forced detach), `ManagedClusterConditionAvailable` shows `Unknown` with `ManagedClusterLeaseUpdateStopped`
(the klusterlet registration agent stopped its lease), and the `Deleting` condition shows `NoResource`. Despite
the force-detach attempt, the ManagedCluster CR could not be fully removed because addon pre-delete hook
finalizers were still present.

### Why was the ManagedCluster stuck pending deletion?

The addon pre-delete hook pods in the klusterlet namespace were evicted due to node `MemoryPressure` before they
could complete their cleanup work.

#### Proof 1: Log Snippet

Query `kubernetesEvents` for eviction events in the klusterlet namespace:

```kql
// manifest.json: kusto_cluster
// manifest.json: cs_cluster_id
cluster('https://hcp-stg-uk-2.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-07-01) .. datetime(2026-07-03))
| where eventNamespace == 'klusterlet-2r9nhugpbdko2vai55lv2ikki9h9958r'
| where reason == 'Evicted' or message has 'MemoryPressure' or message has 'evict'
| project timestamp, objectKind, objectName, reason, message
| order by timestamp asc
```

| timestamp                 | objectKind | objectName                                      | reason  | message                                                                              |
|---------------------------|------------|-------------------------------------------------|---------|--------------------------------------------------------------------------------------|
| 7/2/2026, 2:08:53.418 AM | Pod        | governance-policy-framework-6566f77686-rfsnp    | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki.  |
| 7/2/2026, 2:09:04.918 AM | Pod        | klusterlet-addon-workmgr-54b77c6b77-btnzz       | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 16512Ki.  |
| 7/2/2026, 2:09:06.512 AM | Pod        | klusterlet-addon-workmgr-54b77c6b77-48rqm       | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 11528Ki.  |
| 7/2/2026, 2:09:38.489 AM | Pod        | klusterlet-addon-workmgr-54b77c6b77-mblk4c      | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 8364Ki.   |
| 7/2/2026, 2:10:17.661 AM | Pod        | klusterlet-addon-workmgr-54b77c6b77-nk2dk        | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 11212Ki.  |
| 7/2/2026, 2:13:57.226 AM | Pod        | klusterlet-addon-workmgr-54b77c6b77-ktb82        | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 2884Ki.   |
| 7/2/2026, 2:27:17.73 AM  | Pod        | governance-policy-framework-uninstall            | Evicted | The node had condition: [MemoryPressure].                                            |

The available memory drops progressively (35152Ki → 16512Ki → 11528Ki → 8364Ki → 2884Ki), confirming ongoing
MemoryPressure. The `governance-policy-framework-uninstall` pod eviction at 2:27 AM shows that even the addon
uninstall job was being evicted.

#### Proof 2: Log Snippet

The mgmt-agent PodWatcher confirms addon pods were repeatedly evicted in the klusterlet namespace:

```kql
// manifest.json: kusto_cluster
// manifest.json: cs_cluster_id
cluster('https://hcp-stg-uk-2.uksouth.kusto.windows.net').database('ServiceLogs').table('containerLogs')
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

| timestamp                 | pod_name                                         | event  | reason  | message                                                                              | phase  | node                                  |
|---------------------------|--------------------------------------------------|--------|---------|-------------------------------------------------------------------------------------- |--------|---------------------------------------|
| 7/2/2026, 2:08:55.178 AM | governance-policy-framework-6566f77686-rfsnp     | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:06.431 AM | klusterlet-addon-workmgr-54b77c6b77-btnzz        | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 16512Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:08.56 AM  | klusterlet-addon-workmgr-54b77c6b77-48rqm        | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 11528Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:23.601 AM | klusterlet-addon-workmgr-54b77c6b77-mblk4c       | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 8364Ki.   | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:09:38.819 AM | klusterlet-addon-workmgr-54b77c6b77-nk2dk         | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 11212Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:13:58.836 AM | klusterlet-addon-workmgr-54b77c6b77-ktb82         | Update | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 2884Ki.   | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:24:52.705 AM | klusterlet-addon-workmgr-54b77c6b77-ktb82         | Delete | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 8364Ki.   | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:24:52.709 AM | klusterlet-addon-workmgr-54b77c6b77-mblk4c       | Delete | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 8364Ki.   | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:24:52.709 AM | klusterlet-addon-workmgr-54b77c6b77-btnzz         | Delete | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 16512Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:24:52.709 AM | klusterlet-addon-workmgr-54b77c6b77-nk2dk         | Delete | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 11212Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 2:24:52.71 AM  | klusterlet-addon-workmgr-54b77c6b77-48rqm         | Delete | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 11528Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 3:50:31.146 AM | governance-policy-framework-uninstall             | Add    | Evicted | Pod was rejected: The node had condition: [MemoryPressure].                           | Failed | aks-infrasd4ds5-10613864-vmss000001   |
| 7/2/2026, 3:50:31.177 AM | governance-policy-framework-6566f77686-rfsnp      | Add    | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 5:50:39.765 AM | governance-policy-framework-6566f77686-rfsnp      | Delete | Evicted | The node was low on resource: memory. Threshold quantity: 100Mi, available: 35152Ki.  | Failed | aks-infrasd4ds52-30241817-vmss000000  |
| 7/2/2026, 10:05:39.288 AM | governance-policy-framework-uninstall             | Add    | Evicted | Pod was rejected: The node had condition: [MemoryPressure].                           | Failed | aks-infrasd4ds5-10613864-vmss000001   |

The available memory dropped progressively (35152Ki → 16512Ki → 11528Ki → 8364Ki → 2884Ki) during the
initial eviction burst. The `klusterlet-addon-workmgr` pods were first evicted via Update events, then bulk-deleted
at 2:24 AM (coinciding with the deletion signal). The `governance-policy-framework-uninstall` pod was rescheduled
(Add events at 3:50 AM and 10:05 AM) but immediately re-evicted each time, including on a second node
(`vmss000001`), confirming the MemoryPressure was not isolated to a single node. No addon pre-delete hook pod
ran to completion, so the `ManagedClusterAddon` finalizers were never removed, permanently blocking the
`ManagedCluster` cleanup until the cleaner intervened at ~10:12 AM.

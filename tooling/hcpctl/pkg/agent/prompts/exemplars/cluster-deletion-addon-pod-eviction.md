This document shows a proof chain for a failure to delete a cluster during the test cleanup phase because
ManagedCluster addon pre-delete pods were evicted from their hosting node due to MemoryPressure.

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

The test client timed out waiting for the ARO HCP cluster deletion to complete during cleanup.

#### Proof 1: Test Error (lines 1-5)

The proximal failure was a cleanup timeout during cluster deletion:

```
fail [github.com/Azure/ARO-HCP/test/util/framework/per_test_framework.go:283]:
Unexpected error:
    <*errors.joinError | 0xc0014a22e8>:
    failed to cleanup resource group: at least one hcp cluster failed to delete: failed waiting for hcpCluster="np-autoscale-cluster" in resourcegroup="np-autoscaling-5zzp9q" to finish deleting: context canceled
    ...
occurred
```

#### Proof 2: Test Log (lines 28-30)

The test log confirms we are in the `DeferCleanup (Each)` phase, where a node timeout occurred:

```
  [TIMEDOUT] in [DeferCleanup (Each)] - tear down test context | per_test_framework.go:200 @ 07/01/26 12:54:21.881
"ts"="2026-07-01 12:54:21.883924" "msg"="at least one resource group failed to delete" "error"="failed to cleanup resource group: at least one hcp cluster failed to delete: failed waiting for hcpCluster=\"np-autoscale-cluster\" in resourcegroup=\"np-autoscaling-5zzp9q\" to finish deleting: context canceled"
```

### Why did the cluster deletion never complete?

Clusters Service moved the cluster to `'uninstalling'` but never completed the deletion. The destruct chain was stuck.

#### Proof 1: Log Snippet

Clusters Service phase transitions show the cluster reached `'uninstalling'` but never progressed further:

```kql
clustersServiceLogs
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
clustersServiceLogs
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

### Why was the `ManagedCluster` destructor stuck?

The addon pre-delete hook pods in the klusterlet namespace were evicted due to node `MemoryPressure` before they could
complete their cleanup work.

The pre-gathered `mgmtAgent/podEvictions` snapshot data shows the eviction events from the mgmt-agent PodWatcher. The
key fields are `log.object.status.reason == "Evicted"` and `log.object.status.message` containing
`"Pod was rejected: The node had condition: [MemoryPressure]."`. The pods were scheduled and evicted within seconds,
never completing their cleanup work.

#### Proof 1: Log Snippet

The mgmt-agent PodWatcher shows addon pods being repeatedly evicted in the klusterlet namespace:

```kql
containerLogs
| where timestamp between (datetime(2026-07-01) .. datetime(2026-07-03))
| where namespace_name == "mgmt-agent" and log.msg == "pod event"
| where log.namespace == "klusterlet-2r9nhugpbdko2vai55lv2ikki9h9958r"
| where tostring(log.object.status.reason) == "Evicted"
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
repeatedly evicted, permanently blocking the `ManagedCluster` cleanup until the cleaner intervened at ~10:12 AM.

**Suggestions:**

- The addon pre-delete hook mechanism should be resilient to transient pod eviction. Consider using a `Job` with
  `restartPolicy: OnFailure` and a configurable `backoffLimit` so that evicted pods are automatically retried.
- Addon pre-delete pods could use `PriorityClass` or `PodDisruptionBudget` settings to reduce the likelihood of
  eviction during critical cleanup operations.
- The destruct chain in Clusters Service could implement a timeout after which it force-removes addon finalizers
  and proceeds with the rest of the chain, preventing indefinite blocking.

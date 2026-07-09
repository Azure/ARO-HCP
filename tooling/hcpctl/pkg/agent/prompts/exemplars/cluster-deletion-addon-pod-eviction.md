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

Clusters Service ran the destruct chain, but the first destructor (`hypershift-managed-cluster-destructor`) could not
complete because the `ManagedCluster` resource was stuck in `Detaching` state. Two `ManagedClusterAddon` resources
(`config-policy-controller` and `governance-policy-framework`) retained their `hosting-addon-pre-delete` and
`hosting-manifests-cleanup` finalizers because the pods responsible for running the pre-delete hooks had been evicted
from their node due to a `MemoryPressure` condition. Without the addon pods running to execute the hooks, the finalizers
were never removed.

Because the `hypershift-managed-cluster-destructor` could not complete, the destruct chain never advanced to the
`hypershift-manifest-work-destructor`, and the ManifestWork / HostedCluster cleanup was never initiated. The test timed
out waiting for the deletion to finish.

## Recursive 'Why' Chain

### Why did the test fail?

The test client timed out waiting for the ARO HCP cluster deletion to complete during cleanup.

#### Proof 1: Test Error (lines 1-7)

The proximal failure was a context deadline during cluster deletion while cleaning up the resource group:

```
fail [github.com/Azure/ARO-HCP/test/util/framework/per_test_framework.go:262] A node timeout occurred and then the following failure was recorded in the timedout node before it exited:
Unexpected error:
    <*errors.joinError | 0xc0014a22e8>:
    failed to cleanup resource group: at least one hcp cluster failed to delete: failed waiting for hcpCluster="lifecycle-test-cluster-kv82xr" in resourcegroup="rg-lifecycle-test-kv82xr-t4n9c1" to finish deleting, caused by: timeout '10.000000' minutes exceeded during DeleteHCPCluster for cluster lifecycle-test-cluster-kv82xr in resource group rg-lifecycle-test-kv82xr-t4n9c1, error: context deadline exceeded
    ...
occurred
fail [:0]: A node timeout occurred
```

#### Proof 2: Test Log (lines 28-30)

The test log confirms we are in the `DeferCleanup (Each)` phase:

```
  [TIMEDOUT] in [DeferCleanup (Each)] - tear down test context | per_test_framework.go:195 @ 06/15/26 20:42:15.21
"ts"="2026-06-15 20:42:15.216312" "msg"="at least one resource group failed to delete" "error"="failed to cleanup resource group: at least one hcp cluster failed to delete: failed waiting for hcpCluster=\"lifecycle-test-cluster-kv82xr\" in resourcegroup=\"rg-lifecycle-test-kv82xr-t4n9c1\" to finish deleting, caused by: timeout '10.000000' minutes exceeded during DeleteHCPCluster for cluster lifecycle-test-cluster-kv82xr in resource group rg-lifecycle-test-kv82xr-t4n9c1, error: context deadline exceeded"
```

#### Proof 3: Code Snippet: ARO-HCP/test/util/framework/hcp_helper.go (lines 220-248)

The test client issues an ARM deletion call via `BeginDelete` and polls until done:

```go
func deleteHCPClusterAttempt(
ctx context.Context,
hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
resourceGroupName string,
hcpClusterName string,
) error {
poller, err := hcpClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nil)
if err != nil {
var respErr *azcore.ResponseError
if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
resp, getErr := hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
if getErr == nil && resp.Properties != nil && resp.Properties.ProvisioningState != nil && *resp.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateDeleting {
ginkgo.GinkgoLogr.Info("cluster already deleting, waiting for completion",
"cluster", hcpClusterName, "resourceGroup", resourceGroupName)
return waitForHCPClusterDeletion20240610(ctx, hcpClient, resourceGroupName, hcpClusterName)
}
}
return err
}

operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
Frequency: StandardPollInterval,
})
if err != nil {
if errors.Is(err, context.DeadlineExceeded) {
return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
}
return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting: %w", hcpClusterName, resourceGroupName, err)
}
```

#### Proof 4: Log Snippet

The test client polled the async operation successfully during the entire cleanup window, but the operation never
completed:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('frontendLogs')
| where timestamp between (datetime(2026-06-15T20:32:10Z) .. datetime(2026-06-15T20:42:15Z))
| where log.path =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/providers/microsoft.redhatopenshift/locations/uksouth/hcpoperationstatuses/d917e3a4-5bc8-4f2e-a613-8c42bf10a2e7'
| where log.msg == 'response complete'
| summarize
    first_occurrence = min(timestamp),
    last_occurrence = max(timestamp),
    occurrences = count()
  by method=tostring(log.method), response_status_code=tostring(log.response_status_code), error=tostring(log.error)
| order by first_occurrence asc
```

| method | response_status_code | error | first_occurrence         | last_occurrence          | occurrences |
|--------|----------------------|-------|--------------------------|--------------------------|-------------|
| get    | 200                  |       | 2026-06-15T20:32:11.841Z | 2026-06-15T20:42:14.937Z | 97          |

### Why did the ARO HCP cluster deletion async operation never succeed?

The RP backend computes async operation status based on Clusters Service state; the RP backend had no processing errors,
but Clusters Service never moved the cluster past the `'uninstalling'` phase.

#### Proof 1: Code Snippet: ARO-HCP/backend/pkg/controllers/operationcontrollers/operation_cluster_delete.go (lines 146-152)

The RP backend computes cluster status based on what Clusters Service returns:

```go
newOperationStatus, newOperationError, err := convertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
if err != nil {
return utils.TrackError(err)
}

err = UpdateOperationStatus(ctx, c.resourcesDBClient, operation, newOperationStatus, newOperationError, postAsyncNotificationFn(c.notificationClient))
if err != nil {
return utils.TrackError(err)
}
```

#### Proof 2: Log Snippet

The backend deletion controller posted normal status during the cleanup window for this cluster:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where timestamp between (datetime(2026-06-15T20:32:10Z) .. datetime(2026-06-15T20:42:15Z))
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.resource_group == 'rg-lifecycle-test-kv82xr-t4n9c1'
| where log.resource_name == 'lifecycle-test-cluster-kv82xr'
| where log.content.resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/hcpopenshiftcontrollers'
| where log.content.resourceID has 'lifecycle-test-cluster-kv82xr'
| summarize content=take_any(log.content), observedTime=take_any(timestamp) by etag=tostring(log.content._etag)
| sort by tolong(content._ts) asc
| extend content = parse_json(content)
| extend controller_name = extract("/hcpOpenShiftControllers/([^\\/]+)", 1, tostring(content.resourceID))
| where controller_name == 'OperationClusterDelete'
| mv-expand condition = content.properties.status.conditions
| project observedTime, lastTransitionTime=todatetime(condition.lastTransitionTime), controller_name, type=tostring(condition.type), status=tostring(condition.status), reason=tostring(condition.reason), message=tostring(condition.message)
| summarize observedTime=min(observedTime) by lastTransitionTime, controller_name, type, status, reason, message
| order by lastTransitionTime asc, observedTime asc
```

| lastTransitionTime     | controller_name        | type     | status | reason   | message      | observedTime               |
|------------------------|------------------------|----------|--------|----------|--------------|----------------------------|
| 6/15/2026, 8:32:22 PM | OperationClusterDelete | Degraded | False  | NoErrors | As expected. | 6/15/2026, 8:35:50.312 PM |

#### Proof 3: Log Snippet

Clusters Service never transitioned the cluster past the `'uninstalling'` phase:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-06-15T19:30:00Z) .. datetime(2026-06-15T20:42:15Z))
| where log.aro_hcp_cluster_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-lifecycle-test-kv82xr-t4n9c1/providers/microsoft.redhatopenshift/hcpopenshiftclusters/lifecycle-test-cluster-kv82xr'
| where isempty(log.aro_hcp_node_pool_resource_id)
| where log has 'state to' or log has 'now in'
| project timestamp, msg=tostring(log.msg)
| order by timestamp asc
```

| timestamp                | msg                                                                           |
|--------------------------|-------------------------------------------------------------------------------|
| 2026-06-15T19:32:18.124Z | Cluster '2qulf59mvef3j2qasahsun5vief6rgjr' created, now in 'validating' state |
| 2026-06-15T19:32:25.871Z | updating cluster '2qulf59mvef3j2qasahsun5vief6rgjr' state to 'pending'        |
| 2026-06-15T19:45:12.443Z | updating cluster '2qulf59mvef3j2qasahsun5vief6rgjr' state to 'installing'     |
| 2026-06-15T19:50:38.917Z | updating cluster '2qulf59mvef3j2qasahsun5vief6rgjr' state to 'ready'          |
| 2026-06-15T20:32:10.654Z | updating cluster '2qulf59mvef3j2qasahsun5vief6rgjr' state to 'uninstalling'   |

### Why didn't Clusters Service move past the `'uninstalling'` phase?

Clusters Service ran the destruct chain repeatedly, but the `hypershift-managed-cluster-destructor` could not complete
because the `ManagedCluster` was stuck in `Detaching` state. The chain never advanced to subsequent destructors.

#### Proof 1: Log Snippet

Clusters Service repeated the destruct chain logic throughout the cleanup period, always stopping at the managed cluster
destructor. Note the absence of `managed cluster does not exist for cluster` — the `ManagedCluster` was never fully
deleted:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-06-15T20:32:10Z) .. datetime(2026-06-15T20:42:15Z))
| where log.aro_hcp_cluster_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-lifecycle-test-kv82xr-t4n9c1/providers/microsoft.redhatopenshift/hcpopenshiftclusters/lifecycle-test-cluster-kv82xr'
| where isempty(log.aro_hcp_node_pool_resource_id)
| summarize
    first_occurrence = min(timestamp),
    last_occurrence = max(timestamp),
    occurrences = count()
  by msg = tostring(log.msg)
| order by first_occurrence asc
| where occurrences > 5
```

| msg                                                                            | first_occurrence           | last_occurrence            | occurrences |
|--------------------------------------------------------------------------------|----------------------------|----------------------------|-------------|
| Running chain deletion to clean deleted cluster '2qulf59mvef3j2qasahsun5vief6rgjr'. | 6/15/2026, 8:32:11.312 PM | 6/15/2026, 8:42:14.781 PM | 19          |
| Starting destruct chain for cluster                                            | 6/15/2026, 8:32:11.315 PM | 6/15/2026, 8:42:14.783 PM | 19          |
| Running destructor 'hypershift-managed-cluster-destructor' for cluster         | 6/15/2026, 8:32:11.315 PM | 6/15/2026, 8:42:14.784 PM | 19          |
| Not continuing to the next destructor for cluster                              | 6/15/2026, 8:32:12.847 PM | 6/15/2026, 8:42:14.916 PM | 19          |
| Finished destruct chain for cluster                                            | 6/15/2026, 8:32:12.847 PM | 6/15/2026, 8:42:14.916 PM | 19          |

#### Proof 2: Log Snippet

To confirm the chain is stuck at the `ManagedCluster` level: the `hypershift-manifest-work-destructor` was never reached.
A query for its log messages returns zero occurrences:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-06-15T20:32:10Z) .. datetime(2026-06-15T20:42:15Z))
| where log.aro_hcp_cluster_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-lifecycle-test-kv82xr-t4n9c1/providers/microsoft.redhatopenshift/hcpopenshiftclusters/lifecycle-test-cluster-kv82xr'
| where isempty(log.aro_hcp_node_pool_resource_id)
| where log has 'hypershift-manifest-work-destructor'
| summarize count = count()
```

| count |
|-------|
| 0     |

### Why was the `ManagedCluster` stuck in `Detaching` state?

Two `ManagedClusterAddon` resources (`config-policy-controller` and `governance-policy-framework`) in the cluster's
namespace had held `hosting-addon-pre-delete` and `hosting-manifests-cleanup` finalizers. These finalizers are removed by
addon pre-delete hook pods that run during the `Detaching` flow; with the hooks unable to complete, the `ManagedCluster`
could not finish detaching.

#### Proof 1: Log Snippet

Kubernetes events for `ManagedClusterAddon` objects in the cluster namespace show the addons existed with their
finalizers throughout the cleanup window:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-06-15T20:32:10Z) .. datetime(2026-06-15T20:42:15Z))
| where eventNamespace == '2qulf59mvef3j2qasahsun5vief6rgjr'
| where objectKind == 'ManagedClusterAddon'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind          | objectName                        | reason                     | message                                                                                   | firstSeen             | lastSeen              | count |
|---------------------|-----------------------------------|----------------------------|-------------------------------------------------------------------------------------------|-----------------------|-----------------------|-------|
| ManagedClusterAddon | config-policy-controller          | PreDeleteHookFailed        | Pre-delete hook job failed to complete                                                    | 6/15/2026, 8:32:38 PM | 6/15/2026, 8:42:08 PM | 12    |
| ManagedClusterAddon | governance-policy-framework       | PreDeleteHookFailed        | Pre-delete hook job failed to complete                                                    | 6/15/2026, 8:32:39 PM | 6/15/2026, 8:42:09 PM | 12    |

#### Proof 2: Log Snippet

Kubernetes events for `ManagedCluster` confirm it entered `Detaching` and never progressed:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-06-15T20:30:00Z) .. datetime(2026-06-15T20:42:15Z))
| where objectKind == 'ManagedCluster'
| where objectName == '2qulf59mvef3j2qasahsun5vief6rgjr'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind     | objectName                        | reason     | message                                                        | firstSeen             | lastSeen              | count |
|----------------|-----------------------------------|------------|----------------------------------------------------------------|-----------------------|-----------------------|-------|
| ManagedCluster | 2qulf59mvef3j2qasahsun5vief6rgjr | Detaching  | ManagedCluster is detaching: waiting for addon cleanup          | 6/15/2026, 8:31:58 PM | 6/15/2026, 8:42:10 PM | 15    |

### Why were the `ManagedClusterAddon` pre-delete hook pods unable to complete?

The pods that were responsible for running the addon pre-delete hooks were evicted from their hosting node before they
could complete. The eviction was triggered by the node reporting a `MemoryPressure` condition.

#### Proof 1: Log Snippet

Kubernetes events for pods in the cluster namespace show the addon pre-delete pods being evicted with the MemoryPressure
reason:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-06-15T20:25:00Z) .. datetime(2026-06-15T20:42:15Z))
| where eventNamespace == '2qulf59mvef3j2qasahsun5vief6rgjr'
| where objectKind == 'Pod'
| where objectName has 'config-policy-controller' or objectName has 'governance-policy-framework'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind | objectName                                      | reason    | message                                                                             | firstSeen             | lastSeen              | count |
|------------|--------------------------------------------------|-----------|-------------------------------------------------------------------------------------|-----------------------|-----------------------|-------|
| Pod        | config-policy-controller-pre-delete-7x4kn        | Scheduled | Successfully assigned 2qulf59mvef3j2qasahsun5vief6rgjr/config-policy-controller-pre-delete-7x4kn to aks-mgmt-15776247-vmss000003 | 6/15/2026, 8:31:59 PM | 6/15/2026, 8:31:59 PM | 1     |
| Pod        | governance-policy-framework-pre-delete-m9r2p      | Scheduled | Successfully assigned 2qulf59mvef3j2qasahsun5vief6rgjr/governance-policy-framework-pre-delete-m9r2p to aks-mgmt-15776247-vmss000003 | 6/15/2026, 8:32:00 PM | 6/15/2026, 8:32:00 PM | 1     |
| Pod        | config-policy-controller-pre-delete-7x4kn        | Evicted   | The node had condition: [MemoryPressure]                                            | 6/15/2026, 8:32:24 PM | 6/15/2026, 8:32:24 PM | 1     |
| Pod        | governance-policy-framework-pre-delete-m9r2p      | Evicted   | The node had condition: [MemoryPressure]                                            | 6/15/2026, 8:32:24 PM | 6/15/2026, 8:32:24 PM | 1     |

#### Proof 2: Log Snippet

The node `aks-mgmt-15776247-vmss000003` reported `MemoryPressure` at the time the pods were evicted:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-06-15T20:25:00Z) .. datetime(2026-06-15T20:42:15Z))
| where objectKind == 'Node' and objectName == 'aks-mgmt-15776247-vmss000003'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind | objectName                        | reason                   | message                                                                                             | firstSeen             | lastSeen              | count |
|------------|-----------------------------------|--------------------------|-----------------------------------------------------------------------------------------------------|-----------------------|-----------------------|-------|
| Node       | aks-mgmt-15776247-vmss000003      | NodeHasMemoryPressure    | Node aks-mgmt-15776247-vmss000003 status is now: NodeHasMemoryPressure                             | 6/15/2026, 8:32:18 PM | 6/15/2026, 8:32:18 PM | 1     |
| Node       | aks-mgmt-15776247-vmss000003      | NodeHasNoMemoryPressure  | Node aks-mgmt-15776247-vmss000003 status is now: NodeHasNoMemoryPressure                           | 6/15/2026, 8:38:42 PM | 6/15/2026, 8:38:42 PM | 1     |

### Why did the node experience `MemoryPressure`?

The node's memory utilization exceeded the kubelet's eviction threshold. This is an infrastructure-level condition on
the management cluster, outside the scope of the ARO HCP application code. The root-cause investigation stops here
because the proximate cause (pod eviction) and its impact on the deletion chain have been fully established.

#### Proof 1: Log Snippet

The node condition events confirm the `MemoryPressure` condition was set before the pod evictions and cleared
approximately six minutes later — well after the addon pods had already been evicted and their hooks had failed:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-06-15T20:20:00Z) .. datetime(2026-06-15T20:45:00Z))
| where objectKind == 'Node' and objectName == 'aks-mgmt-15776247-vmss000003'
| where reason has 'MemoryPressure' or reason has 'NotReady'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind | objectName                        | reason                   | message                                                                          | firstSeen             | lastSeen              | count |
|------------|-----------------------------------|--------------------------|----------------------------------------------------------------------------------|-----------------------|-----------------------|-------|
| Node       | aks-mgmt-15776247-vmss000003      | NodeHasMemoryPressure    | Node aks-mgmt-15776247-vmss000003 status is now: NodeHasMemoryPressure          | 6/15/2026, 8:32:18 PM | 6/15/2026, 8:32:18 PM | 1     |
| Node       | aks-mgmt-15776247-vmss000003      | NodeHasNoMemoryPressure  | Node aks-mgmt-15776247-vmss000003 status is now: NodeHasNoMemoryPressure        | 6/15/2026, 8:38:42 PM | 6/15/2026, 8:38:42 PM | 1     |

The `MemoryPressure` condition was transient (lasting ~6 minutes), but the damage was done: the addon pre-delete pods
were evicted and never retried, permanently blocking the `ManagedCluster` cleanup.

**Suggestions:**

- The addon pre-delete hook mechanism should be resilient to transient pod eviction. Consider using a `Job` with
  `restartPolicy: OnFailure` and a configurable `backoffLimit` so that evicted pods are automatically retried.
- Addon pre-delete pods could use `PriorityClass` or `PodDisruptionBudget` settings to reduce the likelihood of
  eviction during critical cleanup operations.
- The `hypershift-managed-cluster-destructor` in Clusters Service could implement a timeout after which it force-removes
  addon finalizers and proceeds with the rest of the destruct chain, preventing indefinite blocking.

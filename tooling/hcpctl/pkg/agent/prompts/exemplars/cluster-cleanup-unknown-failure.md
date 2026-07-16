This document shows a proof chain for a failure to delete a cluster during the test cleanup phase without any clear
root-cause.

# Root Cause

An ARO HCP cluster failed to delete during a test's cleanup phase due to something blocking namespace cleanup; no
obvious root-cause can be determined without more data.

## Summary

An end-to-end test for upgrading NodePool versions ran to completion and succeeded in all assertions; post-test cleanup
failed to delete the ARO HCP cluster within five minutes. The deletion signal was propagated from the frontend to the
backend and Clusters Service moved the cluster to `'uninstalling'` phase, but never finished removing it. HyperShift
`HostedCluster` conditions showed no errors but the `hypershift-operator` indicated that cleanup was stuck on namespace
removal. Without visibility into the namespace, no further root-cause can be determined.

## Recursive 'Why' Chain

### Why did the test fail?

The test client timed out waiting for one of two ARO HCP clusters it created in a resource group to become ready.

#### Proof 1: Test Error (lines 1-7)

The proximal failure was a timeout while deleting an ARO HCP cluster while cleaning up the resource group:

```
fail [github.com/Azure/ARO-HCP/test/util/framework/per_test_framework.go:262] A node timeout occurred and then the following failure was recorded in the timedout node before it exited:
Unexpected error:
    <*errors.joinError | 0xc0013522e8>:
    failed to cleanup resource group: at least one hcp cluster failed to delete: failed waiting for hcpCluster="np-version-upgrade-cluster-rs22qw" in resourcegroup="rg-np-version-upgrade-rs22qw-sssr8k" to finish deleting: context canceled
    ...
occurred
fail [:0]: A node timeout occurred
```

#### Proof 2: Test Log (lines 32-34)

We can see from the test log that we're in the `DeferCleanup (Each)` phase:

```
  [TIMEDOUT] in [DeferCleanup (Each)] - tear down test context | per_test_framework.go:195 @ 05/19/26 23:03:20.53
"ts"="2026-05-19 23:03:20.531621" "msg"="at least one resource group failed to delete" "error"="failed to cleanup resource group: at least one hcp cluster failed to delete: failed waiting for hcpCluster=\"np-version-upgrade-cluster-rs22qw\" in resourcegroup=\"rg-np-version-upgrade-rs22qw-sssr8k\" to finish deleting: context canceled"
```

#### Proof 3: Code Snippet: ARO-HCP/test/util/framework/hcp_helper.go (lines 182-204)

We can see the test client issues an ARM deletion call and polls to see it finish:

```go
poller, err := hcpClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nil)
if err != nil {
var respErr *azcore.ResponseError
if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
resp, getErr := hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
if getErr == nil && resp.Properties != nil && resp.Properties.ProvisioningState != nil && *resp.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateDeleting {
ginkgo.GinkgoLogr.Info("cluster already deleting, waiting for completion",
"cluster", hcpClusterName, "resourceGroup", resourceGroupName)
return waitForHCPClusterDeletion(ctx, hcpClient, resourceGroupName, hcpClusterName)
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

The test client polled on the async operation successfully but never saw it finish:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('frontendLogs')
| where timestamp between (datetime(2026-05-19T21:33:38Z) .. datetime(2026-05-19T23:03:20Z))
| where log.path =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/providers/microsoft.redhatopenshift/locations/uksouth/hcpoperationstatuses/3ce3553b-8976-45c4-871e-0a5fc31b253e'
| where log.msg == 'response complete'
| summarize
    first_occurrence = min(timestamp),
    last_occurrence = max(timestamp),
    occurrences = count()
  by method=tostring(log.method), response_status_code=tostring(log.response_status_code), error=tostring(log.error)
| order by first_occurrence asc
```

| method | response_status_code | error | first_occurrence         | last_occurrence         | occurrences |
|--------|----------------------|-------|--------------------------|-------------------------|-------------|
| get    | 200                  |       | 2026-05-19T22:18:22.374Z | 2026-05-19T23:03:19.97Z | 430         |

### Why did the ARO HCP cluster deletion async operation never succeed?

The RP frontend returns whatever state is current at the time of polling, and the RP backend computes async operation
status based on Clusters Service state; the RP backend had no processing errors, but Clusters Service never moved the
cluster past the `'uninstalling'` phase.

#### Proof 1: Code Snippet: ARO-HCP/backend/pkg/controllers/operationcontrollers/operation_cluster_delete.go (lines 146-152)

The RP backend simply computes cluster status based on what Clusters Service returns.

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

The backend deletion controllers posted normal status during the cleanup time for this cluster:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('cosmosResourceSnapshots')
| where timestamp between (datetime(2026-05-19T22:18:20Z) .. datetime(2026-05-19T23:03:20Z))
| where subscriptionID == '64f0619f-ebc2-4156-9d91-c4c781de7e54'
| where resourceGroup =~ 'rg-np-version-upgrade-rs22qw-sssr8k'
| where resourceID startswith '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-np-version-upgrade-rs22qw-sssr8k/providers/microsoft.redhatopenshift/hcpopenshiftclusters/np-version-upgrade-cluster-rs22qw'
| where resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/hcpopenshiftcontrollers'
| summarize content=take_any(content), observedTime=take_any(timestamp) by etag=tostring(content._etag)
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
| 5/19/2026, 10:19:22 PM | OperationClusterDelete | Degraded | False  | NoErrors | As expected. | 5/19/2026, 10:59:50.258 PM |

#### Proof 3: Log Snippet

Clusters Service never transitioned the cluster past the `'uninstalling'` phase:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-05-19T21:33:38Z) .. datetime(2026-05-19T23:03:20Z))
| where log.aro_hcp_cluster_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-np-version-upgrade-rs22qw-sssr8k/providers/microsoft.redhatopenshift/hcpopenshiftclusters/np-version-upgrade-cluster-rs22qw'
| where isempty(log.aro_hcp_node_pool_resource_id)
| where log has 'state to' or log has 'now in'
| project timestamp, msg=tostring(log.msg)
| order by timestamp asc
```

| timestamp                | msg                                                                           |
|--------------------------|-------------------------------------------------------------------------------|
| 2026-05-19T21:34:28.558Z | Cluster '2qd8ip08flgfav1pl5k2o8u23okgriqe' created, now in 'validating' state |
| 2026-05-19T21:34:36.335Z | updating cluster '2qd8ip08flgfav1pl5k2o8u23okgriqe' state to 'pending'        |
| 2026-05-19T21:51:55.55Z  | updating cluster '2qd8ip08flgfav1pl5k2o8u23okgriqe' state to 'installing'     |
| 2026-05-19T21:55:56.942Z | updating cluster '2qd8ip08flgfav1pl5k2o8u23okgriqe' state to 'ready'          |
| 2026-05-19T22:18:21.861Z | updating cluster '2qd8ip08flgfav1pl5k2o8u23okgriqe' state to 'uninstalling'   |

### Why didn't Clusters Service move past the `'uninstalling'` phase?

Clusters Service ran the deletion logic, but never saw the ManifestWork containing the HostedCluster finish deleting.

#### Proof 1: Log Snippet

Clusters Service repeated the cleanup logic 80+ times during the cleanup period:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-05-19T22:18:20Z) .. datetime(2026-05-19T23:03:20Z))
| where log.aro_hcp_cluster_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-np-version-upgrade-rs22qw-sssr8k/providers/microsoft.redhatopenshift/hcpopenshiftclusters/np-version-upgrade-cluster-rs22qw'
| where isempty(log.aro_hcp_node_pool_resource_id)
| summarize
    first_occurrence = min(timestamp),
    last_occurrence = max(timestamp),
    occurrences = count()
  by msg = tostring(log.msg)
| order by first_occurrence asc
| where occurrences > 80
```

| msg                                                                                                                                                                                                                                                                                                                                                                      | first_occurrence           | last_occurrence            | occurrences |
|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------|----------------------------|-------------|
| Running chain deletion to clean deleted cluster '2qd8ip08flgfav1pl5k2o8u23okgriqe'.                                                                                                                                                                                                                                                                                      | 5/19/2026, 10:18:46.945 PM | 5/19/2026, 11:03:19.594 PM | 86          |
| checking if config changed for shard '696d3cd6-199e-53cb-a6ab-49dffc54cb70'                                                                                                                                                                                                                                                                                              | 5/19/2026, 10:18:46.947 PM | 5/19/2026, 11:03:19.596 PM | 86          |
| Starting destruct chain for cluster                                                                                                                                                                                                                                                                                                                                      | 5/19/2026, 10:18:46.95 PM  | 5/19/2026, 11:03:19.598 PM | 86          |
| Running destructor 'hypershift-managed-cluster-destructor' for cluster                                                                                                                                                                                                                                                                                                   | 5/19/2026, 10:18:46.95 PM  | 5/19/2026, 11:03:19.598 PM | 86          |
| Not continuing to the next destructor for cluster                                                                                                                                                                                                                                                                                                                        | 5/19/2026, 10:18:48.332 PM | 5/19/2026, 11:02:48.985 PM | 85          |
| Finished destruct chain for cluster                                                                                                                                                                                                                                                                                                                                      | 5/19/2026, 10:18:48.332 PM | 5/19/2026, 11:02:48.985 PM | 85          |
| managed cluster does not exist for cluster '2qd8ip08flgfav1pl5k2o8u23okgriqe', skipping                                                                                                                                                                                                                                                                                  | 5/19/2026, 10:20:53.663 PM | 5/19/2026, 11:03:19.847 PM | 82          |
| Running destructor 'hypershift-manifest-work-destructor' for cluster                                                                                                                                                                                                                                                                                                     | 5/19/2026, 10:20:53.663 PM | 5/19/2026, 11:03:19.848 PM | 82          |
| list works with search=source='arohcpint-696d3cd6-199e-53cb-a6ab-49dffc54cb70' and consumer_name='hcp-underlay-ln-mgmt-1' and payload->'metadata'->'labels'@>'{"api.openshift.com/id":"2qd8ip08flgfav1pl5k2o8u23okgriqe","api.openshift.com/name":"np-version-upgrade-cluster-rs22qw","maestro.resource.type":"97b3a7e0-f995-5062-aba0-3410f2d257a2"}', page=1, size=400 | 5/19/2026, 10:20:53.663 PM | 5/19/2026, 11:03:19.85 PM  | 82          |
| list the work hcp-underlay-ln-mgmt-1/5c5d8c32-0497-5840-ac4c-4acb07e76a04 (source=arohcpint-696d3cd6-199e-53cb-a6ab-49dffc54cb70)                                                                                                                                                                                                                                        | 5/19/2026, 10:20:54.163 PM | 5/19/2026, 11:03:19.869 PM | 82          |
| list the work hcp-underlay-ln-mgmt-1/87c18c53-14b7-593d-a09c-80ef1d6ae157 (source=arohcpint-696d3cd6-199e-53cb-a6ab-49dffc54cb70)                                                                                                                                                                                                                                        | 5/19/2026, 10:20:54.163 PM | 5/19/2026, 11:03:19.87 PM  | 82          |
| list the work hcp-underlay-ln-mgmt-1/f40bac18-8f3f-5d0a-8044-2bb23f87ab1e (source=arohcpint-696d3cd6-199e-53cb-a6ab-49dffc54cb70)                                                                                                                                                                                                                                        | 5/19/2026, 10:20:54.163 PM | 5/19/2026, 11:03:19.87 PM  | 82          |
| Skipping ManifestWork 'local-cluster/2qd8ip08flgfav1pl5k2o8u23okgriqe-00-namespaces in this run as it contains namespaces                                                                                                                                                                                                                                                | 5/19/2026, 10:20:54.276 PM | 5/19/2026, 11:03:19.874 PM | 82          |
| Skipping ManifestWork 'local-cluster/2qd8ip08flgfav1pl5k2o8u23okgriqe-00-hcp-namespaces in this run as it contains namespaces                                                                                                                                                                                                                                            | 5/19/2026, 10:20:54.276 PM | 5/19/2026, 11:03:19.874 PM | 82          |
| deleting maestro bundle with maestro bundle name 'f40bac18-8f3f-5d0a-8044-2bb23f87ab1e' containing resource 'local-cluster/2qd8ip08flgfav1pl5k2o8u23okgriqe' with GVK 'work.open-cluster-management.io/v1, Kind=ManifestWork'                                                                                                                                            | 5/19/2026, 10:20:54.276 PM | 5/19/2026, 11:03:19.874 PM | 82          |
| get the work hcp-underlay-ln-mgmt-1/f40bac18-8f3f-5d0a-8044-2bb23f87ab1e (source=arohcpint-696d3cd6-199e-53cb-a6ab-49dffc54cb70)                                                                                                                                                                                                                                         | 5/19/2026, 10:20:54.288 PM | 5/19/2026, 11:02:48.936 PM | 81          |
| successfully sent delete request of maestro bundle name 'f40bac18-8f3f-5d0a-8044-2bb23f87ab1e' containing resource 'local-cluster/2qd8ip08flgfav1pl5k2o8u23okgriqe' with GVK 'work.open-cluster-management.io/v1, Kind=ManifestWork'                                                                                                                                     | 5/19/2026, 10:20:54.32 PM  | 5/19/2026, 11:02:48.985 PM | 81          |
| requested manifest work 'local-cluster/2qd8ip08flgfav1pl5k2o8u23okgriqe' deletion                                                                                                                                                                                                                                                                                        | 5/19/2026, 10:20:54.32 PM  | 5/19/2026, 11:02:48.985 PM | 81          |

#### Proof 2: Log Snippet

The ManifestWork `local-cluster/2qd8ip08flgfav1pl5k2o8u23okgriqe` contains the `HostedCluster`:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-05-19T22:18:20Z) .. datetime(2026-05-19T23:03:20Z))
| where log.aro_hcp_cluster_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-np-version-upgrade-rs22qw-sssr8k/providers/microsoft.redhatopenshift/hcpopenshiftclusters/np-version-upgrade-cluster-rs22qw'
| where msg startswith "ManifestWork for cluster"
| extend raw_content = extract("ManifestWork for cluster '[^']+':(.*)$", 1, tostring(msg))
| extend manifest_work = parse_json(raw_content)
| where manifest_work.metadata.namespace == 'local-cluster' and manifest_work.metadata.name == '2qd8ip08flgfav1pl5k2o8u23okgriqe'
| mv-expand manifest = manifest_work.spec.workload.manifests
| distinct tostring(manifest.apiVersion), tostring(manifest.kind), tostring(manifest.metadata.namespace), tostring(manifest.metadata.name)
```

| manifest_apiVersion             | manifest_kind       | manifest_metadata_namespace                    | manifest_metadata_name                                    |
|---------------------------------|---------------------|------------------------------------------------|-----------------------------------------------------------|
| v1                              | Secret              | ocm-arohcpint-2qd8ip08flgfav1pl5k2o8u23okgriqe | l8c9m8n4d8e7g6i-pull                                      |
| secrets-store.csi.x-k8s.io/v1   | SecretProviderClass | ocm-arohcpint-2qd8ip08flgfav1pl5k2o8u23okgriqe | bound-service-account-signing-key                         |
| secret-sync.x-k8s.io/v1alpha1   | SecretSync          | ocm-arohcpint-2qd8ip08flgfav1pl5k2o8u23okgriqe | bound-service-account-signing-key                         |
| secrets-store.csi.x-k8s.io/v1   | SecretProviderClass | ocm-arohcpint-2qd8ip08flgfav1pl5k2o8u23okgriqe | kube-apiserver-tls-cert                                   |
| secret-sync.x-k8s.io/v1alpha1   | SecretSync          | ocm-arohcpint-2qd8ip08flgfav1pl5k2o8u23okgriqe | kube-apiserver-tls-cert                                   |
| secrets-store.csi.x-k8s.io/v1   | SecretProviderClass | open-cluster-management-policies               | default-ingress-tls-cert-2qd8ip08flgfav1pl5k2o8u23okgriqe |
| secret-sync.x-k8s.io/v1alpha1   | SecretSync          | open-cluster-management-policies               | default-ingress-tls-cert-2qd8ip08flgfav1pl5k2o8u23okgriqe |
| v1                              | ConfigMap           | open-cluster-management-policies               | default-ingress-config-2qd8ip08flgfav1pl5k2o8u23okgriqe   |
| hypershift.openshift.io/v1beta1 | HostedCluster       | ocm-arohcpint-2qd8ip08flgfav1pl5k2o8u23okgriqe | l8c9m8n4d8e7g6i                                           |

### Why didn't the `ManifestWork` containing the `HostedCluster` finish deleting?

The `HostedCluster` condition timeline after the deletion does not show anything out of the ordinary, but the
`hypershift-operator` logs clearly show the namespace could not be deleted. We have no visibility into why the namespace
never finished deleting, so our root-cause investigation stops here.

#### Proof 1: Log Snippet

The `HostedCluster` condition timeline after the test's cleanup start-time shows nothing out of the ordinary:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('cosmosResourceSnapshots')
| where timestamp between (datetime(2026-05-19T22:18:20Z) .. datetime(2026-05-19T23:03:20Z))
| where subscriptionID == '64f0619f-ebc2-4156-9d91-c4c781de7e54'
| where resourceGroup =~ 'rg-np-version-upgrade-rs22qw-sssr8k'
| where resourceID startswith '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/rg-np-version-upgrade-rs22qw-sssr8k/providers/microsoft.redhatopenshift/hcpopenshiftclusters/np-version-upgrade-cluster-rs22qw'
| where resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/readdesires'
| summarize content=take_any(content), observedTime=take_any(timestamp) by etag=tostring(content._etag)
| sort by tolong(content._ts) asc
| extend content = parse_json(content)
| extend manifest = content.properties.status.kubeContent
| where manifest.kind == 'HostedCluster'
| mv-expand condition = manifest.status.conditions
| project observedTime, type=tostring(condition.type), status=tostring(condition.status), reason=tostring(condition.reason), message=tostring(condition.message), lastTransitionTime=todatetime(condition.lastTransitionTime)
| summarize observedTime=min(observedTime) by type, status, reason, message, lastTransitionTime
| order by lastTransitionTime asc, observedTime asc
| where lastTransitionTime > datetime(2026-05-19T22:18:20Z)
```

| type                           | status | reason                  | message                               | lastTransitionTime     | observedTime               |
|--------------------------------|--------|-------------------------|---------------------------------------|------------------------|----------------------------|
| ReconciliationSucceeded        | True   | ReconciliatonSucceeded  | Reconciliation completed successfully | 5/19/2026, 10:27:12 PM | 5/19/2026, 10:28:36.047 PM |
| AWSDefaultSecurityGroupDeleted | True   | AsExpected              | All is well                           | 5/19/2026, 10:27:29 PM | 5/19/2026, 10:59:50.257 PM |
| CloudResourcesDestroyed        | True   | CloudResourcesDestroyed | All guest resources destroyed         | 5/19/2026, 10:27:47 PM | 5/19/2026, 10:59:50.257 PM |

#### Proof 2: Log Snippet

The `hypershift-operator` continues reconciling the `HostedCluster` after the test cleanup start-time and spends the
entire period waiting on namespace deletion:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-05-19T22:18:20Z) .. datetime(2026-05-19T23:03:20Z))
| where namespace_name == 'hypershift'
| where container_name == 'operator'
| where log.controllerKind == 'HostedCluster' and log.HostedCluster.namespace == 'ocm-arohcpint-2qd8ip08flgfav1pl5k2o8u23okgriqe'
| summarize
    first_occurrence = min(timestamp),
    last_occurrence = max(timestamp),
    occurrences = count()
  by msg = tostring(log.msg)
| order by first_occurrence asc
| where occurrences > 10
```

| msg                                                | first_occurrence           | last_occurrence            | occurrences |
|----------------------------------------------------|----------------------------|----------------------------|-------------|
| reconciling                                        | 5/19/2026, 10:18:23.147 PM | 5/19/2026, 11:03:17.074 PM | 504         |
| Reconciling                                        | 5/19/2026, 10:18:23.147 PM | 5/19/2026, 10:27:47.918 PM | 41          |
| hostedcluster is being deleted, aborting reconcile | 5/19/2026, 10:27:12.175 PM | 5/19/2026, 10:27:47.834 PM | 16          |
| hostedcluster is still deleting                    | 5/19/2026, 10:27:12.223 PM | 5/19/2026, 11:03:17.075 PM | 495         |
| Waiting for cluster deletion                       | 5/19/2026, 10:27:12.223 PM | 5/19/2026, 10:27:48.05 PM  | 61          |
| Waiting for namespace deletion                     | 5/19/2026, 10:27:48.183 PM | 5/19/2026, 11:03:17.075 PM | 434         |
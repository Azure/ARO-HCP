This document shows a proof chain for a failure to install a cluster coming from deep in the stack.

# Root Cause

An ARO HCP cluster failed to install because an Azure Disk could not be mounted for an `etcd` pod in the hosted control
plane due to internal Azure Disk errors.

## Summary

An end-to-end test installing two ARO HCP clusters in one resource group installed one without issue, but failed on the
other. The test client polled ARM for the async operation without issue, but the status never proceeded. The RP backend
posted no degraded controller status, but Clusters Service never progressed passed `'installing'` phase. The HyperShift
`HostedCluster` never posted a `Available` condition with `status=True`, claiming that it was stuck on
`reason=KubeconfigWaitingForCreate.` Furthermore, the `HostedCluster` claimed that `KubeAPIServerAvailable=False`
because the `kube-apiserver` deployment could not be found. The `hypershift-operator`, however, logged that `etcd` was
not fully available. Events for the `etcd` pods showed that one replica failed to mount its persistent volume due to an
internal Azure Disk error. The node this replica was scheduled to failed its health-checks, and after the node
auto-repair mechanism replaced this node, the `etcd` replica re-scheduled, mounted its persistent volume and ran without
issue, but this happened too late to save the test from timing out.

## Recursive 'Why' Chain

### Why did the test fail?

The test client timed out waiting for one of two ARO HCP clusters it created in a resource group to become ready.

#### Proof 1: Test Error (lines 1-5)

The test error is caused by a context deadline being exceeded:

```
fail [github.com/Azure/ARO-HCP/test/e2e/clusters_sharing_resgroup.go:132]: Unexpected error:
    <context.deadlineExceededError>: 
    context deadline exceeded
    ...
occurred
```

#### Proof 2: Test Log (lines 10-11)

In the test log, we can see the step that failed was waiting on a cluster to become ready:

```
  STEP: waiting for first cluster to complete creation @ 05/19/26 20:58:52.976
  [FAILED] in [It] - /opt/app-root/src/github.com/Azure/ARO-HCP/test/e2e/clusters_sharing_resgroup.go:132 @ 05/19/26 21:43:52.979 
```

#### Proof 3: Code Snippet: ARO-HCP/test/e2e/clusters_sharing_resgroup.go (lines 102-132)

The test begins the creation of both clusters, then polls to see the first one complete.

```go
// Start first cluster creation
poller1, err := framework.BeginCreateHCPCluster(
ctx,
GinkgoLogr,
clusterClient,
*customerResourceGroup.Name,
clusterParams1.ClusterName,
clusterParams1,
tc.Location(),
)
Expect(err).NotTo(HaveOccurred())

// Start second cluster creation
poller2, err := framework.BeginCreateHCPCluster(
ctx,
GinkgoLogr,
clusterClient,
*customerResourceGroup.Name,
clusterParams2.ClusterName,
clusterParams2,
tc.Location(),
)
Expect(err).NotTo(HaveOccurred())

By("waiting for first cluster to complete creation")
pollCtx, pollCancel := context.WithTimeout(ctx, 45*time.Minute)
defer pollCancel()
_, err = poller1.PollUntilDone(pollCtx, &runtime.PollUntilDoneOptions{
Frequency: framework.StandardPollInterval,
})
Expect(err).NotTo(HaveOccurred())
```

### Proof 4: Log Snippet

We can see the test client polling successfully for the entire time period:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('frontendLogs')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where log.path =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/providers/microsoft.redhatopenshift/locations/uksouth/hcpoperationstatuses/a049288c-cdf3-4431-ab37-ba94acfedbd1'
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
| get    | 200                  |       | 2026-05-19T20:58:53.476Z | 2026-05-19T21:44:06.643Z | 432         |

### Why did the async operation to create the cluster never finish?

The RP frontend returns whatever state is current at the time of polling, and the RP backend computes async operation
status based on Clusters Service state; the RP backend had no processing errors, but Clusters Service never moved the
cluster past the `'installing'` phase.

#### Proof 1: Code Snippet: ARO-HCP/backend/pkg/controllers/operationcontrollers/operation_cluster_create.go (lines 140-155)

The RP backend simply computes cluster status based on what Clusters Service returns.

```go
newOperationStatus, opError, err := convertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
if err != nil {
return utils.TrackError(err)
}
logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)

if newOperationStatus == arm.ProvisioningStateSucceeded && cosmosNewOperationState.provisioningState != arm.ProvisioningStateSucceeded {
// we want to require that the cosmos view of cluster creation is also complete before we mark it.  This ensures (among other things)
// that our ability to read maestro is successful.
// Once we have confidence in our ability to determine that cluster is functional, we'll stop checking cluster-service at all.
return fmt.Errorf("cosmos operation status is %q, but cluster-service operation status is %q", cosmosNewOperationState.provisioningState, newOperationStatus)
}

logger.Info("updating status")
err = UpdateOperationStatus(ctx, c.resourcesDBClient, operation, newOperationStatus, opError, postAsyncNotificationFn(c.notificationClient))
if err != nil {
return utils.TrackError(err)
}
```

#### Proof 2: Log Snippet

No backend controllers posted unnatural status during the test run for this cluster.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T21:43:55Z))
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.resource_group == 'customer-rg-crkv7p'
| where log.resource_name == 'basic-hcp-cluster'
| where log.content.resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/hcpopenshiftcontrollers'
| where log.content.resourceID has 'basic-hcp-cluster'
| summarize payload=take_any(log.content) by etag=tostring(log.content._etag)
| extend payload = parse_json(payload)
| extend controller_name = extract("/hcpOpenShiftControllers/([^\\/]+)", 1, tostring(payload.resourceID))
| extend ts = tolong(payload._ts)
| summarize arg_max(ts, payload) by controller_name
| mv-expand condition = payload.properties.status.conditions
| project lastTransitionTime=todatetime(condition.lastTransitionTime), controller_name, type=tostring(condition.type), status=tostring(condition.status), reason=tostring(condition.reason), message=tostring(condition.message)
| summarize count = count() by type, status
```

| type         | status | count |
|--------------|--------|-------|
| Degraded     | False  | 22    |
| IntentFailed | False  | 1     |

#### Proof 3: Log Snippet

Clusters Service never transitioned the cluster past the `'installing'` phase:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where log.aro_hcp_cluster_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/customer-rg-crkv7p/providers/microsoft.redhatopenshift/hcpopenshiftclusters/basic-hcp-cluster'
| where isempty(log.aro_hcp_node_pool_resource_id)
| where log has 'state to' or log has 'now in'
| project timestamp, msg=tostring(log.msg)
| order by timestamp asc
```

| timestamp                | msg                                                                           |
|--------------------------|-------------------------------------------------------------------------------|
| 2026-05-19T20:58:51.188Z | Cluster '2qd822p7guniitr7ibsb7qjeccri0gdl' created, now in 'validating' state |
| 2026-05-19T20:58:56.345Z | updating cluster '2qd822p7guniitr7ibsb7qjeccri0gdl' state to 'pending'        |
| 2026-05-19T21:00:44.321Z | updating cluster '2qd822p7guniitr7ibsb7qjeccri0gdl' state to 'installing'     |
| 2026-05-19T21:43:58.277Z | updating cluster '2qd822p7guniitr7ibsb7qjeccri0gdl' state to 'uninstalling'   |

### Why didn't Clusters Service transition the cluster past the `'installing'` phase to `'ready'`?

Clusters Service bubbles up the HyperShift `HostedCluster` status, and the `HostedCluster` never saw an `Available=true`
condition.

#### Proof 1: Code Snippet: aro-hcp-clusters-service/pkg/controller/manifestwork/cluster_status.go (lines 36-53)

```go
// syncInstallingClusterOnReady checks whether a hcp cluster in the "installing" phase is available.
// Then it transitions to a final "ready" state.
func (r *Reconciler) syncInstallingClusterOnReady(ctx context.Context, cluster *models.Cluster,
values acm.HypershiftConditions) error {
if condition, ok := values[hsv1beta1.HostedClusterAvailable]; !ok || condition.Status != v1.ConditionTrue {
return nil
}

err := r.updateClusterStateReady(ctx, cluster)
if err != nil {
return errors.Wrapf(err, "Failed to update cluster state to Ready")
}

r.logReadyCluster(ctx, cluster)

r.trackFinalClusterState(ctx, cluster, "")
return nil
}
```

#### Proof 2: Log Snippet

The `Available` condition on the `HostedCluster` never had a `true` status:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.resource_group == 'customer-rg-crkv7p'
| where log.resource_name == 'basic-hcp-cluster'
| where log.content.resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/managementclustercontents'
| summarize content=take_any(log.content), observedTime=take_any(timestamp) by etag=tostring(log.content._etag)
| sort by tolong(content._ts) asc
| extend content = parse_json(content)
| mv-expand manifest = content.properties.status.kubeContent.items
| where manifest.kind == 'HostedCluster'
| mv-expand condition = manifest.status.conditions
| project observedTime, type=tostring(condition.type), status=tostring(condition.status), reason=tostring(condition.reason), message=tostring(condition.message), lastTransitionTime=todatetime(condition.lastTransitionTime)
| summarize observedTime=min(observedTime) by type, status, reason, message, lastTransitionTime
| order by lastTransitionTime asc, observedTime asc
| where type == 'Available'
```

| type      | status | reason                       | message                                                   | lastTransitionTime    | observedTime              |
|-----------|--------|------------------------------|-----------------------------------------------------------|-----------------------|---------------------------| 
| Available | False  | WaitingOnInfrastructureReady | Cluster infrastructure is still provisioning              | 5/19/2026, 9:00:45 PM | 5/19/2026, 9:01:42.891 PM |
| Available | False  | KubeconfigWaitingForCreate   | Waiting for hosted control plane kubeconfig to be created | 5/19/2026, 9:00:45 PM | 5/19/2026, 9:02:06.346 PM |

### Why did the `Available` condition on the `HostedCluster` never have a `true` status?

The conditions on the `HostedCluster` claim that the `kube-apiserver` was not found, and the `kubeconfig` never created.
The `hypershift-operator` complains that `etcd` is not fully available for the entire duration of the test.

#### Proof 1: Log Snippet

The last snapshot of the `HostedCluster` conditions before the test cleanup began shows that the `kubeconfig` was not
present and the `kube-apiserver` deployment had not been found:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T21:43:55Z))
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.resource_group == 'customer-rg-crkv7p'
| where log.resource_name == 'basic-hcp-cluster'
| where log.content.resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/managementclustercontents'
| summarize content=take_any(log.content), observedTime=take_any(timestamp) by etag=tostring(log.content._etag)
| top 1 by tolong(content._ts) desc
| extend content = parse_json(content)
| mv-expand manifest = content.properties.status.kubeContent.items
| where manifest.kind == 'HostedCluster'
| mv-expand condition = manifest.status.conditions
| project observedTime, type=tostring(condition.type), status=tostring(condition.status), reason=tostring(condition.reason), message=tostring(condition.message), lastTransitionTime=todatetime(condition.lastTransitionTime)
| where type in ('KubeAPIServerAvailable', 'Available')
```

| observedTime              | type                   | status | reason                     | message                                                   | lastTransitionTime    | 
|---------------------------|------------------------|--------|----------------------------|-----------------------------------------------------------|-----------------------|
| 5/19/2026, 9:43:08.978 PM | KubeAPIServerAvailable | False  | NotFound                   | Kube APIServer deployment not found                       | 5/19/2026, 9:01:01 PM | 
| 5/19/2026, 9:43:08.978 PM | Available              | False  | KubeconfigWaitingForCreate | Waiting for hosted control plane kubeconfig to be created | 5/19/2026, 9:00:45 PM |

#### Proof 2: Log Snippet

The `hypershift-operator` is continually not finding `etcd` in a fully ready state during the entire test duration:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where namespace_name == 'hypershift'
| where container_name == 'operator'
| where log.controllerKind == 'HostedCluster' and log.HostedCluster.namespace == 'ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl'
| summarize
    first_occurrence = min(timestamp),
    last_occurrence = max(timestamp),
    occurrences = count()
  by msg = tostring(log.msg), err = tostring(log.error)
| order by first_occurrence asc
| where msg has 'etcd is not reporting fully available, need to watch'
```

| msg                                                  | err | first_occurrence         | last_occurrence           | occurrences |
|------------------------------------------------------|-----|--------------------------|---------------------------|-------------|
| etcd is not reporting fully available, need to watch |     | 5/19/2026, 9:01:12.96 PM | 5/19/2026, 9:46:11.032 PM | 311         |

### Why was `etcd` not fully available?

The persistent volume for the `etcd-0` replica failed to mount during the entire time the test was waiting on the
cluster to install.

#### Proof 1: Log Snippet

Events for the `etcd` pods show that volumes mounted quickly for the other replicas, but we don't see a successful mount
for `etcd-0` until after the test has timed out.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where eventNamespace == 'ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a'
        and objectName contains 'etcd-'
| where reason contains 'Volume' or message contains 'Volume'
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind            | objectName  | reason                 | message                                                                                                                                                                                                                                    | firstSeen             | lastSeen              | count | 
|-----------------------|-------------|------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------|-----------------------|-------|
| PersistentVolumeClaim | data-etcd-0 | Provisioning           | External provisioner is provisioning volume for claim "ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a/data-etcd-0"                                                                                                         | 5/19/2026, 9:01:13 PM | 5/19/2026, 9:01:13 PM | 1     | 
| PersistentVolumeClaim | data-etcd-2 | Provisioning           | External provisioner is provisioning volume for claim "ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a/data-etcd-2"                                                                                                         | 5/19/2026, 9:01:13 PM | 5/19/2026, 9:01:13 PM | 1     | 
| PersistentVolumeClaim | data-etcd-1 | Provisioning           | External provisioner is provisioning volume for claim "ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a/data-etcd-1"                                                                                                         | 5/19/2026, 9:01:13 PM | 5/19/2026, 9:01:13 PM | 1     | 
| PersistentVolumeClaim | data-etcd-1 | ExternalProvisioning   | Waiting for a volume to be created either by the external provisioner 'disk.csi.azure.com' or manually by the system administrator. If volume creation is delayed, please verify that the provisioner is running and correctly registered. | 5/19/2026, 9:01:13 PM | 5/19/2026, 9:01:16 PM | 2     | 
| PersistentVolumeClaim | data-etcd-0 | ExternalProvisioning   | Waiting for a volume to be created either by the external provisioner 'disk.csi.azure.com' or manually by the system administrator. If volume creation is delayed, please verify that the provisioner is running and correctly registered. | 5/19/2026, 9:01:13 PM | 5/19/2026, 9:01:16 PM | 3     | 
| PersistentVolumeClaim | data-etcd-2 | ExternalProvisioning   | Waiting for a volume to be created either by the external provisioner 'disk.csi.azure.com' or manually by the system administrator. If volume creation is delayed, please verify that the provisioner is running and correctly registered. | 5/19/2026, 9:01:13 PM | 5/19/2026, 9:01:16 PM | 3     | 
| PersistentVolumeClaim | data-etcd-0 | ProvisioningSucceeded  | Successfully provisioned volume pvc-40f955d8-0d40-4d5f-a635-91ce592f6a2a                                                                                                                                                                   | 5/19/2026, 9:01:23 PM | 5/19/2026, 9:01:23 PM | 1     | 
| PersistentVolumeClaim | data-etcd-1 | ProvisioningSucceeded  | Successfully provisioned volume pvc-4aef6076-7215-4e95-baf7-d39e98c719d5                                                                                                                                                                   | 5/19/2026, 9:01:23 PM | 5/19/2026, 9:01:23 PM | 1     | 
| PersistentVolumeClaim | data-etcd-2 | ProvisioningSucceeded  | Successfully provisioned volume pvc-2f8b86e8-b48b-468d-a132-9263d78b774d                                                                                                                                                                   | 5/19/2026, 9:01:23 PM | 5/19/2026, 9:01:23 PM | 1     | 
| Pod                   | etcd-2      | SuccessfulAttachVolume | AttachVolume.Attach succeeded for volume "pvc-2f8b86e8-b48b-468d-a132-9263d78b774d"                                                                                                                                                        | 5/19/2026, 9:01:31 PM | 5/19/2026, 9:01:31 PM | 1     | 
| Pod                   | etcd-1      | SuccessfulAttachVolume | AttachVolume.Attach succeeded for volume "pvc-4aef6076-7215-4e95-baf7-d39e98c719d5"                                                                                                                                                        | 5/19/2026, 9:01:31 PM | 5/19/2026, 9:01:31 PM | 1     | 
| Pod                   | etcd-0      | FailedMount            | MountVolume.MountDevice failed for volume "pvc-40f955d8-0d40-4d5f-a635-91ce592f6a2a" : rpc error: code = Internal desc = failed to find disk on lun 3. azureDisk - findDiskByLun(3) failed with error(failed to find disk by lun 3)        | 5/19/2026, 9:01:33 PM | 5/19/2026, 9:22:32 PM | 18    | 
| Pod                   | etcd-0      | SuccessfulAttachVolume | AttachVolume.Attach succeeded for volume "pvc-40f955d8-0d40-4d5f-a635-91ce592f6a2a"                                                                                                                                                        | 5/19/2026, 9:46:04 PM | 5/19/2026, 9:46:04 PM | 1     | 

### Why did the persistent volume fail to mount for the `etcd-0` replica in the first 45 minutes, but then succeed?

The storage driver got an internal error from Azure disk for 15 minutes, at which point the node was marked unhealthy,
and the auto-repair mechanism tried to reboot, reimage and redeploy the node to no avail, finally deleting the node and
spinning up a new one. The `etcd-0` pod scheduled on the new node and mounted the persistent volume without issue.

#### Proof 1: Log Snippet

The direct error from Azure Disk is seen in the failed mount events:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where eventNamespace == 'ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a'
        and objectName == 'etcd-0'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind | objectName | reason                 | message                                                                                           | firstSeen                                                                                                                                           | lastSeen                     | count                        | 
|------------|------------|------------------------|---------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------|------------------------------|
| Pod        | etcd-0     | Scheduled              | Successfully assigned ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a/etcd-0 to    | aks-userswft3-15776247-vmss00000h                                                                                                                   | 5/19/2026, 9:01:24.073141 PM | 5/19/2026, 9:01:24.073141 PM 
| Pod        | etcd-0     | FailedMount            | MountVolume.MountDevice failed for volume "pvc-40f955d8-0d40-4d5f-a635-91ce592f6a2a" : rpc error: | code = Internal desc = failed to find disk on lun 3. azureDisk - findDiskByLun(3) failed with error(failed to find disk by lun 3)                   | 5/19/2026, 9:01:33 PM        | 5/19/2026, 9:22:32 PM        | 18
| Pod        | etcd-0     | TaintManagerEviction   | Marking for deletion Pod                                                                          | ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a/etcd-0                                                                               | 5/19/2026, 9:29:52 PM        | 5/19/2026, 9:29:52 PM        | 1
| Pod        | etcd-0     | Scheduled              | Successfully assigned ocm-arohcpint-2qd822p7guniitr7ibsb7qjeccri0gdl-x0c2h7b6o1c1w1a/etcd-0 to    | aks-userswft3-15776247-vmss00000f                                                                                                                   | 5/19/2026, 9:45:57.993747 PM | 5/19/2026, 9:45:57.993747 PM 
| Pod        | etcd-0     | SuccessfulAttachVolume | AttachVolume.Attach succeeded for volume "pvc-40f955d8-0d40-4d5f-a635-91ce592f6a2a"               |                                                                                                                                                     | 5/19/2026, 9:46:04 PM        | 5/19/2026, 9:46:04 PM        | 1
| Pod        | etcd-0     | Pulling                | Pulling image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                              | 0-art-dev@sha256:d8c2b75f4be30014e9d04f7edba6d9adbb4744d57b2938731860a66d66ac8c75"                                                                  | 5/19/2026, 9:46:12 PM        | 5/19/2026, 9:46:12 PM        | 1
| Pod        | etcd-0     | Pulled                 | Successfully pulled image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                  | 0-art-dev@sha256:d8c2b75f4be30014e9d04f7edba6d9adbb4744d57b2938731860a66d66ac8c75" in 101ms (101ms including waiting). Image size: 193214387 bytes. | 5/19/2026, 9:46:12 PM        | 5/19/2026, 9:46:12 PM        | 1
| Pod        | etcd-0     | Created                | Container created                                                                                 | 5/19/2026, 9:46:12 PM                                                                                                                               | 5/19/2026, 9:46:12 PM        | 1                            | 
| Pod        | etcd-0     | Started                | Container started                                                                                 | 5/19/2026, 9:46:12 PM                                                                                                                               | 5/19/2026, 9:46:12 PM        | 1                            | 
| Pod        | etcd-0     | Pulling                | Pulling image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                              | 0-art-dev@sha256:bab348336022bc9038541b0d2e902394ac4238864f3eff7d025810f40140e9d4"                                                                  | 5/19/2026, 9:46:23 PM        | 5/19/2026, 9:46:23 PM        | 1
| Pod        | etcd-0     | Pulled                 | Successfully pulled image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                  | 0-art-dev@sha256:bab348336022bc9038541b0d2e902394ac4238864f3eff7d025810f40140e9d4" in 68ms (68ms including waiting). Image size: 135157226 bytes.   | 5/19/2026, 9:46:23 PM        | 5/19/2026, 9:46:23 PM        | 1
| Pod        | etcd-0     | Pulled                 | Container image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                            | 0-art-dev@sha256:bab348336022bc9038541b0d2e902394ac4238864f3eff7d025810f40140e9d4" already present on machine and can be accessed by the pod        | 5/19/2026, 9:46:24 PM        | 5/19/2026, 9:46:24 PM        | 1
| Pod        | etcd-0     | Pulling                | Pulling image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                              | 0-art-dev@sha256:440ded05fd114f9d115896f8522886683953358f4c78a6a17d2ad0de77a93398"                                                                  | 5/19/2026, 9:46:24 PM        | 5/19/2026, 9:46:24 PM        | 1
| Pod        | etcd-0     | Pulled                 | Successfully pulled image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                  | 0-art-dev@sha256:440ded05fd114f9d115896f8522886683953358f4c78a6a17d2ad0de77a93398" in 92ms (92ms including waiting). Image size: 182879933 bytes.   | 5/19/2026, 9:46:24 PM        | 5/19/2026, 9:46:24 PM        | 1
| Pod        | etcd-0     | Pulled                 | Container image "arohcpocpint.azurecr.io/openshift-release-dev/ocp-v4.                            | 0-art-dev@sha256:d8c2b75f4be30014e9d04f7edba6d9adbb4744d57b2938731860a66d66ac8c75" already present on machine and can be accessed by the pod        | 5/19/2026, 9:46:24 PM        | 5/19/2026, 9:46:24 PM        | 1
| Pod        | etcd-0     | Killing                | Stopping container etcd                                                                           | 5/19/2026, 9:56:17 PM                                                                                                                               | 5/19/2026, 9:56:17 PM        | 1                            | 
| Pod        | etcd-0     | Killing                | Stopping container etcd-metrics                                                                   | 5/19/2026, 9:56:17 PM                                                                                                                               | 5/19/2026, 9:56:17 PM        | 1                            | 
| Pod        | etcd-0     | Killing                | Stopping container etcd-defrag                                                                    | 5/19/2026, 9:56:17 PM                                                                                                                               | 5/19/2026, 9:56:17 PM        | 1                            | 
| Pod        | etcd-0     | Killing                | Stopping container healthz                                                                        | 5/19/2026, 9:56:17 PM                                                                                                                               | 5/19/2026, 9:56:17 PM        | 1                            | 

#### Proof 2: Log Snippet

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where objectKind == 'Node' and objectName == 'aks-userswft3-15776247-vmss00000h'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind | objectName                        | reason            | message                                                                                                                                | firstSeen                                         | lastSeen                     | count                 |
|------------|-----------------------------------|-------------------|----------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------|------------------------------|-----------------------|
| Node       | aks-userswft3-15776247-vmss00000h | NodeNotReady      | Node aks-userswft3-15776247-vmss00000h status is now: NodeNotReady                                                                     | 5/19/2026, 9:24:51 PM                             | 5/19/2026, 9:24:51 PM        | 1                     |
| Node       | aks-userswft3-15776247-vmss00000h | NodeRebootStart   | Node auto-repair is initiating a reboot action due to NotReady status persisting >5 minutes. Learn more: aka.ms/aks/node-auto-repair   | 5/19/2026, 9:34:43.708597 PM                      | 5/19/2026, 9:34:43.708597 PM |
| Node       | aks-userswft3-15776247-vmss00000h | NodeRebootEnd     | Reboot action from node auto-repair has completed. Learn more: aka.ms/aks/node-auto-repair                                             | 5/19/2026, 9:34:43.847463 PM                      | 5/19/2026, 9:34:43.847463 PM |
| Node       | aks-userswft3-15776247-vmss00000h | NodeReimageStart  | Node auto-repair is initiating a reimage action due to NotReady status persisting >5 minutes. Learn more: aka.ms/aks/node-auto-repair  | 5/19/2026, 9:39:43.281945 PM                      | 5/19/2026, 9:39:43.281945 PM |
| Node       | aks-userswft3-15776247-vmss00000h | NodeReimageEnd    | Reimage action from node auto-repair has completed. Learn more: aka.ms/aks/node-auto-repair                                            | 5/19/2026, 9:39:43.573352 PM                      | 5/19/2026, 9:39:43.573352 PM |
| Node       | aks-userswft3-15776247-vmss00000h | NodeRedeployStart | Node auto-repair is initiating a redeploy action due to NotReady status persisting >5 minutes. Learn more: aka.ms/aks/node-auto-repair | 5/19/2026, 9:44:43.632632 PM                      | 5/19/2026, 9:44:43.632632 PM |
| Node       | aks-userswft3-15776247-vmss00000h | NodeRedeployEnd   | Redeploy action from node auto-repair has completed. Learn more: aka.ms/aks/node-auto-repair                                           | 5/19/2026, 9:44:43.752846 PM                      | 5/19/2026, 9:44:43.752846 PM |
| Node       | aks-userswft3-15776247-vmss00000h | NodeRedeployError | Node auto-repair redeploy action failed due to an operation failure. Learn more: aka.ms/aks/node-auto-repair-errors                    | 5/19/2026, 9:44:43.752888 PM                      | 5/19/2026, 9:44:43.752888 PM |
| Node       | aks-userswft3-15776247-vmss00000h | DeletingNode      | Deleting node aks-userswft3-15776247-vmss00000h because it does not exist in the cloud provider                                        | 5/19/2026, 9:45:00 PM                             | 5/19/2026, 9:45:00 PM        | 1                     |
| Node       | aks-userswft3-15776247-vmss00000h | RemovingNode      | Node aks-userswft3-15776247-vmss00000h event: Removing Node                                                                            | aks-userswft3-15776247-vmss00000h from Controller | 5/19/2026, 9:45:03 PM        | 5/19/2026, 9:45:03 PM | 1 |
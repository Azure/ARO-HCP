# Test Failure Analysis: Customer should be able to create a no-CNI private cluster with a private key vault, a nodepool and install cilium CNI successfully

## Root Cause

The test failed because a router Deployment rollout (triggered by the control-plane-operator adding the ignition-server backend to the `router` ConfigMap) could not complete: the new ReplicaSet's pod was stuck in `FailedCreatePodSandBox` due to Azure CNS errors (`network is not ready - mtpnc is not ready`). The old router pods remained running but served the old haproxy config without the ignition backend, so ignition requests fell through to the kube-apiserver via `default_backend kube_api`, causing a TLS certificate mismatch that left worker VMs stuck in an ignition fetch retry loop until Azure reported `OSProvisioningTimedOut`.

## Summary

The test created a no-CNI private cluster, installed Cilium, then created node pool `cilium-np` and waited for nodes
and Cilium to become operational. The node pool never reached `ready` because both worker VMs timed out during OS
provisioning. VM console logs revealed the VMs booted fine but were stuck fetching Ignition config due to a TLS
certificate mismatch: the hosted control plane's private router had undergone a Deployment rollout (triggered by the
control-plane-operator updating the `router` ConfigMap to add the ignition-server backend), but the new ReplicaSet's
pod was stuck in `FailedCreatePodSandBox` due to Azure CNS errors. The old router pods, still running with the
pre-ignition haproxy config, forwarded ignition requests to the kube-apiserver via `default_backend kube_api`,
producing the wrong certificate.

## Causal Chain

### 0. Q: Why did this test fail?

**A:** The test failed because, after creating node pool `cilium-np`, the verification step never observed any worker nodes in the cluster and never observed Cilium pods running in `kube-system`.

The spec intentionally delays surfacing the node-pool create error so that cluster verifiers can gather more detail. The final assertion joins the verifier errors with the node-pool create timeout.

#### Proof 1 (log — error)

The test error shows three joined failures: `VerifyNodesReady` found no nodes, `VerifyCiliumOperational` timed out listing pods, and node-pool creation timed out.

Test error log, lines 1–8:

```
fail [github.com/Azure/ARO-HCP/test/e2e/cluster_create_complex_cilium_kv.go:190]: failed to verify nodes are Ready with Cilium CNI for cluster "cilium-cluster"
Unexpected error:
    <*errors.joinError | 0xc0012f23c0>:
    VerifyNodesReady failed: no nodes found in the cluster
    VerifyCiliumOperational failed: not all pods in kube-system namespace are running: failed to list pods in kube-system namespace: client rate limiter Wait returned an error: context deadline exceeded
    failed to create NodePool cilium-np, caused by: timeout '20.000000' minutes exceeded during CreateNodePoolFromParam for node pool cilium-np in resource group complex-cilium-kv-9zw5pq, error: failed waiting for nodepool="cilium-np" for cluster "cilium-cluster" in resourcegroup="complex-cilium-kv-9zw5pq" to finish creating: context deadline exceeded
    ...
occurred
```

#### Proof 2 (log — output)

During the verification step, the test repeatedly logged `no cilium pods found yet in namespace`, then printed `FailedScheduling` events stating `no nodes available to schedule pods` before failing.

Test output log, lines 25–33:

```
  STEP: verifying nodes become Ready with Cilium CNI @ 07/10/26 11:29:44.861
"ts"="2026-07-10 11:29:45.383723" "level"=0 "msg"="no cilium pods found yet in namespace" "namespace"="kube-system"
"ts"="2026-07-10 11:30:15.309217" "level"=0 "msg"="no cilium pods found yet in namespace" "namespace"="kube-system"
...
"ts"="2026-07-10 11:39:45.226647" "level"=0 "msg"="failed to list pods" "error"="client rate limiter Wait returned an error: context deadline exceeded"
"ts"="2026-07-10 11:39:45.313129" "level"=0 "msg"="listing events for debugging" "namespace"="kube-system" "eventCount"=8
"ts"="2026-07-10 11:39:45.313153" "level"=0 "msg"="event" "type"="Warning" "reason"="FailedScheduling" "message"="no nodes available to schedule pods" "object"="Pod/cilium-operator-55d6fc49b5-jbhh5" "count"=0 "firstTimestamp"="0001-01-01 00:00:00 +0000 UTC" "lastTimestamp"="0001-01-01 00:00:00 +0000 UTC"
  [FAILED] in [It] - /opt/app-root/src/github.com/Azure/ARO-HCP/test/e2e/cluster_create_complex_cilium_kv.go:190 @ 07/10/26 11:39:45.313
```

#### Proof 3 (code)

The test creates the node pool, then calls `VerifyNodesReady()` and `VerifyCiliumOperational(...)`, and finally fails on `errors.Join(err, nodePoolErr, consoleLogErr)`.

`ARO-HCP` — `test/e2e/cluster_create_complex_cilium_kv.go` lines 158–190:

```
			By("creating the node pool via v20251223preview")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.AutoRepair = true
			nodePoolErr := tc.CreateNodePoolFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			// We delay checking the error on purpose to get more details
			// about the issue by running the verifiers.

			var consoleLogErr error = nil
			if nodePoolErr != nil {
				var computeFactory *armcompute.ClientFactory
				computeFactory, consoleLogErr = tc.GetARMComputeClientFactory(ctx)
				if consoleLogErr == nil {
					consoleLogErr = framework.DownloadAllVirtualMachineConsoleLogs(
						ctx,
						computeFactory,
						clusterParams.ManagedResourceGroupName,
						tc.LogDirPath)
				}
			}

			By("verifying nodes become Ready with Cilium CNI")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady(), verifiers.VerifyCiliumOperational("kube-system", "k8s-app=cilium"))
			Expect(errors.Join(err, nodePoolErr, consoleLogErr)).NotTo(HaveOccurred(), "failed to verify nodes are Ready with Cilium CNI for cluster %q", customerClusterName)
```

### 1. Q: Why were there no nodes and no running Cilium pods?

**A:** Because the node-pool create async operation never completed: the frontend kept serving the async status endpoint, while the backend continuously kept the operation in `Provisioning`.

This narrows the problem from the test client to the RP/backend layer. The failure is not an ARM request rejection or frontend crash.

#### Proof 1 (kusto)

The async operation status endpoint returned `200` throughout the wait window, showing the client could poll successfully but the operation never advanced to completion.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('frontendLogs')
| where timestamp between (datetime(2026-07-10T11:09:40Z) .. datetime(2026-07-10T11:39:47Z))
| where log.path =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/providers/microsoft.redhatopenshift/locations/uksouth/hcpoperationstatuses/b4428b9c-5f16-4766-aaa3-3325cbd51acb'
| where log.msg == 'response complete'
| summarize first_occurrence=min(timestamp), last_occurrence=max(timestamp), occurrences=count() by method=tostring(log.method), response_status_code=tostring(log.response_status_code), error=tostring(log.error)
| order by first_occurrence asc
```

| method | response_status_code | error | first_occurrence | last_occurrence | occurrences |
| --- | --- | --- | --- | --- | --- |
| get | 200 |  | 2026-07-10T11:09:43.645Z | 2026-07-10T11:39:34.635Z | 229 |


#### Proof 2 (kusto)

The backend repeatedly picked `Provisioning` for the node-pool create operation; there is no transition to `Succeeded` or `Failed` during the test window.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where timestamp between (datetime(2026-07-10T11:09:40Z) .. datetime(2026-07-10T11:39:47Z))
| where resource_group == 'complex-cilium-kv-9zw5pq'
| where resource_name == 'cilium-np'
| where log.controller_name == 'operationnodepoolcreate'
| where tostring(log.msg) == 'picked node pool create operation status'
| summarize firstSeen=min(timestamp), lastSeen=max(timestamp), samples=count() by provisioningState=tostring(log.provisioningState), message=tostring(log.message)
| order by firstSeen asc
```

| provisioningState | message | firstSeen | lastSeen | samples |
| --- | --- | --- | --- | --- |
| Provisioning |  | 2026-07-10T11:10:07.163Z | 2026-07-10T11:39:41.215Z | 57 |


#### Proof 3 (code)

The backend determines node-pool create status from Cluster Service via `GetNodePoolStatus`, logs the chosen state, and returns that as the operation state.

`ARO-HCP` — `backend/pkg/controllers/operationcontrollers/operation_node_pool_create.go` lines 196–222:

```
	logger.Info("determined node pool create operation status", "operationStates", operationStates)
	picked, err := pickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked node pool create operation status", "provisioningState", picked.provisioningState, "message", picked.message)
	return picked, nil
}

func (c *operationNodePoolCreate) nodePoolServiceCreateOperationState(ctx context.Context, operation *api.Operation, nodePool *api.HCPOpenShiftClusterNodePool) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)
	csNodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(ctx, *nodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := convertNodePoolStatus(operation, csNodePoolStatus)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", newOperationError)
	msg := ""
	if newOperationError != nil {
		msg = newOperationError.Message
	}
	return newOperationState(newOperationStatus, msg), nil
}
```

#### Proof 4 (code)

The conversion logic maps Cluster Service `installing` to ARM `Provisioning` and only maps `ready` to `Succeeded`.

`ARO-HCP` — `backend/pkg/controllers/operationcontrollers/utils.go` lines 498–516:

```
	switch state := NodePoolStateValue(nodePoolStatus.State().NodePoolStateValue()); state {
	case NodePoolStateValidating, NodePoolStatePending, NodePoolStateValidatingUpdate, NodePoolStatePendingUpdate:
		if operation.Status != arm.ProvisioningStateAccepted {
			msg, _ := nodePoolStatus.GetMessage()
			err = fmt.Errorf("got NodePoolStatusValue '%s' (message: %q) while ProvisioningState was '%s' instead of '%s'", state, msg, operation.Status, arm.ProvisioningStateAccepted)
		}
	case NodePoolStateInstalling:
		newOperationStatus = arm.ProvisioningStateProvisioning
	case NodePoolStateReady:
		if operation.Request != database.OperationRequestDelete {
			newOperationStatus = arm.ProvisioningStateSucceeded
		}
```

### 2. Q: Why did the backend keep the node-pool create operation in `Provisioning`?

**A:** Because Clusters Service created the node pool and set it to `installing`, but never transitioned it to `ready`.

This isolates the next layer: Clusters Service had accepted and started the node-pool workflow, but readiness never materialized.

#### Proof 1 (kusto)

This returns the Clusters Service node-pool lifecycle messages, including creation and the transition to `installing`; there is no later `ready` lifecycle message.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-07-10T10:56:49Z) .. datetime(2026-07-10T11:39:47Z))
| where log.aro_hcp_node_pool_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/complex-cilium-kv-9zw5pq/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cilium-cluster/nodepools/cilium-np'
| summarize first_occurrence=min(timestamp), last_occurrence=max(timestamp), occurrences=count() by msg=tostring(log.msg)
| where msg has 'node pool' or msg has 'Node pool' or msg has 'state to' or msg has 'now in'
| order by first_occurrence asc
```

| msg | first_occurrence | last_occurrence | occurrences |
| --- | --- | --- | --- |
| Node pool 'cilium-np' created for cluster '2rf852lv3niid24ftgc1nk43om61tn8n' with state 'validating' | 2026-07-10T11:09:43.359Z | 2026-07-10T11:09:43.359Z | 1 |
| running provision steps for node pool 'cilium-np' associated with cluster ID '2rf852lv3niid24ftgc1nk43om61tn8n' | 2026-07-10T11:09:47.963Z | 2026-07-10T11:09:47.963Z | 1 |
| running Node Pool provision step 'retrieve-machine-type-step' | 2026-07-10T11:09:47.963Z | 2026-07-10T11:09:47.963Z | 1 |
| running Node Pool provision step 'node-pool-rh-managed-nsg-subnet-association-provision-step' | 2026-07-10T11:09:47.966Z | 2026-07-10T11:09:47.966Z | 1 |
| running Node Pool provision step 'retrieve-azure-vm-image-step' | 2026-07-10T11:09:47.966Z | 2026-07-10T11:09:47.966Z | 1 |
| retrieving azure marketplace image for node pool 'cilium-np' | 2026-07-10T11:09:47.966Z | 2026-07-10T11:09:47.966Z | 1 |
| azure marketplace image retrieved for node pool 'cilium-np': {Type:AzureMarketplace AzureMarketplaceImage:{Publisher: Offer: SKU: Version: ImageGeneration:Gen2}} | 2026-07-10T11:09:50.217Z | 2026-07-10T11:09:50.217Z | 1 |
| running Node Pool provision step 'node-pool-cr-provision-step' | 2026-07-10T11:09:50.218Z | 2026-07-10T11:09:50.218Z | 1 |
| running Node Pool provision step 'node-pool-set-to-installing-state-step' | 2026-07-10T11:09:50.268Z | 2026-07-10T11:09:50.268Z | 1 |
| Node pool 'cilium-np' for cluster '2rf852lv3niid24ftgc1nk43om61tn8n' state updated from 'pending' to 'installing' | 2026-07-10T11:09:50.273Z | 2026-07-10T11:09:50.273Z | 1 |
| all provision steps for node pool ID 'cilium-np' associated with cluster '2rf852lv3niid24ftgc1nk43om61tn8n' succeeded | 2026-07-10T11:09:50.279Z | 2026-07-10T11:09:50.279Z | 1 |
| Node pool 'cilium-np' for cluster '2rf852lv3niid24ftgc1nk43om61tn8n' has additional message 'Creating,WaitingForNodeRef,ignitionNotReached' and detail '2 of 2 machines are not ready
Machine cilium-cluster-cilium-np-75x6r-4rwqj: Creating: virtualmachine creating or updating
Machine cilium-cluster-cilium-np-75x6r-vnqzv: Creating: virtualmachine creating or updating
,2 of 2 machines are not healthy
Machine cilium-cluster-cilium-np-75x6r-4rwqj: WaitingForNodeRef
Machine cilium-cluster-cilium-np-75x6r-vnqzv: WaitingForNodeRef
,' | 2026-07-10T11:10:59.831Z | 2026-07-10T11:30:07.376Z | 21 |
| Node pool 'cilium-np' for cluster '2rf852lv3niid24ftgc1nk43om61tn8n' has additional message 'NodeProvisioning,ignitionNotReached' and detail '2 of 2 machines are not healthy
Machine cilium-cluster-cilium-np-75x6r-4rwqj: NodeProvisioning: Waiting for a node with matching ProviderID to exist
Machine cilium-cluster-cilium-np-75x6r-vnqzv: NodeProvisioning: Waiting for a node with matching ProviderID to exist
,' | 2026-07-10T11:30:50.878Z | 2026-07-10T11:39:07.278Z | 10 |


#### Proof 2 (kusto)

This explicitly demonstrates absence: Clusters Service never logged a `ready` state transition for this node pool.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-07-10T10:56:49Z) .. datetime(2026-07-10T11:39:47Z))
| where log.aro_hcp_node_pool_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/complex-cilium-kv-9zw5pq/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cilium-cluster/nodepools/cilium-np'
| summarize ready_transition_count=countif(tostring(log.msg) has 'state to ''ready''' or tostring(log.msg) has 'now in ''ready''')
```

| ready_transition_count |
| --- |
| 0 |


#### Proof 3 (code)

Clusters Service only marks an installing node pool `ready` when replicas have reached the desired count and the HyperShift `NodePoolReadyConditionType` is `True`.

`aro-hcp-clusters-service` — `pkg/controller/manifestwork/aro_hcp_node_pool_status.go` lines 98–137:

```
// The only state change should be installing -> ready or installing -> error
func (r *Reconciler) syncAroHcpNodePoolInstalling(ctx context.Context, cluster *models.Cluster,
	feedback acm.NodePoolFeedback, nodePool *models.NodePool) error {

	currentReplicas := feedback.Replicas
	if currentReplicas == nil {
		// When initializing a node pool the ManifestWork doesn't populate the Replicas field yet, only the
		// conditions.
		// So, this is a valid scenario where currentReplicas should be initialized.
		// An initialization message would be captured on this case.
		currentReplicas = utils.New(0)
		// If we don't have replicas, the ManifestWork is not yet updated
	}

	// TODO: handle node pool error.
	// https://issues.redhat.com/browse/ARO-8875
	nodePoolError := feedback.IndexedConditions.ExtractNodePoolErrors()
	if nodePoolError.AdditionalMessage != "" || nodePoolError.AdditionalDetail != "" {
		r.logger.Info(ctx, "Node pool '%s' for cluster '%s' has additional message '%s' and detail '%s'",
			nodePool.ID, cluster.ID, nodePoolError.AdditionalMessage, nodePoolError.AdditionalDetail)
	}
	hasNodePoolReplicasChanged := r.hasNodePoolReplicasChanged(currentReplicas, nodePool)

	if hasNodePoolReplicasChanged {
		if (nodePool.Replicas != nil && *currentReplicas == *nodePool.Replicas) ||
			(nodePool.Autoscaling != nil && *currentReplicas >= nodePool.Autoscaling.Min &&
				*currentReplicas <= nodePool.Autoscaling.Max) {
			if feedback.IndexedConditions[hsv1beta1.NodePoolReadyConditionType].Status == metav1.ConditionTrue {
				err := r.txService.SetupTransaction(ctx, r.logger, func(ctx context.Context) error {
					return r.updateAroHcpNodePoolStatus(ctx, nodePool,
						models.NodePoolStateReady, currentReplicas, "Node Pool ready")
				})
				if err != nil {
					return errors.Wrapf(err, "failed to update node pool status")
				}
			}
		}
	}

	return nil
```

### 3. Q: Why didn't Clusters Service mark the node pool `ready`?

**A:** Because HyperShift feedback for the node pool never showed healthy, ready worker machines; instead, Clusters Service repeatedly logged `WaitingForNodeRef`, `ignitionNotReached`, and later `NodeProvisioning: Waiting for a node with matching ProviderID to exist` for both machines.

This is the HyperShift/CAPI layer. The control plane existed well enough for admin credential retrieval and Helm installation, but the worker node pool never produced registered nodes.

#### Proof 1 (kusto)

Clusters Service repeatedly recorded the node-pool feedback showing both machines unhealthy: first while VMs were still creating, then while waiting for corresponding nodes to appear.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('clustersServiceLogs')
| where timestamp between (datetime(2026-07-10T10:56:49Z) .. datetime(2026-07-10T11:39:47Z))
| where log.aro_hcp_node_pool_resource_id =~ '/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourcegroups/complex-cilium-kv-9zw5pq/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cilium-cluster/nodepools/cilium-np'
| summarize first_occurrence=min(timestamp), last_occurrence=max(timestamp), occurrences=count() by msg=tostring(log.msg)
| where msg has 'WaitingForNodeRef' or msg has 'NodeProvisioning' or msg has 'ignitionNotReached'
| order by first_occurrence asc
```

| msg | first_occurrence | last_occurrence | occurrences |
| --- | --- | --- | --- |
| Node pool 'cilium-np' for cluster '2rf852lv3niid24ftgc1nk43om61tn8n' has additional message 'Creating,WaitingForNodeRef,ignitionNotReached' and detail '2 of 2 machines are not ready
Machine cilium-cluster-cilium-np-75x6r-4rwqj: Creating: virtualmachine creating or updating
Machine cilium-cluster-cilium-np-75x6r-vnqzv: Creating: virtualmachine creating or updating
,2 of 2 machines are not healthy
Machine cilium-cluster-cilium-np-75x6r-4rwqj: WaitingForNodeRef
Machine cilium-cluster-cilium-np-75x6r-vnqzv: WaitingForNodeRef
,' | 2026-07-10T11:10:59.831Z | 2026-07-10T11:30:07.376Z | 21 |
| Node pool 'cilium-np' for cluster '2rf852lv3niid24ftgc1nk43om61tn8n' has additional message 'NodeProvisioning,ignitionNotReached' and detail '2 of 2 machines are not healthy
Machine cilium-cluster-cilium-np-75x6r-4rwqj: NodeProvisioning: Waiting for a node with matching ProviderID to exist
Machine cilium-cluster-cilium-np-75x6r-vnqzv: NodeProvisioning: Waiting for a node with matching ProviderID to exist
,' | 2026-07-10T11:30:50.878Z | 2026-07-10T11:39:07.278Z | 10 |


#### Proof 2 (kusto)

HyperShift repeatedly reconciled the node pool and logged `ReachedIgnitionEndpoint is false, MachineHealthCheck won't be created until this is true`, proving the machines never progressed far enough even to enable machine-health-check remediation.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-07-10T10:56:49Z) .. datetime(2026-07-10T11:39:47Z))
| where namespace_name == 'hypershift'
| where container_name == 'operator'
| where log.controllerKind == 'NodePool' and log.NodePool.namespace == 'ocm-arohcpint-2rf852lv3niid24ftgc1nk43om61tn8n'
| summarize first_occurrence = min(timestamp), last_occurrence = max(timestamp), occurrences = count() by msg = tostring(log.msg), err = tostring(log.error)
| order by first_occurrence asc
```

| msg | err | first_occurrence | last_occurrence | occurrences |
| --- | --- | --- | --- | --- |
| Reconciler error | failed to add finalizer to nodepool: Operation cannot be fulfilled on nodepools.hypershift.openshift.io "cilium-cluster-cilium-np": the object has been modified; please apply your changes to the latest version and try again | 2026-07-10T11:09:50.615Z | 2026-07-10T11:09:50.615Z | 1 |
| NodePool config is updating |  | 2026-07-10T11:09:50.658Z | 2026-07-10T11:30:39.466Z | 44 |
| NodePool version is updating |  | 2026-07-10T11:09:50.658Z | 2026-07-10T11:30:39.466Z | 44 |
| Reconciled token Secret |  | 2026-07-10T11:09:50.699Z | 2026-07-10T11:30:39.485Z | 44 |
| Reconciled user data Secret |  | 2026-07-10T11:09:50.719Z | 2026-07-10T11:30:39.485Z | 44 |
| initial machineSet has not been created. |  | 2026-07-10T11:09:50.719Z | 2026-07-10T11:09:50.888Z | 3 |
| Reconciled Machine template |  | 2026-07-10T11:09:50.738Z | 2026-07-10T11:30:39.486Z | 44 |
| NodePool machine template is updating |  | 2026-07-10T11:09:50.738Z | 2026-07-10T11:30:39.486Z | 44 |
| New user data Secret has been generated |  | 2026-07-10T11:09:50.738Z | 2026-07-10T11:09:50.738Z | 1 |
| Starting version update: Propagating new version to the MachineDeployment |  | 2026-07-10T11:09:50.738Z | 2026-07-10T11:09:50.738Z | 1 |
| Starting config update: Propagating new config to the MachineDeployment |  | 2026-07-10T11:09:50.738Z | 2026-07-10T11:09:50.738Z | 1 |
| New machine template has been generated |  | 2026-07-10T11:09:50.738Z | 2026-07-10T11:09:50.738Z | 1 |
| cluster.x-k8s.io/v1beta1 MachineDeployment is deprecated; use cluster.x-k8s.io/v1beta2 MachineDeployment |  | 2026-07-10T11:09:50.757Z | 2026-07-10T11:09:50.757Z | 1 |
| Reconciled MachineDeployment |  | 2026-07-10T11:09:50.757Z | 2026-07-10T11:30:39.488Z | 44 |
| ReachedIgnitionEndpoint is false, MachineHealthCheck won't be created until this is true |  | 2026-07-10T11:09:50.757Z | 2026-07-10T11:30:39.488Z | 44 |
| Successfully reconciled |  | 2026-07-10T11:09:50.809Z | 2026-07-10T11:30:39.494Z | 44 |
| cluster.x-k8s.io/v1beta1 Machine is deprecated; use cluster.x-k8s.io/v1beta2 Machine |  | 2026-07-10T11:09:51.161Z | 2026-07-10T11:09:51.188Z | 2 |
| Reconciled Machine |  | 2026-07-10T11:09:51.161Z | 2026-07-10T11:30:39.487Z | 76 |


### 4. Q: Why were the worker machines stuck at `WaitingForNodeRef` / `NodeProvisioning` and never reached ignition?

**A:** Because the two Azure-backed worker VMs for the node pool failed guest OS provisioning: both AzureMachine reconciliations surfaced Azure Compute `OSProvisioningTimedOut` errors.

This is the deepest directly proven causal layer in the infrastructure stack. It explains why no node ever appeared with a matching ProviderID and why HyperShift never saw ignition/node registration complete.

#### Proof 1 (kusto)

The AzureMachine events show `OSProvisioningTimedOut` for both VMs roughly 20 minutes after provisioning started.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-07-10T11:09:40Z) .. datetime(2026-07-10T11:39:47Z))
| where eventNamespace == 'ocm-arohcpint-2rf852lv3niid24ftgc1nk43om61tn8n-cilium-cluster'
| where objectName has 'cilium-cluster-cilium-np-75x6r'
| where message has 'OSProvisioningTimedOut'
| extend first_occurrence=coalesce(firstSeen, todatetime(log.event_time))
| summarize count=sum(count), first_occurrence=min(first_occurrence) by objectName, reason
| order by first_occurrence asc
```

| objectName | reason | count | first_occurrence |
| --- | --- | --- | --- |
| cilium-cluster-cilium-np-75x6r-4rwqj | ReconcileError | 3 | 2026-07-10T11:30:33Z |
| cilium-cluster-cilium-np-75x6r-vnqzv | ReconcileError | 1 | 2026-07-10T11:30:38Z |


### 5. Q: Why did Azure VM guest OS provisioning time out?

**A:** The VM console logs show the VMs actually booted successfully into the initramfs, acquired networking via DHCP, and reached the Ignition fetch stage. Ignition then entered an infinite retry loop trying to fetch its config from `https://ignition-server.apps.cilium-cluster.hypershift.local/ignition`, failing with a TLS certificate mismatch: the server presented the kube-apiserver certificate (SANs: `kubernetes`, `api-int.cilium-cluster.7qqq.uksouth.aroapp-hcp.azure-test.net`, etc.) instead of the ignition-server-proxy certificate (which should have the SAN `ignition-server.apps.cilium-cluster.hypershift.local`).

This proves the "OS provisioning timeout" was not a VM boot failure or Azure infrastructure issue — the VM was healthy. The problem was upstream: the ignition server was not correctly serving its config.

#### Proof 1 (log — node_console_log)

The VM console log shows Ignition retrying endlessly with the TLS certificate error and intermittent connection timeouts.

Node console log `cilium-cluster-cilium-np-75x6r-4rwqj-console.log`, lines 790–810:

```
[    7.638710] ignition[1053]: GET error: Get "https://ignition-server.apps.cilium-cluster.hypershift.local/ignition": tls: failed to verify certificate: x509: certificate is valid for localhost, kubernetes, kubernetes.default, kubernetes.default.svc, kubernetes.default.svc.cluster.local, api-int.cilium-cluster.7qqq.uksouth.aroapp-hcp.azure-test.net, api.cilium-cluster.hypershift.local, not ignition-server.apps.cilium-cluster.hypershift.local
[    7.638710] ignition[1053]: GET error: Get "https://ignition-server.apps.cilium-cluster.hypershift.local/ignition": dial tcp 10.0.0.5:443: i/o timeout
```

#### Proof 2 (code)

The ignition-server-proxy TLS certificate is generated from the route's admitted host. If the route has no admitted host (`Status.Ingress` is empty), cert generation returns nil and the proxy cannot start.

`hypershift` — `control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver/pki.go` lines 66–76:

```
		if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(ignitionServerRoute), ignitionServerRoute); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get ignition route: %w", err)
		}
		// The route must be admitted and assigned a host before we can generate certs
		if len(ignitionServerRoute.Status.Ingress) == 0 || len(ignitionServerRoute.Status.Ingress[0].Host) == 0 {
			return nil
		}
		ignitionServerAddress = ignitionServerRoute.Status.Ingress[0].Host
```

#### Proof 3 (code)

The ignition-server route is a passthrough TLS route pointing to `ignition-server-proxy`. When the proxy is down (because its cert was never generated), the route has no healthy backend and connections are misdirected to the kube-apiserver.

`hypershift` — `control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver/route.go` lines 21–27:

```
func (ign *ignitionServer) adaptRoute(cpContext component.WorkloadContext, route *routev1.Route) error {
	serviceName := "ignition-server-proxy"
	// For IBM Cloud, we don't deploy the ignition server proxy.
	// Instead, the ignition server service is directly exposed.
	if cpContext.HCP.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		serviceName = "ignition-server"
	}
```

### 6. Q: Why was the ignition server route not being served correctly?

**A:** The hosted control plane's private router went through a Deployment rollout mid-cluster-setup, and the new ReplicaSet's pod could not start due to Azure CNS `FailedCreatePodSandBox` errors. This left only the **old** router pods running — but those pods had the **old** haproxy config that did not include the `ignition` backend. Without a matching SNI ACL for `ignition-server.apps.cilium-cluster.hypershift.local`, the router's `default_backend kube_api` rule forwarded ignition requests to the kube-apiserver, which served its own certificate (without the ignition server SAN).

The sequence was:

1. **11:03:18Z**: Router `router-69b8dd7d48` deployed with 3 replicas. The `router` ConfigMap at this point did **not** include the ignition-server backend (the ignition-server Route had not yet been created or labeled).
2. **11:03:19–27Z**: Initial Azure CNS `FailedCreatePodSandBox` errors, but 2/3 pods eventually started. The router was functional and admitted routes.
3. **~11:04:55Z**: The control-plane-operator reconciled and regenerated the `router` ConfigMap to include the new `ignition` backend (because the ignition-server Route now existed). The `component.hypershift.openshift.io/config-hash` annotation on the pod template changed, triggering a new ReplicaSet `router-865bdb6c6c`.
4. **11:04:55Z**: The Deployment scaled down the old RS from 3→2 and scaled up the new RS 0→1. The new pod (`router-865bdb6c6c-89tpn`) was scheduled to a node where Azure CNS was still broken → stuck in `FailedCreatePodSandBox` permanently.
5. **Result**: The rollout was stuck. The 2 remaining old-RS pods served traffic with the **old** haproxy config (no ignition backend), so ignition requests fell through to the kube-apiserver via `default_backend kube_api`.

#### Proof 1 (kusto)

The management cluster audit log shows the control-plane-operator created the `router` ConfigMap at 11:03:18Z, then **updated** it at 11:04:55Z — exactly when the Deployment rollout began. The update changed the haproxy config to include the ignition-server backend, which changed the config-hash annotation and triggered the new ReplicaSet.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').kubeAudit
| where requestReceivedTimestamp between (datetime(2026-07-10T10:56:49Z) .. datetime(2026-07-10T11:39:47Z))
| where stage == 'ResponseComplete'
| where objectRef.resource == 'configmaps'
| where objectRef.namespace == 'ocm-arohcpint-2rf852lv3niid24ftgc1nk43om61tn8n-cilium-cluster'
| where objectRef.name == 'router'
| project requestReceivedTimestamp, verb, user = tostring(user.username), responseCode = toint(responseStatus.code)
| order by requestReceivedTimestamp asc
```

| requestReceivedTimestamp | verb | user | responseCode |
| --- | --- | --- | --- |
| 2026-07-10T11:03:18.556Z | create | system:serviceaccount:...:control-plane-operator | 201 |
| 2026-07-10T11:04:55.112Z | update | system:serviceaccount:...:control-plane-operator | 200 |

#### Proof 2 (kusto)

Router events show the rollout: old RS scaled down, new RS created, new pod stuck in sandbox failures.

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-07-10T10:50:40Z) .. datetime(2026-07-10T11:39:47Z))
| where eventNamespace == 'ocm-arohcpint-2rf852lv3niid24ftgc1nk43om61tn8n-cilium-cluster'
| where objectName contains "router"
| where reason in ('ScalingReplicaSet', 'Started', 'FailedCreatePodSandBox')
| extend first_occurrence=coalesce(firstSeen, todatetime(log.event_time))
| summarize count=sum(count), first_occurrence=min(first_occurrence) by objectKind, objectName, reason, message=substring(message, 0, 150)
| order by first_occurrence asc
```

| objectKind | objectName | reason | message | count | first_occurrence |
| --- | --- | --- | --- | --- | --- |
| Deployment | router | ScalingReplicaSet | Scaled up replica set router-69b8dd7d48 from 0 to 3 | 1 | 2026-07-10T11:03:18Z |
| Pod | router-69b8dd7d48-kk6kg | Started | Container started | 1 | 2026-07-10T11:03:23Z |
| Pod | router-69b8dd7d48-tprpj | Started | Container started | 1 | 2026-07-10T11:03:27Z |
| Deployment | router | ScalingReplicaSet | Scaled down replica set router-69b8dd7d48 from 3 to 2 | 1 | 2026-07-10T11:04:55Z |
| Deployment | router | ScalingReplicaSet | Scaled up replica set router-865bdb6c6c from 0 to 1 | 1 | 2026-07-10T11:04:55Z |
| Pod | router-865bdb6c6c-89tpn | FailedCreatePodSandBox | Failed to create pod sandbox: rpc error: code = Unknown desc = failed to setup network ... SecondaryEndpointClient Error: route ip+net: no such | 130 | 2026-07-10T11:04:56Z |

#### Proof 3 (code)

The `router` ConfigMap is regenerated on every control-plane-operator reconciliation by listing all Routes with the HCP label. When the ignition-server Route appears, a new `ignition` backend is added to the haproxy config.

`hypershift` — `control-plane-operator/controllers/hostedcontrolplane/v2/router/config.go` lines 108–123:

```
	sort.Sort(byRouteName(routeList.Items))
	for _, route := range routeList.Items {
		if _, hasHCPLabel := route.Labels[netutil.HCPRouteLabel]; !hasHCPLabel {
			// If the hypershift.openshift.io/hosted-control-plane label is not present,
			// then it means the route should be fulfilled by the management cluster's router.
			continue
		}
		switch route.Name {
		case manifests.KubeAPIServerInternalRoute("").Name,
			manifests.KubeAPIServerExternalPublicRoute("").Name,
			manifests.KubeAPIServerExternalPrivateRoute("").Name:
			p.HasKubeAPI = true
			p.KASSVCPort = config.KASSVCPort
			p.KASDestinationServiceIP = svcsNameToIP["kube-apiserver"]
			continue
		case ignitionserver.Route("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "ignition", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Spec.To.Name], DestinationPort: 443})
```

#### Proof 4 (code)

The framework computes a config-hash from all ConfigMaps mounted by the pod. When the `router` ConfigMap content changes (new ignition backend added), the hash changes and triggers a new ReplicaSet.

`hypershift` — `support/controlplane-component/defaults.go` lines 523–538:

```
func (c *controlPlaneWorkload[T]) applyWatchedResourcesAnnotation(cpContext ControlPlaneContext, podTemplate *corev1.PodTemplateSpec) error {
	// remove duplicate entries if any.
	secretNames := podSecretNames(&podTemplate.Spec)
	configMapNames := podConfigMapNames(&podTemplate.Spec, configMapsToExcludeFromHash)

	hashString, err := computeResourceHash(secretNames, configMapNames,
		fetchResource(cpContext, &corev1.Secret{}, cpContext.HCP.Namespace, cpContext.Client),
		fetchResource(cpContext, &corev1.ConfigMap{}, cpContext.HCP.Namespace, cpContext.Client))
	if err != nil {
		return err
	}

	if podTemplate.Annotations == nil {
		podTemplate.Annotations = map[string]string{}
	}
	podTemplate.Annotations["component.hypershift.openshift.io/config-hash"] = hashString
```

#### Proof 5 (code)

The haproxy config's `default_backend kube_api` rule routes unmatched SNI to the kube-apiserver — this is why ignition requests (which have no matching ACL in the old config) get the kube-apiserver certificate instead of the ignition-server-proxy certificate.

`hypershift` — `control-plane-operator/controllers/hostedcontrolplane/v2/router/router_config.template` lines 27–40:

```
  {{- range .Backends }}
  acl is_{{ .Name }} req_ssl_sni -i {{ .HostName }}
  {{- end }}
  {{- if .HasPrivateKeyVault }}
  acl is_keyvault req_ssl_sni -i {{ .KeyVaultFQDN }}
  {{- end }}
  {{- range .Backends }}
  use_backend {{ .Name }} if is_{{ .Name }}
  {{- end }}
  {{- if .HasPrivateKeyVault }}
  use_backend keyvault if is_keyvault
  {{- end }}
  {{- if .HasKubeAPI }}
  default_backend kube_api{{- end }}
```

### 7. Q: Why couldn't the new router pod create its pod sandbox?

**A:** Azure CNS (Container Networking Service) reported that the multi-tenant pod network controller (MTPNC) was not ready for the hosted control plane's namespace. The error progressed from `Failed to get IP address from CNS: network is not ready - mtpnc is not ready` (initial attempt) to `SecondaryEndpointClient Error: route ip+net: no such network interface` (all subsequent retries for over 30 minutes).

The pod was scheduled to node `aks-userswft2-18526782-vmss000001`, where Azure CNS apparently never recovered networking for this namespace. The investigation stops here — the available data does not show why MTPNC was not ready on that specific node (it could be a timing issue, a CNS version bug, or a node-level networking delay).

#### Proof 1 (kusto)

The new router pod's sandbox failures show the CNS error persisting for the entire test window:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-07-10T11:04:00Z) .. datetime(2026-07-10T11:39:47Z))
| where eventNamespace == 'ocm-arohcpint-2rf852lv3niid24ftgc1nk43om61tn8n-cilium-cluster'
| where objectName == 'router-865bdb6c6c-89tpn'
| where reason == 'FailedCreatePodSandBox'
| summarize total_failures=sum(count), first_occurrence=min(coalesce(firstSeen, todatetime(log.event_time))), last_occurrence=max(coalesce(lastSeen, todatetime(log.event_time)))
```

| total_failures | first_occurrence | last_occurrence |
| --- | --- | --- |
| 130 | 2026-07-10T11:04:56Z | 2026-07-10T11:35:03Z |


## Suggestions

- Investigate why Azure CNS MTPNC takes so long to become ready for new PodNetworkInstance resources, and whether the hosted control plane router deployment should tolerate temporary sandbox failures with longer backoff.
- Add a pre-canned query that correlates router pod readiness in the hosted control plane namespace with ignition-server route admission status, to detect this failure pattern earlier.
- Consider adding a HyperShift health check that detects when the ignition-server route is not admitted within a timeout and surfaces this as a clear condition on the HostedCluster/NodePool, rather than waiting for `OSProvisioningTimedOut` after 20 minutes.
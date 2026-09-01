# Cosmos DB Data Flow: Frontend Endpoints and Backend Controllers

This document maps every actor (frontend endpoint or backend controller) to the Cosmos DB
objects and fields it reads and writes, shows the execution order as a digraph, and highlights
fields written by more than one actor.

All resources live in a single Cosmos container ("Resources"). Every write is a full document
replacement with ETag-based optimistic concurrency. The `InstanceVersion` field is
auto-incremented on each Replace.

---

## 1. Frontend Endpoint Writes

### PUT Subscription

**Path:** `PUT /subscriptions/{subscriptionId}`
**Handler:** `ArmSubscriptionPut` ([frontend.go](../frontend/pkg/frontend/frontend.go))
**Write method:** Standalone `Create()` or `Replace()` (not transactional)


| Object         | Fields Written |
| -------------- | -------------- |
| `Subscription` |                |


Side effect: if `State == Deleted`, calls `DeleteAllResourcesInSubscription` which
transitively deletes all clusters (and their children) via transactional batches.

---



### PUT Cluster (Create)

**Path:** `PUT .../hcpOpenShiftClusters/{name}` (resource does not exist)
**Handler:** `createHCPCluster` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Transactional batch (`AddCreateToTransaction` x2)


| Object                | Fields Written |
| --------------------- | -------------- |
| `HCPOpenShiftCluster` |                |
| `Operation`           |                |


---



### PUT Cluster (Update)

**Path:** `PUT .../hcpOpenShiftClusters/{name}` (resource exists)
**Handler:** `updateHCPClusterInCosmos` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)


| Object                | Fields Written |
| --------------------- | -------------- |
| `HCPOpenShiftCluster` |                |
| `Operation`           |                |


---



### PATCH Cluster (Update)

**Path:** `PATCH .../hcpOpenShiftClusters/{name}`
**Handler:** `updateHCPClusterInCosmos` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)


| Object                | Fields Written |
| --------------------- | -------------- |
| `HCPOpenShiftCluster` |                |
| `Operation`           |                |


---



### DELETE Cluster

**Path:** `DELETE .../hcpOpenShiftClusters/{name}`
**Handler:** `addDeleteClusterToTransaction` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Single transactional batch containing cluster + all child resources


| Object                                   | Fields Written |
| ---------------------------------------- | -------------- |
| `HCPOpenShiftCluster`                    |                |
| `Operation`                              |                |
| Child `NodePool`s (each)                 |                |
| Child `NodePool` `Operation`s (each)     |                |
| Child `ExternalAuth`s (each)             |                |
| Child `ExternalAuth` `Operation`s (each) |                |
| Canceled `Operation`s                    |                |


---



### PUT NodePool (Create)

**Path:** `PUT .../nodePools/{name}` (resource does not exist)
**Handler:** `createNodePool` ([node_pool.go](../frontend/pkg/frontend/node_pool.go))
**Write method:** Transactional batch (`AddCreateToTransaction` x2)


| Object                        | Fields Written |
| ----------------------------- | -------------- |
| `HCPOpenShiftClusterNodePool` |                |
| `Operation`                   |                |


---



### PUT/PATCH NodePool (Update)

**Path:** `PUT/PATCH .../nodePools/{name}` (resource exists)
**Handler:** `updateNodePoolInCosmos` ([node_pool.go](../frontend/pkg/frontend/node_pool.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)


| Object                        | Fields Written |
| ----------------------------- | -------------- |
| `HCPOpenShiftClusterNodePool` |                |
| `Operation`                   |                |


---



### DELETE NodePool

**Path:** `DELETE .../nodePools/{name}`
**Handler:** `addDeleteNodePoolToTransaction` ([node_pool.go](../frontend/pkg/frontend/node_pool.go))
**Write method:** Transactional batch


| Object                        | Fields Written |
| ----------------------------- | -------------- |
| `HCPOpenShiftClusterNodePool` |                |
| `Operation`                   |                |
| Canceled `Operation`s         |                |


---



### PUT ExternalAuth (Create)

**Path:** `PUT .../externalAuths/{name}` (resource does not exist)
**Handler:** `createExternalAuth` ([external_auth.go](../frontend/pkg/frontend/external_auth.go))
**Write method:** Transactional batch (`AddCreateToTransaction` x2)


| Object                            | Fields Written |
| --------------------------------- | -------------- |
| `HCPOpenShiftClusterExternalAuth` |                |
| `Operation`                       |                |


---



### PUT/PATCH ExternalAuth (Update)

**Path:** `PUT/PATCH .../externalAuths/{name}` (resource exists)
**Handler:** `updateExternalAuthInCosmos` ([external_auth.go](../frontend/pkg/frontend/external_auth.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)


| Object                            | Fields Written |
| --------------------------------- | -------------- |
| `HCPOpenShiftClusterExternalAuth` |                |
| `Operation`                       |                |


---



### DELETE ExternalAuth

**Path:** `DELETE .../externalAuths/{name}`
**Handler:** `addDeleteExternalAuthToTransaction` ([external_auth.go](../frontend/pkg/frontend/external_auth.go))
**Write method:** Transactional batch


| Object                            | Fields Written |
| --------------------------------- | -------------- |
| `HCPOpenShiftClusterExternalAuth` |                |
| `Operation`                       |                |
| Canceled `Operation`s             |                |


---



### POST RequestAdminCredential

**Path:** `POST .../requestadmincredential`
**Handler:** `ArmResourceActionRequestAdminCredential` ([frontend.go](../frontend/pkg/frontend/frontend.go))
**Write method:** Transactional batch (single item)


| Object      | Fields Written |
| ----------- | -------------- |
| `Operation` |                |


No resource document is modified.

---



### POST RevokeCredentials

**Path:** `POST .../revokecredentials`
**Handler:** `ArmResourceActionRevokeCredentials` ([frontend.go](../frontend/pkg/frontend/frontend.go))
**Write method:** Transactional batch (canceled ops + operation + cluster replace)


| Object                | Fields Written |
| --------------------- | -------------- |
| `HCPOpenShiftCluster` |                |
| `Operation`           |                |
| Canceled `Operation`s |                |




### Admin API: GET BackupSchedule

**Path:** `GET /admin/v1/hcp/subscriptions/{subscriptionId}/resourcegroups/{resourceGroupName}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{resourceName}/backupschedules`
**Handler:** `HCPGetBackupScheduleHandler` ([backups.go](../admin/server/handlers/hcp/backups.go))


|      | Object                         | Fields |
| ---- | ------------------------------ | ------ |
| Read | `ServiceProviderCluster`       |        |
| Read | `ReadDesire` (kube-applier DB) |        |


No writes to Cosmos Resources container.

### Admin API: PATCH BackupSchedule

**Path:** `PATCH /admin/v1/hcp/subscriptions/{subscriptionId}/resourcegroups/{resourceGroupName}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{resourceName}/backupschedules`
**Handler:** `HCPPatchBackupScheduleHandler` ([backups.go](../admin/server/handlers/hcp/backups.go))


| Object                   | Fields Written |
| ------------------------ | -------------- |
| `ServiceProviderCluster` |                |


---



## 2. Backend Controller Reads and Writes



### Operation Controllers

These watch the ActiveOperations informer (10s resync). Each gates on `Operation.Request` type,
`ExternalID.ResourceType`, and non-terminal `Operation.Status`. All use `UpdateOperationStatus`
which performs a **transactional batch** to atomically update the operation and associated resource.

#### OperationClusterCreate

**File:** [operation_cluster_create.go](../backend/pkg/controllers/cluster/operations/operation_cluster_create.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Create`
- `Operation.ExternalID.ResourceType` == `ClusterResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on Cluster):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                     | Fields |
| --------- | -------------------------- | ------ |
| Read      | `Operation`                |        |
| Read      | `HCPOpenShiftCluster`      |        |
| Read      | ReadDesire (HostedCluster) |        |
| Read      | Cluster Service            |        |
| **Write** | `Operation`                |        |
| **Write** | `HCPOpenShiftCluster`      |        |




#### OperationClusterUpdate

**File:** [operation_cluster_update.go](../backend/pkg/controllers/cluster/operations/operation_cluster_update.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Update`
- `Operation.ExternalID.ResourceType` == `ClusterResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on Cluster):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                                   | Fields |
| --------- | ---------------------------------------- | ------ |
| Read      | `Operation`                              |        |
| Read      | `HCPOpenShiftCluster`                    |        |
| Read      | `ServiceProviderCluster`                 |        |
| Read      | Controller(`ControlPlaneDesiredVersion`) |        |
| Read      | ReadDesire (HostedCluster)               |        |
| Read      | Cluster Service                          |        |
| **Write** | `Operation`                              |        |
| **Write** | `HCPOpenShiftCluster`                    |        |




#### OperationClusterDelete

**File:** [operation_cluster_delete.go](../backend/pkg/controllers/cluster/operations/operation_cluster_delete.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Delete`
- `Operation.ExternalID.ResourceType` == `ClusterResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on Cluster):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `Operation`           |        |
| Read      | `HCPOpenShiftCluster` |        |
| Read      | Cluster Service       |        |
| **Write** | `Operation`           |        |
| **Write** | `HCPOpenShiftCluster` |        |




#### OperationNodePoolCreate

**File:** [operation_node_pool_create.go](../backend/pkg/controllers/nodepool/operations/operation_node_pool_create.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Create`
- `Operation.ExternalID.ResourceType` == `NodePoolResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on NodePool):**

- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `Operation`                   |        |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | Cluster Service               |        |
| **Write** | `Operation`                   |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |




#### OperationNodePoolUpdate

**File:** [operation_node_pool_update.go](../backend/pkg/controllers/nodepool/operations/operation_node_pool_update.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Update`
- `Operation.ExternalID.ResourceType` == `NodePoolResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on NodePool):**

- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `Operation`                   |        |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | `ServiceProviderNodePool`     |        |
| Read      | Controller(`NodepoolVersion`) |        |
| Read      | ReadDesire (NodePool)         |        |
| Read      | Cluster Service               |        |
| **Write** | `Operation`                   |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |




#### OperationNodePoolDelete

**File:** [operation_node_pool_delete.go](../backend/pkg/controllers/nodepool/operations/operation_node_pool_delete.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Delete`
- `Operation.ExternalID.ResourceType` == `NodePoolResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on NodePool):**

- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `Operation`                   |        |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | Cluster Service               |        |
| **Write** | `Operation`                   |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |




#### OperationExternalAuthCreate

**File:** [operation_external_auth_create.go](../backend/pkg/controllers/externalauth/operations/operation_external_auth_create.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Create`
- `Operation.ExternalID.ResourceType` == `ExternalAuthResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `Operation`                       |        |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| Read      | Cluster Service                   |        |
| **Write** | `Operation`                       |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |




#### OperationExternalAuthUpdate

**File:** [operation_external_auth_update.go](../backend/pkg/controllers/externalauth/operations/operation_external_auth_update.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Update`
- `Operation.ExternalID.ResourceType` == `ExternalAuthResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `Operation`                       |        |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| Read      | ReadDesire (HostedCluster)        |        |
| Read      | Cluster Service                   |        |
| **Write** | `Operation`                       |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |




#### OperationExternalAuthDelete

**File:** [operation_external_auth_delete.go](../backend/pkg/controllers/externalauth/operations/operation_external_auth_delete.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Delete`
- `Operation.ExternalID.ResourceType` == `ExternalAuthResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `Operation`                       |        |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| Read      | Cluster Service                   |        |
| **Write** | `Operation`                       |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |




#### DispatchRequestCredential

**File:** [dispatch_request_credential.go](../backend/pkg/controllers/cluster/credentials/operations/dispatch_request_credential.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RequestCredential`
- `len(Operation.InternalID.String())` == 0 (not yet dispatched)


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `Operation`           |        |
| Read      | `HCPOpenShiftCluster` |        |
| **Write** | `Operation`           |        |




#### OperationRequestCredential

**File:** [operation_request_credential.go](../backend/pkg/controllers/cluster/credentials/operations/operation_request_credential.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RequestCredential`
- `len(Operation.InternalID.String())` > 0 (has been dispatched)


|           | Object          | Fields |
| --------- | --------------- | ------ |
| Read      | `Operation`     |        |
| Read      | Cluster Service |        |
| **Write** | `Operation`     |        |




#### DispatchRevokeCredentials

**File:** [dispatch_revoke_credentials.go](../backend/pkg/controllers/cluster/credentials/operations/dispatch_revoke_credentials.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RevokeCredentials`
- `Operation.Status` == `Accepted` (not yet dispatched)


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `Operation`           |        |
| Read      | `HCPOpenShiftCluster` |        |
| **Write** | `Operation`           |        |




#### OperationRevokeCredentials

**File:** [operation_revoke_credentials.go](../backend/pkg/controllers/cluster/credentials/operations/operation_revoke_credentials.go)
**Gate (ShouldProcess on Operation):**

- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RevokeCredentials`
- `Operation.Status` != `Accepted` (must already be dispatched)


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `Operation`           |        |
| Read      | `HCPOpenShiftCluster` |        |
| Read      | Cluster Service       |        |
| **Write** | `Operation`           |        |
| **Write** | `HCPOpenShiftCluster` |        |


---



### Cluster Creation Controllers



#### ClusterClusterServiceCreate

**File:** [cluster_cluster_service_create_controller.go](../backend/pkg/controllers/cluster/creation/cluster_cluster_service_create_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (needsWork on Cluster):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` == nil or empty


|           | Object                   | Fields |
| --------- | ------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`    |        |
| Read      | `ServiceProviderCluster` |        |
| Read      | `Subscription`           |        |
| Read      | Cluster Service          |        |
| **Write** | `HCPOpenShiftCluster`    |        |
| **Write** | `ServiceProviderCluster` |        |


---



### Cluster Update Controllers



#### ClusterClusterServiceUpdateDispatch

**File:** [cluster_cluster_service_update_dispatch_controller.go](../backend/pkg/controllers/cluster/update/cluster_cluster_service_update_dispatch_controller.go)
**Trigger:** Cluster informer, 1-minute resync **Gate (needsWork on Cluster):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil and non-empty


|      | Object                   | Fields |
| ---- | ------------------------ | ------ |
| Read | `HCPOpenShiftCluster`    |        |
| Read | `ServiceProviderCluster` |        |
| Read | `Subscription`           |        |
| Read | Cluster Service          |        |


No Cosmos writes. Dispatches updates to Cluster Service via PATCH.

---



### Cluster Deletion Controllers



#### ClusterClusterServiceDeleteDispatch

**File:** [cluster_cluster_service_delete_dispatch_controller.go](../backend/pkg/controllers/cluster/deletion/cluster_cluster_service_delete_dispatch_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (NeedsWork on Cluster):**

- `Cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach` == true
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` == nil


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `HCPOpenShiftCluster` |        |
| **Write** | `HCPOpenShiftCluster` |        |




#### ClusterDeletionClusterServiceIDClearer

**File:** [cluster_cluster_service_id_clearer.go](../backend/pkg/controllers/cluster/deletion/cluster_cluster_service_id_clearer.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (NeedsWork on Cluster):**

- `Cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach` == true
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil and non-empty


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `HCPOpenShiftCluster` |        |
| Read      | Cluster Service       |        |
| **Write** | `HCPOpenShiftCluster` |        |




#### ClusterChildResourcesCleanupController

**File:** [cluster_child_resources_cleanup_controller.go](../backend/pkg/controllers/cluster/deletion/cluster_child_resources_cleanup_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (NeedsWork on Cluster):**

- `Cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach` == true
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` == nil


|           | Object                   | Fields |
| --------- | ------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`    |        |
| Read      | `ServiceProviderCluster` |        |
| Read      | Child NodePools          |        |
| Read      | Child ExternalAuths      |        |
| **Write** | Child Cosmos docs        |        |




#### ClusterDeletionController

**File:** [cluster_deletion_controller.go](../backend/pkg/controllers/cluster/deletion/cluster_deletion_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (NeedsWork on Cluster):**

- `Cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach` == true
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` == nil

**Additional SyncOnce preconditions:**

- All NodePools deleted
- All ExternalAuths deleted
- All child Cosmos resources deleted (except controllers)
- All Maestro readonly bundles cleared


|           | Object                   | Fields |
| --------- | ------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`    |        |
| Read      | `ServiceProviderCluster` |        |
| Read      | Child NodePools          |        |
| Read      | Child ExternalAuths      |        |
| Read      | Child Cosmos resources   |        |
| **Write** | `BillingDocument`        |        |
| **Write** | `HCPOpenShiftCluster`    |        |


---



### NodePool Creation Controllers



#### NodePoolClusterServiceCreate

**File:** [node_pool_cluster_service_create_controller.go](../backend/pkg/controllers/nodepool/creation/node_pool_cluster_service_create_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (needsWork on NodePool):**

- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` == nil or empty


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | `HCPOpenShiftCluster`         |        |
| Read      | Cluster Service               |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |


---



### NodePool Update Controllers



#### NodePoolClusterServiceUpdateDispatch

**File:** [node_pool_cluster_service_update_dispatch_controller.go](../backend/pkg/controllers/nodepool/update/node_pool_cluster_service_update_dispatch_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (needsWork on NodePool):**

- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil and non-empty


|      | Object                        | Fields |
| ---- | ----------------------------- | ------ |
| Read | `HCPOpenShiftClusterNodePool` |        |
| Read | Cluster Service               |        |


No Cosmos writes. Dispatches updates to Cluster Service via PATCH.

---



### NodePool Deletion Controllers



#### NodePoolClusterServiceDeleteDispatch

**File:** [node_pool_cluster_service_delete_dispatch_controller.go](../backend/pkg/controllers/nodepool/deletion/node_pool_cluster_service_delete_dispatch_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**

- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` == nil


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |




#### NodePoolDeletionClusterServiceIDClearer

**File:** [node_pool_cluster_service_id_clearer.go](../backend/pkg/controllers/nodepool/deletion/node_pool_cluster_service_id_clearer.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**

- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil and non-empty


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | Cluster Service               |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |




#### NodePoolChildResourcesCleanupController

**File:** [node_pool_child_resources_cleanup_controller.go](../backend/pkg/controllers/nodepool/deletion/node_pool_child_resources_cleanup_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**

- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` == nil


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | `ServiceProviderNodePool`     |        |
| Read      | `ServiceProviderCluster`      |        |
| **Write** | Child Cosmos docs             |        |




#### NodePoolDeletionController

**File:** [node_pool_deletion_controller.go](../backend/pkg/controllers/nodepool/deletion/node_pool_deletion_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**

- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` == nil

**Additional SyncOnce preconditions:**

- All Maestro readonly bundles cleared
- All child Cosmos resources deleted (except controllers)


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | `ServiceProviderNodePool`     |        |
| Read      | Child Cosmos resources        |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |


---



### ExternalAuth Creation Controllers



#### ExternalAuthClusterServiceCreate

**File:** [external_auth_cluster_service_create_controller.go](../backend/pkg/controllers/externalauth/creation/external_auth_cluster_service_create_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (needsWork on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` == nil or empty


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| Read      | `HCPOpenShiftCluster`             |        |
| Read      | Cluster Service                   |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |


---



### ExternalAuth Update Controllers



#### ExternalAuthClusterServiceUpdateDispatch

**File:** [external_auth_cluster_service_update_dispatch_controller.go](../backend/pkg/controllers/externalauth/update/external_auth_cluster_service_update_dispatch_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (needsWork on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil and non-empty


|      | Object                            | Fields |
| ---- | --------------------------------- | ------ |
| Read | `HCPOpenShiftClusterExternalAuth` |        |
| Read | Cluster Service                   |        |


No Cosmos writes. Dispatches updates to Cluster Service via PATCH.

---



### ExternalAuth Deletion Controllers



#### ExternalAuthClusterServiceDeleteDispatch

**File:** [external_auth_cluster_service_delete_dispatch_controller.go](../backend/pkg/controllers/externalauth/deletion/external_auth_cluster_service_delete_dispatch_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` == nil


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |




#### ExternalAuthDeletionClusterServiceIDClearer

**File:** [external_auth_cluster_service_id_clearer.go](../backend/pkg/controllers/externalauth/deletion/external_auth_cluster_service_id_clearer.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil and non-empty


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| Read      | Cluster Service                   |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |




#### ExternalAuthChildResourcesCleanupController

**File:** [external_auth_child_resources_cleanup_controller.go](../backend/pkg/controllers/externalauth/deletion/external_auth_child_resources_cleanup_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` == nil


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| **Write** | Child Cosmos docs                 |        |




#### ExternalAuthDeletionController

**File:** [external_auth_deletion_controller.go](../backend/pkg/controllers/externalauth/deletion/external_auth_deletion_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**

- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` == nil

**Additional SyncOnce preconditions:**

- All child resources deleted (except controllers)


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| Read      | Child Cosmos resources            |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |


---



### Upgrade Controllers



#### ControlPlaneDesiredVersion

**File:** [control_plane_desired_version_controller.go](../backend/pkg/controllers/cluster/version/control_plane_desired_version_controller.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate:** No formal NeedsWork. Skips inside SyncOnce if `DeletionTimestamp != nil`, or if `DesiredVersion` already set AND cluster < 2hr old AND active Create operation exists.


|           | Object                               | Fields |
| --------- | ------------------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`                |        |
| Read      | `ServiceProviderCluster`             |        |
| Read      | `Subscription`                       |        |
| Read      | NodePools + ServiceProviderNodePools |        |
| Read      | Cincinnati                           |        |
| **Write** | `ServiceProviderCluster`             |        |
| **Write** | **Controller doc**                   |        |




#### ControlPlaneActiveVersions

**File:** [control_plane_active_version_controller.go](../backend/pkg/controllers/cluster/version/control_plane_active_version_controller.go)
**Trigger:** Cluster informer, 5-minute resync


|           | Object                     | Fields |
| --------- | -------------------------- | ------ |
| Read      | ReadDesire (HostedCluster) |        |
| Read      | ReadDesire (HostedCluster) |        |
| **Write** | `ServiceProviderCluster`   |        |




#### TriggerControlPlaneUpgrade

**Trigger:** Cluster informer, 5-minute resync

No Cosmos writes. Posts `ControlPlaneUpgradePolicy` to Cluster Service.

#### NodePoolVersion

**File:** [nodepool_version_controller.go](../backend/pkg/controllers/nodepool/version/nodepool_version_controller.go)
**Trigger:** NodePool informer, 5-minute resync
**Gate (NeedsWork on NodePool + ServiceProviderNodePool):**

- `len(NodePool.Properties.Version.ID)` > 0
- `ServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion` == nil, or differs from parsed `NodePool.Properties.Version.ID`


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | `ServiceProviderNodePool`     |        |
| Read      | `ServiceProviderCluster`      |        |
| **Write** | `ServiceProviderNodePool`     |        |
| **Write** | **Controller doc**            |        |




#### NodePoolActiveVersions

**File:** [nodepool_active_version_controller.go](../backend/pkg/controllers/nodepool/version/nodepool_active_version_controller.go)
**Trigger:** NodePool informer, 5-minute resync
**Gate (NeedsWork on ServiceProviderNodePool):**

- `ServiceProviderNodePool` != nil (document must exist)


|           | Object                    | Fields |
| --------- | ------------------------- | ------ |
| Read      | ReadDesire (NodePool)     |        |
| **Write** | `ServiceProviderNodePool` |        |




#### TriggerNodePoolUpgrade

**Trigger:** NodePool informer, 5-minute resync

No Cosmos writes. Posts `NodePoolUpgradePolicy` to Cluster Service.

---



### Properties Sync Controllers



#### ClusterPropertiesSync

**File:** [cluster_properties_sync.go](../backend/pkg/controllers/cluster/properties/cluster_properties_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate:** No formal NeedsWork. Skips inside SyncOnce if `CustomerProperties.DNS.BaseDomainPrefix` empty or HostedCluster ReadDesire does not exist.


|           | Object                     | Fields |
| --------- | -------------------------- | ------ |
| Read      | `HCPOpenShiftCluster`      |        |
| Read      | ReadDesire (HostedCluster) |        |
| **Write** | `HCPOpenShiftCluster`      |        |




#### ClusterBaseDomainPrefixSync

**File:** [cluster_base_domain_prefix_sync.go](../backend/pkg/controllers/cluster/properties/cluster_base_domain_prefix_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate (needsWork on Cluster):**

- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil and non-empty
- `len(Cluster.CustomerProperties.DNS.BaseDomainPrefix)` == 0


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `HCPOpenShiftCluster` |        |
| Read      | Cluster Service       |        |
| **Write** | `HCPOpenShiftCluster` |        |




#### DesiredControlPlaneSize

**File:** [desired_control_plane_size_sync.go](../backend/pkg/controllers/cluster/properties/desired_control_plane_size_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate (NeedsWork on ServiceProviderCluster):**

- `ServiceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize` != `ServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize` (pointer comparison via `ptrStringEqual`)


|           | Object                   | Fields |
| --------- | ------------------------ | ------ |
| Read      | `ServiceProviderCluster` |        |
| Read      | `HCPOpenShiftCluster`    |        |
| **Write** | `ServiceProviderCluster` |        |
| **Write** | Cluster Service          |        |




#### ServiceProviderClusterPropertiesSync

**File:** [serviceprovidercluster_properties_sync.go](../backend/pkg/controllers/cluster/properties/serviceprovidercluster_properties_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate:** No formal NeedsWork. Skips inside SyncOnce if ServiceProviderCluster does not exist or HostedCluster ReadDesire has no namespace/name.


|           | Object                     | Fields |
| --------- | -------------------------- | ------ |
| Read      | `ServiceProviderCluster`   |        |
| Read      | ReadDesire (HostedCluster) |        |
| **Write** | `ServiceProviderCluster`   |        |




#### ClusterIdentitySync

**File:** [cluster_identity_sync.go](../backend/pkg/controllers/cluster/identity/cluster_identity_sync.go)
**Trigger:** Cluster informer, 60-minute resync
**Gate (NeedsWork on Cluster):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil, AND
- `Cluster.Identity` != nil and `len(Cluster.Identity.UserAssignedIdentities)` > 0


|           | Object                   | Fields |
| --------- | ------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`    |        |
| Read      | `ServiceProviderCluster` |        |
| **Write** | `HCPOpenShiftCluster`    |        |




#### FetchMSIIdentitiesInfo

**File:** [fetch_msi_identities_info.go](../backend/pkg/controllers/cluster/identity/fetch_msi_identities_info.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (needsWork):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `ServiceProviderCluster.Status.MSIManagedIdentities.EarliestRecheckTime` is nil or in the past, OR
- stored MSI identities on SPC no longer match `OperatorsAuthentication` (control-plane operator bindings / service managed identity resource ID), in which case EarliestRecheckTime is ignored


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftCluster`         |        |
| Read      | `ServiceProviderCluster`      |        |
| Read      | Managed Identities Data Plane |        |
| **Write** | `ServiceProviderCluster`      |        |


---



### Other Controllers



#### ManagementClusterPlacementSync

**File:** [management_cluster_placement_sync.go](../backend/pkg/controllers/cluster/placement/management_cluster_placement_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate (needsWork on ServiceProviderCluster):**

- `ServiceProviderCluster.Status.ManagementClusterResourceID` == nil


|           | Object                   | Fields |
| --------- | ------------------------ | ------ |
| Read      | `ServiceProviderCluster` |        |
| Read      | `HCPOpenShiftCluster`    |        |
| Read      | Cluster Service          |        |
| Read      | `ManagementCluster`      |        |
| **Write** | `ServiceProviderCluster` |        |




#### BackfillClusterUID

**File:** [backfill_cluster_uid.go](../backend/pkg/controllers/mismatch/backfill_cluster_uid.go)
**Trigger:** Cluster informer, 60-minute cooldown
**Gate (NeedsWork on Cluster):**

- `len(Cluster.ServiceProviderProperties.ClusterUID)` == 0


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `HCPOpenShiftCluster` |        |
| Read      | `BillingDocument`     |        |
| **Write** | `HCPOpenShiftCluster` |        |




#### CreateBillingDoc

**File:** [create_billing_doc.go](../backend/pkg/controllers/billing/create_billing_doc.go)
**Trigger:** Cluster informer, 60-second cooldown
**Gate (NeedsWork on Cluster):**

- `len(Cluster.ServiceProviderProperties.ClusterUID)` > 0
- `len(Cluster.ServiceProviderProperties.BillingDocumentCosmosID)` == 0
- `Cluster.ServiceProviderProperties.ProvisioningState` == `Succeeded`


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `HCPOpenShiftCluster` |        |
| Read      | `Subscription`        |        |
| **Write** | `BillingDocument`     |        |
| **Write** | `HCPOpenShiftCluster` |        |




#### OrphanedBillingCleanup

**File:** [orphaned_billing_cleanup.go](../backend/pkg/controllers/billing/orphaned_billing_cleanup.go)
**Trigger:** Time-based, 60-minute jitter (no informer — queues work directly)
**Gate:**

- `BillingDocument.DeletionTime` == nil (skip already-deleted docs)
- Corresponding `HCPOpenShiftCluster` must not exist (orphan detection)


|           | Object                | Fields |
| --------- | --------------------- | ------ |
| Read      | `BillingDocument`     |        |
| Read      | `HCPOpenShiftCluster` |        |
| **Write** | `BillingDocument`     |        |




#### DeleteOrphanedCosmosResources

**File:** [delete_orphaned_cosmos.go](../backend/pkg/controllers/mismatch/delete_orphaned_cosmos.go)
**Trigger:** Time-based, 60-minute jitter (no informer — queues all subscriptions)
**Gate:**

- Resource is not a cluster (clusters own themselves)
- Resource is inside a resource group (resources outside RG have TTL)
- Parent resource does not exist (orphan detection)


|           | Object               | Fields |
| --------- | -------------------- | ------ |
| Read      | All resources        |        |
| Read      | `ManagementCluster`  |        |
| Read      | Kube-applier desires |        |
| **Write** | Orphaned resources   |        |
| **Write** | Orphaned desires     |        |


---



#### ClusterValidation / NodePoolValidation

**File:** [cluster_validation_controller.go](../backend/pkg/controllers/cluster/validation/cluster_validation_controller.go), [nodepool_validation_controller.go](../backend/pkg/controllers/nodepool/validation/nodepool_validation_controller.go)
**Trigger:** Cluster/NodePool informer, 1-minute resync
**Gate (shouldProcess on ServiceProviderCluster/ServiceProviderNodePool):**

- `!meta.IsStatusConditionTrue(ServiceProviderCluster.Status.Validations, validation.Name())` (condition must not yet be True)
- SyncOnce also checks `DeletionTimestamp == nil` on the resource


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `ServiceProviderCluster`      |        |
| Read      | `ServiceProviderNodePool`     |        |
| Read      | `HCPOpenShiftCluster`         |        |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| **Write** | `ServiceProviderCluster`      |        |
| **Write** | `ServiceProviderNodePool`     |        |




#### DegradedAggregators (Cluster / NodePool / ExternalAuth)

**File:** [cluster_degraded_aggregator.go](../backend/pkg/controllers/cluster/status/cluster_degraded_aggregator.go), [nodepool_degraded_aggregator.go](../backend/pkg/controllers/nodepool/status/nodepool_degraded_aggregator.go), [externalauth_degraded_aggregator.go](../backend/pkg/controllers/externalauth/status/externalauth_degraded_aggregator.go)
**Trigger:** Resource informer, 1-minute resync


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | Controller docs                   |        |
| **Write** | `HCPOpenShiftCluster`             |        |
| **Write** | `HCPOpenShiftClusterNodePool`     |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |




#### ClusterRequirementsValidAggregator

**File:** [cluster_requirements_valid_aggregator.go](../backend/pkg/controllers/statuscontrollers/cluster_requirements_valid_aggregator.go)
**Trigger:** Cluster / ServiceProviderCluster informer, 1-minute resync
**Gate (SyncOnce preconditions):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `ServiceProviderCluster` exists


|           | Object                   | Fields |
| --------- | ------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`    |        |
| Read      | `ServiceProviderCluster` |        |
| **Write** | `HCPOpenShiftCluster`    |        |




#### NodePoolRequirementsValidAggregator

**File:** [nodepool_requirements_valid_aggregator.go](../backend/pkg/controllers/statuscontrollers/nodepool_requirements_valid_aggregator.go)
**Trigger:** NodePool / ServiceProviderNodePool informer, 1-minute resync
**Gate (SyncOnce preconditions):**

- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `ServiceProviderNodePool` exists


|           | Object                        | Fields |
| --------- | ----------------------------- | ------ |
| Read      | `HCPOpenShiftClusterNodePool` |        |
| Read      | `ServiceProviderNodePool`     |        |
| **Write** | `HCPOpenShiftClusterNodePool` |        |




#### ExternalAuthAvailableController

**File:** [externalauth_available_controller.go](../backend/pkg/controllers/externalauth/status/externalauth_available_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (SyncOnce preconditions):**

- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil


|           | Object                            | Fields |
| --------- | --------------------------------- | ------ |
| Read      | `HCPOpenShiftClusterExternalAuth` |        |
| Read      | ReadDesire (HostedCluster)        |        |
| **Write** | `HCPOpenShiftClusterExternalAuth` |        |




#### BackupScheduleSyncer

**File:** [schedule_controller.go](../backend/pkg/controllers/backupcontroller/schedule_controller.go)
**Trigger:** Cluster informer, periodic resync
**Gate (needsWork on Cluster):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.BillingDocumentCosmosID` != ""


|           | Object                          | Fields |
| --------- | ------------------------------- | ------ |
| Read      | `HCPOpenShiftCluster`           |        |
| Read      | `ServiceProviderCluster`        |        |
| **Write** | `ApplyDesire` (kube-applier DB) |        |
| **Write** | `ReadDesire` (kube-applier DB)  |        |


No writes to the Cosmos Resources container.

#### CreateClusterScopedReadDesires / CreateNodePoolScopedReadDesires

**File:** [create_cluster_scoped_read_desires_controller.go](../backend/pkg/controllers/cluster/readdesires/create_cluster_scoped_read_desires_controller.go), [create_nodepool_scoped_read_desires_controller.go](../backend/pkg/controllers/nodepool/readdesires/create_nodepool_scoped_read_desires_controller.go)
**Trigger:** Cluster/NodePool informer, 1-minute resync
**Gate (SyncOnce preconditions + ReadDesire spec drift):**

- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil
- `ServiceProviderCluster.Status.ManagementClusterResourceID` != nil
- `len(Cluster.CustomerProperties.DNS.BaseDomainPrefix)` > 0
- Existing `ReadDesire` == nil, or `ReadDesire.Spec.ManagementCluster` differs, or `ReadDesire.Spec.TargetItem` differs (both controllers reconcile via the shared `kubeapplierhelpers.EnsureReadDesire` helper, consulting the ReadDesire informer lister)


|           | Object                         | Fields |
| --------- | ------------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`          |        |
| Read      | `ServiceProviderCluster`       |        |
| Read      | Existing `ReadDesire`          |        |
| **Write** | `ReadDesire` (kube-applier DB) |        |




#### FetchDataPlaneOperatorsManagedIdentitiesInfo

**File:** [fetch_data_plane_operators_managed_identities_info.go](../backend/pkg/controllers/cluster/identity/fetch_data_plane_operators_managed_identities_info.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (needsWork on ServiceProviderCluster):**

- Skipped entirely when `HCPOpenShiftCluster.ServiceProviderProperties.DeletionTimestamp` != nil
- Honors `ServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime` only when the unique data plane operator ResourceID set on `Status.DataPlaneOperatorsManagedIdentities.Identities` still matches the desired set from `CustomerProperties`; returns true (query Azure) immediately on any mismatch
- When identities match: returns false while `EarliestRecheckTime` is in the future; true when `EarliestRecheckTime` is nil or already past


|           | Object                               | Fields |
| --------- | ------------------------------------ | ------ |
| Read      | `HCPOpenShiftCluster`                |        |
| Read      | `ServiceProviderCluster`             |        |
| Read      | Azure (UserAssignedIdentitiesClient) |        |
| **Write** | `ServiceProviderCluster`             |        |




#### ObserveManagedResourceGroup

**File:** [managed_resource_group_controller.go](../backend/pkg/controllers/cluster/azureresources/managed_resource_group_controller.go)
**Trigger:** Cluster informer, 5-minute resync
**Behavior:** Observe-only — never creates or deletes the managed resource group. A `NeedsWork` gate skips the sync when there is nothing to do: while the cluster is not being deleted, only until the managed resource group is confirmed as `AzureResource` (it is immutable, so a confirmed reference never needs re-checking); while the cluster is being deleted, only while a reference is still set.

- Not deleting (reconcile): records the managed resource group as `PendingAzureResource` and persists that intent **before** querying Azure ("set pending before Get"), so a Get failure — or a resource group that does not exist yet — still leaves a durable pending marker (keeping the deletion gate closed) rather than an empty reference. It then queries Azure and switches on the result:
  - **not found** → does nothing, leaving the pending marker in place (Cluster Service owns creation; this controller is observe-only).
  - **other error** → returns the error so the sync retries.
  - **exists** → if the resource group is owned by another cluster (its `ManagedBy` is set and does not equal this cluster's ID via the `ResourceIDsEqual` helper) it returns an error and does **not** set `AzureResource`; otherwise (owned by this cluster, or `ManagedBy` absent) it clears `PendingAzureResource` and records the resource group as `AzureResource`.
- Deleting: derives the resource group ID from the reference still on the document (guaranteed set by `NeedsWork`), queries Azure and switches on the result: once the resource group is gone it clears both references so the deletion gate opens; on any other error it returns the error so the gate stays closed until the state is known; while the resource group still exists it does nothing (it does not set `AzureResource`, write a pending marker, or perform the ownership check).


|           | Object                       | Fields |
| --------- | ---------------------------- | ------ |
| Read      | `HCPOpenShiftCluster`        |        |
| Read      | `Subscription`               |        |
| Read      | `ServiceProviderCluster`     |        |
| Read      | Azure (ResourceGroupsClient) |        |
| **Write** | `ServiceProviderCluster`     |        |


---



## 3. Execution Order Digraphs



### Cluster Create Flow

```
                        PUT Cluster (Frontend)
                               |
                    creates Operation(Create)
                    creates HCPOpenShiftCluster
                               |
              +----------------+----------------+
              |                                 |
              v                                 v
  ControlPlaneDesiredVersion          ManagementClusterPlacementSync
  (sets SPC.Spec.DesiredVersion)      (sets SPC.Status.MgmtClusterResourceID)
              |                                 |
              v                                 |
  ClusterClusterServiceCreate  <----------------+
  (sets Cluster.SP.ClusterServiceID)    (gates on DesiredVersion + MgmtCluster)
              |
              +---------------------+----------------+------------------------------+
              |                     |                |                              |
              v                     v                v                              v
  ControlPlaneActiveVersions  ClusterPropertiesSync  SPClusterPropertiesSync  CreateClusterScopedReadDesires
  (sets SPC.Status.ActiveVers)  (sets SP.Console,   (sets SPC.Status          (creates kube-applier ReadDesire)
              |                  DNS, API, IssuerURL) .HostedClusterNamespace,
              v                                       .ControlPlaneNamespace)  |
  TriggerControlPlaneUpgrade                                                   v
  (posts upgrade policy to CS)                                        ClusterValidation*
              |                                                       (sets SPC.Status.Validations)
              v
  OperationClusterCreate
  (polls CS + ReadDesire status -> sets Operation.Status -> sets Cluster.SP.ProvisioningState)
              |
              v
  BackfillClusterUID (gates on ClusterUID empty)
  (sets Cluster.SP.ClusterUID)
              |
              v
  CreateBillingDoc (gates on ProvisioningState=Succeeded + ClusterUID non-empty)
  (creates BillingDocument, sets Cluster.SP.BillingDocumentCosmosID)
```



### Cluster Update Flow

```
  PUT/PATCH Cluster (Frontend)
         |
  creates Operation(Update)
  replaces HCPOpenShiftCluster
         |
         +----------------------------+
         |                            |
         v                            v
  ControlPlaneDesiredVersion   ClusterClusterServiceUpdateDispatch
  (advances SPC.Spec.          (PATCHes CS with dispatch config
   DesiredVersion if changed)   for CIDRs, autoscaling, etc.)
         |
         v
  TriggerControlPlaneUpgrade
  (posts upgrade policy to CS)
         |
         v
  OperationClusterUpdate
  (polls CS status + ReadDesire -> sets Operation.Status -> sets Cluster.SP.ProvisioningState)
```



### Cluster Delete Flow

```
  DELETE Cluster (Frontend)
         |
  creates Operation(Delete)
  sets DeletionTimestamp, UsesNewClusterDeletionApproach
  (also creates delete ops for child NodePools + ExternalAuths)
         |
         v
  ClusterClusterServiceDeleteDispatch
  (calls CS DeleteCluster -> sets ClusterServiceDeletionTimestamp)
         |
         v
  ClusterDeletionClusterServiceIDClearer
  (polls CS until 404 -> clears ClusterServiceID)
         |
         v
  ClusterChildResourcesCleanupController
  (waits for all NPs/EAs deleted -> deletes child Cosmos docs)
         |
         v
  ClusterDeletionController
  (marks BillingDoc deleted -> DELETES Cluster document)
         |
         v
  OperationClusterDelete
  (detects cluster doc missing -> marks Operation Succeeded)
```



### NodePool Create Flow

```
  PUT NodePool (Frontend)
         |
  creates Operation(Create)
  creates HCPOpenShiftClusterNodePool
         |
         +---------------------+
         |                     |
         v                     v
  NodePoolClusterServiceCreate   NodePoolVersion
  (sets NP.SP.ClusterServiceID)  (sets SPNP.Spec.DesiredVersion)
         |                        |
         |                        v
         |              TriggerNodePoolUpgrade
         |              (posts upgrade policy to CS)
         |                        |
         +------------------------+
         |
         v
  OperationNodePoolCreate
  (polls CS -> sets Operation.Status -> sets NP.Properties.ProvisioningState)
```



### NodePool Delete Flow

```
  DELETE NodePool (Frontend)
         |
  creates Operation(Delete), sets DeletionTimestamp
         |
         v
  NodePoolClusterServiceDeleteDispatch
  (calls CS -> sets ClusterServiceDeletionTimestamp)
         |
         v
  NodePoolDeletionClusterServiceIDClearer
  (polls CS until 404 -> clears ClusterServiceID)
         |
         v
  NodePoolChildResourcesCleanupController
  (deletes child Cosmos docs)
         |
         v
  NodePoolDeletionController
  (DELETES NodePool document)
         |
         v
  OperationNodePoolDelete
  (detects NP doc missing -> marks Operation Succeeded)
```

---



## 4. Fields Written by Multiple Actors

Each entry links to every actor that writes the field.

### `HCPOpenShiftCluster.ServiceProviderProperties.ProvisioningState`


| Actor                                                       | When                                            |
| ----------------------------------------------------------- | ----------------------------------------------- |
| [Frontend: PUT Cluster (Create)](#put-cluster-create)       | Sets to `Accepted`                              |
| [Frontend: PUT/PATCH Cluster (Update)](#put-cluster-update) | Sets to `Accepted`                              |
| [Frontend: DELETE Cluster](#delete-cluster)                 | Sets to `Deleting`                              |
| [OperationClusterCreate](#operationclustercreate)           | Advances to `Provisioning`/`Succeeded`/`Failed` |
| [OperationClusterUpdate](#operationclusterupdate)           | Advances to `Updating`/`Succeeded`/`Failed`     |
| [OperationClusterDelete](#operationclusterdelete)           | Advances to `Deleting`/`Succeeded`/`Failed`     |




### `HCPOpenShiftCluster.ServiceProviderProperties.ActiveOperationID`


| Actor                                                       | When                             |
| ----------------------------------------------------------- | -------------------------------- |
| [Frontend: PUT Cluster (Create)](#put-cluster-create)       | Sets to new operation ID         |
| [Frontend: PUT/PATCH Cluster (Update)](#put-cluster-update) | Sets to new operation ID         |
| [Frontend: DELETE Cluster](#delete-cluster)                 | Sets to new operation ID         |
| [OperationClusterCreate](#operationclustercreate)           | Clears to `""` on terminal state |
| [OperationClusterUpdate](#operationclusterupdate)           | Clears to `""` on terminal state |
| [OperationClusterDelete](#operationclusterdelete)           | Clears to `""` on terminal state |




### `HCPOpenShiftCluster.ServiceProviderProperties.ClusterServiceID`


| Actor                                                                             | When                       |
| --------------------------------------------------------------------------------- | -------------------------- |
| [ClusterClusterServiceCreate](#clusterclusterservicecreate)                       | Sets from CS POST response |
| [ClusterDeletionClusterServiceIDClearer](#clusterdeletionclusterserviceidclearer) | Clears to `nil` on CS 404  |




### `HCPOpenShiftCluster.ServiceProviderProperties.RevokeCredentialsOperationID`


| Actor                                                       | When                                    |
| ----------------------------------------------------------- | --------------------------------------- |
| [Frontend: POST RevokeCredentials](#post-revokecredentials) | Sets to operation ID                    |
| [OperationRevokeCredentials](#operationrevokecredentials)   | Clears to `""` when operation completes |




### `HCPOpenShiftCluster.CustomerProperties.DNS.BaseDomainPrefix`


| Actor                                                       | When                       |
| ----------------------------------------------------------- | -------------------------- |
| [Frontend: PUT Cluster (Create)](#put-cluster-create)       | Sets from request body     |
| [ClusterBaseDomainPrefixSync](#clusterbasedomainprefixsync) | Backfills from CS if empty |




### `HCPOpenShiftCluster.Identity.UserAssignedIdentities`


| Actor                                                       | When                                                                                                                 |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| [Frontend: PUT Cluster (Create)](#put-cluster-create)       | Rebuilt via `completeClusterIdentity`                                                                                |
| [Frontend: PUT/PATCH Cluster (Update)](#put-cluster-update) | Rebuilt via `completeClusterIdentity` with old data                                                                  |
| [ClusterIdentitySync](#clusteridentitysync)                 | Keeps ClientID/PrincipalID on existing Identity keys in sync with ServiceProviderCluster.Status.MSIManagedIdentities |




### `HCPOpenShiftCluster.ServiceProviderProperties.DeletionTimestamp`


| Actor                                       | When                 |
| ------------------------------------------- | -------------------- |
| [Frontend: DELETE Cluster](#delete-cluster) | Sets to current time |


Single writer, but gates the entire deletion pipeline.

### `HCPOpenShiftCluster.ServiceProviderProperties.ClusterUID`


| Actor                                     | When                                 |
| ----------------------------------------- | ------------------------------------ |
| [BackfillClusterUID](#backfillclusteruid) | Sets from billing doc ID or new UUID |


Single writer, but gates `CreateBillingDoc`.

### `HCPOpenShiftCluster.ServiceProviderProperties.BillingDocumentCosmosID`


| Actor                                 | When                            |
| ------------------------------------- | ------------------------------- |
| [CreateBillingDoc](#createbillingdoc) | Sets after billing doc creation |


Single writer, but gates billing lifecycle.

### `HCPOpenShiftCluster.Status.Conditions`


| Actor                                                                             | When                                                            |
| --------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| [ClusterDegradedAggregator](#degradedaggregators-cluster--nodepool--externalauth) | Aggregated `Degraded` condition from all controller status docs |


Single writer.

### `HCPOpenShiftCluster.Status.UserFacingConditions`


| Actor                                                                     | When                                                                                      |
| ------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| [ClusterRequirementsValidAggregator](#clusterrequirementsvalidaggregator) | Aggregated `RequirementsValid` condition from `ServiceProviderCluster.Status.Validations` |


Single writer today (`RequirementsValid` only).

### `HCPOpenShiftClusterNodePool.Status.UserFacingConditions`


| Actor                                                                       | When                                                                                       |
| --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| [NodePoolRequirementsValidAggregator](#nodepoolrequirementsvalidaggregator) | Aggregated `RequirementsValid` condition from `ServiceProviderNodePool.Status.Validations` |


Single writer today (`RequirementsValid` only).

### `HCPOpenShiftClusterNodePool.Properties.ProvisioningState`


| Actor                                                              | When                                            |
| ------------------------------------------------------------------ | ----------------------------------------------- |
| [Frontend: PUT NodePool (Create)](#put-nodepool-create)            | Sets to `Accepted`                              |
| [Frontend: PUT/PATCH NodePool (Update)](#putpatch-nodepool-update) | Sets to `Accepted`                              |
| [Frontend: DELETE NodePool](#delete-nodepool)                      | Sets to `Deleting`                              |
| [OperationNodePoolCreate](#operationnodepoolcreate)                | Advances to `Provisioning`/`Succeeded`/`Failed` |
| [OperationNodePoolUpdate](#operationnodepoolupdate)                | Advances to `Updating`/`Succeeded`/`Failed`     |
| [OperationNodePoolDelete](#operationnodepooldelete)                | Advances to `Deleting`/`Succeeded`/`Failed`     |




### `HCPOpenShiftClusterNodePool.ServiceProviderProperties.ActiveOperationID`


| Actor                                                              | When                     |
| ------------------------------------------------------------------ | ------------------------ |
| [Frontend: PUT NodePool (Create)](#put-nodepool-create)            | Sets to new operation ID |
| [Frontend: PUT/PATCH NodePool (Update)](#putpatch-nodepool-update) | Sets to new operation ID |
| [Frontend: DELETE NodePool](#delete-nodepool)                      | Sets to new operation ID |
| [OperationNodePoolCreate](#operationnodepoolcreate)                | Clears on terminal       |
| [OperationNodePoolUpdate](#operationnodepoolupdate)                | Clears on terminal       |
| [OperationNodePoolDelete](#operationnodepooldelete)                | Clears on terminal       |




### `HCPOpenShiftClusterNodePool.ServiceProviderProperties.ClusterServiceID`


| Actor                                                                               | When                       |
| ----------------------------------------------------------------------------------- | -------------------------- |
| [NodePoolClusterServiceCreate](#nodepoolclusterservicecreate)                       | Sets from CS POST response |
| [NodePoolDeletionClusterServiceIDClearer](#nodepooldeletionclusterserviceidclearer) | Clears to `nil` on CS 404  |




### `HCPOpenShiftClusterExternalAuth.Properties.ProvisioningState`


| Actor                                                                      | When                                        |
| -------------------------------------------------------------------------- | ------------------------------------------- |
| [Frontend: PUT ExternalAuth (Create)](#put-externalauth-create)            | Sets to `Accepted`                          |
| [Frontend: PUT/PATCH ExternalAuth (Update)](#putpatch-externalauth-update) | Sets to `Accepted`                          |
| [Frontend: DELETE ExternalAuth](#delete-externalauth)                      | Sets to `Deleting`                          |
| [OperationExternalAuthCreate](#operationexternalauthcreate)                | Advances to `Succeeded`/`Failed`            |
| [OperationExternalAuthUpdate](#operationexternalauthupdate)                | Advances to `Updating`/`Succeeded`/`Failed` |
| [OperationExternalAuthDelete](#operationexternalauthdelete)                | Advances to `Deleting`/`Succeeded`/`Failed` |




### `HCPOpenShiftClusterExternalAuth.ServiceProviderProperties.ClusterServiceID`


| Actor                                                                                       | When                       |
| ------------------------------------------------------------------------------------------- | -------------------------- |
| [ExternalAuthClusterServiceCreate](#externalauthclusterservicecreate)                       | Sets from CS POST response |
| [ExternalAuthDeletionClusterServiceIDClearer](#externalauthdeletionclusterserviceidclearer) | Clears to `nil` on CS 404  |




### `Operation.Status`


| Actor                                                            | When                                                                                      |
| ---------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| [Frontend (all mutating endpoints)](#1-frontend-endpoint-writes) | Sets to `Accepted` (or `Deleting` for Delete operations)                                  |
| [All Operation* controllers](#operation-controllers)             | Advances through lifecycle (`Provisioning`/`Updating`/`Deleting` -> `Succeeded`/`Failed`) |
| [DispatchRequestCredential](#dispatchrequestcredential)          | Sets to `Canceled` (if revocation in progress) or sets `InternalID`                       |
| [DispatchRevokeCredentials](#dispatchrevokecredentials)          | Sets to `Deleting` (after CS dispatch) or `Canceled` (on mismatch)                        |




### `ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion`


| Actor                                                     | When                                                        |
| --------------------------------------------------------- | ----------------------------------------------------------- |
| [ControlPlaneDesiredVersion](#controlplanedesiredversion) | Sets/advances based on customer version intent + Cincinnati |


Single writer, but read by `ClusterClusterServiceCreate` (gate), `OperationClusterUpdate`, and `TriggerControlPlaneUpgrade`.

### `ServiceProviderCluster.Status.ManagementClusterResourceID`


| Actor                                                             | When                         |
| ----------------------------------------------------------------- | ---------------------------- |
| [ManagementClusterPlacementSync](#managementclusterplacementsync) | Sets from CS provision shard |


Single writer, but gates `CreateClusterScopedReadDesires` and deletion cleanup.

### `ServiceProviderCluster.Status.HostedClusterNamespace`


| Actor                                                                         | When                                         |
| ----------------------------------------------------------------------------- | -------------------------------------------- |
| [ServiceProviderClusterPropertiesSync](#serviceproviderclusterpropertiessync) | Sets from HostedCluster ReadDesire namespace |


Single writer, but tracks the namespace containing the HostedCluster CR and user-provided secrets on the management cluster.

### `ServiceProviderCluster.Status.ControlPlaneNamespace`


| Actor                                                                         | When                                                                             |
| ----------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| [ServiceProviderClusterPropertiesSync](#serviceproviderclusterpropertiessync) | Sets to `<hostedClusterNamespace>-<hostedClusterName>` (dots replaced by dashes) |


Single writer, but tracks the namespace containing control plane pods (etcd, kube-apiserver, etc.) on the management cluster.

### `ServiceProviderCluster.Status.MSIManagedIdentities`


| Actor                                             | When                                                                                                                                                   |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| [FetchMSIIdentitiesInfo](#fetchmsiidentitiesinfo) | Sets ControlPlaneOperatorsIdentities (lowercased resource ID keys), ServiceManagedIdentity, and EarliestRecheckTime from Managed Identities Data Plane |


Single writer. Read by [ClusterIdentitySync](#clusteridentitysync) to populate `HCPOpenShiftCluster.Identity.UserAssignedIdentities`.

### `ServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities`


| Actor                                                                                         | When                                                                                                                                                                                                    |
| --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [FetchDataPlaneOperatorsManagedIdentitiesInfo](#fetchdataplaneoperatorsmanagedidentitiesinfo) | Resolves each data plane operator identity's `ClientID`/`PrincipalID` from Azure (or clears them and sets `RetrievalError` on a Get failure), and sets `EarliestRecheckTime` for the next Azure recheck |


Single writer. Mirrors the customer's data plane operator managed identities (`CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators`) into `Identities` keyed by lowercased Azure ResourceID, each carrying the Azure-resolved `ClientID`/`PrincipalID` or a `RetrievalError`.

### `ServiceProviderCluster.Status.AzureResources.ManagedResourceGroup`


| Actor                                                       | When                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| [ObserveManagedResourceGroup](#observemanagedresourcegroup) | Observe-only: while the cluster is not being deleted, records `PendingAzureResource` before querying Azure, then sets `AzureResource` (clearing pending) when the resource group exists and is not owned by another cluster, leaves the pending marker when it is missing, and returns an error when it is owned by another cluster (`ManagedBy` set to a different cluster ID); while the cluster is being deleted, clears both references once the resource group is gone and otherwise does nothing |


Single writer. Read by [ClusterChildResourcesCleanupController](#clusterchildresourcescleanupcontroller) to gate deletion of the `ServiceProviderCluster` document until the managed resource group is gone.

### `ServiceProviderCluster.Status.Validations`


| Actor                                                        | When                                                                        |
| ------------------------------------------------------------ | --------------------------------------------------------------------------- |
| [ClusterValidation*](#clustervalidation--nodepoolvalidation) | Multiple validation controllers write different conditions on the same list |




### `BillingDocument.DeletionTime`


| Actor                                                   | When                                               |
| ------------------------------------------------------- | -------------------------------------------------- |
| [ClusterDeletionController](#clusterdeletioncontroller) | Sets when cluster document is being deleted        |
| [OrphanedBillingCleanup](#orphanedbillingcleanup)       | Sets when billing doc has no corresponding cluster |




### `ServiceProviderCluster.Spec.BackupState`


| Actor                                                             | When                                         |
| ----------------------------------------------------------------- | -------------------------------------------- |
| [Admin API PATCH BackupSchedule](#admin-api-patch-backupschedule) | SRE sets `Enabled` or `Paused` via Admin API |


Single writer. Read by [Admin API GET BackupSchedule](#admin-api-get-backupschedule) (returned in response) and [BackupScheduleSyncer](#backupschedulesyncer) (determines whether Velero schedules are paused).

---



## Generation Prompt

This document was generated by Claude Code. To regenerate or refine it, paste the prompt below
into a conversation rooted in the ARO-HCP repo and edit the instructions to taste.

```
Examine the frontend and backend source code to produce a markdown file at
docs/cosmos-data-flow.md that documents the Cosmos DB data flow for the
ARO-HCP resource provider. The file must contain these sections in order:

1. **Frontend Endpoint Writes** — For each mutating HTTP endpoint in
   frontend/pkg/frontend/ (cluster.go, node_pool.go, external_auth.go,
   frontend.go), list:
   - HTTP method and path pattern
   - Handler function name and source file
   - Every Cosmos object it creates or replaces (HCPOpenShiftCluster,
     Operation, NodePool, ExternalAuth, Subscription, etc.)
   - The specific fields set or modified on each object before the write
   - Whether it uses a transactional batch or a standalone write

2. **Backend Controller Reads and Writes** — For each controller registered in
   backend/pkg/controllers/ (operation controllers, creation controllers,
   deletion controllers, upgrade controllers, properties sync controllers,
   validation controllers, status aggregators, billing controllers, management
   cluster controllers, read-desire controllers), list:
   - Controller name (the constant)
   - Source file
   - What triggers it (which informer or resync interval)
   - Gate/precondition (what provisioning state, deletion timestamp, or field
     value must be true before it runs)
   - Objects and fields READ (from cache/lister or live DB)
   - Objects and fields WRITTEN (be specific: which fields change, what values)

3. **Execution Order Digraphs** — ASCII art digraphs showing the causal order
   of controllers after each frontend endpoint fires:
   - Cluster Create flow
   - Cluster Update flow
   - Cluster Delete flow
   - NodePool Create flow
   - NodePool Delete flow
   Show which field write by controller A is the gate that enables controller B.

4. **Fields Written by Multiple Actors** — For every field on every Cosmos
   object that is written by more than one actor (frontend endpoint or backend
   controller), list every actor and when it writes, in a table. Include
   single-writer fields only when they gate important downstream controllers.

Key source locations to examine:
- frontend/pkg/frontend/{cluster,node_pool,external_auth,frontend,helpers,routes}.go
- internal/api/types_{cluster,nodepool,externalauth,operation,controller,
  serviceprovider_cluster,serviceprovider_nodepool,management_cluster_content}.go
- internal/api/arm/{resource,subscription,types_cosmosdata}.go
- internal/database/{crud_helpers,crud_nested_resource,types_operation,database}.go
- internal/conversion/readonly_{cluster,nodepool,externalauth}.go
- backend/pkg/controllers/operation/*.go
- backend/pkg/controllers/cluster/operations/*.go
- backend/pkg/controllers/nodepool/operations/*.go
- backend/pkg/controllers/externalauth/operations/*.go
- backend/pkg/controllers/cluster/credentials/operations/*.go
- backend/pkg/controllers/cluster/creation/*.go
- backend/pkg/controllers/cluster/deletion/*.go
- backend/pkg/controllers/nodepool/creation/*.go
- backend/pkg/controllers/nodepool/deletion/*.go
- backend/pkg/controllers/externalauth/creation/*.go
- backend/pkg/controllers/externalauth/deletion/*.go
- backend/pkg/controllers/upgradecontrollers/*.go
- backend/pkg/controllers/cluster/properties/*.go
- backend/pkg/controllers/validationcontrollers/*.go
- backend/pkg/controllers/statuscontrollers/*.go
- backend/pkg/controllers/billing/*.go
- backend/pkg/controllers/cluster/placement/*.go
- backend/pkg/controllers/mismatch/*.go
- backend/pkg/controllers/create_*_read_desires_controller.go
- backend/pkg/controllers/controllerutils/{cluster,nodepool,external_auth}_watching_controller.go
- backend/pkg/controllers/controllerutils/generic_watching_controller.go

Style rules:
- Use tables for structured field lists, ASCII art for digraphs.
- Use bullet points for lists within the table.
- Link to source files with relative paths from docs/.
- In the multi-writer section, link each actor back to its section heading.
- Omit read-only / diagnostic controllers (data dumps, metrics, mismatch
  detectors) unless they write to Cosmos.
- Never use shorthand like "deletion fields", "same fields as above", or
  "same pattern as X". Always list every individual field explicitly, even
  if it repeats across similar controllers. The reader should never have to
  look at another section to know what a controller reads or writes.
- For each controller's Gate, express it as the exact NeedsWork /
  ShouldProcess conditions from the source code — field == value or
  field != nil, one per bullet. In the Read table, annotate each field
  that participates in the NeedsWork / ShouldProcess check with
  "(NeedsWork: must be X)" so the reader can see at a glance which reads
  are precondition checks vs. data reads. Every field mentioned in the
  Gate must appear as a Read row in the table — if a NeedsWork function
  checks a field, that field is read, and it must be listed.
- Keep this generation prompt at the bottom of the file so it can be edited
  and re-run.
```


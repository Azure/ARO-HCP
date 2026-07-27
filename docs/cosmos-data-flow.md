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

| Object | Fields Written |
|--------|---------------|
| `Subscription` | <ul><li>All fields from request body (`State`, `Properties.*`)</li><li>On create: `CosmosMetadata.ResourceID`, `PartitionKey`</li><li>On replace: preserves `CosmosMetadata` from existing doc</li></ul> |

Side effect: if `State == Deleted`, calls `DeleteAllResourcesInSubscription` which
transitively deletes all clusters (and their children) via transactional batches.

---

### PUT Cluster (Create)

**Path:** `PUT .../hcpOpenShiftClusters/{name}` (resource does not exist)
**Handler:** `createHCPCluster` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Transactional batch (`AddCreateToTransaction` x2)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftCluster` | <ul><li>All `CustomerProperties.*` from request body (unmarshaled, converted to internal, `EnsureDefaults()` applied)</li><li>`TrackedResource.ID` (from URL resource ID)</li><li>`TrackedResource.Name` (from URL resource ID)</li><li>`TrackedResource.Type` (from URL resource ID)</li><li>`TrackedResource.Location` = `azureLocation`</li><li>`Tags`</li><li>`SystemData.CreatedAt`, `SystemData.CreatedBy`, `SystemData.CreatedByType`</li><li>`SystemData.LastModifiedAt`, `SystemData.LastModifiedBy`, `SystemData.LastModifiedByType`</li><li>`CosmosMetadata.ResourceID`, `CosmosMetadata.PartitionKey`</li><li>`ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL` (from `X-Ms-Identity-Url` header)</li><li>`Identity.UserAssignedIdentities` (cleared then rebuilt via `completeClusterIdentity` from `CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators` and `.ServiceManagedIdentity`)</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`ServiceProviderProperties.ProvisioningState` = `Accepted`</li></ul> |
| `Operation` | <ul><li>`Request` = `Create`</li><li>`ExternalID` = cluster ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Accepted`</li><li>`TenantID` (from `X-Ms-Home-Tenant-Id` header)</li><li>`ClientID` (from `X-Ms-Client-Object-Id` header)</li><li>`NotificationURI` (from `X-Ms-Async-Notification-Uri` header)</li><li>`StartTime` = now</li><li>`LastTransitionTime` = now</li><li>`OperationID` = generated ARM resource ID</li><li>`ResourceID` = generated ARM resource ID</li><li>`ClientRequestID`, `CorrelationRequestID` (from correlation data)</li></ul> |

---

### PUT Cluster (Update)

**Path:** `PUT .../hcpOpenShiftClusters/{name}` (resource exists)
**Handler:** `updateHCPClusterInCosmos` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftCluster` | <ul><li>`CustomerProperties.*` (from request body; `DNS.BaseDomainPrefix` and `Platform.ManagedResourceGroup` carried from old if empty)</li><li>`Tags` (nil in request = keep old; non-nil = replace)</li><li>`SystemData.LastModifiedAt`, `LastModifiedBy`, `LastModifiedByType`</li><li>Read-only fields copied from old via `CopyReadOnlyClusterValues`: `TrackedResource`, `CosmosMetadata`, `Identity` (PrincipalID, TenantID, non-nil UserAssignedIdentity values), `ServiceProviderProperties` (entire deep copy), `Status` (entire deep copy)</li><li>`Identity.UserAssignedIdentities` (cleared then rebuilt via `completeClusterIdentity` with old identity data)</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`ServiceProviderProperties.ProvisioningState` = `Accepted`</li></ul> |
| `Operation` | <ul><li>`Request` = `Update`</li><li>`ExternalID` = cluster ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Accepted`</li><li>`TenantID`, `ClientID`, `NotificationURI` (from headers)</li><li>`StartTime`, `LastTransitionTime`, `OperationID`, `ResourceID`, `ClientRequestID`, `CorrelationRequestID`</li></ul> |

---

### PATCH Cluster (Update)

**Path:** `PATCH .../hcpOpenShiftClusters/{name}`
**Handler:** `updateHCPClusterInCosmos` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftCluster` | <ul><li>`CustomerProperties.*` (old resource used as base, PATCH body overlaid, then converted to internal)</li><li>`Tags` (nil in request = keep old; non-nil = replace)</li><li>`SystemData.LastModifiedAt`, `LastModifiedBy`, `LastModifiedByType`</li><li>Read-only fields copied from old via `CopyReadOnlyClusterValues`: `TrackedResource`, `CosmosMetadata`, `Identity`, `ServiceProviderProperties`, `Status`</li><li>`Identity.UserAssignedIdentities` (cleared then rebuilt via `completeClusterIdentity` with old identity data)</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`ServiceProviderProperties.ProvisioningState` = `Accepted`</li></ul> |
| `Operation` | <ul><li>`Request` = `Update`</li><li>`ExternalID` = cluster ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Accepted`</li><li>`TenantID`, `ClientID`, `NotificationURI`</li></ul> |

---

### DELETE Cluster

**Path:** `DELETE .../hcpOpenShiftClusters/{name}`
**Handler:** `addDeleteClusterToTransaction` ([cluster.go](../frontend/pkg/frontend/cluster.go))
**Write method:** Single transactional batch containing cluster + all child resources

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` = now (if nil)</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`ServiceProviderProperties.ProvisioningState` = `Deleting`</li><li>`ServiceProviderProperties.UsesNewClusterDeletionApproach` = `true`</li></ul> |
| `Operation` | <ul><li>`Request` = `Delete`</li><li>`ExternalID` = cluster ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Deleting`</li><li>`UsesNewClusterDeletionApproach` = `true`</li><li>`TenantID`, `ClientID`, `NotificationURI` (if from user request)</li></ul> |
| Child `NodePool`s (each) | <ul><li>`ServiceProviderProperties.DeletionTimestamp` = now (if nil)</li><li>`ServiceProviderProperties.ActiveOperationID` = new per-NP delete operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Deleting`</li><li>`ServiceProviderProperties.UsesNewNodePoolDeletionApproach` = `true`</li></ul> |
| Child `NodePool` `Operation`s (each) | <ul><li>`Request` = `Delete`, `ExternalID`, `Status` = `Deleting`</li><li>`UsesNewNodePoolDeletionApproach` = `true`</li></ul> |
| Child `ExternalAuth`s (each) | <ul><li>`ServiceProviderProperties.DeletionTimestamp` = now (if nil)</li><li>`ServiceProviderProperties.ActiveOperationID` = new per-EA delete operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Deleting`</li><li>`ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` = `true`</li></ul> |
| Child `ExternalAuth` `Operation`s (each) | <ul><li>`Request` = `Delete`, `ExternalID`, `Status` = `Deleting`</li><li>`UsesNewExternalAuthDeletionApproach` = `true`</li></ul> |
| Canceled `Operation`s | <ul><li>Active operations on the cluster get `Status` = `Canceled`, `LastTransitionTime` = now</li></ul> |

---

### PUT NodePool (Create)

**Path:** `PUT .../nodePools/{name}` (resource does not exist)
**Handler:** `createNodePool` ([node_pool.go](../frontend/pkg/frontend/node_pool.go))
**Write method:** Transactional batch (`AddCreateToTransaction` x2)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftClusterNodePool` | <ul><li>All `Properties.*` from request body (unmarshaled, converted to internal, `EnsureDefaults()` applied)</li><li>`TrackedResource.ID`, `TrackedResource.Name`, `TrackedResource.Type`, `TrackedResource.Location`</li><li>`Tags`, `SystemData`</li><li>`CosmosMetadata.ResourceID`, `CosmosMetadata.PartitionKey`</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Accepted`</li></ul> |
| `Operation` | <ul><li>`Request` = `Create`</li><li>`ExternalID` = node pool ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Accepted`</li><li>`TenantID`, `ClientID`, `NotificationURI`</li></ul> |

---

### PUT/PATCH NodePool (Update)

**Path:** `PUT/PATCH .../nodePools/{name}` (resource exists)
**Handler:** `updateNodePoolInCosmos` ([node_pool.go](../frontend/pkg/frontend/node_pool.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftClusterNodePool` | <ul><li>`Properties.*` (from request; `Version.ID` carried from old if empty, `Platform.SubnetID` carried from old if nil)</li><li>`Tags` (nil in request = keep old; non-nil = replace)</li><li>`SystemData.LastModifiedAt`, `LastModifiedBy`, `LastModifiedByType`</li><li>Read-only fields copied from old via `CopyReadOnlyNodePoolValues`: `TrackedResource`, `CosmosMetadata`, `Identity`, `Properties.ProvisioningState`, `ServiceProviderProperties`, `Status`</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Accepted`</li></ul> |
| `Operation` | <ul><li>`Request` = `Update`</li><li>`ExternalID` = node pool ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Accepted`</li></ul> |

---

### DELETE NodePool

**Path:** `DELETE .../nodePools/{name}`
**Handler:** `addDeleteNodePoolToTransaction` ([node_pool.go](../frontend/pkg/frontend/node_pool.go))
**Write method:** Transactional batch

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` = now (if nil)</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Deleting`</li><li>`ServiceProviderProperties.UsesNewNodePoolDeletionApproach` = `true`</li></ul> |
| `Operation` | <ul><li>`Request` = `Delete`</li><li>`ExternalID`, `Status` = `Deleting`</li><li>`UsesNewNodePoolDeletionApproach` = `true`</li></ul> |
| Canceled `Operation`s | <ul><li>Active operations on the node pool get `Status` = `Canceled`, `LastTransitionTime` = now</li></ul> |

---

### PUT ExternalAuth (Create)

**Path:** `PUT .../externalAuths/{name}` (resource does not exist)
**Handler:** `createExternalAuth` ([external_auth.go](../frontend/pkg/frontend/external_auth.go))
**Write method:** Transactional batch (`AddCreateToTransaction` x2)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftClusterExternalAuth` | <ul><li>All `Properties.*` from request body (unmarshaled, converted to internal, `EnsureDefaults()` applied)</li><li>`ProxyResource.ID`, `ProxyResource.Name`, `ProxyResource.Type`</li><li>`SystemData`</li><li>`CosmosMetadata.ResourceID`, `CosmosMetadata.PartitionKey`</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Accepted`</li></ul> |
| `Operation` | <ul><li>`Request` = `Create`</li><li>`ExternalID` = external auth ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Accepted`</li></ul> |

---

### PUT/PATCH ExternalAuth (Update)

**Path:** `PUT/PATCH .../externalAuths/{name}` (resource exists)
**Handler:** `updateExternalAuthInCosmos` ([external_auth.go](../frontend/pkg/frontend/external_auth.go))
**Write method:** Transactional batch (`AddCreateToTransaction` + `AddReplaceToTransaction`)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftClusterExternalAuth` | <ul><li>`Properties.*` (from request)</li><li>`SystemData.LastModifiedAt`, `LastModifiedBy`, `LastModifiedByType`</li><li>Read-only fields copied from old via `CopyReadOnlyExternalAuthValues`: `ProxyResource`, `CosmosMetadata`, `Properties.ProvisioningState`, `ServiceProviderProperties`, `Status`</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Accepted`</li></ul> |
| `Operation` | <ul><li>`Request` = `Update`</li><li>`ExternalID`, `InternalID` = `*externalAuth.ServiceProviderProperties.ClusterServiceID`</li><li>`Status` = `Accepted`</li></ul> |

---

### DELETE ExternalAuth

**Path:** `DELETE .../externalAuths/{name}`
**Handler:** `addDeleteExternalAuthToTransaction` ([external_auth.go](../frontend/pkg/frontend/external_auth.go))
**Write method:** Transactional batch

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` = now (if nil)</li><li>`ServiceProviderProperties.ActiveOperationID` = new operation's `ResourceID.Name`</li><li>`Properties.ProvisioningState` = `Deleting`</li><li>`ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` = `true`</li></ul> |
| `Operation` | <ul><li>`Request` = `Delete`</li><li>`ExternalID`, `Status` = `Deleting`</li><li>`UsesNewExternalAuthDeletionApproach` = `true`</li></ul> |
| Canceled `Operation`s | <ul><li>Active operations on the external auth get `Status` = `Canceled`, `LastTransitionTime` = now</li></ul> |

---

### POST RequestAdminCredential

**Path:** `POST .../requestadmincredential`
**Handler:** `ArmResourceActionRequestAdminCredential` ([frontend.go](../frontend/pkg/frontend/frontend.go))
**Write method:** Transactional batch (single item)

| Object | Fields Written |
|--------|---------------|
| `Operation` | <ul><li>`Request` = `RequestCredential`</li><li>`ExternalID` = parent cluster ARM resource ID</li><li>`InternalID` = empty</li><li>`Status` = `Accepted`</li><li>`TenantID`, `ClientID`, `NotificationURI` (from headers)</li><li>`CertificateRequest` = PEM-encoded PKCS#10 CSR from request body</li></ul> |

No resource document is modified. The `SystemAdminCredentialRequest` document is created later by the backend dispatch controller.

---

### POST RevokeCredentials

**Path:** `POST .../revokecredentials`
**Handler:** `ArmResourceActionRevokeCredentials` ([frontend.go](../frontend/pkg/frontend/frontend.go))
**Write method:** Transactional batch (canceled ops + operation + cluster replace)

| Object | Fields Written |
|--------|---------------|
| `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.RevokeCredentialsOperationID` = new operation's `OperationID.Name`</li></ul> |
| `Operation` | <ul><li>`Request` = `RevokeCredentials`</li><li>`ExternalID` = parent cluster ARM resource ID</li><li>`InternalID` = `*cluster.ServiceProviderProperties.ClusterServiceID`</li><li>`Status` = `Accepted`</li></ul> |
| Canceled `Operation`s | <ul><li>Active `RequestCredential` operations get `Status` = `Canceled`, `LastTransitionTime` = now</li></ul> |

---

## 2. Backend Controller Reads and Writes

### Operation Controllers

These watch the ActiveOperations informer (10s resync). Each gates on `Operation.Request` type,
`ExternalID.ResourceType`, and non-terminal `Operation.Status`. All use `UpdateOperationStatus`
which performs a **transactional batch** to atomically update the operation and associated resource.

#### OperationClusterCreate

**File:** [operation_cluster_create.go](../backend/pkg/controllers/operationcontrollers/operation_cluster_create.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Create`
- `Operation.ExternalID.ResourceType` == `ClusterResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on Cluster):**
- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Create`)</li><li>`ExternalID` (ShouldProcess: resource type must be `ClusterResourceType`)</li><li>`OperationID.Name`</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ActiveOperationID` (mismatch check)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.API.URL`</li><li>`ServiceProviderProperties.CreateOperationCompletionDeadline`</li></ul> |
| Read | ReadDesire (HostedCluster) | <ul><li>`Status.Conditions` (ConditionTypeSuccessful)</li><li>`Status.KubeContent` -> HostedCluster `status.controlPlaneVersion.history[].state`, `status.controlPlaneVersion.history[].version`, `status.conditions` (Available, Degraded), `status.controlPlaneEndpoint.host`, `status.controlPlaneEndpoint.port`</li></ul> |
| Read | Cluster Service | <ul><li>cluster state, provision error</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Provisioning`/`Succeeded`/`Failed`</li><li>**`Error`** (on failure)</li><li>**`LastTransitionTime`**</li><li>**`NotificationURI`** (cleared after ARM notification)</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.ProvisioningState`** = new status</li><li>**`.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationClusterUpdate

**File:** [operation_cluster_update.go](../backend/pkg/controllers/operationcontrollers/operation_cluster_update.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Update`
- `Operation.ExternalID.ResourceType` == `ClusterResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on Cluster):**
- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Update`)</li><li>`ExternalID` (ShouldProcess: resource type must be `ClusterResourceType`)</li><li>`ResourceID.Name`</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ActiveOperationID` (mismatch check)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li><li>`CustomerProperties.Version.ID`</li><li>`CustomerProperties.API.AuthorizedCIDRs`</li><li>`CustomerProperties.NodeDrainTimeoutMinutes`</li><li>`CustomerProperties.Autoscaling.*`</li><li>`CustomerProperties.ImageDigestMirrors`</li><li>`ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability`</li><li>`ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing`</li><li>`ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Spec.ControlPlaneVersion.DesiredVersion`</li><li>`Spec.DesiredHostedClusterControlPlaneSize`</li></ul> |
| Read | Controller(`ControlPlaneDesiredVersion`) | <ul><li>`Status.Conditions[IntentFailed]` (Reason, Message)</li></ul> |
| Read | ReadDesire (HostedCluster) | <ul><li>`Spec.Networking.APIServer.AllowedCIDRBlocks`</li><li>`Spec.ControllerAvailabilityPolicy`</li><li>`Spec.InfrastructureAvailabilityPolicy`</li><li>`Annotations[ClusterSizeOverrideAnnotation]`</li><li>`Annotations[ControlPlaneOperatorImageAnnotation]`</li><li>`Spec.Autoscaling.*`</li><li>`Spec.ImageContentSources`</li></ul> |
| Read | Cluster Service | <ul><li>cluster status, API CIDRBlockAccess, NodeDrainGracePeriod</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Updating`/`Succeeded`/`Failed`</li><li>**`Error`** (on failure)</li><li>**`LastTransitionTime`**</li><li>**`NotificationURI`** (cleared after ARM notification)</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.ProvisioningState`** = new status</li><li>**`.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationClusterDelete

**File:** [operation_cluster_delete.go](../backend/pkg/controllers/operationcontrollers/operation_cluster_delete.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Delete`
- `Operation.ExternalID.ResourceType` == `ClusterResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on Cluster):**
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Delete`)</li><li>`UsesNewClusterDeletionApproach`</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li></ul> |
| Read | Cluster Service | <ul><li>cluster status (or 404)</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Succeeded` (when cluster doc deleted via `SetDeleteOperationAsCompleted`)</li><li>**`Error`**, **`LastTransitionTime`**</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.ProvisioningState`** = new status (while doc exists)</li><li>**`.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationNodePoolCreate

**File:** [operation_node_pool_create.go](../backend/pkg/controllers/operationcontrollers/operation_node_pool_create.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Create`
- `Operation.ExternalID.ResourceType` == `NodePoolResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on NodePool):**
- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Create`)</li><li>`ExternalID` (ShouldProcess: resource type must be `NodePoolResourceType`)</li><li>`ResourceID.Name`</li></ul> |
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.ActiveOperationID` (mismatch check)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.CreateOperationCompletionDeadline`</li></ul> |
| Read | Cluster Service | <ul><li>node pool status</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Provisioning`/`Succeeded`/`Failed`</li><li>**`Error`** (on failure)</li><li>**`LastTransitionTime`**</li><li>**`NotificationURI`** (cleared after ARM notification)</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**`Properties.ProvisioningState`** = new status</li><li>**`ServiceProviderProperties.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationNodePoolUpdate

**File:** [operation_node_pool_update.go](../backend/pkg/controllers/operationcontrollers/operation_node_pool_update.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Update`
- `Operation.ExternalID.ResourceType` == `NodePoolResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on NodePool):**
- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Update`)</li><li>`ExternalID` (ShouldProcess: resource type must be `NodePoolResourceType`)</li><li>`ResourceID.Name`, `ResourceID.String()`</li></ul> |
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.ActiveOperationID` (mismatch check)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li><li>`Properties.Version.ID`</li><li>`Properties.Labels`</li><li>`Properties.AutoScaling` (Min, Max)</li><li>`Properties.Replicas`</li><li>`Properties.Taints`</li><li>`Properties.NodeDrainTimeoutMinutes`</li></ul> |
| Read | `ServiceProviderNodePool` | <ul><li>`Spec.NodePoolVersion.DesiredVersion`</li></ul> |
| Read | Controller(`NodepoolVersion`) | <ul><li>`Status.Conditions[IntentFailed]` (Status, Reason, Message)</li></ul> |
| Read | ReadDesire (NodePool) | <ul><li>`Spec.NodeLabels`</li><li>`Spec.AutoScaling` (Min, Max)</li><li>`Spec.Replicas`</li><li>`Spec.Taints`</li><li>`Spec.NodeDrainTimeout`</li><li>`Status.Replicas`</li><li>`Status.Conditions` (AllMachinesReadyConditionType)</li></ul> |
| Read | Cluster Service | <ul><li>node pool status, labels, taints, nodeDrainGracePeriod</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Updating`/`Succeeded`/`Failed`</li><li>**`Error`** (on failure)</li><li>**`LastTransitionTime`**</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**`Properties.ProvisioningState`** = new status</li><li>**`ServiceProviderProperties.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationNodePoolDelete

**File:** [operation_node_pool_delete.go](../backend/pkg/controllers/operationcontrollers/operation_node_pool_delete.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Delete`
- `Operation.ExternalID.ResourceType` == `NodePoolResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on NodePool):**
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Delete`)</li><li>`UsesNewNodePoolDeletionApproach`</li></ul> |
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li></ul> |
| Read | Cluster Service | <ul><li>node pool status (or 404)</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Succeeded` (when NP doc deleted)</li><li>**`Error`**, **`LastTransitionTime`**</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**`Properties.ProvisioningState`** = new status (while doc exists)</li><li>**`ServiceProviderProperties.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationExternalAuthCreate

**File:** [operation_external_auth_create.go](../backend/pkg/controllers/operationcontrollers/operation_external_auth_create.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Create`
- `Operation.ExternalID.ResourceType` == `ExternalAuthResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Create`)</li><li>`ExternalID` (ShouldProcess: resource type must be `ExternalAuthResourceType`)</li><li>`ResourceID.Name`</li></ul> |
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.ActiveOperationID` (mismatch check)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li></ul> |
| Read | Cluster Service | <ul><li>external auth GET (success implies Succeeded)</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Succeeded`/`Failed`</li><li>**`Error`** (on failure)</li><li>**`LastTransitionTime`**</li><li>**`NotificationURI`** (cleared after ARM notification)</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**`Properties.ProvisioningState`** = new status</li><li>**`ServiceProviderProperties.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationExternalAuthUpdate

**File:** [operation_external_auth_update.go](../backend/pkg/controllers/operationcontrollers/operation_external_auth_update.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Update`
- `Operation.ExternalID.ResourceType` == `ExternalAuthResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Update`)</li><li>`ExternalID` (ShouldProcess: resource type must be `ExternalAuthResourceType`)</li><li>`ResourceID.Name`</li></ul> |
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.ActiveOperationID` (mismatch check)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li><li>`Name`</li><li>`Properties.Issuer` (URL, Audiences)</li><li>`Properties.Clients` (ClientID, Component.Name, Component.AuthClientNamespace, ExtraScopes)</li><li>`Properties.Claim` (Mappings.Username.Claim/PrefixPolicy/Prefix, Mappings.Groups.Claim/Prefix, ValidationRules)</li></ul> |
| Read | ReadDesire (HostedCluster) | <ul><li>`Spec.Configuration.Authentication.OIDCProviders` (Name, Issuer, OIDCClients, ClaimMappings, ClaimValidationRules)</li></ul> |
| Read | Cluster Service | <ul><li>external auth spec (canonical JSON comparison)</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Updating`/`Succeeded`/`Failed`</li><li>**`Error`** (on failure)</li><li>**`LastTransitionTime`**</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**`Properties.ProvisioningState`** = new status</li><li>**`ServiceProviderProperties.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### OperationExternalAuthDelete

**File:** [operation_external_auth_delete.go](../backend/pkg/controllers/operationcontrollers/operation_external_auth_delete.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `Delete`
- `Operation.ExternalID.ResourceType` == `ExternalAuthResourceType`

**Gate (shouldReconcileOperationAndResourceStatus on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `Delete`)</li><li>`UsesNewExternalAuthDeletionApproach`</li></ul> |
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li></ul> |
| Read | Cluster Service | <ul><li>external auth status (or 404)</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Succeeded` (when EA doc deleted)</li><li>**`Error`**, **`LastTransitionTime`**</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**`Properties.ProvisioningState`** = new status (while doc exists)</li><li>**`ServiceProviderProperties.ActiveOperationID`** = `""` (on terminal)</li></ul> |

#### SystemAdminCredentialDispatchRequestCredential

**File:** [dispatch_request_credential.go](../backend/pkg/controllers/systemadmincredentialcontrollers/dispatch_request_credential.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RequestCredential`
- `len(Operation.InternalID.String())` == 0 (not yet dispatched)

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `RequestCredential`)</li><li>`InternalID` (ShouldProcess: must be empty)</li><li>`ExternalID`</li><li>`OperationID.Name`</li><li>`CertificateRequest`</li><li>`ClientID`</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.RevokeCredentialsOperationID` (if non-empty, cancels operation)</li></ul> |
| Read | `SystemAdminCredentialRequest` | <ul><li>`Spec.OperationID` (list scan for idempotency check)</li></ul> |
| **Write** | **`SystemAdminCredentialRequest`** | <ul><li>**CREATE**: `CosmosMetadata.ResourceID`, `CosmosMetadata.PartitionKey`</li><li>**`Spec.Username`** = `system:customer-break-glass:<clientID>`</li><li>**`Spec.CreationTimestamp`** = now</li><li>**`Spec.ExpirationTimestamp`** = now + 24h</li><li>**`Spec.OperationID`** = operation ID name</li><li>**`Spec.CertificateRequestPEM`** = operation's CertificateRequest</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`InternalID`** = SystemAdminCredentialRequest ARM resource ID (on success)</li><li>**`Status`** = `Canceled` (if revocation in progress, via `CancelOperation`)</li><li>**`LastTransitionTime`** (if canceled)</li></ul> |

#### SystemAdminCredentialOperationRequestCredentialPoll

**File:** [operation_request_credential.go](../backend/pkg/controllers/systemadmincredentialcontrollers/operation_request_credential.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RequestCredential`
- `len(Operation.InternalID.String())` > 0 (has been dispatched)

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal)</li><li>`Request` (ShouldProcess: must be `RequestCredential`)</li><li>`InternalID` (ShouldProcess: must be non-empty)</li><li>`ExternalID`</li><li>`OperationID`</li></ul> |
| Read | `SystemAdminCredentialRequest` | <ul><li>`Status.Conditions` (checks Issued, Failed, AwaitingRevocation, Revoked)</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Provisioning`/`Succeeded`/`Failed`/`Canceled`</li><li>**`Error`** (on failure)</li><li>**`LastTransitionTime`**</li><li>**`NotificationURI`** (cleared after ARM notification)</li></ul> |

#### SystemAdminCredentialDispatchRevokeCredentials

**File:** [dispatch_revoke_credentials.go](../backend/pkg/controllers/systemadmincredentialcontrollers/dispatch_revoke_credentials.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RevokeCredentials`
- `Operation.Status` == `Accepted` (not yet dispatched)

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal, must be `Accepted`)</li><li>`Request` (ShouldProcess: must be `RevokeCredentials`)</li><li>`ExternalID`</li><li>`OperationID.Name`</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.RevokeCredentialsOperationID` (must match operation ID)</li></ul> |
| Read | `SystemAdminCredentialRevocation` | <ul><li>Existence check (idempotency)</li></ul> |
| **Write** | **`SystemAdminCredentialRevocation`** | <ul><li>**CREATE**: `CosmosMetadata.ResourceID`, `CosmosMetadata.PartitionKey`</li><li>**`Spec.OperationID`** = operation ID name</li><li>**`Spec.RevokeOpSuffix`** = first 16 hex chars of operation UUID</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`InternalID`** = SystemAdminCredentialRevocation ARM resource ID</li><li>**`Status`** = `Deleting`</li><li>**`LastTransitionTime`**</li><li>**`Status`** = `Canceled` (if RevokeCredentialsOperationID mismatch, via `CancelOperation`)</li></ul> |

#### SystemAdminCredentialOperationRevokeCredentialsPoll

**File:** [operation_revoke_credentials.go](../backend/pkg/controllers/systemadmincredentialcontrollers/operation_revoke_credentials.go)
**Gate (ShouldProcess on Operation):**
- `Operation.Status.IsTerminal()` == false
- `Operation.Request` == `RevokeCredentials`
- `Operation.Status` == `Deleting` (must already be dispatched)

| | Object | Fields |
|---|--------|--------|
| Read | `Operation` | <ul><li>`Status` (ShouldProcess: must not be terminal, must be `Deleting`)</li><li>`Request` (ShouldProcess: must be `RevokeCredentials`)</li><li>`InternalID`</li><li>`ExternalID`</li><li>`OperationID.Name`</li></ul> |
| Read | `SystemAdminCredentialRevocation` | <ul><li>Existence check (waits for 404 = complete)</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.RevokeCredentialsOperationID`</li></ul> |
| **Write** | **`Operation`** | <ul><li>**`Status`** -> `Succeeded`</li><li>**`LastTransitionTime`**</li><li>**`NotificationURI`** (cleared after ARM notification)</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.RevokeCredentialsOperationID`** = `""` (cleared when matches)</li></ul> |

---

### Cluster Creation Controllers

#### ClusterClusterServiceCreate

**File:** [cluster_cluster_service_create_controller.go](../backend/pkg/controllers/clustercreation/cluster_cluster_service_create_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (needsWork on Cluster):**
- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` == nil or empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil or empty)</li><li>All `CustomerProperties.*` (for building CS cluster request)</li><li>`ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL`</li><li>`ServiceProviderProperties.ExperimentalFeatures.*`</li><li>`ID`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Spec.ControlPlaneVersion.DesiredVersion` (precondition: must be non-nil)</li><li>`Spec.DesiredHostedClusterControlPlaneSize`</li></ul> |
| Read | `Subscription` | <ul><li>`Properties.TenantId`</li></ul> |
| Read | Cluster Service | <ul><li>`ListClusters` (search by Azure info), `PostCluster`</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.ClusterServiceID`** = CS internal ID</li></ul> |
| **Write** | **`ServiceProviderCluster`** | <ul><li>Created if not exists (via `GetOrCreateServiceProviderCluster`)</li></ul> |

---

### Cluster Update Controllers

#### ClusterClusterServiceUpdateDispatch

**File:** [cluster_cluster_service_update_dispatch_controller.go](../backend/pkg/controllers/clusterupdate/cluster_cluster_service_update_dispatch_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (needsWork on Cluster):**
- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil and non-empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li><li>`CustomerProperties.API.AuthorizedCIDRs`</li><li>`CustomerProperties.NodeDrainTimeoutMinutes`</li><li>`CustomerProperties.Autoscaling.*`</li><li>`CustomerProperties.ImageDigestMirrors`</li><li>`ServiceProviderProperties.ExperimentalFeatures.*`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Spec.DesiredHostedClusterControlPlaneSize`</li></ul> |
| Read | `Subscription` | <ul><li>`Properties.TenantId`</li></ul> |
| Read | Cluster Service | <ul><li>`GetCluster` (for config comparison)</li></ul> |

No Cosmos writes. Dispatches updates to Cluster Service via PATCH.

---

### Cluster Deletion Controllers

#### ClusterClusterServiceDeleteDispatch

**File:** [cluster_cluster_service_delete_dispatch_controller.go](../backend/pkg/controllers/clusterdeletion/cluster_cluster_service_delete_dispatch_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (NeedsWork on Cluster):**
- `Cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach` == true
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.UsesNewClusterDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.ClusterServiceDeletionTimestamp`** = now</li></ul> |

#### ClusterDeletionClusterServiceIDClearer

**File:** [cluster_cluster_service_id_clearer.go](../backend/pkg/controllers/clusterdeletion/cluster_cluster_service_id_clearer.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (NeedsWork on Cluster):**
- `Cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach` == true
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil and non-empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.UsesNewClusterDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li></ul> |
| Read | Cluster Service | <ul><li>expects 404</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.ClusterServiceID`** = `nil`</li></ul> |

#### ClusterChildResourcesCleanupController

**File:** [cluster_child_resources_cleanup_controller.go](../backend/pkg/controllers/clusterdeletion/cluster_child_resources_cleanup_controller.go)
**Trigger:** Cluster informer, 1-minute resync
**Gate (NeedsWork on Cluster):**
- `Cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach` == true
- `Cluster.ServiceProviderProperties.DeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.UsesNewClusterDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil)</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID`</li><li>`Status.MaestroReadonlyBundles`</li></ul> |
| Read | Child NodePools | <ul><li>list (must be empty)</li></ul> |
| Read | Child ExternalAuths | <ul><li>list (must be empty)</li></ul> |
| **Write** | Child Cosmos docs | <ul><li>**DELETES** ServiceProviderCluster (when MaestroReadonlyBundles empty and kube-applier desires gone)</li><li>**DELETES** ManagementClusterContent docs</li><li>**DELETES** kube-applier desire documents</li></ul> |

#### ClusterDeletionController

**File:** [cluster_deletion_controller.go](../backend/pkg/controllers/clusterdeletion/cluster_deletion_controller.go)
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

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.UsesNewClusterDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil)</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.MaestroReadonlyBundles` (must be empty)</li></ul> |
| Read | Child NodePools | <ul><li>list (must be empty)</li></ul> |
| Read | Child ExternalAuths | <ul><li>list (must be empty)</li></ul> |
| Read | Child Cosmos resources | <ul><li>list excluding controllers (must be empty)</li></ul> |
| **Write** | **`BillingDocument`** | <ul><li>**`DeletionTime`** = now (via `PatchByClusterID`)</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**DELETES the document**</li></ul> |

---

### NodePool Creation Controllers

#### NodePoolClusterServiceCreate

**File:** [node_pool_cluster_service_create_controller.go](../backend/pkg/controllers/nodepoolcreationcontrollers/node_pool_cluster_service_create_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (needsWork on NodePool):**
- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` == nil or empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil or empty)</li><li>All `Properties.*` (for building CS node pool request)</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| Read | Cluster Service | <ul><li>`GetNodePool` (adoption check), `PostNodePool`</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**`ServiceProviderProperties.ClusterServiceID`** = CS internal ID</li></ul> |

---

### NodePool Update Controllers

#### NodePoolClusterServiceUpdateDispatch

**File:** [node_pool_cluster_service_update_dispatch_controller.go](../backend/pkg/controllers/nodepoolupdate/node_pool_cluster_service_update_dispatch_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (needsWork on NodePool):**
- `NodePool.ServiceProviderProperties.DeletionTimestamp` == nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil and non-empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li><li>`Properties.Labels`</li><li>`Properties.Taints`</li><li>`Properties.Replicas`</li><li>`Properties.AutoScaling`</li><li>`Properties.NodeDrainTimeoutMinutes`</li></ul> |
| Read | Cluster Service | <ul><li>`GetNodePool` (for config comparison)</li></ul> |

No Cosmos writes. Dispatches updates to Cluster Service via PATCH.

---

### NodePool Deletion Controllers

#### NodePoolClusterServiceDeleteDispatch

**File:** [node_pool_cluster_service_delete_dispatch_controller.go](../backend/pkg/controllers/nodepooldeletion/node_pool_cluster_service_delete_dispatch_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**
- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.UsesNewNodePoolDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**`ServiceProviderProperties.ClusterServiceDeletionTimestamp`** = now</li></ul> |

#### NodePoolDeletionClusterServiceIDClearer

**File:** [node_pool_cluster_service_id_clearer.go](../backend/pkg/controllers/nodepooldeletion/node_pool_cluster_service_id_clearer.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**
- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` != nil and non-empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.UsesNewNodePoolDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li></ul> |
| Read | Cluster Service | <ul><li>expects 404</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**`ServiceProviderProperties.ClusterServiceID`** = `nil`</li></ul> |

#### NodePoolChildResourcesCleanupController

**File:** [node_pool_child_resources_cleanup_controller.go](../backend/pkg/controllers/nodepooldeletion/node_pool_child_resources_cleanup_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**
- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.UsesNewNodePoolDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil)</li></ul> |
| Read | `ServiceProviderNodePool` | <ul><li>`Status.MaestroReadonlyBundles`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID`</li></ul> |
| **Write** | Child Cosmos docs | <ul><li>**DELETES** ManagementClusterContent docs under NodePool</li><li>**DELETES** ServiceProviderNodePool (when Maestro bundles cleared and kube-applier desires gone)</li><li>**DELETES** nodepool-scoped kube-applier desire documents</li></ul> |

#### NodePoolDeletionController

**File:** [node_pool_deletion_controller.go](../backend/pkg/controllers/nodepooldeletion/node_pool_deletion_controller.go)
**Trigger:** NodePool informer, 1-minute resync
**Gate (NeedsWork on NodePool):**
- `NodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach` == true
- `NodePool.ServiceProviderProperties.DeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `NodePool.ServiceProviderProperties.ClusterServiceID` == nil

**Additional SyncOnce preconditions:**
- All Maestro readonly bundles cleared
- All child Cosmos resources deleted (except controllers)

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.UsesNewNodePoolDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil)</li></ul> |
| Read | `ServiceProviderNodePool` | <ul><li>`Status.MaestroReadonlyBundles` (must be empty)</li></ul> |
| Read | Child Cosmos resources | <ul><li>list excluding controllers (must be empty)</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**DELETES the document**</li></ul> |

---

### ExternalAuth Creation Controllers

#### ExternalAuthClusterServiceCreate

**File:** [external_auth_cluster_service_create_controller.go](../backend/pkg/controllers/externalauthcreationcontrollers/external_auth_cluster_service_create_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (needsWork on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` == nil or empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil or empty)</li><li>All `Properties.*` (for building CS external auth request)</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| Read | Cluster Service | <ul><li>`PostExternalAuth`</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**`ServiceProviderProperties.ClusterServiceID`** = CS internal ID</li></ul> |

---

### ExternalAuth Update Controllers

#### ExternalAuthClusterServiceUpdateDispatch

**File:** [external_auth_cluster_service_update_dispatch_controller.go](../backend/pkg/controllers/externalauthupdate/external_auth_cluster_service_update_dispatch_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (needsWork on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` == nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil and non-empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li><li>`Properties.Issuer` (URL, Audiences, CA)</li><li>`Properties.Clients` (ClientID, Component, ExtraScopes, Type)</li><li>`Properties.Claim` (Mappings, ValidationRules)</li></ul> |
| Read | Cluster Service | <ul><li>`GetExternalAuth` (for config comparison)</li></ul> |

No Cosmos writes. Dispatches updates to Cluster Service via PATCH.

---

### ExternalAuth Deletion Controllers

#### ExternalAuthClusterServiceDeleteDispatch

**File:** [external_auth_cluster_service_delete_dispatch_controller.go](../backend/pkg/controllers/externalauthdeletion/external_auth_cluster_service_delete_dispatch_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**`ServiceProviderProperties.ClusterServiceDeletionTimestamp`** = now</li></ul> |

#### ExternalAuthDeletionClusterServiceIDClearer

**File:** [external_auth_cluster_service_id_clearer.go](../backend/pkg/controllers/externalauthdeletion/external_auth_cluster_service_id_clearer.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` != nil and non-empty

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li></ul> |
| Read | Cluster Service | <ul><li>expects 404</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**`ServiceProviderProperties.ClusterServiceID`** = `nil`</li></ul> |

#### ExternalAuthChildResourcesCleanupController

**File:** [external_auth_child_resources_cleanup_controller.go](../backend/pkg/controllers/externalauthdeletion/external_auth_child_resources_cleanup_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil)</li></ul> |
| **Write** | Child Cosmos docs | <ul><li>**DELETES** child Cosmos documents (excluding controllers)</li></ul> |

#### ExternalAuthDeletionController

**File:** [external_auth_deletion_controller.go](../backend/pkg/controllers/externalauthdeletion/external_auth_deletion_controller.go)
**Trigger:** ExternalAuth informer, 1-minute resync
**Gate (NeedsWork on ExternalAuth):**
- `ExternalAuth.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` == true
- `ExternalAuth.ServiceProviderProperties.DeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp` != nil
- `ExternalAuth.ServiceProviderProperties.ClusterServiceID` == nil

**Additional SyncOnce preconditions:**
- All child resources deleted (except controllers)

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterExternalAuth` | <ul><li>`ServiceProviderProperties.UsesNewExternalAuthDeletionApproach` (NeedsWork: must be true)</li><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceDeletionTimestamp` (NeedsWork: must not be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must be nil)</li></ul> |
| Read | Child Cosmos resources | <ul><li>list excluding controllers (must be empty)</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**DELETES the document**</li></ul> |

---

### Upgrade Controllers

#### ControlPlaneDesiredVersion

**File:** [control_plane_desired_version_controller.go](../backend/pkg/controllers/upgradecontrollers/control_plane_desired_version_controller.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate:** No formal NeedsWork. Skips inside SyncOnce if `DeletionTimestamp != nil`, or if `DesiredVersion` already set AND cluster < 2hr old AND active Create operation exists.

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`CustomerProperties.Version.ID`, `.Version.ChannelGroup`</li><li>`SystemData.CreatedAt`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Spec.ControlPlaneVersion.DesiredVersion`</li><li>`Status.ControlPlaneVersion.ActiveVersions`</li></ul> |
| Read | `Subscription` | <ul><li>Registered features (AFEC)</li></ul> |
| Read | NodePools + ServiceProviderNodePools | <ul><li>For y-stream skew validation</li></ul> |
| Read | Cincinnati | <ul><li>Version graph resolution</li></ul> |
| **Write** | **`ServiceProviderCluster`** | <ul><li>**`Spec.ControlPlaneVersion.DesiredVersion`** = resolved version</li></ul> |
| **Write** | **Controller doc** | <ul><li>**`IntentFailed`** condition (True with `VersionUpgradeNotAccepted` / False with `AsExpected`)</li></ul> |

#### ControlPlaneActiveVersions

**File:** [control_plane_active_version_controller.go](../backend/pkg/controllers/upgradecontrollers/control_plane_active_version_controller.go)
**Trigger:** Cluster informer, 5-minute resync

| | Object | Fields |
|---|--------|--------|
| Read | ReadDesire (HostedCluster) | <ul><li>`Status.ControlPlaneVersion.History`</li></ul> |
| **Write** | **`ServiceProviderCluster`** | <ul><li>**`Status.ControlPlaneVersion.ActiveVersions`** = [{Version, State}, ...]</li></ul> |

#### TriggerControlPlaneUpgrade

**Trigger:** Cluster informer, 5-minute resync

No Cosmos writes. Posts `ControlPlaneUpgradePolicy` to Cluster Service.

#### NodePoolVersion

**File:** [nodepool_version_controller.go](../backend/pkg/controllers/upgradecontrollers/nodepool_version_controller.go)
**Trigger:** NodePool informer, 5-minute resync
**Gate (NeedsWork on NodePool + ServiceProviderNodePool):**
- `len(NodePool.Properties.Version.ID)` > 0
- `ServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion` == nil, or differs from parsed `NodePool.Properties.Version.ID`

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`Properties.Version.ID` (NeedsWork: must be non-empty)</li><li>`Properties.Version.ChannelGroup`</li></ul> |
| Read | `ServiceProviderNodePool` | <ul><li>`Spec.NodePoolVersion.DesiredVersion` (NeedsWork: must be nil or differ from customer desired)</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ControlPlaneVersion.ActiveVersions`</li></ul> |
| **Write** | **`ServiceProviderNodePool`** | <ul><li>**`Spec.NodePoolVersion.DesiredVersion`** = customer desired version</li></ul> |
| **Write** | **Controller doc** | <ul><li>**`IntentFailed`** condition</li></ul> |

#### NodePoolActiveVersions

**File:** [nodepool_active_version_controller.go](../backend/pkg/controllers/upgradecontrollers/nodepool_active_version_controller.go)
**Trigger:** NodePool informer, 5-minute resync
**Gate (NeedsWork on ServiceProviderNodePool):**
- `ServiceProviderNodePool` != nil (document must exist)

| | Object | Fields |
|---|--------|--------|
| Read | ReadDesire (NodePool) | <ul><li>`Status.NodesInfo.NodeVersions`</li></ul> |
| **Write** | **`ServiceProviderNodePool`** | <ul><li>**`Status.NodePoolVersion.ActiveVersions`** = [{Version}, ...]</li></ul> |

#### TriggerNodePoolUpgrade

**Trigger:** NodePool informer, 5-minute resync

No Cosmos writes. Posts `NodePoolUpgradePolicy` to Cluster Service.

---

### Properties Sync Controllers

#### ClusterPropertiesSync

**File:** [cluster_properties_sync.go](../backend/pkg/controllers/clusterpropertiescontroller/cluster_properties_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate:** No formal NeedsWork. Skips inside SyncOnce if `CustomerProperties.DNS.BaseDomainPrefix` empty or HostedCluster ReadDesire does not exist.

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`CustomerProperties.DNS.BaseDomainPrefix` (SyncOnce: must be non-empty)</li></ul> |
| Read | ReadDesire (HostedCluster) | <ul><li>`Spec.DNS.BaseDomain`, `Spec.KubeAPIServerDNSName`</li><li>`Status.ControlPlaneEndpoint.Port`</li><li>`Spec.IssuerURL`</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.Console.URL`** = `https://console-openshift-console.apps.<baseDomain>`</li><li>**`.DNS.BaseDomain`** = derived from KubeAPIServerDNSName</li><li>**`.API.URL`** = `https://<dnsName>:<port>`</li><li>**`.Platform.IssuerURL`** = HostedCluster IssuerURL</li></ul> |

#### ClusterBaseDomainPrefixSync

**File:** [cluster_base_domain_prefix_sync.go](../backend/pkg/controllers/clusterpropertiescontroller/cluster_base_domain_prefix_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate (needsWork on Cluster):**
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil and non-empty
- `len(Cluster.CustomerProperties.DNS.BaseDomainPrefix)` == 0

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li><li>`CustomerProperties.DNS.BaseDomainPrefix` (NeedsWork: must be empty)</li></ul> |
| Read | Cluster Service | <ul><li>`DomainPrefix()`</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`CustomerProperties.DNS.BaseDomainPrefix`** = CS domain prefix</li></ul> |

#### DesiredControlPlaneSize

**File:** [desired_control_plane_size_sync.go](../backend/pkg/controllers/clusterpropertiescontroller/desired_control_plane_size_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate (NeedsWork on ServiceProviderCluster):**
- `ServiceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize` != `ServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize` (pointer comparison via `ptrStringEqual`)

| | Object | Fields |
|---|--------|--------|
| Read | `ServiceProviderCluster` | <ul><li>`Spec.DesiredHostedClusterControlPlaneSize` (NeedsWork: must differ from Status)</li><li>`Status.DesiredHostedClusterControlPlaneSize` (NeedsWork: must differ from Spec)</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| **Write** | **`ServiceProviderCluster`** | <ul><li>**`Status.DesiredHostedClusterControlPlaneSize`** = Spec value</li></ul> |
| **Write** | Cluster Service | <ul><li>`CSPropertySizeOverride` (external write)</li></ul> |

#### ServiceProviderClusterPropertiesSync

**File:** [serviceprovidercluster_properties_sync.go](../backend/pkg/controllers/clusterpropertiescontroller/serviceprovidercluster_properties_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate:** No formal NeedsWork. Skips inside SyncOnce if ServiceProviderCluster does not exist or HostedCluster ReadDesire has no namespace/name.

| | Object | Fields |
|---|--------|--------|
| Read | `ServiceProviderCluster` | <ul><li>Entire document (deep-equal comparison to avoid no-op writes)</li></ul> |
| Read | ReadDesire (HostedCluster) | <ul><li>`Namespace` (HostedCluster namespace)</li><li>`Name` (HostedCluster name)</li></ul> |
| **Write** | **`ServiceProviderCluster`** | <ul><li>**`Status.HostedClusterNamespace`** = HostedCluster namespace</li><li>**`Status.ControlPlaneNamespace`** = `<hostedClusterNamespace>-<hostedClusterName>` (dots replaced by dashes)</li></ul> |

#### IdentityMigration

**File:** [identity_migration.go](../backend/pkg/controllers/clusterpropertiescontroller/identity_migration.go)
**Trigger:** Cluster informer, 60-minute resync
**Gate (NeedsWork on Cluster):**
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil and non-empty
- `Cluster.Identity` == nil, OR `len(Cluster.Identity.UserAssignedIdentities)` == 0, OR any entry has empty ClientID/PrincipalID, OR entries don't match `CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities`

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil and non-empty)</li><li>`Identity` (NeedsWork: must be nil, or `Identity.UserAssignedIdentities` empty, or entries incomplete)</li><li>`Identity.UserAssignedIdentities` (NeedsWork: each must have non-empty ClientID/PrincipalID)</li><li>`CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities` (NeedsWork: entries must match Identity map)</li></ul> |
| Read | Cluster Service | <ul><li>`GetCluster` -> `GetClusterServiceUserAssignedIdentities`</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`Identity.UserAssignedIdentities`** = migrated map from CS</li></ul> |

---

### Other Controllers

#### ManagementClusterPlacementSync

**File:** [management_cluster_placement_sync.go](../backend/pkg/controllers/managementclustercontrollers/management_cluster_placement_sync.go)
**Trigger:** Cluster informer, 5-minute resync
**Gate (needsWork on ServiceProviderCluster):**
- `ServiceProviderCluster.Status.ManagementClusterResourceID` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID` (NeedsWork: must be nil)</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| Read | Cluster Service | <ul><li>`GetClusterProvisionShard`</li></ul> |
| Read | `ManagementCluster` | <ul><li>`ResourceID` (via `GetByCSProvisionShardID`)</li></ul> |
| **Write** | **`ServiceProviderCluster`** | <ul><li>**`Status.ManagementClusterResourceID`** = management cluster resource ID</li></ul> |

#### BackfillClusterUID

**File:** [backfill_cluster_uid.go](../backend/pkg/controllers/mismatchcontrollers/backfill_cluster_uid.go)
**Trigger:** Cluster informer, 60-minute cooldown
**Gate (NeedsWork on Cluster):**
- `len(Cluster.ServiceProviderProperties.ClusterUID)` == 0

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterUID` (NeedsWork: must be empty)</li><li>`SystemData.CreatedAt`</li><li>`ID`</li></ul> |
| Read | `BillingDocument` | <ul><li>`ListActiveForCluster` (matching creation time)</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.ClusterUID`** = billing doc ID or new UUID</li></ul> |

#### CreateBillingDoc

**File:** [create_billing_doc.go](../backend/pkg/controllers/billingcontrollers/create_billing_doc.go)
**Trigger:** Cluster informer, 60-second cooldown
**Gate (NeedsWork on Cluster):**
- `len(Cluster.ServiceProviderProperties.ClusterUID)` > 0
- `len(Cluster.ServiceProviderProperties.BillingDocumentCosmosID)` == 0
- `Cluster.ServiceProviderProperties.ProvisioningState` == `Succeeded`

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterUID` (NeedsWork: must be non-empty)</li><li>`ServiceProviderProperties.BillingDocumentCosmosID` (NeedsWork: must be empty)</li><li>`ServiceProviderProperties.ProvisioningState` (NeedsWork: must be `Succeeded`)</li><li>`SystemData.CreatedAt`</li><li>`CustomerProperties.Platform.ManagedResourceGroup`</li><li>`ID`</li></ul> |
| Read | `Subscription` | <ul><li>`Properties.TenantId`</li></ul> |
| **Write** | **`BillingDocument`** | <ul><li>`ClusterUID`, `CreationTime`, `Location`, `TenantID`, `ManagedResourceGroup`, `ResourceID`</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`ServiceProviderProperties.BillingDocumentCosmosID`** = billing doc ID</li></ul> |

#### OrphanedBillingCleanup

**File:** [orphaned_billing_cleanup.go](../backend/pkg/controllers/billingcontrollers/orphaned_billing_cleanup.go)
**Trigger:** Time-based, 60-minute jitter (no informer — queues work directly)
**Gate:**
- `BillingDocument.DeletionTime` == nil (skip already-deleted docs)
- Corresponding `HCPOpenShiftCluster` must not exist (orphan detection)

| | Object | Fields |
|---|--------|--------|
| Read | `BillingDocument` | <ul><li>All billing docs via lister (list scan)</li><li>`DeletionTime` (skip if non-nil)</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>Existence check only (if cluster exists, billing doc is not orphaned)</li></ul> |
| **Write** | **`BillingDocument`** | <ul><li>**`DeletionTime`** = now (via `PatchByID`)</li></ul> |

#### DeleteOrphanedCosmosResources

**File:** [delete_orphaned_cosmos.go](../backend/pkg/controllers/mismatchcontrollers/delete_orphaned_cosmos.go)
**Trigger:** Time-based, 60-minute jitter (no informer — queues all subscriptions)
**Gate:**
- Resource is not a cluster (clusters own themselves)
- Resource is inside a resource group (resources outside RG have TTL)
- Parent resource does not exist (orphan detection)

| | Object | Fields |
|---|--------|--------|
| Read | All resources | <ul><li>`ListRecursive` per subscription (untyped scan)</li><li>`ResourceID` (parent existence check)</li></ul> |
| Read | `ManagementCluster` | <ul><li>All management clusters (for kube-applier desire cleanup)</li></ul> |
| Read | Kube-applier desires | <ul><li>`ListRecursive` per management cluster container (ReadDesire, ApplyDesire, DeleteDesire)</li></ul> |
| **Write** | Orphaned resources | <ul><li>**DELETES** resources whose parent no longer exists (via `DeleteByCosmosID`)</li></ul> |
| **Write** | Orphaned desires | <ul><li>**DELETES** kube-applier desire documents whose parent resource no longer exists (via `DeleteByCosmosID`)</li></ul> |

---

#### ClusterValidation / NodePoolValidation

**File:** [cluster_validation_controller.go](../backend/pkg/controllers/validationcontrollers/cluster_validation_controller.go), [nodepool_validation_controller.go](../backend/pkg/controllers/validationcontrollers/nodepool_validation_controller.go)
**Trigger:** Cluster/NodePool informer, 1-minute resync
**Gate (shouldProcess on ServiceProviderCluster/ServiceProviderNodePool):**
- `!meta.IsStatusConditionTrue(ServiceProviderCluster.Status.Validations, validation.Name())` (condition must not yet be True)
- SyncOnce also checks `DeletionTimestamp == nil` on the resource

| | Object | Fields |
|---|--------|--------|
| Read | `ServiceProviderCluster` | <ul><li>`Status.Validations[<name>]` (shouldProcess: condition must not be True)</li></ul> |
| Read | `ServiceProviderNodePool` | <ul><li>`Status.Validations[<name>]` (shouldProcess: condition must not be True)</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (SyncOnce: must be nil)</li></ul> |
| Read | `HCPOpenShiftClusterNodePool` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (SyncOnce: must be nil)</li></ul> |
| **Write** | **`ServiceProviderCluster`** | <ul><li>**`Status.Validations[<name>]`** = condition (True/False)</li></ul> |
| **Write** | **`ServiceProviderNodePool`** | <ul><li>**`Status.Validations[<name>]`** = condition (True/False)</li></ul> |

#### DegradedAggregators (Cluster / NodePool / ExternalAuth)

**File:** [cluster_degraded_aggregator.go](../backend/pkg/controllers/statuscontrollers/cluster_degraded_aggregator.go), [nodepool_degraded_aggregator.go](../backend/pkg/controllers/statuscontrollers/nodepool_degraded_aggregator.go), [externalauth_degraded_aggregator.go](../backend/pkg/controllers/statuscontrollers/externalauth_degraded_aggregator.go)
**Trigger:** Resource informer, 1-minute resync

| | Object | Fields |
|---|--------|--------|
| Read | Controller docs | <ul><li>All `Status.Conditions[Degraded]` under the resource</li></ul> |
| **Write** | **`HCPOpenShiftCluster`** | <ul><li>**`Status.Conditions[Degraded]`** = aggregated union</li></ul> |
| **Write** | **`HCPOpenShiftClusterNodePool`** | <ul><li>**`Status.Conditions[Degraded]`** = aggregated union</li></ul> |
| **Write** | **`HCPOpenShiftClusterExternalAuth`** | <ul><li>**`Status.Conditions[Degraded]`** = aggregated union</li></ul> |

#### CreateClusterScopedReadDesires / CreateNodePoolScopedReadDesires

**File:** [create_cluster_scoped_read_desires_controller.go](../backend/pkg/controllers/create_cluster_scoped_read_desires_controller.go), [create_nodepool_scoped_read_desires_controller.go](../backend/pkg/controllers/create_nodepool_scoped_read_desires_controller.go)
**Trigger:** Cluster/NodePool informer, 1-minute resync
**Gate (SyncOnce preconditions + readDesireNeedsWork):**
- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil
- `ServiceProviderCluster.Status.ManagementClusterResourceID` != nil
- `len(Cluster.CustomerProperties.DNS.BaseDomainPrefix)` > 0
- Existing `ReadDesire` == nil, or `ReadDesire.Spec.ManagementCluster` differs, or `ReadDesire.Spec.TargetItem` differs

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (SyncOnce: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (SyncOnce: must not be nil)</li><li>`CustomerProperties.DNS.BaseDomainPrefix` (SyncOnce: must be non-empty)</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID` (SyncOnce: must not be nil)</li></ul> |
| Read | Existing `ReadDesire` | <ul><li>`Spec.ManagementCluster` (readDesireNeedsWork: compared to desired)</li><li>`Spec.TargetItem` (readDesireNeedsWork: compared to desired)</li></ul> |
| **Write** | `ReadDesire` (kube-applier DB) | <ul><li>Creates or replaces `ReadDesire` documents (not Resources container)</li></ul> |

---

### System Admin Credential Request Lifecycle Controllers

These watch the SystemAdminCredentialRequests informer (1-minute resync) via `CredentialRequestWatchingController`.
The kube-applier ReadDesires informer is also wired in (maxDepth=1) for controllers that need to observe
desire status.

#### SystemAdminCredentialDesiresCreator

**File:** [desires_creator.go](../backend/pkg/controllers/systemadmincredentialcontrollers/desires_creator.go)
**Trigger:** SystemAdminCredentialRequests + ReadDesires informer, 1-minute resync
**Gate (needsWork on Cluster + CredentialRequest):**
- `Cluster.ServiceProviderProperties.DeletionTimestamp` == nil
- `Cluster.ServiceProviderProperties.ClusterServiceID` != nil
- `isCredentialRequestPending(cred)` (none of Issued/Failed/AwaitingRevocation/Revoked conditions are True)

| | Object | Fields |
|---|--------|--------|
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.DeletionTimestamp` (NeedsWork: must be nil)</li><li>`ServiceProviderProperties.ClusterServiceID` (NeedsWork: must not be nil)</li></ul> |
| Read | `SystemAdminCredentialRequest` | <ul><li>`Status.Conditions` (NeedsWork: must be pending)</li><li>`Spec.CertificateRequestPEM`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID`</li><li>`Status.ControlPlaneNamespace`</li></ul> |
| Read | `ApplyDesire` | <ul><li>Existing desire content (idempotency check)</li></ul> |
| Read | `ReadDesire` | <ul><li>Existing desire content (idempotency check)</li></ul> |
| **Write** | **`ApplyDesire`** (kube-applier DB) | <ul><li>**CREATE**: CSR RBAC ApplyDesires (ClusterRole, ClusterRoleBinding), CSR ApplyDesire, CSR Approval ApplyDesire — each with `Spec.ManagementCluster`, `Spec.Type=ServerSideApply`, `Spec.TargetItem`, `Spec.ServerSideApply.KubeContent`</li></ul> |
| **Write** | **`ReadDesire`** (kube-applier DB) | <ul><li>**CREATE**: CSR ReadDesire targeting `certificates.k8s.io/v1/certificatesigningrequests`</li></ul> |

#### SystemAdminCredentialIssuanceObserver

**File:** [issuance_observer.go](../backend/pkg/controllers/systemadmincredentialcontrollers/issuance_observer.go)
**Trigger:** SystemAdminCredentialRequests + ReadDesires informer, 1-minute resync
**Gate (needsWork on CredentialRequest):**
- `isCredentialRequestPending(cred)` (none of Issued/Failed/AwaitingRevocation/Revoked conditions are True)

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRequest` | <ul><li>`Status.Conditions` (NeedsWork: must be pending)</li></ul> |
| Read | ReadDesire (cached CSR) | <ul><li>`Status.Certificate` (signed cert bytes)</li><li>`Status.Conditions` (CertificateDenied, CertificateFailed)</li></ul> |
| **Write** | **`SystemAdminCredentialRequest`** | <ul><li>**`Status.Conditions[Issued]`** = True + **`Status.SignedCertificate`** = base64-encoded PEM (on success)</li><li>**`Status.Conditions[Failed]`** = True (on CSR denied/failed)</li></ul> |

#### SystemAdminCredentialPostIssuanceCleanup

**File:** [post_issuance_cleanup.go](../backend/pkg/controllers/systemadmincredentialcontrollers/post_issuance_cleanup.go)
**Trigger:** SystemAdminCredentialRequests + ReadDesires informer, 1-minute resync
**Gate (needsWork on CredentialRequest):**
- `Issued` == True OR `Failed` == True condition on the credential request

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRequest` | <ul><li>`Status.Conditions` (NeedsWork: Issued or Failed must be True)</li><li>`ResourceID.Name`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID`</li></ul> |
| **Write** | **`ApplyDesire`** (kube-applier DB) | <ul><li>**REPLACE** (flip `Spec.Type` to `Delete`) then **DELETE** (once delete reports `Successful=True`)</li></ul> |
| **Write** | **`ReadDesire`** (kube-applier DB) | <ul><li>**DELETE**: all ReadDesires matching the credential name</li></ul> |

#### SystemAdminCredentialClusterDeletionCleanup

**File:** [cluster_deletion_cleanup.go](../backend/pkg/controllers/systemadmincredentialcontrollers/cluster_deletion_cleanup.go)
**Trigger:** SystemAdminCredentialRequests + ReadDesires informer, 1-minute resync
**Gate (needsWork on CredentialRequest):**
- `cred.Status.DeleteTimestamp` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRequest` | <ul><li>`Status.DeleteTimestamp` (NeedsWork: must not be nil)</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID`</li></ul> |
| **Write** | **`ApplyDesire`** (kube-applier DB) | <ul><li>**REPLACE** (flip `Spec.Type` to `Delete`) then **DELETE** for all desires under this credential request</li></ul> |
| **Write** | **`ReadDesire`** (kube-applier DB) | <ul><li>**DELETE**: all ReadDesires under this credential request</li></ul> |
| **Write** | **`SystemAdminCredentialRequest`** | <ul><li>**DELETES the document** (once all desires are gone)</li></ul> |

#### SystemAdminCredentialRevokedGC

**File:** [revoked_gc.go](../backend/pkg/controllers/systemadmincredentialcontrollers/revoked_gc.go)
**Trigger:** SystemAdminCredentialRequests informer (no ReadDesires), 1-hour resync
**Gate (needsWork on CredentialRequest):**
- `cred.Spec.CreationTimestamp` is non-zero AND age >= 48h

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRequest` | <ul><li>`Spec.CreationTimestamp` (NeedsWork: must be non-zero and >= 48h old)</li></ul> |
| **Write** | **`SystemAdminCredentialRequest`** | <ul><li>**DELETES the document** (unconditionally after 48h)</li></ul> |

---

### System Admin Credential Revocation Lifecycle Controllers

These watch the SystemAdminCredentialRevocations informer (1-minute resync) via `RevocationWatchingController`.

#### SystemAdminCredentialRevocationMarkRequests

**File:** [revocation_mark_requests.go](../backend/pkg/controllers/systemadmincredentialcontrollers/revocation_mark_requests.go)
**Trigger:** SystemAdminCredentialRevocations informer, 1-minute resync
**Gate (needsWork on Revocation):**
- `revocation.Status.DeleteTimestamp` == nil
- `CredentialsMarkedForDeletion` condition is NOT True

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRevocation` | <ul><li>`Status.DeleteTimestamp` (NeedsWork: must be nil)</li><li>`Status.Conditions` (NeedsWork: CredentialsMarkedForDeletion must not be True)</li></ul> |
| Read | `SystemAdminCredentialRequest` | <ul><li>`Status.DeleteTimestamp` (list scan: checked for each request)</li></ul> |
| **Write** | **`SystemAdminCredentialRequest`** (each) | <ul><li>**`Status.DeleteTimestamp`** = now (on every request that does not already have one)</li></ul> |
| **Write** | **`SystemAdminCredentialRevocation`** | <ul><li>**`Status.Conditions[CredentialsMarkedForDeletion]`** = True</li></ul> |

#### SystemAdminCredentialRevocationDesires

**File:** [revocation_desires.go](../backend/pkg/controllers/systemadmincredentialcontrollers/revocation_desires.go)
**Trigger:** SystemAdminCredentialRevocations informer, 1-minute resync
**Gate (needsWork on Revocation + Cluster):**
- `revocation.Status.DeleteTimestamp` == nil
- Cluster has `ClusterServiceID`
- `ServiceProviderCluster` has `ManagementClusterResourceID` and `ControlPlaneNamespace`

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRevocation` | <ul><li>`Status.DeleteTimestamp` (NeedsWork: must be nil)</li><li>`Spec.RevokeOpSuffix`</li></ul> |
| Read | `HCPOpenShiftCluster` | <ul><li>`ServiceProviderProperties.ClusterServiceID`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID`</li><li>`Status.ControlPlaneNamespace`</li></ul> |
| Read | `ApplyDesire` | <ul><li>Existing content (idempotency check)</li></ul> |
| Read | `ReadDesire` | <ul><li>Existing content (idempotency check)</li></ul> |
| **Write** | **`ApplyDesire`** (kube-applier DB) | <ul><li>**CREATE**: CRR RBAC ApplyDesires and CRR ApplyDesire targeting `certificates.hypershift.openshift.io/v1alpha1/certificaterevocationrequests`</li></ul> |
| **Write** | **`ReadDesire`** (kube-applier DB) | <ul><li>**CREATE**: CRR ReadDesire targeting the CertificateRevocationRequest</li></ul> |

#### SystemAdminCredentialRevocationCompletion

**File:** [revocation_completion.go](../backend/pkg/controllers/systemadmincredentialcontrollers/revocation_completion.go)
**Trigger:** SystemAdminCredentialRevocations informer, 1-minute resync
**Gate (needsWork on Revocation):**
- `revocation.Status.DeleteTimestamp` == nil

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRevocation` | <ul><li>`Status.DeleteTimestamp` (NeedsWork: must be nil)</li><li>`Status.Conditions` (checks `CertificatesRevoked`, `CredentialsMarkedForDeletion`)</li><li>`Spec.RevokeOpSuffix`</li></ul> |
| Read | ReadDesire (cached CRR) | <ul><li>`Status.Conditions` (checks `PreviousCertificatesRevoked=True`)</li></ul> |
| **Write** | **`SystemAdminCredentialRevocation`** | <ul><li>**`Status.Conditions[CertificatesRevoked]`** = True (once CRR confirms revocation)</li><li>**`Status.Conditions[Complete]`** = True + **`Status.DeleteTimestamp`** = now (once both CertificatesRevoked and CredentialsMarkedForDeletion are True)</li></ul> |

#### SystemAdminCredentialRevocationDeletion

**File:** [revocation_deletion.go](../backend/pkg/controllers/systemadmincredentialcontrollers/revocation_deletion.go)
**Trigger:** SystemAdminCredentialRevocations informer, 1-minute resync
**Gate (needsWork on Revocation):**
- `revocation.Status.DeleteTimestamp` != nil

| | Object | Fields |
|---|--------|--------|
| Read | `SystemAdminCredentialRevocation` | <ul><li>`Status.DeleteTimestamp` (NeedsWork: must not be nil)</li><li>`Spec.RevokeOpSuffix`</li></ul> |
| Read | `ServiceProviderCluster` | <ul><li>`Status.ManagementClusterResourceID`</li></ul> |
| **Write** | **`ApplyDesire`** (kube-applier DB) | <ul><li>**REPLACE** (flip `Spec.Type` to `Delete`) then **DELETE** for all desires matching the revocation suffix</li></ul> |
| **Write** | **`ReadDesire`** (kube-applier DB) | <ul><li>**DELETE**: all ReadDesires matching the revocation suffix</li></ul> |
| **Write** | **`SystemAdminCredentialRevocation`** | <ul><li>**DELETES the document** (once all desires are gone). This deletion signals `SystemAdminCredentialOperationRevokeCredentialsPoll` that the revocation is complete.</li></ul> |

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

### Request Admin Credential Flow

```
  POST RequestAdminCredential (Frontend)
         |
  creates Operation(RequestCredential)
  with CertificateRequest = CSR from body
         |
         v
  SystemAdminCredentialDispatchRequestCredential
  (creates SystemAdminCredentialRequest doc,
   sets Operation.InternalID = credential resource ID)
         |
         +-----------------------------------+
         |                                   |
         v                                   v
  SystemAdminCredentialDesiresCreator   SystemAdminCredentialOperationRequestCredentialPoll
  (creates kube-applier ApplyDesires    (polls credential request conditions,
   for CSR, CSR Approval, RBAC;         maps Issued/Failed -> Operation.Status)
   creates ReadDesire for CSR)                |
         |                                   |
         v                                   |
  SystemAdminCredentialIssuanceObserver       |
  (watches ReadDesire-cached CSR ->          |
   sets Issued or Failed condition,          |
   stores SignedCertificate)                 |
         |                                   |
         v                                   v
  SystemAdminCredentialPostIssuanceCleanup   Operation Succeeded
  (flips ApplyDesires to Delete,        (ARM notification sent)
   removes all desires)
         |
         v
  SystemAdminCredentialRevokedGC
  (deletes credential request after 48h)
```

### Revoke Credentials Flow

```
  POST RevokeCredentials (Frontend)
         |
  creates Operation(RevokeCredentials)
  sets Cluster.SP.RevokeCredentialsOperationID
  cancels active RequestCredential operations
         |
         v
  SystemAdminCredentialDispatchRevokeCredentials
  (creates SystemAdminCredentialRevocation doc,
   sets Operation.InternalID, Status = Deleting)
         |
         +-----------------------------------+
         |                                   |
         v                                   v
  SystemAdminCredentialRevocationMarkRequests  SystemAdminCredentialRevocationDesires
  (sets DeleteTimestamp on all                (creates kube-applier ApplyDesires
   SystemAdminCredentialRequests,              for CRR + RBAC, ReadDesire for CRR)
   sets CredentialsMarkedForDeletion)              |
         |                                        v
         |                              SystemAdminCredentialRevocationCompletion
         |                              (observes CRR status ->
         |                               sets CertificatesRevoked,
         |                               then Complete + DeleteTimestamp)
         |                                        |
         +----------------------------------------+
         |
         v
  SystemAdminCredentialClusterDeletionCleanup
  (triggered by DeleteTimestamp on each
   credential request -> removes desires,
   deletes credential request doc)
         |
         v
  SystemAdminCredentialRevocationDeletion
  (triggered by revocation DeleteTimestamp ->
   removes desires, deletes revocation doc)
         |
         v
  SystemAdminCredentialOperationRevokeCredentialsPoll
  (detects revocation doc 404 ->
   clears RevokeCredentialsOperationID,
   marks Operation Succeeded)
```

---

## 4. Fields Written by Multiple Actors

Each entry links to every actor that writes the field.

### `HCPOpenShiftCluster.ServiceProviderProperties.ProvisioningState`

| Actor | When |
|-------|------|
| [Frontend: PUT Cluster (Create)](#put-cluster-create) | Sets to `Accepted` |
| [Frontend: PUT/PATCH Cluster (Update)](#put-cluster-update) | Sets to `Accepted` |
| [Frontend: DELETE Cluster](#delete-cluster) | Sets to `Deleting` |
| [OperationClusterCreate](#operationclustercreate) | Advances to `Provisioning`/`Succeeded`/`Failed` |
| [OperationClusterUpdate](#operationclusterupdate) | Advances to `Updating`/`Succeeded`/`Failed` |
| [OperationClusterDelete](#operationclusterdelete) | Advances to `Deleting`/`Succeeded`/`Failed` |

### `HCPOpenShiftCluster.ServiceProviderProperties.ActiveOperationID`

| Actor | When |
|-------|------|
| [Frontend: PUT Cluster (Create)](#put-cluster-create) | Sets to new operation ID |
| [Frontend: PUT/PATCH Cluster (Update)](#put-cluster-update) | Sets to new operation ID |
| [Frontend: DELETE Cluster](#delete-cluster) | Sets to new operation ID |
| [OperationClusterCreate](#operationclustercreate) | Clears to `""` on terminal state |
| [OperationClusterUpdate](#operationclusterupdate) | Clears to `""` on terminal state |
| [OperationClusterDelete](#operationclusterdelete) | Clears to `""` on terminal state |

### `HCPOpenShiftCluster.ServiceProviderProperties.ClusterServiceID`

| Actor | When |
|-------|------|
| [ClusterClusterServiceCreate](#clusterclusterservicecreate) | Sets from CS POST response |
| [ClusterDeletionClusterServiceIDClearer](#clusterdeletionclusterserviceidclearer) | Clears to `nil` on CS 404 |

### `HCPOpenShiftCluster.ServiceProviderProperties.RevokeCredentialsOperationID`

| Actor | When |
|-------|------|
| [Frontend: POST RevokeCredentials](#post-revokecredentials) | Sets to operation ID |
| [SystemAdminCredentialOperationRevokeCredentialsPoll](#systemadmincredentialoperationrevokecredentialspoll) | Clears to `""` when revocation doc is deleted |

### `HCPOpenShiftCluster.CustomerProperties.DNS.BaseDomainPrefix`

| Actor | When |
|-------|------|
| [Frontend: PUT Cluster (Create)](#put-cluster-create) | Sets from request body |
| [ClusterBaseDomainPrefixSync](#clusterbasedomainprefixsync) | Backfills from CS if empty |

### `HCPOpenShiftCluster.Identity.UserAssignedIdentities`

| Actor | When |
|-------|------|
| [Frontend: PUT Cluster (Create)](#put-cluster-create) | Rebuilt via `completeClusterIdentity` |
| [Frontend: PUT/PATCH Cluster (Update)](#put-cluster-update) | Rebuilt via `completeClusterIdentity` with old data |
| [IdentityMigration](#identitymigration) | Migrated from CS for clusters with incomplete identity |

### `HCPOpenShiftCluster.ServiceProviderProperties.DeletionTimestamp`

| Actor | When |
|-------|------|
| [Frontend: DELETE Cluster](#delete-cluster) | Sets to current time |

Single writer, but gates the entire deletion pipeline.

### `HCPOpenShiftCluster.ServiceProviderProperties.ClusterUID`

| Actor | When |
|-------|------|
| [BackfillClusterUID](#backfillclusteruid) | Sets from billing doc ID or new UUID |

Single writer, but gates `CreateBillingDoc`.

### `HCPOpenShiftCluster.ServiceProviderProperties.BillingDocumentCosmosID`

| Actor | When |
|-------|------|
| [CreateBillingDoc](#createbillingdoc) | Sets after billing doc creation |

Single writer, but gates billing lifecycle.

### `HCPOpenShiftCluster.Status.Conditions`

| Actor | When |
|-------|------|
| [ClusterDegradedAggregator](#degradedaggregators-cluster--nodepool--externalauth) | Aggregated `Degraded` condition from all controller status docs |

Single writer.

### `HCPOpenShiftClusterNodePool.Properties.ProvisioningState`

| Actor | When |
|-------|------|
| [Frontend: PUT NodePool (Create)](#put-nodepool-create) | Sets to `Accepted` |
| [Frontend: PUT/PATCH NodePool (Update)](#putpatch-nodepool-update) | Sets to `Accepted` |
| [Frontend: DELETE NodePool](#delete-nodepool) | Sets to `Deleting` |
| [OperationNodePoolCreate](#operationnodepoolcreate) | Advances to `Provisioning`/`Succeeded`/`Failed` |
| [OperationNodePoolUpdate](#operationnodepoolupdate) | Advances to `Updating`/`Succeeded`/`Failed` |
| [OperationNodePoolDelete](#operationnodepooldelete) | Advances to `Deleting`/`Succeeded`/`Failed` |

### `HCPOpenShiftClusterNodePool.ServiceProviderProperties.ActiveOperationID`

| Actor | When |
|-------|------|
| [Frontend: PUT NodePool (Create)](#put-nodepool-create) | Sets to new operation ID |
| [Frontend: PUT/PATCH NodePool (Update)](#putpatch-nodepool-update) | Sets to new operation ID |
| [Frontend: DELETE NodePool](#delete-nodepool) | Sets to new operation ID |
| [OperationNodePoolCreate](#operationnodepoolcreate) | Clears on terminal |
| [OperationNodePoolUpdate](#operationnodepoolupdate) | Clears on terminal |
| [OperationNodePoolDelete](#operationnodepooldelete) | Clears on terminal |

### `HCPOpenShiftClusterNodePool.ServiceProviderProperties.ClusterServiceID`

| Actor | When |
|-------|------|
| [NodePoolClusterServiceCreate](#nodepoolclusterservicecreate) | Sets from CS POST response |
| [NodePoolDeletionClusterServiceIDClearer](#nodepooldeletionclusterserviceidclearer) | Clears to `nil` on CS 404 |

### `HCPOpenShiftClusterExternalAuth.Properties.ProvisioningState`

| Actor | When |
|-------|------|
| [Frontend: PUT ExternalAuth (Create)](#put-externalauth-create) | Sets to `Accepted` |
| [Frontend: PUT/PATCH ExternalAuth (Update)](#putpatch-externalauth-update) | Sets to `Accepted` |
| [Frontend: DELETE ExternalAuth](#delete-externalauth) | Sets to `Deleting` |
| [OperationExternalAuthCreate](#operationexternalauthcreate) | Advances to `Succeeded`/`Failed` |
| [OperationExternalAuthUpdate](#operationexternalauthupdate) | Advances to `Updating`/`Succeeded`/`Failed` |
| [OperationExternalAuthDelete](#operationexternalauthdelete) | Advances to `Deleting`/`Succeeded`/`Failed` |

### `HCPOpenShiftClusterExternalAuth.ServiceProviderProperties.ClusterServiceID`

| Actor | When |
|-------|------|
| [ExternalAuthClusterServiceCreate](#externalauthclusterservicecreate) | Sets from CS POST response |
| [ExternalAuthDeletionClusterServiceIDClearer](#externalauthdeletionclusterserviceidclearer) | Clears to `nil` on CS 404 |

### `Operation.Status`

| Actor | When |
|-------|------|
| [Frontend (all mutating endpoints)](#1-frontend-endpoint-writes) | Sets to `Accepted` (or `Deleting` for Delete operations) |
| [All Operation* controllers](#operation-controllers) | Advances through lifecycle (`Provisioning`/`Updating`/`Deleting` -> `Succeeded`/`Failed`) |
| [SystemAdminCredentialDispatchRequestCredential](#systemadmincredentialdispatchrequestcredential) | Sets to `Canceled` (if revocation in progress) or sets `InternalID` |
| [SystemAdminCredentialOperationRequestCredentialPoll](#systemadmincredentialoperationrequestcredentialpoll) | Advances to `Provisioning`/`Succeeded`/`Failed`/`Canceled` |
| [SystemAdminCredentialDispatchRevokeCredentials](#systemadmincredentialdispatchrevokecredentials) | Sets to `Deleting` (after dispatch) or `Canceled` (on mismatch) |
| [SystemAdminCredentialOperationRevokeCredentialsPoll](#systemadmincredentialoperationrevokecredentialspoll) | Sets to `Succeeded` (when revocation doc deleted) |

### `ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion`

| Actor | When |
|-------|------|
| [ControlPlaneDesiredVersion](#controlplanedesiredversion) | Sets/advances based on customer version intent + Cincinnati |

Single writer, but read by `ClusterClusterServiceCreate` (gate), `OperationClusterUpdate`, and `TriggerControlPlaneUpgrade`.

### `ServiceProviderCluster.Status.ManagementClusterResourceID`

| Actor | When |
|-------|------|
| [ManagementClusterPlacementSync](#managementclusterplacementsync) | Sets from CS provision shard |

Single writer, but gates `CreateClusterScopedReadDesires` and deletion cleanup.

### `ServiceProviderCluster.Status.HostedClusterNamespace`

| Actor | When |
|-------|------|
| [ServiceProviderClusterPropertiesSync](#serviceproviderclusterpropertiessync) | Sets from HostedCluster ReadDesire namespace |

Single writer, but tracks the namespace containing the HostedCluster CR and user-provided secrets on the management cluster.

### `ServiceProviderCluster.Status.ControlPlaneNamespace`

| Actor | When |
|-------|------|
| [ServiceProviderClusterPropertiesSync](#serviceproviderclusterpropertiessync) | Sets to `<hostedClusterNamespace>-<hostedClusterName>` (dots replaced by dashes) |

Single writer, but tracks the namespace containing control plane pods (etcd, kube-apiserver, etc.) on the management cluster.

### `ServiceProviderCluster.Status.Validations`

| Actor | When |
|-------|------|
| [ClusterValidation*](#clustervalidation--nodepoolvalidation) | Multiple validation controllers write different conditions on the same list |

### `BillingDocument.DeletionTime`

| Actor | When |
|-------|------|
| [ClusterDeletionController](#clusterdeletioncontroller) | Sets when cluster document is being deleted |
| [OrphanedBillingCleanup](#orphanedbillingcleanup) | Sets when billing doc has no corresponding cluster |

### `SystemAdminCredentialRequest.Status.Conditions`

| Actor | When |
|-------|------|
| [SystemAdminCredentialIssuanceObserver](#systemadmincredentialissuanceobserver) | Sets `Issued=True` + `SignedCertificate` (success) or `Failed=True` (failure) |

Single writer, but gates `SystemAdminCredentialOperationRequestCredentialPoll` and `SystemAdminCredentialPostIssuanceCleanup`.

### `SystemAdminCredentialRequest.Status.DeleteTimestamp`

| Actor | When |
|-------|------|
| [SystemAdminCredentialRevocationMarkRequests](#systemadmincredentialrevocationmarkrequests) | Sets to now on every request during revocation |

Single writer, but gates `SystemAdminCredentialClusterDeletionCleanup` which deletes the document.

### `SystemAdminCredentialRevocation.Status.Conditions`

| Actor | When |
|-------|------|
| [SystemAdminCredentialRevocationMarkRequests](#systemadmincredentialrevocationmarkrequests) | Sets `CredentialsMarkedForDeletion=True` |
| [SystemAdminCredentialRevocationCompletion](#systemadmincredentialrevocationcompletion) | Sets `CertificatesRevoked=True`, then `Complete=True` |

### `SystemAdminCredentialRevocation.Status.DeleteTimestamp`

| Actor | When |
|-------|------|
| [SystemAdminCredentialRevocationCompletion](#systemadmincredentialrevocationcompletion) | Sets to now when revocation is fully complete |

Single writer, but gates `SystemAdminCredentialRevocationDeletion` which deletes the document.

### `Operation.CertificateRequest`

| Actor | When |
|-------|------|
| [Frontend: POST RequestAdminCredential](#post-requestadmincredential) | Sets from request body (PEM-encoded CSR) |

Single writer, but read by `SystemAdminCredentialDispatchRequestCredential` when creating the `SystemAdminCredentialRequest` document.

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
   - Request Admin Credential flow
   - Revoke Credentials flow
   Show which field write by controller A is the gate that enables controller B.

4. **Fields Written by Multiple Actors** — For every field on every Cosmos
   object that is written by more than one actor (frontend endpoint or backend
   controller), list every actor and when it writes, in a table. Include
   single-writer fields only when they gate important downstream controllers.

Key source locations to examine:
- frontend/pkg/frontend/{cluster,node_pool,external_auth,frontend,helpers,routes}.go
- internal/api/types_{cluster,nodepool,externalauth,operation,controller,
  serviceprovider_cluster,serviceprovider_nodepool,management_cluster_content,
  systemadmincredential,systemadmincredentialrevocation}.go
- internal/api/arm/{resource,subscription,types_cosmosdata}.go
- internal/database/{crud_helpers,crud_nested_resource,types_operation,database}.go
- internal/conversion/readonly_{cluster,nodepool,externalauth}.go
- backend/pkg/controllers/operationcontrollers/*.go
- backend/pkg/controllers/clustercreation/*.go
- backend/pkg/controllers/clusterdeletion/*.go
- backend/pkg/controllers/nodepoolcreationcontrollers/*.go
- backend/pkg/controllers/nodepooldeletion/*.go
- backend/pkg/controllers/externalauthcreationcontrollers/*.go
- backend/pkg/controllers/externalauthdeletion/*.go
- backend/pkg/controllers/upgradecontrollers/*.go
- backend/pkg/controllers/clusterpropertiescontroller/*.go
- backend/pkg/controllers/validationcontrollers/*.go
- backend/pkg/controllers/statuscontrollers/*.go
- backend/pkg/controllers/billingcontrollers/*.go
- backend/pkg/controllers/managementclustercontrollers/*.go
- backend/pkg/controllers/mismatchcontrollers/*.go
- backend/pkg/controllers/systemadmincredentialcontrollers/*.go
- backend/pkg/controllers/create_*_read_desires_controller.go
- backend/pkg/controllers/controllerutils/{cluster,nodepool,external_auth,credential_request,revocation}_watching_controller.go
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

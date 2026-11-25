# Stuck cluster cleanup: NamespaceDeletion

`NamespaceDeletion` is an operator initiated **cleanup** request for a HostedCluster namespace already stuck in `Terminating` after its cluster record is gone. The admin API records intent; the backend checks Cosmos and Azure state, and kube-applier checks live Kubernetes state before any finalizer is removed. It drains known finalizers bottom-up across the control-plane and HostedCluster namespaces, finalizes the control-plane namespace before the HostedCluster namespace, and never issues a new HostedCluster or namespace `DELETE`.

This iteration accepts only the HostedCluster namespace (`ocm-<environment>-<id>`). `HostedClusterNamespaceName` is its exact name, and `HostedClusterName` identifies the HostedCluster in it. The controller derives the control-plane namespace (`<HostedCluster namespace>-<hostedClusterName>`) for ownership checks and cleanup. A control-plane namespace cannot be submitted as the target.

## Target population

The 19 October 2026 fleet sweep found 20 HostedCluster namespaces stuck in `Terminating`: 12 in stage and eight in production. Sixteen lacked an ARM Cosmos cluster document and a Clusters Service record in that sweep; four retained records and belong to normal deletion. A 12 November Kubernetes snapshot showed seven production HostedClusters already deleting, two of which still had NodePools and worker CAPI objects. These are candidates, not proof that all live safety checks pass. The current state of the 12 stage cases is not established by that snapshot.

The request acts only after live checks confirm that no Cosmos ServiceProviderCluster or foreign ApplyDesire owns either related namespace and the Azure managed resource group is absent before any HostedCluster or HostedControlPlane finalizer is bypassed. Failed reads block action. Maestro and ManifestWork ownership checks are not part of this API path.

## Request and response

```http
POST /admin/v1/stamps/{stampIdentifier}/managementclusters/default/namespacedeletions
Content-Type: application/json

{
  "hostedClusterNamespaceName": "ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "hostedClusterName": "hcp1",
  "requestReason": "AROSLSRE-2477 cleanup"
}
```

The request name is the exact HostedCluster namespace name. The Fleet document is nested under the `default` management cluster, and its immutable target contains `managementClusterName`, `hostedClusterNamespaceName`, `hostedClusterName`, and the original `requestReason`. The management cluster name comes from the route; the controller checks the explicit spec value. The operator does not provide an ID for a missing cluster, a namespace UID, or a separate deletion flag. Unknown JSON fields are rejected.

```http
GET /admin/v1/stamps/{stampIdentifier}/managementclusters/default/namespacedeletions/{hostedClusterNamespaceName}
```

GET returns the request identity and reason, conditions, pinned namespace and HostedCluster UIDs, the persisted cleanup intent, the current attempt generation, previous stopped attempts, and `completedAt` after it settles. These status fields are controller owned. The UIDs prevent a replacement object from inheriting the request; the intent is saved before a destructive desire is published.

```http
POST /admin/v1/stamps/{stampIdentifier}/managementclusters/default/namespacedeletions
Content-Type: application/json

{
  "hostedClusterNamespaceName": "ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "hostedClusterName": "hcp1",
  "requestReason": "removed stale ManifestWork"
}
```

The same POST route retries an existing, settled `Stopped` request with a matching namespace and HostedCluster name. Active and completed requests return the stored request without restarting work. The retry `requestReason` is recorded separately from the original reason. A duplicate POST while a retry is pending returns that pending attempt when the reason matches; a different reason conflicts. Once a later attempt stops and settles, another matching POST starts another retry. The backend first retires old desires, waits for their three-minute authorization to expire, archives the stop reason, and starts a numbered attempt with fresh ReadDesires. The pinned namespace and HostedCluster UIDs remain unchanged: a replacement object cannot inherit the request. Retries do not change the namespace or HostedCluster name, and a `Complete` request cannot be retried. At most ten retries are supported for one retained document.

The status has `Complete`, `Degraded`, and `Stopped` conditions. Condition reasons and messages describe current observations; their exact strings are not a stable API for automation. Admin API CloudError codes are the stable error contract. A successful POST records intent, not an assertion that the namespace is safe to finalize.

## Admission and reconciliation

The admin API validates the stamp, singleton management cluster, exact namespace grammar, HostedCluster relationship, and protected namespace denylist. It checks live Cosmos ownership before creating the request. A cluster record, or an ApplyDesire that still owns either related namespace, blocks creation. An unavailable or indeterminate ownership lookup never counts as absence.

The backend repeats the ownership gate before mutation. It creates ReadDesires for the target namespace, related HostedCluster and derived control-plane namespace, and waits for successful observations. An unreadable object is pending, never treated as absent. The backend validates the environment-specific namespace grammar, verifies identities and persists the UIDs before publishing a cleanup desire. A changed UID stops the attempt.

The target namespace must already have a `deletionTimestamp` and satisfy the configured minimum dwell. An Active namespace is refused. A HostedCluster that has not begun deleting is also refused. Historical rollout status does not authorize deletion.

When the HostedCluster is already deleting, the backend makes a live Azure point read of its managed resource group. Only an Azure 404 permits an `UnblockHostedCluster` desire; an Azure `Deleting` state is retryable, while a stable resource group stops the attempt with `ManagedResourceGroupPresent`. A resource group whose deletion previously failed or was canceled stops with `ManagedResourceGroupDeletionFailed`: that is an Azure failure to investigate, not something a bare retry will clear, so delete the group and then submit the same POST again. Kube-applier rechecks the namespace and HostedCluster UIDs and deletion timestamps. It clears only the expected finalizer from worker CAPI resources, then component-finalized Deployments, the CAPI `Cluster`, and the `HostedControlPlane` in the control-plane namespace. It waits for each layer to disappear and for the control-plane namespace to disappear before continuing to NodePools and finally the HostedCluster. An unexpected object or finalizer blocks the action. No Kubernetes object is deleted by this API.

After the control-plane resources are drained, the backend publishes a `FinalizeNamespace` desire if the control-plane namespace still exists. Kube-applier requires the `HostedControlPlane` to be absent and confirms successful discovery and an empty namespace inventory before clearing the `kubernetes` finalizer. After the control-plane namespace disappears, cleanup can continue to NodePools and the HostedCluster. The HostedCluster namespace is finalized after its HostedCluster disappears and the same inventory checks pass. `Complete=True` requires both namespaces to be absent.

The controller writes decision evidence to structured logs, including the namespace UID, deletion timestamp, conditions and finalizers, and the HostedCluster identity and state. These observations can be queried in Kusto. The Fleet document keeps only the execution state needed for reconciliation; it does not duplicate the Kubernetes snapshot in an audit object. The admin API's audit middleware records the caller, and `requestReason` records the operator's justification.

## Lifecycle and limits

Retryable observation, database, and kube-applier errors set `Degraded` and retry. A safety stop latches `Stopped` and prevents further mutation until an operator explicitly requests another attempt. Settled requests retire their owned desires and are retained for 14 days after the latest settled attempt. The operator should inspect the conditions and logs, resolve the underlying issue, then submit the same POST for the same pinned namespace. A changed UID or incorrect HostedCluster identity requires separate investigation.

This API is for HostedCluster namespaces whose normal cluster deletion owner is gone. It does not replace the current Cosmos-backed deletion flow. A live cluster record or active owner belongs to that flow. Cosmos absence alone does not prove Azure resources are gone; PR #6688 separately cleans orphaned managed resource groups, so this cleanup waits for the group's absence before bypassing a HostedCluster finalizer.

HyperShift normally deletes the derived control-plane namespace during HostedCluster teardown. This controller drains its known finalizer layers and finalizes it if it remains and passes the live inventory gate. Cleanup continues to NodePools, the HostedCluster, and its namespace after the control-plane namespace disappears. `Complete=True` means both namespaces are absent.

## Review decision

The request input remains one HostedCluster namespace, and a control-plane namespace remains invalid as a target. The controller derives the control-plane namespace for ownership checks and cleanup, drains it first, then retires the HostedCluster and both namespaces.

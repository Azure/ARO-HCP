# Stuck cluster cleanup: NamespaceDeletion

`NamespaceDeletion` is an operator initiated **cleanup** request for a HostedCluster namespace already stuck in `Terminating` after its cluster record is gone. The admin API records intent; the backend checks Cosmos and Azure state, and kube-applier checks live Kubernetes state before any finalizer is removed. It can clear known finalizers on an already deleting HostedCluster, its NodePools and selected worker CAPI objects, then finalize the empty HostedCluster namespace. It never issues a new HostedCluster or namespace `DELETE`.

This iteration accepts only the HostedCluster namespace (`ocm-<environment>-<id>`). `HostedClusterNamespaceName` is its exact name, and `HostedClusterName` identifies the HostedCluster in it. The controller derives the control-plane namespace (`<HostedCluster namespace>-<hostedClusterName>`) for ownership checks. A control-plane namespace cannot be submitted as the target.

## Target population

The 19 October 2026 fleet sweep found 20 HostedCluster namespaces stuck in `Terminating`: 12 in stage and eight in production. Sixteen lacked an ARM Cosmos cluster document and a Clusters Service record in that sweep; four retained records and belong to normal deletion. A 12 November Kubernetes snapshot showed seven production HostedClusters already deleting, two of which still had NodePools and worker CAPI objects. These are candidates, not proof that all live safety checks pass. The current state of the 12 stage cases is not established by that snapshot.

The 15 November ManifestWork snapshot showed matching work in `local-cluster` for four of the seven production candidates. Those four fail the chosen legacy-owner gate if that work is still present when the API runs. The other three had no matching rows in this snapshot; that is not proof of live absence. The request acts only after live checks confirm that no Cosmos ServiceProviderCluster or foreign ApplyDesire owns either related namespace, the Azure managed resource group is absent when a HostedCluster finalizer would be bypassed, and no matching ManifestWork or ManagedClusterAddon remains. A failed lookup blocks action. Before submitting a request, SRE must also verify the Maestro agent and server retirement rollout for the target stamp as described in the [cleanup procedure](cleanup-stuck-cluster-deletion.md#namespacedeletion-api-preflight-for-retired-maestro).

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

When the HostedCluster is already deleting, the backend makes a live Azure point read of its managed resource group. Only an Azure 404 permits an `UnblockHostedCluster` desire. Kube-applier rechecks the namespace and HostedCluster UIDs and deletion timestamps, lists matching ManifestWorks in `local-cluster` and ManagedClusterAddons in the cluster ID namespace, and stops on a match or failed list. It then clears only the expected finalizer from already deleting worker CAPI resources, NodePools, and finally the HostedCluster. It waits for each lower layer to disappear before clearing the next layer's finalizer. An unexpected object or finalizer blocks the action. HostedControlPlane and its namespace are never patched by this request.

Only after the HostedCluster is observed absent may the controller issue `FinalizeNamespace`. Kube-applier lists all HostedClusters in the namespace and refuses to finalize while any remains. It also requires the namespace controller's live discovery, content and finalizer conditions to report successful cleanup, and repeats the legacy-owner check. Namespace deletion is complete only after a later observation confirms that the HostedCluster namespace is absent.

The controller writes decision evidence to structured logs, including the namespace UID, deletion timestamp, conditions and finalizers, and the HostedCluster identity and state. These observations can be queried in Kusto. The Fleet document keeps only the execution state needed for reconciliation; it does not duplicate the Kubernetes snapshot in an audit object. The admin API's audit middleware records the caller, and `requestReason` records the operator's justification.

## Lifecycle and limits

Retryable observation, database, and kube-applier errors set `Degraded` and retry. A safety stop latches `Stopped` and prevents further mutation until an operator explicitly requests another attempt. Settled requests retire their owned desires and are retained for 14 days after the latest settled attempt. The operator should inspect the conditions and logs, resolve the underlying issue, then submit the same POST for the same pinned namespace. A changed UID or incorrect HostedCluster identity requires separate investigation.

This API is for HostedCluster namespaces whose normal cluster deletion owner is gone. It does not replace the current Cosmos-backed deletion flow. A live cluster record or active owner belongs to that flow. Cosmos absence alone does not prove Azure resources are gone; PR #6688 separately cleans orphaned managed resource groups, so this cleanup waits for the group's absence before bypassing a HostedCluster finalizer.

HyperShift normally deletes the derived control-plane namespace during HostedCluster teardown. This controller does not create a desire for that namespace or wait for its absence. `Complete=True` means the requested HostedCluster namespace is absent; operators must check the derived control-plane namespace separately. If it remains, its cleanup is outside this iteration.

## Review decision

The request uses `HostedClusterNamespaceName` and rejects a control-plane namespace as input. The derived control-plane namespace participates in ownership checks, but this iteration only retires the HostedCluster and its own namespace.

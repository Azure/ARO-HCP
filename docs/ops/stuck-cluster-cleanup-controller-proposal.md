# Stuck cluster cleanup: NamespaceDeletion

`NamespaceDeletion` is an operator initiated request to finish deleting a namespace that is already stuck in `Terminating` after its cluster record is gone. The admin API records the request; the backend controller observes Kubernetes through kube-applier and reconciles it. The API never issues a Kubernetes namespace `DELETE`. It clears the namespace's `kubernetes` finalizer through a `FinalizeNamespace` ApplyDesire after safety checks.

This iteration accepts only the HostedCluster namespace (`ocm-<environment>-<id>`). `HostedClusterNamespaceName` is its exact name, and `HostedClusterName` identifies the HostedCluster in it. The controller derives the control-plane namespace (`<HostedCluster namespace>-<hostedClusterName>`) for ownership checks. A control-plane namespace cannot be submitted as the target.

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

The request name is the exact HostedCluster namespace name. The Fleet document is nested under the `default` management cluster, and its immutable spec contains `managementClusterName`, `hostedClusterNamespaceName`, `hostedClusterName`, and `requestReason`. The management cluster name comes from the route; the controller checks the explicit spec value. The operator does not provide an ID for a missing cluster, a namespace UID, or a separate deletion flag. Unknown JSON fields are rejected.

```http
GET /admin/v1/stamps/{stampIdentifier}/managementclusters/default/namespacedeletions/{hostedClusterNamespaceName}
```

GET returns the request identity and reason, conditions, `namespaceUID` once observed, `hostedClusterDeleteIssuedAt` if this request issued a HostedCluster delete, and `completedAt` after it settles. `NamespaceUID` and the delete-issued timestamp are controller owned status fields. The UID pins the observed namespace so a replacement with the same name cannot inherit the request. The timestamp starts the bounded HostedCluster removal wait. Neither field is an input to POST.

The status has `Complete`, `Degraded`, and `Stopped` conditions. Condition reasons and messages describe current observations; their exact strings are not a stable API for automation. Admin API CloudError codes are the stable error contract. A successful POST records intent, not an assertion that the namespace is safe to finalize.

## Admission and reconciliation

The admin API validates the stamp, singleton management cluster, exact namespace grammar, HostedCluster relationship, and protected namespace denylist. It checks live Cosmos ownership before creating the request. A cluster record, or an ApplyDesire that still owns either related namespace, blocks creation. An unavailable or indeterminate ownership lookup never counts as absence.

The backend repeats the ownership gate before mutation. It creates ReadDesires for the target namespace and related HostedCluster and waits for successful observations. An unreadable object is pending, never treated as absent. The backend validates the environment-specific namespace grammar, verifies the observed HostedCluster identity, and pins the target namespace UID in status. A changed UID stops the attempt.

The target namespace must already have a `deletionTimestamp` and must satisfy the configured minimum dwell. An Active namespace is refused. These rules apply before both HostedCluster deletion and namespace finalization.

If the related HostedCluster is present without a `deletionTimestamp`, the controller checks whether it looks like a working cluster. A live looking HostedCluster stops the attempt. Otherwise, it issues a kube-applier `Type=Delete` ApplyDesire for that HostedCluster and records `hostedClusterDeleteIssuedAt`. It then waits until the HostedCluster is observed absent, even if a later observation shows its `deletionTimestamp`. If it remains present beyond `hostedClusterRemovalTimeout`, the request stops without finalizing the namespace.

If the HostedCluster was already deleting before this request, the controller waits for HyperShift to finish; it does not bypass the HostedCluster finalizer. Only after the HostedCluster is observed absent may the controller issue `FinalizeNamespace`. The desire carries the pinned namespace UID, minimum dwell, and HostedCluster identity. Kube-applier checks these against live Kubernetes state and refuses to clear the namespace's `kubernetes` finalizer while the HostedCluster still exists, even if it has a deletion timestamp. Namespace deletion is complete only after a later observation confirms the namespace is absent.

The controller writes decision evidence to structured logs, including the namespace UID, deletion timestamp, conditions and finalizers, and the HostedCluster identity and state. These observations can be queried in Kusto. The Fleet document keeps only the execution state needed for reconciliation; it does not duplicate the Kubernetes snapshot in an audit object. The admin API's audit middleware records the caller, and `requestReason` records the operator's justification.

## Lifecycle and limits

Retryable observation, database, and kube-applier errors set `Degraded` and retry. A safety stop latches `Stopped` and prevents further mutation. Settled requests retire their owned desires and are retained for 14 days before deletion. The operator should inspect the conditions and logs, resolve the underlying issue, and create a new request if a stopped attempt requires another run.

This API is for HostedCluster namespaces whose normal cluster deletion owner is gone. It does not replace the normal Clusters Service → Maestro → ManifestWork → HostedCluster deletion chain. A live cluster record or active owner should be handled through that chain. Clearing a namespace finalizer before HostedCluster deprovisioning can strand Azure resources; the ownership, liveness, dwell, UID, and HostedCluster removal gates are designed around that risk.

HyperShift normally deletes the derived control-plane namespace during HostedCluster teardown. This controller does not create a desire for that namespace or wait for its absence. `Complete=True` means the requested HostedCluster namespace is absent; operators must check the derived control-plane namespace separately. If it remains, its cleanup is outside this iteration.

## Review decision

The request uses `HostedClusterNamespaceName` and rejects a control-plane namespace as input. The derived control-plane namespace participates in ownership checks, but this iteration only retires the HostedCluster and its own namespace.

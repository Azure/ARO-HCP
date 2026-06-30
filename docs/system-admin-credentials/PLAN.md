# SystemAdminCredentialRequest Architecture

This document describes the architecture for replacing cluster-service
break-glass credentials with a native `SystemAdminCredentialRequest` flow
managed directly by the backend and kube-applier.

## Motivation

The existing break-glass credential flow routes through Clusters Service (CS),
which adds latency, couples credential lifecycle to CS availability, and
prevents fine-grained lifecycle tracking. Moving to a native
`SystemAdminCredentialRequest` document in Cosmos lets the backend orchestrate
issuance, observation, revocation, and cleanup directly via kube-applier desires,
with each phase tracked by standard `metav1.Conditions`.

## Key Types

### SystemAdminCredentialRequest

A Cosmos document representing a single credential request. Nested under the
cluster ARM resource ID.

**Why "Request" in the name** (review feedback from deads2k): The document
represents a *request* for a credential, not the credential itself. The actual
credential (signed certificate + private key) is ephemeral and assembled
on-the-fly by the frontend. The Cosmos document tracks the lifecycle of that
request. Using "Request" aligns with Kubernetes naming conventions
(CertificateSigningRequest, TokenRequest) and makes the intent clearer.

```go
type SystemAdminCredentialRequest struct {
    CosmosMetadata
    Spec   SystemAdminCredentialRequestSpec
    Status SystemAdminCredentialRequestStatus
}
```

**Spec fields:**
- `Username` - K8s username embedded in the certificate CN
- `ExpirationTimestamp` - Server-set, now + 24h
- `OperationID` - ARM operation that created the request
- `PublicKeyPEM` / `PrivateKeyPEM` - RSA 4096 keypair

**Status fields:**
- `SignedCertificate` - Base64-DER cert from the management cluster signer
- `Conditions` - Standard `metav1.Conditions` tracking lifecycle
- `RevokedAt` - Timestamp when revocation completed
- `DeleteTimestamp` - Set when deletion has been requested; controllers use
  this to drive teardown of associated kube-applier desires before removing
  the credential request document itself

### Conditions instead of Phase

**Why Conditions** (review feedback from deads2k): A Phase enum forces a linear
state machine where only one state is active at a time. Conditions allow
independent tracking of orthogonal concerns:

- `Issued` - CSR has been signed
- `Failed` - CSR was denied or issuance failed
- `AwaitingRevocation` - Revocation requested but not yet confirmed
- `Revoked` - Certificate has been revoked
- `ContentDeleted` - All management-cluster-side objects cleaned up

This lets controllers reason about individual concerns without needing to
understand the full state machine. For example, the cleanup controller only
cares about `ContentDeleted`, not whether the credential was issued or revoked.

### SystemAdminCredentialsRevocation

A separate Cosmos document type tracking a revocation event. Created when a
`RevokeCredentials` operation fires.

**Why a separate type** (review feedback from deads2k): Revocation is a
cluster-scoped event (revoking *all* active credentials), not a per-credential
action. Having a separate document:

1. Gives the CRR Apply/Read desires a natural parent to scope under
2. Decouples the revocation lifecycle from individual credential requests
3. Makes it possible for controllers to fire specifically on revocation changes
4. Simplifies the cleanup: mark all credential requests with a
   DeletionTimestamp, wait for them to drain

```go
type SystemAdminCredentialsRevocation struct {
    CosmosMetadata
    Spec   SystemAdminCredentialsRevocationSpec   // OperationID, RevokeOpSuffix
    Status SystemAdminCredentialsRevocationStatus // Conditions
}
```

## Controller Architecture

### Desire Scoping

Desire resources (ApplyDesire, ReadDesire, DeleteDesire) support being nested
under `SystemAdminCredentialRequest` as a parent resource, in addition to
cluster-scoped and node-pool-scoped nesting. This means resource IDs follow
the pattern:

```
/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.RedHatOpenShift/
  hcpOpenShiftClusters/{cluster}/systemAdminCredentialRequests/{cred}/
  applyDesires/{name}
```

**Why credential-request-scoped desires** (review feedback from deads2k):
Nesting desires under the `SystemAdminCredentialRequest` parent:

1. Makes cleanup automatic when the parent document is deleted
2. Removes the need for an `OutstandingDesires` tracking field
3. Lets controllers fire when desires change (informer watches on scoped desires)
4. Makes it easy to find all desires for a specific credential request

The kube-applier types (`registry.go`, `types_cosmosdata.go`) include
`CredentialRequestScoped*ResourceType` constants and
`ToCredentialRequestScoped*ResourceIDString` builder functions for all three
desire types.

### Controller Pattern

**Cluster-scoped controllers** (CABundleSync, ServingCAReadDesireCreator) use
`ClusterWatchingController` because they operate on cluster-level data shared
across all credential requests (e.g., serving CA bundle). They fire on cluster
and ServiceProviderCluster informer events.

**Why ClusterWatchingController for these** (review feedback from deads2k):
The CA bundle sync and serving CA read desire creator are inherently
cluster-scoped operations. The serving CA is the same for all credential
requests on a cluster. Using `CredentialRequestWatchingController` for them
was incorrect — it would fire per-credential-request even though the work is
identical regardless of which credential request triggered it.

**Credential-scoped controllers** (DesiresCreator, ClusterDeletionCleanup,
IssuanceObserver, etc.) use `CredentialRequestWatchingController` because
they react to individual credential request lifecycle changes.

### Cluster Create Operation Requires CA Bundle

The cluster create operation (`operationClusterCreate`) requires the
`ServingCABundle` field on `ServiceProviderClusterStatus` to be populated
before considering the cluster created. This ensures that the serving CA is
available for building kubeconfigs before the cluster is marked as ready.

**Why this is a precondition** (review feedback from deads2k): Without the
serving CA bundle, the frontend cannot construct valid kubeconfigs for system
admin credential responses. Marking a cluster as created before the CA bundle
is available would allow credential request operations to proceed but fail to
produce usable kubeconfigs.

### Credential Request Deletion via DeleteTimestamp

When a credential request needs to be cleaned up (during cluster deletion or
revocation), the controller sets `Status.DeleteTimestamp` on the
`SystemAdminCredentialRequest`. The `ClusterDeletionCleanup` controller then
drives a 4-step teardown:

1. **Delete ApplyDesires**: For each ApplyDesire matching the credential prefix,
   create a corresponding DeleteDesire so the kube-applier removes the
   management-cluster-side objects.
2. **Wait for DeleteDesires**: Check that all DeleteDesires have the
   `Successful=True` condition, indicating the kube-applier has confirmed
   deletion on the management cluster.
3. **Clean up DeleteDesires and ReadDesires**: Delete the completed DeleteDesires
   and any credential-related ReadDesires.
4. **Delete credential document**: Once all desires are cleaned up, delete the
   `SystemAdminCredentialRequest` document itself.

**Why DeleteTimestamp** (review feedback from deads2k): This pattern is more
natural than checking cluster-level deletion timestamps because:

1. Each credential request drives its own cleanup independently
2. The controller only needs to check one field (`Status.DeleteTimestamp`)
   instead of correlating with cluster-level state
3. It decouples credential cleanup from cluster deletion — revocation can
   also use the same mechanism

### Lister-based Reads

**Rationale** (review feedback from deads2k): Controllers must use lister-based
gets (from the informer cache) instead of live Cosmos reads via
`GetOrCreateServiceProviderCluster`. Live reads:

1. Add unnecessary load to Cosmos
2. Can fail transiently, blocking controller progress
3. Are inconsistent with the informer-driven controller pattern

The `ServiceProviderClusterLister.Get()` from the informer cache is sufficient
for all read-only lookups. Only write paths (like `DispatchRevokeCredentials`)
that genuinely need to create the SPC if it doesn't exist should use the live
`GetOrCreateServiceProviderCluster`.

### PreconditionFailed Handling

When a Cosmos `Replace` returns HTTP 412 (PreconditionFailed), it means the
document was concurrently modified. The correct response is to return nil and
let the informer re-trigger the controller, rather than propagating the error
(which would cause exponential backoff retries).

## Controller List

| # | Controller | Type | Trigger | Purpose |
|---|-----------|------|---------|---------|
| 1 | DispatchRequestCredential | Operation | Operation (RequestCredential, Accepted) | Create credential request doc, generate keypair |
| 2 | OperationRequestCredentialPoll | Operation | Operation (RequestCredential, non-terminal) | Map conditions to ARM provisioning state |
| 3 | IssuanceObserver | CredentialRequest | CredentialRequest + ReadDesire informers | Watch CSR ReadDesire for signed cert |
| 4 | DispatchRevokeCredentials | Operation | Operation (RevokeCredentials, Accepted) | Flip credentials to AwaitingRevocation, create CRR |
| 5 | OperationRevokeCredentialsPoll | Operation | Operation (RevokeCredentials, Deleting) | Drive revocation phases |
| 6 | ClusterDeletionCleanup | CredentialRequest | CredentialRequest + ReadDesire informers | Gate cluster deletion on credential cleanup |
| 7 | PostIssuanceCleanup | CredentialRequest | CredentialRequest + ReadDesire informers | Tear down MC objects after issuance |
| 8 | CABundleSync | Cluster | Cluster + ReadDesire informers | Sync serving CA to ServiceProviderCluster |
| 9 | RevokedGC | CredentialRequest | CredentialRequest informer (1h interval) | Delete revoked credential docs after 48h |
| 10 | ServingCAReadDesireCreator | Cluster | Cluster + ReadDesire informers | Ensure serving CA ReadDesire exists |
| 11 | DesiresCreator | CredentialRequest | CredentialRequest informer | Create CSR/CSRA/RBAC desires for pending requests |

## RBAC Object Ordering

RBAC objects (ClusterRole, ClusterRoleBinding, Role, RoleBinding) are created
*before* the CSR and CSRA desires. This ensures the HyperShift signer has the
necessary permissions before it sees the CSR, preventing a window where the CSR
exists but the signer lacks permission to act on it.

## Open Items

1. ~~Kube-applier changes to support desire scoping under SystemAdminCredentialRequest~~ — **Done** (resource type constants and ID builders added)
2. ~~SystemAdminCredentialRequest informer-based controller pattern~~ — **Done**
3. ~~DeletionTimestamp-based cleanup replacing the explicit desire teardown~~ — **Done** (DeleteTimestamp field added, controller updated)
4. Migration path from Phase-based documents to Conditions-based documents
5. Wire credential-scoped desire CRUD into controllers (currently desires are cluster-scoped at the Cosmos CRUD level)

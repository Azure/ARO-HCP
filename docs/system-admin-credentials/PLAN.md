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
- `SignedCertificate` - Base64-encoded PEM cert from the management cluster
  signer. The Kubernetes CSR `Status.Certificate` is PEM-encoded and client-go's
  `clientcmd` expects PEM, so the frontend base64-decodes this straight into the
  kubeconfig with no DER→PEM wrapping.
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

### SystemAdminCredentialRevocation

A separate Cosmos document type representing a single revocation of *all* of a
cluster's system admin credentials. Nested under the cluster ARM resource ID and
created when a `RevokeCredentials` operation fires.

**Why a separate, first-class type** (review feedback from deads2k): The original
implementation had the dispatch controller do everything inline — flip every
credential to `AwaitingRevocation`, create the CRR desires, and poll for
completion. deads2k asked for this to be modeled as a nested type with dedicated
controllers instead, because:

1. Revocation is a cluster-scoped event (revoking *all* active credentials), not
   a per-credential action, so it deserves its own object rather than being
   smeared across every credential request.
2. A dedicated document gives the revocation a stable identity: the dispatch
   controller records its resource ID on the operation's `InternalID`, and the
   operation completes precisely when the document is gone.
3. Splitting the lifecycle across small controllers (mark → desires → deletion)
   keeps each reconcile focused and testable, and lets controllers fire
   specifically on revocation changes.
4. Reusing the per-credential `DeleteTimestamp` teardown means revocation and
   cluster deletion share the same credential-cleanup path.

```go
type SystemAdminCredentialRevocation struct {
    CosmosMetadata
    Spec   SystemAdminCredentialRevocationSpec   // OperationID, RevokeOpSuffix
    Status SystemAdminCredentialRevocationStatus // Conditions, DeleteTimestamp
}
```

**Status conditions:**
- `CredentialsMarkedForDeletion` - every `SystemAdminCredentialRequest` has been
  stamped with a `DeleteTimestamp`
- `CertificatesRevoked` - the hosted cluster confirmed previously-issued
  certificates are revoked (via the mirrored CertificateRevocationRequest)
- `Complete` - the whole flow is done; the `DeleteTimestamp` is set at the same
  time so the deletion controller can tear everything down

## Controller Architecture

### Desire Scoping

Desire resources (ApplyDesire, ReadDesire) support being nested
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
revocation), a controller sets `Status.DeleteTimestamp` on the
`SystemAdminCredentialRequest`. The deletion controller (still registered as
`ClusterDeletionCleanup`) fires on every credential request change and, for the
**single** request named by its key, drives a 4-step teardown:

1. **Flip ApplyDesires to Delete**: For each ApplyDesire belonging to *this*
   credential request, set `Spec.Type=Delete` (clearing the ServerSideApply
   payload) so the kube-applier removes the management-cluster-side objects.
2. **Wait for the Delete desires**: Check that each flipped ApplyDesire has the
   `Successful=True` condition, indicating the kube-applier has confirmed
   deletion on the management cluster.
3. **Clean up the desires**: Delete the completed Delete-type ApplyDesires and
   this credential request's ReadDesires.
4. **Delete credential document**: Once all of this request's desires are cleaned
   up, delete the `SystemAdminCredentialRequest` document itself.

**Single-request scope** (review feedback from deads2k): An earlier version
matched desires by the shared `systemAdminCredential` name prefix and then set a
cluster-wide `SystemAdminCredentialContentDeleted` condition once *all* of a
cluster's credential requests were gone — that made a per-request controller act
on the whole cluster. It now matches only the desires belonging to the request
in the key (via `isCredentialDesire`), and the cluster-wide condition (which had
no consumer) was removed. The `DeleteTimestamp` gate is factored into a
`needsWork` helper, and every waiting branch logs what specifically it is waiting
for.

**Why DeleteTimestamp** (review feedback from deads2k): This pattern is more
natural than checking cluster-level deletion timestamps because:

1. Each credential request drives its own cleanup independently
2. The controller only needs to check one field (`Status.DeleteTimestamp`)
   instead of correlating with cluster-level state
3. It decouples credential cleanup from cluster deletion — revocation reuses the
   same mechanism by stamping `DeleteTimestamp` on every credential request

### Revocation Lifecycle

`RevokeCredentials` is driven through the `SystemAdminCredentialRevocation`
document by four cooperating controllers, replacing the single do-everything
dispatch controller:

1. **DispatchRevokeCredentials** (operation-keyed): creates the
   `SystemAdminCredentialRevocation` document (named by the shortened operation
   ID), records its resource ID on the operation's `InternalID`, and moves the
   operation to `Deleting`. It performs no revocation work itself.
2. **RevocationMarkRequests** (revocation-keyed): live-lists every
   `SystemAdminCredentialRequest` for the cluster and stamps each with a
   `DeleteTimestamp`, then sets `CredentialsMarkedForDeletion`. The
   per-credential deletion controller above then tears each one down.
3. **RevocationDesires** (revocation-keyed): ensures the CRR RBAC, CRR
   ApplyDesire, and CRR ReadDesire exist; watches the mirrored CRR; sets
   `CertificatesRevoked` once the hosted cluster confirms revocation; and — when
   both `CertificatesRevoked` and `CredentialsMarkedForDeletion` hold — sets
   `Complete` and stamps the revocation's own `DeleteTimestamp`.
4. **RevocationDeletion** (revocation-keyed): once the revocation carries a
   `DeleteTimestamp`, tears down the revocation's desires (matched by the
   revocation suffix) by flipping their ApplyDesires to `Type=Delete` and, when
   they are all gone, deletes the `SystemAdminCredentialRevocation` document.

The `OperationRevokeCredentialsPoll` controller no longer performs revocation
work; it simply marks the operation `Succeeded` (and clears the cluster's revoke
sentinel) once the revocation document identified by `InternalID` disappears.

Revocation controllers are wired through a new `RevocationWatchingController`
that fires on `SystemAdminCredentialRevocation` informer events and re-polls on
resync. Their controller-status documents are written cluster-scoped so the
read/write paths agree.

### Skipping Redundant Desire Writes

**Rationale** (review feedback from deads2k): `DesiresCreator` consults the
ApplyDesire and ReadDesire listers before writing each per-credential desire.
When a desire already exists with the desired management cluster, target, and
rendered content, the redundant Cosmos create is skipped entirely. The controller
is also wired to fire on ReadDesire changes so drift is repaired promptly.

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
| 4 | DispatchRevokeCredentials | Operation | Operation (RevokeCredentials, Accepted) | Create SystemAdminCredentialRevocation, record InternalID, move op to Deleting |
| 5 | OperationRevokeCredentialsPoll | Operation | Operation (RevokeCredentials, Deleting) | Complete the operation once the revocation document is gone |
| 6 | ClusterDeletionCleanup | CredentialRequest | CredentialRequest + ReadDesire informers | Tear down a single credential request's desires and doc when its DeleteTimestamp is set |
| 7 | PostIssuanceCleanup | CredentialRequest | CredentialRequest + ReadDesire informers | Tear down MC objects after issuance |
| 8 | CABundleSync | Cluster | Cluster + ReadDesire informers | Sync serving CA to ServiceProviderCluster |
| 9 | RevokedGC | CredentialRequest | CredentialRequest informer (1h interval) | Delete revoked credential docs after 48h |
| 10 | ServingCAReadDesireCreator | Cluster | Cluster + ReadDesire informers | Ensure serving CA ReadDesire exists |
| 11 | DesiresCreator | CredentialRequest | CredentialRequest + ReadDesire informers | Create CSR/CSRA/RBAC desires for pending requests (skips writes when a lister shows the desire already matches) |
| 12 | RevocationMarkRequests | Revocation | SystemAdminCredentialRevocation informer | Stamp every credential request with a DeleteTimestamp |
| 13 | RevocationDesires | Revocation | SystemAdminCredentialRevocation informer | Manage CRR desires, detect revocation, mark the revocation complete/for-deletion |
| 14 | RevocationDeletion | Revocation | SystemAdminCredentialRevocation informer | Tear down the revocation's desires and delete the revocation doc |

## RBAC Object Ordering

RBAC objects (ClusterRole, ClusterRoleBinding, Role, RoleBinding) are created
*before* the CSR and CSRA desires. This ensures the HyperShift signer has the
necessary permissions before it sees the CSR, preventing a window where the CSR
exists but the signer lacks permission to act on it.

## Open Items

1. ~~Kube-applier changes to support desire scoping under SystemAdminCredentialRequest~~ — **Done** (resource type constants and ID builders added)
2. ~~SystemAdminCredentialRequest informer-based controller pattern~~ — **Done**
3. ~~DeletionTimestamp-based cleanup replacing the explicit desire teardown~~ — **Done** (DeleteTimestamp field added, controller updated)
4. ~~Revocation modeled as a nested SystemAdminCredentialRevocation type with dedicated controllers~~ — **Done** (dispatch → mark → desires → deletion; operation completes when the revocation document is gone)
5. Migration path from Phase-based documents to Conditions-based documents
6. Wire credential/revocation-scoped desire CRUD into controllers. The Cosmos
   CRUD (`KubeApplierDBClient`) currently only exposes cluster- and node-pool-scoped
   desire accessors, so both per-credential and per-revocation desires are stored
   cluster-scoped and disambiguated by name (credential name / revocation suffix).
   The revocation controllers own their desires' full lifecycle regardless, but a
   dedicated CRUD scope would let them nest physically under the revocation.

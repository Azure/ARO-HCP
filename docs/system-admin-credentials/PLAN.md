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

**Current approach:** Desires are created at the cluster scope and tracked via a
list of references on the credential request document.

**Future direction** (review feedback from deads2k): Desires should be scoped
*under* the `SystemAdminCredentialRequest` (and `SystemAdminCredentialsRevocation`)
types. This nesting:

1. Makes cleanup automatic when the parent document is deleted
2. Removes the need for an `OutstandingDesires` tracking field
3. Lets controllers fire when desires change (informer watches on scoped desires)
4. Makes it easy to find all desires for a specific credential request

This requires kube-applier changes to support desire documents scoped under
arbitrary parent types, which will be done in a follow-up PR.

### Controller Pattern

**Implemented** (review feedback from deads2k): All system-admin-credential
controllers now fire on individual `SystemAdminCredentialRequest` informer
events via `NewCredentialRequestWatchingController`, rather than watching
cluster-level events and iterating over all credential requests per cluster.

This change was mandatory for two reasons:

1. **Responsiveness**: The previous `ClusterWatchingController` pattern relied on
   periodic cluster resync (1-minute intervals). With an 11-controller chain,
   each waiting for the next resync cycle, the compound latency could easily
   exceed 10 minutes — causing e2e timeouts. Informer-driven controllers fire
   immediately when a credential request is created or updated.

2. **Correctness**: Controllers should react to the events they care about.
   Credential lifecycle controllers care about credential request changes, not
   cluster changes. Watching cluster events was an indirection that added
   unnecessary coupling.

The `CredentialRequestWatchingController` pattern mirrors `ClusterWatchingController`
but:
- Watches the `SystemAdminCredentialRequest` informer instead of cluster informers
- Uses `SystemAdminCredentialRequestKey` (subscription, RG, cluster, credential name)
  instead of `HCPClusterKey`
- Derives cluster identity from the credential request's parent resource ID
- Optionally watches ReadDesire informer events (walking up to the credential
  request parent type)

Cluster-scoped controllers (CABundleSync, ServingCAReadDesireCreator,
ClusterDeletionCleanup) also use this pattern. They receive credential request
events but perform cluster-wide operations, which is idempotent — processing the
same cluster-level operation multiple times (once per credential request) produces
the same result.

### Cluster Deletion Cleanup

**Current approach:** The cleanup controller walks each credential's tracked
desires and issues delete desires for each apply desire.

**Future direction** (review feedback from deads2k): Use a DeletionTimestamp
pattern. The revocation controller:

1. Sets a DeletionTimestamp on every `SystemAdminCredentialRequest`
2. Each credential request's controller handles its own cleanup
3. The revocation controller waits for all DeletionTimestamp'd requests to be
   gone
4. This is more natural and lets each credential request clean itself up

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

| # | Controller | Trigger | Purpose |
|---|-----------|---------|---------|
| 1 | DispatchRequestCredential | Operation (RequestCredential, Accepted) | Create credential request doc, generate keypair |
| 2 | OperationRequestCredentialPoll | Operation (RequestCredential, non-terminal) | Map conditions to ARM provisioning state |
| 3 | IssuanceObserver | CredentialRequest + ReadDesire informers | Watch CSR ReadDesire for signed cert |
| 4 | DispatchRevokeCredentials | Operation (RevokeCredentials, Accepted) | Flip credentials to AwaitingRevocation, create CRR |
| 5 | OperationRevokeCredentialsPoll | Operation (RevokeCredentials, Deleting) | Drive revocation phases |
| 6 | ClusterDeletionCleanup | CredentialRequest + ReadDesire informers | Gate cluster deletion on credential cleanup |
| 7 | PostIssuanceCleanup | CredentialRequest + ReadDesire informers | Tear down MC objects after issuance |
| 8 | CABundleSync | CredentialRequest + ReadDesire informers | Sync serving CA to ServiceProviderCluster |
| 9 | RevokedGC | CredentialRequest informer (1h interval) | Delete revoked credential docs after 48h |
| 10 | ServingCAReadDesireCreator | CredentialRequest + ReadDesire informers | Ensure serving CA ReadDesire exists |
| 11 | DesiresCreator | CredentialRequest informer | Create CSR/CSRA/RBAC desires for pending requests |

## RBAC Object Ordering

RBAC objects (ClusterRole, ClusterRoleBinding, Role, RoleBinding) are created
*before* the CSR and CSRA desires. This ensures the HyperShift signer has the
necessary permissions before it sees the CSR, preventing a window where the CSR
exists but the signer lacks permission to act on it.

## Open Items

1. Kube-applier changes to support desire scoping under SystemAdminCredentialRequest
2. ~~SystemAdminCredentialRequest informer-based controller pattern~~ — **Done**
3. DeletionTimestamp-based cleanup replacing the explicit desire teardown
4. Migration path from Phase-based documents to Conditions-based documents

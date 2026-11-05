# Cosmos data flow: HostedCluster data-plane identities

This document covers the Cosmos documents and controller boundary changed by
#6997. The backend owns persistence and publication; Cluster Service (CS) still
provides the base HostedCluster manifest.

## Documents and writers

| Data | Writer | Readers |
|---|---|---|
| `Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators` | Frontend cluster creation | Identity resolvers, federation intent controller, identity intent writer |
| `ServiceProviderCluster.Status.ManagedIdentityDetails[lowercaseResourceID].MetadataFromARMUserAssignedIdentitiesAPI` | `FetchManagedIdentitiesInfo` | Federation intent controller, identity intent writer, legacy principal mirror refresh gate |
| `Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[identityResourceID, operatorName]` | #6982 federation intent/executor controllers | Identity intent writer |
| `Status.DataPlaneOperatorsManagedIdentities.Identities[lowercaseResourceID]` | `FetchDataPlaneOperatorsManagedIdentitiesInfo` | Role assignment, deny assignment, identity intent controllers |
| `Status.AzureResources.RoleAssignments.AzureResources` | Role assignment controller | Identity intent writer |
| `Status.AzureResources.DenyAssignments.AzureResources[].ExcludedPrincipalIDs` | `ClusterDenyAssignment` after successful Azure reconciliation | Identity intent writer; deny assignment refresh gate |
| `Spec.DesiredHostedCluster` three data-plane ClientID leaves | `HostedClusterDataPlaneIdentities` | `ClusterResources` |
| `Spec.HostedClusterDataPlaneIdentitiesOwnedByRP` | `HostedClusterDataPlaneIdentities` | `ClusterResources` |
| Cluster-scoped `HostedCluster` ApplyDesire | `ClusterResources` | Kube-applier |
| Readonly HostedCluster ReadDesire status | Kube-applier observation | Identity intent bootstrap |

All `Spec` and `Status` paths above, unless another document is named, belong to
one ServiceProviderCluster (SPC) document per customer cluster. The ARM identity
resource ID identifies an Azure resource address. Deleting and recreating a UAMI
at that address produces a new ClientID and PrincipalID.

## Ownership and bootstrap

The rollout flag `enableHostedClusterDataPlaneIdentityWriter` defaults to false.
When enabled, the identity controller reads the Cluster and SPC from Cosmos. It
skips deleting clusters, clusters without a CS identity, and clusters without a
management-cluster placement. Before taking ownership, it waits for a successful
Readonly HostedCluster observation with managed-identity authentication and all
three ClientIDs populated.

It writes those three observed IDs and the ownership marker in one conditional
SPC replacement. These are the existing values, so bootstrap does not require
Azure readiness for a replacement identity. Until this write succeeds,
`ClusterResources` retains the CS values. Afterward it always overlays the owned
leaves from SPC, including when the identity writer is disabled. Missing or
malformed owned intent fails closed: the previous ApplyDesire remains intact.

The owned leaves under
`spec.platform.azure.azureAuthenticationConfig.managedIdentities.dataPlane` are:

- `imageRegistryMSIClientID`
- `diskMSIClientID`
- `fileMSIClientID`

The controller preserves all other SPC data, including certificate intent.
`DesiredHostedCluster` is partial intent; it is never serialized wholesale as
the replacement for the CS HostedCluster manifest.

## Advancing an identity

Each operator advances independently after these checks pass on the SPC snapshot:

1. ARM metadata resolves a nonempty ClientID, PrincipalID, and TenantID without a
   retrieval error.
2. The federation assignment for the exact `(lowercase identity resource ID,
   operator name)` targets that tuple, is not deconfiguring, and is confirmed by
   `IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured`.
3. The legacy data-plane identity mirror agrees on ClientID and PrincipalID.
4. All configured managed-resource-group role assignments for that principal
   appear in confirmed status, and every required deny assignment confirms an
   exclusion for it.

An operator whose dependencies are incomplete retains its last desired ClientID;
other ready operators can advance. Empty/missing operator configuration fails
closed. Confirmed Cosmos status is evidence from a completed reconciliation, not
a live transaction across Azure, Cosmos, and Kubernetes.

A newer resolved ARM instance bypasses the legacy mirror's 12-hour refresh timer
when its client/principal differs. Changed principal exclusions likewise bypass
the deny assignment refresh timer. Neither controller changes another controller's
timer. Unresolved ARM metadata does not defeat the mirror's normal backoff.

## Ordering and concurrent writers

Cosmos `_etag` and the application's `InstanceVersion` serve different purposes:

- The ETag is the conditional-write token for one document. After a conflict, the
  identity writer rereads SPC, recomputes its three leaves, and retries up to three
  times. This preserves concurrent certificate/status writes.
- `SPC.InstanceVersion` is the source revision for publication. It covers the
  entire SPC document, not one operator and not all clusters globally.

`ClusterResources` reads SPC directly, validates the placement and positive
InstanceVersion, overlays only the owned identity leaves, and writes one HC
ApplyDesire using the existing field manager. It records the source revision in
`Tags["hostedClusterSourceInstanceVersion"]`.

The publisher reads the existing desire directly and conditionally replaces it.
It refuses a candidate older than the stored source revision, including after an
ETag conflict, with up to five attempts. Unversioned existing desires can be
adopted when owned by `ClusterResources`; malformed versions and different
controller owners fail closed. A tag-only update preserves apply status. A spec
change clears it for reconciliation. Source/processing errors suppress stale
desire cleanup for that pass.

This orders identity snapshots. It does not provide a transaction across the
SPC and ApplyDesire documents or version the CS response. Concurrent candidates
with the same SPC revision contain the same identity intent; unrelated CS fields
retain the existing eventual-consistency behavior.

Using the full SPC InstanceVersion is intentionally conservative. Unrelated
status writes can cause additional desire updates/conflicts. If measurements show
retry or write-amplification problems, a dedicated shared revision for the three
identity leaves is the agreed optimization. It is not needed for correctness now.

## Dependency boundary

The preview was prepared against main `6dcc32ab` and #6982 `01d8a696`.
#6944 is already in main. #6997's remaining dependency on #6982 is its persisted
per-operator assignment contract and completed-configuration helper.

An ensured stamp is historical confirmation for the target identity instance.
A failed recheck of an already confirmed FIC can leave it in place. New or known
outstanding configuration clears it in #6982. Consumer tests must not redefine
that stamp as a live Azure health signal.

FIC creation, repair, delayed deconfiguration, and delete-error policy remain
owned by #6982. #6997 does not patch those controllers or rely on their test
fixtures. This feature handles recovery when a configured UAMI is recreated at
the same resource ID. A future API allowing switching between two live identity
resources needs coordinated draining of old FICs, role assignments, and deny
exclusions; this PR does not claim that broader migration guarantee.

## Validation and maintenance

Tests cover operator/instance readiness, per-operator progress, authorization
prerequisites, bootstrap/deletion/disable guards, preservation of certificate
intent across ETag conflicts, publisher ordering under a competing write, and
fail-closed publication. Tests use the in-memory Cosmos CRUD implementation;
they do not establish production Azure authorization propagation timing.

Update this document when changing the identity pipeline's persisted fields,
writers, readiness contract, or concurrency behavior. Verify paths and semantics
against the code, and record separately any assumptions requiring Azure or
Cosmos integration validation.

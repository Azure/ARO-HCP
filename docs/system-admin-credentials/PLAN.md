# Plan: replace cluster-service break-glass credentials with `SystemAdminCredential` + kube-applier Desires

## Status

Proposal, not yet implemented. Review this file end-to-end before any code lands;
the cutover is "rip and replace in a single PR" so structural mistakes are
expensive to fix later.

## Naming convention used throughout this doc

Everything we *introduce* is named `SystemAdminCredential` (and variants
`systemAdminCredential` / `system-admin-credential`). We never spell
"BreakGlass" in anything we create. Where the text uses "break-glass" or
"BreakGlass", that is one of:

- A legacy cluster-service or OCM-SDK identifier we are deleting (e.g.
  `PostBreakGlassCredential`, `break_glass_credentials` DB table).
- A HyperShift-side identifier we cannot change unilaterally (e.g. the
  signer name `customer-break-glass`, the CRR name segment
  `customer-break-glass-revocation`). These names are part of HyperShift's
  contract with its control-plane-pki-operator; renaming requires a
  HyperShift change. Out of scope here.

Anything inside ARO-HCP that we own — Cosmos types, controllers, package
names, helper functions, ReadDesire bundle internal names — uses
`SystemAdminCredential`.

## Goals & non-goals

**Goals**

- Stop calling cluster-service for `PostBreakGlassCredential`,
  `GetBreakGlassCredential`, `ListBreakGlassCredentials`, and
  `DeleteBreakGlassCredentials`.
- Drive the same Kubernetes objects (CSR, CSRA, Secret, CRR, RBAC) onto the
  management cluster via the kube-applier `ApplyDesire` / `ReadDesire` /
  `DeleteDesire` surface that we already use for HostedCluster and NodePool
  mirroring.
- Preserve the **ARM-customer-visible contract** unchanged: the same two
  `POST` endpoints (`/requestadmincredential`, `/revokecredentials`) and the
  same `GET /operationresults/{operationId}` response shape, including the
  kubeconfig body.
- Land it in a single PR so we are never simultaneously calling both paths
  for the same flow.

**Deliberate departure from cluster-service's security model**

Cluster-service keeps the customer-break-glass private key off its own
disk: at create-time it writes the key into a Secret on the management
cluster, and at GET-time it reads the Secret back to assemble the
kubeconfig. We are **not** preserving that property. The new
`SystemAdminCredentialSpec` carries both the public and private key
directly, so the kubeconfig assembly is a pure Cosmos read with no
management-cluster round trip. The trade-off is conscious:

- *Cost*: anyone with read access to the Cosmos partition can extract a
  working ≤24h credential. Cosmos has at-rest encryption by default,
  and access is gated by the same RBAC as every other ARO-HCP
  per-cluster document.
- *Benefit*: one fewer `ApplyDesire`, one fewer `ReadDesire`, one fewer
  per-credential ManifestWork on the MC, no GET-time MC dependency,
  and a substantially simpler `OperationResult` handler that can serve
  the kubeconfig without ever waking the kube-applier informer.

If the threat model later demands cluster-service's original property,
we can layer envelope encryption (customer-managed key in Key Vault)
over `Spec.PrivateKeyPEM` without touching the controller graph. (Open
question 7 records that we explicitly chose **not** to do this in the
initial PR.)

**Non-goals**

- We will not redesign break-glass auth, change the signer name, change the
  CSR shape, or change the cert TTL in this PR. This is a pure
  delivery-mechanism swap.
- We will not change the ARM API surface or the response body shape
  customers see from `OperationResult`.
- We will not migrate already-in-flight cluster-service-issued credentials.
  This is acceptable because credentials live ≤24h and revoke-all is a
  cheap action a customer can repeat — a 24h window after rollout during
  which no new requests use the new path *and* old requests continue to
  resolve via the legacy path is fine.

## How the credential flow works today (background)

This section is reference material — every claim here is something the
plan below depends on. The cluster-service / HyperShift identifiers
appear unchanged because that is the system we are *removing*.

### Cluster-service side (what we are replacing)

`PostBreakGlassCredential` (`pkg/osd/breakglasscredentials/service.go:137`):
1. Generates a row in the `break_glass_credentials` table with status
   `created`, signer `customer`, a server-chosen ID, and a 24-hour
   expiration.
2. Generates an RSA keypair in-memory. Writes a Secret on the management
   cluster containing the private key; the public key is the CSR payload.
3. Bundles a `CertificateSigningRequest`, a `CertificateSigningRequestApproval`
   (HyperShift CRD), the Secret, and the prerequisite RBAC objects into a
   single ACM `ManifestWork` and dispatches it to the management cluster.
4. CSR signer is
   `hypershift.openshift.io/{hcp-namespace}.customer-break-glass`. HyperShift's
   `control-plane-pki-operator` signs the CSR once the CSRA exists.
5. The ManifestWork carries a feedback rule on `.status.certificate`. When
   the certificate is populated, cluster-service's `manifestwork_controller`
   flips the DB row to status `issued` and stores the base64 cert.

`GetBreakGlassCredential` (`service.go:76`):
- Reads the row.
- If status is `issued`, base64-decodes the stored signed certificate,
  reads the private-key Secret back off the management cluster
  (`retrievePrivateKey`), and assembles a kubeconfig (cert + key + cluster
  CA + API URL). The kubeconfig is **generated on every GET** and is not
  persisted.
- If status is anything else, the kubeconfig is nil.

`DeleteBreakGlassCredentials` (`service.go:246`):
- Flips every `customer`-signer row to `awaiting_revocation`.
- Bundles a `CertificateRevocationRequest` (HyperShift CRD; signerClass
  `customer-break-glass` — revokes *all* customer-signer certs for the
  cluster) into a ManifestWork.
- Feedback rule watches
  `.status.conditions[?(@.type=="PreviousCertificatesRevoked")].status`.
  On True, controller flips the rows to `revoked`.

`ListBreakGlassCredentials` (`service.go:60`):
- Pure DB list. No management-cluster interaction.

### ARO-HCP side (the four call sites we will remove)

| File | Line | Cluster-service call | Role |
|---|---|---|---|
| `backend/pkg/controllers/operationcontrollers/dispatch_request_credential.go` | 133 | `PostBreakGlassCredential` | dispatch the create |
| `backend/pkg/controllers/operationcontrollers/operation_request_credential.go` | 111 | `GetBreakGlassCredential` | poll until `Issued` |
| `backend/pkg/controllers/operationcontrollers/dispatch_revoke_credentials.go` | 134 | `DeleteBreakGlassCredentials` | dispatch the revoke |
| `backend/pkg/controllers/operationcontrollers/operation_revoke_credentials.go` | 110 | `ListBreakGlassCredentials` | poll until every row is terminal |
| `frontend/pkg/frontend/frontend.go` | 1012 | `GetBreakGlassCredential` | build the response body for `GET /operationresults/{id}` |

These five lines (four dispatch/poll, one response-builder) are the entire
surface we need to replace.

Two Operation-doc fields drive the flow today:
- `Operation.InternalID` — the cluster-service credential HREF after dispatch
  (request flow) or the cluster's CS internal ID (revoke flow).
- `HCPOpenShiftCluster.ServiceProviderProperties.RevokeCredentialsOperationID`
  — sentinel that blocks a second revoke and gates request dispatch.

## Target architecture

### One Cosmos document per credential: `SystemAdminCredential`

We introduce a new Cosmos resource type, `SystemAdminCredential`, nested
under the cluster. The frontend's `requestadmincredential` action
creates the document; revoke marks every non-terminal one for deletion.
The operation document continues to be the ARM-visible handle; the new
resource is the durable per-credential tracking record we previously had
in cluster-service.

```
/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.RedHatOpenShift/
    hcpOpenShiftClusters/<cluster>/systemAdminCredentials/<credential-name>
```

`<credential-name>` is a 16-character random hex suffix generated by
the dispatch controller at create time —
`strings.ReplaceAll(uuid.New().String(), "-", "")[:16]`. Dispatch
idempotency is keyed on `Spec.OperationID == Operation.OperationID`,
not on the name itself, so a duplicate dispatch finds the existing
doc and skips creation. (See open question 2 for the design rationale.)

We do not expose `SystemAdminCredential` through ARM — it is an internal
resource type indexed only for the operation-controller and frontend
lookups. Same pattern as `Controller` documents.

Proposed Go shape (lives next to `types_cluster.go`):

```go
type SystemAdminCredential struct {
    CosmosMetadata     `json:"cosmosMetadata"`

    Spec   SystemAdminCredentialSpec   `json:"spec"`
    Status SystemAdminCredentialStatus `json:"status"`
}

type SystemAdminCredentialSpec struct {
    // Username is the K8s username embedded in the cert CN. Defaulted at
    // create; the cluster's ACM cluster-admin role binding picks it up.
    Username string `json:"username,omitempty"`
    // ExpirationTimestamp is when the cert ceases to be valid. Server-set
    // at create (now + 24h) — we never let the customer pick.
    ExpirationTimestamp metav1.Time `json:"expirationTimestamp"`
    // OperationID is the ARM operation that created this credential. Used
    // to link the doc back to the customer-visible OperationResult.
    OperationID string `json:"operationID"`
    // PublicKeyPEM is the public half of the keypair generated at dispatch
    // time, PEM-encoded. The CSR carries the DER form of the same key;
    // we keep PEM here only as a convenience for diagnostics and for
    // golden-file fixtures. The PublicKeyPEM is the authoritative input
    // to the CSR built by internal/systemadmincredential.BuildCSR.
    PublicKeyPEM string `json:"publicKeyPEM"`
    // PrivateKeyPEM is the private half of the keypair, PEM-encoded.
    // It is the input to OperationResult's kubeconfig assembly and
    // never leaves Cosmos — see "Deliberate departure from cluster-
    // service's security model" above. Treat as a secret in logs,
    // dumps, and telemetry.
    PrivateKeyPEM string `json:"privateKeyPEM"`
}

type SystemAdminCredentialStatus struct {
    // Phase is the lifecycle state. Mirrors the cluster-service `status`
    // column we are replacing.
    Phase SystemAdminCredentialPhase `json:"phase"`
    // SignedCertificate is the base64-DER cert the management-cluster
    // signer produced. Populated when Phase moves to Issued. We mirror
    // it into Cosmos so GET does not need to chase the MC for
    // CSR.status.certificate on the hot path.
    SignedCertificate string `json:"signedCertificate,omitempty"`
    // Conditions is the standard rolling-status array.
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    // RevokedAt is set when Phase transitions to Revoked. It is the
    // anchor used by the SystemAdminCredentialRevokedGC controller to
    // delete the doc 48h after revocation lands — chosen to outlast
    // the certificate's 24h TTL so a stale row can never describe a
    // still-valid kubeconfig.
    RevokedAt *metav1.Time `json:"revokedAt,omitempty"`
    // OutstandingDesires names every per-credential kube-applier desire
    // that still exists in Cosmos for this credential. Controllers
    // append to it when they create a desire and remove from it when
    // they delete the doc. The post-issuance cleanup controller (#7)
    // and the cluster-deletion gate (#6) walk this list to drive
    // teardown; no other source of truth for "what desires still exist
    // for this credential" is kept. Empty list means the credential has
    // no live MC content of its own — either it never reached Issued
    // and was cleaned up cold, or controller #7 has finished its work.
    OutstandingDesires []SystemAdminCredentialDesireRef `json:"outstandingDesires,omitempty"`
}

// SystemAdminCredentialDesireRef points at a single kube-applier desire
// document scoped under the credential's parent cluster. Kind selects
// the container (ApplyDesires / ReadDesires / DeleteDesires); Name is
// the desire document's last-segment name within that container.
// Together they form the desire's full resource ID via the standard
// kubeapplier.To<…>ScopedDesireResourceIDString helpers.
type SystemAdminCredentialDesireRef struct {
    Kind SystemAdminCredentialDesireKind `json:"kind"`
    Name string                          `json:"name"`
}

type SystemAdminCredentialDesireKind string

const (
    SystemAdminCredentialDesireKindApply  SystemAdminCredentialDesireKind = "ApplyDesire"
    SystemAdminCredentialDesireKindRead   SystemAdminCredentialDesireKind = "ReadDesire"
    SystemAdminCredentialDesireKindDelete SystemAdminCredentialDesireKind = "DeleteDesire"
)

type SystemAdminCredentialPhase string

const (
    SystemAdminCredentialPhaseRequested          SystemAdminCredentialPhase = "Requested"
    SystemAdminCredentialPhaseIssued             SystemAdminCredentialPhase = "Issued"
    SystemAdminCredentialPhaseAwaitingRevocation SystemAdminCredentialPhase = "AwaitingRevocation"
    SystemAdminCredentialPhaseRevoked            SystemAdminCredentialPhase = "Revoked"
    SystemAdminCredentialPhaseFailed             SystemAdminCredentialPhase = "Failed"
)
```

Both halves of the keypair live in `Spec`. The management cluster never
sees the private key — see "Deliberate departure from cluster-service's
security model" above. The dispatch controller generates the keypair
in-process, writes `PublicKeyPEM` and `PrivateKeyPEM` onto the doc as
part of the same `Create` call, and never holds the key in memory
afterward.

### What we apply / read / delete

For each credential the dispatcher materializes the following set of
desires. Every name uses the credential's 16-character `<credName>` as
its suffix so the resource-ID set is deterministic per credential and
a sweep is trivial. The full per-credential set is enumerated under
"The three RBAC prerequisite bundles…" below; the bullet here is the
quick summary.

- **Apply**: CSR, CSRA, and three RBAC bundles (give-csr-perm,
  csra-perm, revocation-perm). Five `ApplyDesire` documents in total.
- **Read**: the CSR (so we can observe `status.certificate` and flip
  `Phase` to `Issued`). One `ReadDesire` document.
- **Delete**: created later by controllers #5, #6, and #7, one
  `DeleteDesire` per Apply target as teardown proceeds.

There is no per-credential Secret `ApplyDesire` or `ReadDesire` — the
private key lives in `Spec.PrivateKeyPEM` and never needs to be
delivered to or retrieved from the management cluster.

### Owner annotation on every ApplyDesire

Every k8s object we land on a management cluster via `ApplyDesire`
**must** carry the annotation

```
aro-hcp.openshift.io/owner: <resourceID>
```

on its `metadata.annotations`. `<resourceID>` is the lowercased string
form of the ARM resource ID of the ARO-HCP source — the cluster ID for
cluster-scoped applies, the node pool ID for nodepool-scoped applies.
This applies to every `ApplyDesire` in this plan — each per-credential
CSR, CSRA, and three RBAC bundles, plus the per-revoke CRR — and is
the convention all new ApplyDesire writers should follow.

Why:
- **Attribution.** When a management-cluster operator is staring at an
  unfamiliar `CertificateSigningRequest` or `ClusterRole`, the
  annotation is the single string they need to identify the source
  cluster. Tagging by cluster name is ambiguous across subscriptions;
  the resource ID is not.
- **Cleanup.** A future GC sweep on a management cluster — or an
  ad-hoc `kubectl delete --selector` against orphaned objects after a
  source cluster is gone — only works if owner is a structured
  annotation. The Cosmos doc tracking the ApplyDesire can vanish; the
  annotation is the durable signal.
- **Telemetry.** Dashboards that join MC-side k8s metrics back to
  ARO-HCP resources can do so without joining on namespace conventions
  that we may change.

Enforcement: the annotation is set inside every `Build*` helper (see
the helper list further down). Helpers take an explicit
`ownerResourceID *azcorearm.ResourceID` parameter so a call site
cannot forget. There is no fallback default; passing a nil owner is a
programming error and the helper panics in tests (`t.Helper()` + a
`requireOwner` guard) so it never reaches production.

`ReadDesire` and `DeleteDesire` do not need the annotation — they
either mirror an object that an earlier `ApplyDesire` already tagged,
or they delete one. The annotation lives on the k8s object on the MC,
not on the desire doc in Cosmos.

The three RBAC prerequisite bundles cluster-service ships today
(allowing klusterlet to manage CSRs / CSRAs / CRRs) become
**per-credential `ApplyDesire`s** — every credential request creates
its own copy. Each bundle's K8s object names on the MC carry the
credential's 16-character random suffix (the `<credName>` from open
question 2) so two credentials cannot collide on the same
`ClusterRole` / `ClusterRoleBinding` / `Role` / `RoleBinding`, even
within a single ARO-HCP cluster. This also makes ownership and
cleanup straightforward: when the credential's `OutstandingDesires`
are torn down, every k8s object the credential ever caused to exist
goes with it.

The full set of `ApplyDesire`s the dispatcher writes per credential:

| Bundle internal name | TargetItem (on the MC) |
|---|---|
| `systemAdminCredentialCSR-<credName>` | `CertificateSigningRequest` |
| `systemAdminCredentialCSRA-<credName>` | `CertificateSigningRequestApproval` (HyperShift) |
| `systemAdminCredentialRBACGiveCSRPerm-<credName>` | `ClusterRole` + `ClusterRoleBinding`, named `system-admin-credential-give-csr-perm-<credName>` |
| `systemAdminCredentialRBACCSRA-<credName>` | `Role` + `RoleBinding`, named `system-admin-credential-csra-perm-<credName>` |
| `systemAdminCredentialRBACRevocation-<credName>` | `Role` + `RoleBinding`, named `system-admin-credential-revocation-perm-<credName>` |

Plus one `ReadDesire` on the CSR
(`systemAdminCredentialCSR-<credName>`).

Multiple credentials thus produce multiple disjoint k8s object sets
on the MC. There is no shared cluster-scoped RBAC and no per-cluster
suffix to keep stable. `createClusterScopedReadDesiresSyncer` does
**not** seed any RBAC bundle — that job moved entirely into the
credential dispatcher (controller 1). The kube-applier will apply the
RBAC bundle ApplyDesires for a freshly-requested credential before
the CSR can be signed; there is no apply ordering guarantee, but the
applier retries on permission errors so eventual consistency suffices.

The cluster-scoped CRR is still cluster-scoped — one CRR at a time,
created during revocation. Its k8s object name uses the
**revoke-operation's** 16-character suffix (first 16 hex chars of the
revoke `Operation.OperationID.Name` minus dashes), not the cluster
hash. Two concurrent revokes are already blocked by the
`RevokeCredentialsOperationID` sentinel on the cluster, so we never
need more than one active CRR per cluster at a time.

Cleanup: per-credential teardown by controllers #5 and #7 walks the
credential's `OutstandingDesires` and tears down every CSR / CSRA /
RBAC ApplyDesire and ReadDesire — there are no shared k8s objects to
keep alive for other credentials. Controller #6 (cluster-deletion
gate) only needs to handle credentials whose lifecycle never reached
the per-credential cleanup path, plus any straggler CRR.

(Cluster-service ships a fourth bundle that grants klusterlet `secrets`
verb access for the private-key Secret. We do not need that bundle
because we are not creating a per-credential Secret on the MC.)

For revocation we add one *cluster-scoped* pair (the CRR revokes the
whole signer class, not a single credential). Both names carry the
revoke `Operation.OperationID.Name`'s 16-character suffix —
`<revokeOpSuffix>` = `strings.ReplaceAll(Operation.OperationID.Name, "-", "")[:16]`
— so a future replay (if a revoke ever has to be reissued) doesn't
adopt the old object:

| Desire | TargetItem (on the MC) | Bundle internal name | Purpose |
|---|---|---|---|
| Apply | the `CertificateRevocationRequest` | `systemAdminCredentialRevocation-<revokeOpSuffix>` | trigger revocation of every customer-signer cert |
| Read  | the same `CertificateRevocationRequest` (mirror) | `systemAdminCredentialRevocation-<revokeOpSuffix>` | observe `status.conditions[PreviousCertificatesRevoked]` to know we are done |

When revocation finishes, controller #5 (Phase R-2) tears down the CRR
ApplyDesire / ReadDesire / DeleteDesire trio, and walks any straggler
credentials' `OutstandingDesires` for residual per-credential cleanup.
The `SystemAdminCredential` Cosmos documents themselves are kept
until 48 hours after `Phase` reaches `Revoked` — see open question 3
and controller #9 (`SystemAdminCredentialRevokedGC`).

## Controllers

We replace the four operation controllers with two **operation-driver**
pairs (controllers 1+2 and 4+5), one **issuance reconciler**
(controller 3), one **cluster-deletion gate** (controller 6), one
**post-issuance cleanup** (controller 7), one **serving-CA mirror**
(controller 8 — see the "Where the parent cluster's CA and API URL
come from" subsection under Frontend changes), and one **revoked-doc
janitor** (controller 9 — see open question 3, which is now resolved).
All nine live under a new package
`backend/pkg/controllers/systemadmincredentialcontrollers/`. The split
mirrors the pattern in the existing version controllers
(`nodepool_version_controller` + `nodepool_active_version_controller`).

### 1. `OperationRequestCredentialDispatch` (replaces `dispatch_request_credential.go`)

Runs against the operation document. **Intentionally narrow** — its
only job is to create the credential doc and stamp the operation.
Building the `ApplyDesire`s/`ReadDesire`s is owned by controller #11
(see below), which fires from the SystemAdminCredential informer.

Inputs: `Operation` with `Request=RequestCredential`, status `Accepted`, empty
`InternalID`, and the parent cluster.

Outputs:
- Generates an RSA keypair in-process.
- A new `SystemAdminCredential` Cosmos document under the cluster, with
  `Spec.PublicKeyPEM` and `Spec.PrivateKeyPEM` populated and
  `Status.Phase=Requested`. The keypair exists in process memory only
  between generation and the Cosmos `Create`; afterwards the dispatcher
  discards it.
- Sets `Operation.InternalID` to the `SystemAdminCredential` resource ID
  so downstream controllers can find it without another round trip.

`Status.OutstandingDesires` is left empty; controller #11 appends to it
as it creates each desire.

Idempotency: keyed on `Operation.OperationID`. If the
`SystemAdminCredential` already exists with `Spec.OperationID` matching,
this controller just (re-)stamps `Operation.InternalID` and exits.

### 11. `SystemAdminCredentialDesiresCreator`

Driven by the **SystemAdminCredential informer**. For credentials in
`Phase=Requested` it makes sure every expected `ApplyDesire` (CSR, CSRA,
3 RBAC bundles → 8 desires) and the CSR `ReadDesire` exist in the
kube-applier container scoped to the cluster's placed management cluster.
For every desire it newly creates, it appends a `{Kind, Name}` entry to
`Status.OutstandingDesires` (idempotent — re-runs skip names already
present, and `ConflictError` from a `Create` is treated as success).

Why this is informer-driven on the credential rather than folded into
controller #1:
- The dispatch path stays a single Cosmos write — fast, easy to reason
  about, and trivially idempotent.
- Desire creation depends on the cluster's MC placement, which often
  hasn't been resolved at dispatch time. Driving from the credential
  informer means we naturally retry as placement lands.
- Subsequent controllers (#3, #6, #7) only need to know the credential
  doc exists; the desires materialize behind it.

### 2. `OperationRequestCredentialPoll` (replaces `operation_request_credential.go`)

Runs against the operation document.

Inputs: `Operation` with `Request=RequestCredential`, non-empty
`InternalID` (= a `SystemAdminCredential` resource ID), non-terminal status.

Logic:
- Look up the linked `SystemAdminCredential`.
- Map `Status.Phase` to ARM provisioning state exactly as today:
  - `Requested` → `Provisioning`
  - `Issued` → `Succeeded`
  - `Failed` → `ProvisioningFailed`
- No cluster-service involvement.

### 3. `SystemAdminCredentialIssuanceObserver` (new reconciler that watches CSRs)

A `ClusterWatchingController` — now that ReadDesire events fire it (see
the commit on `node-pool-version-01-better-api`).

Inputs: a `SystemAdminCredential` doc in `Phase=Requested` plus the
`ReadDesire` mirror of its CSR.

Logic:
- Read the mirrored CSR's `.status.certificate`. Until populated, no-op.
- When populated:
  1. Replace the credential doc setting `Status.Phase=Issued` and
     `Status.SignedCertificate=<base64 cert>`.
  2. The Operation poller (controller #2) will pick it up via its own
     5-minute resync and on the ReadDesire-driven enqueue.

Failure handling: if the CSR is denied or marked failed, flip the doc to
`Failed` with a Condition carrying the reason. ARM operation status
becomes `ProvisioningFailed`.

This controller deliberately stops at the Phase transition; it does
**not** tear down the per-credential ApplyDesires/ReadDesires. That is
controller #7's job, kicked off by this same Phase change.

### 4. `OperationRevokeCredentialsDispatch` (replaces `dispatch_revoke_credentials.go`)

Inputs: `Operation` with `Request=RevokeCredentials`, status `Accepted`,
and the parent cluster's `RevokeCredentialsOperationID` matches.

Logic:
- List every `SystemAdminCredential` under the cluster with
  `Phase ∈ {Requested, Issued}` and flip each to `AwaitingRevocation`.
- Write a cluster-scoped `ApplyDesire` for the
  `CertificateRevocationRequest`, named
  `systemAdminCredentialRevocation-<revokeOpSuffix>`. One CRR per
  revoke operation, not one per credential.
- Write the matching cluster-scoped `ReadDesire` to monitor its
  status, same suffix.
- Move the Operation to `Deleting`.

This is one of two places we still write a multi-document Cosmos batch
— the doc updates and the desire writes are not atomic, but each
controller is idempotent so the recovery story is the standard
"retry until everything is in the right state."

### 5. `OperationRevokeCredentialsPoll` (replaces `operation_revoke_credentials.go`)

Inputs: `Operation` with `Request=RevokeCredentials`, status `Deleting`.

Logic:
The revoke poller is a state machine across three phases — the CRR
landing, the per-credential teardown, and the final operation
transition. Each reconcile re-evaluates from scratch; never assumes a
prior reconcile's intent.

**Phase R-1 — wait for the CRR to confirm revocation.**

- Look up the cluster-scoped CRR `ReadDesire`. If its mirrored CRR has not
  yet reported
  `status.conditions[PreviousCertificatesRevoked].Status == True` —
  no-op; the ReadDesire informer will retrigger.
- If the CRR reports failure (e.g. a `Failed` condition surfaces) —
  flip the operation to `ProvisioningFailed` and stop. (Cleanup of the
  half-applied content happens via controller #6 on cluster delete.)

**Phase R-2 — drive the per-credential content and the CRR to gone.**

Once the CRR is `PreviousCertificatesRevoked=True`, the certs are
revoked on the management cluster *but the CRR object is still
present*, and there may be a few `SystemAdminCredential` docs whose
per-credential CSR/CSRA were never cleaned up because Phase never
reached `Issued` (the cert was never signed before the customer
revoked, or signing failed). The operation is not Succeeded until
every credential under the cluster has no MC presence.

For every `SystemAdminCredential` doc under the cluster:

- If the per-credential CSR/CSRA `ApplyDesire`s still exist (i.e.
  controller #7 did not get to them — Phase never reached `Issued`),
  drive their teardown via the shared
  `internal/systemadmincredential.IssueDeleteAndAwait(...)` helper.
- When the credential has no remaining MC-targeting desires, flip its
  Phase to `Revoked` (with the revocation timestamp) and zero out
  `Spec.PrivateKeyPEM` so a later Cosmos read cannot recover the key.
  `Spec.PublicKeyPEM` stays for diagnostics.

Also drive the cluster-scoped CRR teardown in this phase: ensure a
`DeleteDesire` for the CRR, wait for `Successful=True`, then delete the
CRR `ApplyDesire`, the CRR `DeleteDesire`, and the CRR `ReadDesire`.
Future revocations create a fresh CRR ApplyDesire — never adopt the
prior one.

In steady state most credentials in `AwaitingRevocation` already had
their CSR/CSRA torn down by controller #7 at issuance time, so
Phase R-2 typically only walks the CRR. The per-credential loop is
present for correctness in the edge case where revoke arrives while a
credential is still in `Phase=Requested`.

**Phase R-3 — close out the operation.**

When every `AwaitingRevocation` credential doc under the cluster has
moved to `Revoked` *and* the CRR teardown above has finished:

- Clear the cluster's `RevokeCredentialsOperationID`.
- Move the operation to `Succeeded`.

While Phase R-2 is still draining for any credential, the operation
stays in `Deleting` — the ARM customer sees a normal
"deletion-in-progress" until the MC content is verifiably gone.

Phase R-2 sets `Status.RevokedAt = now` on each credential it flips
to `Revoked`. The doc itself stays in Cosmos; controller #9
(`SystemAdminCredentialRevokedGC`) deletes it 48 hours later — see
open question 3.

### 6. `SystemAdminCredentialClusterDeletionCleanup` (cluster-deletion gate)

When a cluster is being deleted, every `ApplyDesire` we created for
credentials — the per-credential CSR / CSRA / three RBAC bundles per
credential, plus any straggler CRR — must be torn down on the
management cluster *and* their Cosmos documents removed before
cluster deletion can finish. This controller is the precondition
gate.

Inputs: an `HCPOpenShiftCluster` with `ServiceProviderProperties.DeletionTimestamp`
set, AND `ServiceProviderProperties.ClusterServiceDeletionTimestamp` set
(i.e. cluster-service-side deletion is complete and we are inside the
existing cluster-cleanup window).

Logic, on each reconcile:

1. List every credential-related `ApplyDesire` under the cluster:
   - For each `SystemAdminCredential` doc still under the cluster:
     walk its `Status.OutstandingDesires` — that's the canonical
     list of every per-credential CSR / CSRA / RBAC bundle Apply
     plus the CSR Read still owned by that credential. Credentials
     that controller #7 already finished come in with an empty list
     and contribute nothing.
   - Any straggler cluster-scoped CRR ApplyDesire (revoke ran but
     controller #5 didn't get all the way through Phase R-2).

2. For each `ApplyDesire`, ensure a matching `DeleteDesire` exists with
   the *same* `TargetItem`. The kube-applier sees the DeleteDesire and
   issues the delete on the management cluster. The DeleteDesire's
   `Status.Conditions[type=Successful]` only flips to True once the
   k8s object is fully gone on the MC (finalizers drained, GC complete)
   — that is "the DeleteDesire clears the content".

3. Once every DeleteDesire reports `Successful=True`:
   - Delete the matching `ApplyDesire` Cosmos document.
   - Delete the `DeleteDesire` Cosmos document.
   - Delete the corresponding `ReadDesire` (if any — per-credential CSR
     reads only) Cosmos document.

4. When every credential-related desire under the cluster is gone,
   delete every `SystemAdminCredential` doc under the cluster (we no
   longer need the cert / private-key record once the MC content is
   gone; we are mid-cluster-delete and no kubeconfig will ever be
   served again).

5. When step 4 is complete, set a condition on
   `ServiceProviderCluster.Status` —
   `SystemAdminCredentialContentDeleted=True` — so the existing
   cluster-deletion finalizer can advance.

The cluster-deletion main controller treats this condition as a hard
precondition: cluster deletion may not finish until
`SystemAdminCredentialContentDeleted=True`. Implementation note: that
gate gets wired into the same precondition stack used today by
`childresourcescleanup`-style controllers under
`backend/pkg/controllers/clusterdeletion/` (or wherever the cluster
analog of `nodepool_child_resources_cleanup_controller` lives — confirm
location during implementation).

This controller is idempotent: a partial run is safe because every
step is keyed on the set of `ApplyDesire` docs in Cosmos, and the
DeleteDesire's `Status` is the only synchronization signal we trust.
A controller restart picks up exactly where it left off.

Controller #5 (revoke poll) is the other writer of `DeleteDesire`s in
the credential flow: it tears down per-credential CSR / CSRA / their
CRR after the CRR confirms revocation, using the same idempotent
"issue DeleteDesire, wait for `Successful=True`, then delete the Cosmos
doc" pattern. Both controllers call the shared helper
`internal/systemadmincredential.IssueDeleteAndAwait(...)` so they
cannot drift.

In steady state most clusters never see a revoke, so this
controller (cluster-deletion gate) is the path that runs for every
cluster eventually; controller #5 only runs on explicit customer
revocation. The two write disjoint sets of `DeleteDesire`s — controller
#5 against the CSR / CSRA / CRR that were applied for credentials still
in flight at revoke time, this controller against whatever remains
when the cluster is deleted. They cannot race on the same `DeleteDesire`
because revoke must finish (operation `Succeeded`) before the
cluster-deletion finalizer ever starts, and the cluster-deletion gate
only acts once `ClusterServiceDeletionTimestamp` is set — which only
happens after every in-flight operation against the cluster is
terminal.

### 7. `SystemAdminCredentialPostIssuanceCleanup` (eager teardown after Issued)

Once `Status.Phase` moves to `Issued` (or `Failed`),
`Status.SignedCertificate` plus `Spec.PrivateKeyPEM` have everything
`OperationResult` will ever need to assemble the kubeconfig. The
per-credential CSR / CSRA on the management cluster, and the desires
that put them there, are now dead weight — they consume MC k8s objects
that nothing reads, and they bloat the Cosmos partition.

This controller eagerly tears them down. It is **not** the
cluster-deletion gate (controller #6); it runs on live clusters as
part of the normal credential lifecycle. The cluster-deletion gate
exists for the residual case where #7 has not yet finished — or
will never run, e.g. the customer requested a credential and the
control plane was decommissioned before issuance.

Inputs: a `SystemAdminCredential` doc whose `Status.Phase` is one of
`{Issued, Failed}` and whose `Status.OutstandingDesires` is non-empty.

Logic, per credential:

1. For every entry in `Status.OutstandingDesires` whose `Kind` is
   `ApplyDesire` or `ReadDesire`:
   - If a matching `DeleteDesire` does not yet exist, create one with
     the same `TargetItem` as the ApplyDesire (for the Apply side) or
     do nothing for the Read side — ReadDesires can be deleted
     directly from Cosmos because they only mirror, they do not
     deliver content. Add the new DeleteDesire's `{Kind, Name}` to
     `OutstandingDesires`.
2. For every `DeleteDesire` entry, check its
   `Status.Conditions[type=Successful]`. When True:
   - Delete the matching `ApplyDesire` Cosmos document and remove its
     ref from `OutstandingDesires`.
   - Delete the `DeleteDesire` Cosmos document and remove its ref.
3. For every `ReadDesire` entry, delete the Cosmos document directly
   (a ReadDesire only mirrors observed state; the kube-applier does
   not need a DeleteDesire to stop reading) and remove its ref.
4. When `OutstandingDesires` is empty, this controller is a no-op for
   the credential. The credential's MC presence is now zero;
   `OperationResult` continues to serve the kubeconfig from Cosmos.

This controller uses the same
`internal/systemadmincredential.IssueDeleteAndAwait(...)` helper as
controllers #5 and #6 — the shared loop is "given an ApplyDesire ref,
drive it to gone and update OutstandingDesires."

Interaction with controller #5 (revoke):
- A credential in `Phase=Issued` that controller #7 has already cleaned
  up has an empty `OutstandingDesires` by the time revoke fires. The
  revoke flow (Phase R-2) sees nothing to tear down for that
  credential and immediately flips it to `Revoked`.
- A credential in `Phase=Requested` (never reached Issued) carries a
  populated `OutstandingDesires`. Controller #5's Phase R-2 walks it
  using the same teardown logic — the two controllers share the
  per-credential cleanup engine.

Interaction with controller #6 (cluster delete):
- In steady state, every Issued/Failed credential has already had its
  per-credential desires (CSR/CSRA/RBAC) cleaned by #7 by the time #6
  runs. #6's per-credential loop is then a no-op for those credentials
  and only needs to drive a straggler CRR (one that controller #5
  applied but didn't get all the way through Phase R-2).

### Frontend changes

`frontend.go:1012` (in `OperationResult`) stops calling
`f.clusterServiceClient.GetBreakGlassCredential` and instead:
- Looks up the `SystemAdminCredential` doc by `Operation.InternalID`.
- If `Status.SignedCertificate` is populated, assembles the kubeconfig
  in-process from `Status.SignedCertificate` + `Spec.PrivateKeyPEM` +
  the parent cluster's serving CA + the cluster's API URL. No
  management-cluster round trip; no ReadDesire dependency on the GET
  path. The assembly logic is a straight port of cluster-service's
  `GenerateKubeconfig`, pulled into a new helper under
  `internal/systemadmincredential/`.

The two action handlers (`ArmResourceActionRequestAdminCredential`,
`ArmResourceActionRevokeCredentials`) keep their current shape — they
still write the same Operation document, the same sentinel field on the
cluster. Only the downstream dispatcher behavior changes. This keeps the
ARM API surface byte-for-byte identical.

#### Where the parent cluster's CA and API URL come from

Both inputs must be present on the cluster doc by the time we serve
the kubeconfig. The kubeconfig assembly is a pure Cosmos read; it does
not call cluster-service and does not chase the management cluster.

**API URL.** Already on the cluster doc at
`HCPOpenShiftCluster.ServiceProviderProperties.API.URL`. Populated by
the existing `clusterPropertiesSyncer` from cluster-service today;
once the full cluster-service migration completes the same field will
be sourced from the kube-applier-mirrored HostedCluster. We do not
touch the field in this PR — we only read it.

**Serving CA bundle (new).** Today the CA is not stored on the cluster
doc; cluster-service generates the kubeconfig with a CA it pulls from
its own DB at GET time. We add a new field

```go
ServiceProviderClusterStatus.ServingCABundle string  // PEM, omitempty
```

(on `ServiceProviderCluster`, not on `HCPOpenShiftCluster` — the cluster
doc is reserved for fields the frontend's cluster CRUD reads/writes)
and populate it from a new long-lived per-cluster `ReadDesire` on the
HyperShift-managed serving CA Secret. The ReadDesire is created by a
dedicated cluster-watching controller (#10
`SystemAdminCredentialServingCAReadDesireCreator`) — only something
in this repo can create the kube-applier Cosmos doc. Its
`Spec.TargetItem` points at the kube-apiserver serving CA Secret
under the cluster's HCP namespace (concrete name + namespace TBD
during implementation — pin against the HyperShift version we target;
candidates are `kube-apiserver-server-ca`, `<hcp>-ca-bundle`, or
whatever HyperShift currently exposes in the published kubeconfig
Secret).

A sibling controller — `SystemAdminCredentialCABundleSync` (#8) —
watches the new ReadDesire, extracts the CA bytes from the mirrored
Secret, and writes them onto `ServiceProviderClusterStatus.ServingCABundle`.
It uses the same "if observed value differs from stored value,
Replace" pattern as `clusterPropertiesSyncer`. The cutover wiring step
already accounts for both controllers.

The CA bundle is stable over the cluster's lifetime in normal
operation. A rotation event would surface as a ReadDesire status
update and the sync controller would write the new bytes; in-flight
credentials issued before the rotation would still validate against
the *previous* CA until the customer requested a fresh credential.
That matches cluster-service's behavior today (cluster-service does
not pin a per-credential CA snapshot either).

We do **not** store the CA inside each `SystemAdminCredential` doc.
Storing it on the cluster doc keeps one copy, lets the existing
cluster-properties controllers own the field, and avoids fanout
rewrites if the bundle rotates.

### New helpers under `internal/systemadmincredential/` and `backend/pkg/maestrohelpers/`

We add (or extend) these for the controllers and frontend to share:

- `maestrohelpers.GetCachedCSRForSystemAdminCredential(ctx, readDesireLister, …) (*certificatesv1.CertificateSigningRequest, error)`
- `maestrohelpers.GetCachedCertificateRevocationRequestForCluster(ctx, readDesireLister, …) (*hypershiftcertv1alpha1.CertificateRevocationRequest, error)`
- `internal/systemadmincredential.GenerateKeypair() (publicPEM, privatePEM []byte, err error)` — pure RSA keygen; called by the dispatcher.
- `internal/systemadmincredential.BuildKubeconfig(cluster, signedCert, privateKeyPEM) ([]byte, error)` — pure function, no I/O.
- `internal/systemadmincredential.BuildCSR(owner *azcorearm.ResourceID, credName, username string, publicKeyPEM []byte) *certificatesv1.CertificateSigningRequest`
- `internal/systemadmincredential.BuildCSRA(owner *azcorearm.ResourceID, credName string) *hypershiftcertv1alpha1.CertificateSigningRequestApproval`
- `internal/systemadmincredential.BuildRevocationRequest(owner *azcorearm.ResourceID, revokeOpSuffix string) *hypershiftcertv1alpha1.CertificateRevocationRequest` — `revokeOpSuffix` is the 16-char form of the revoke operation's ID.
- `internal/systemadmincredential.BuildRBACGiveCSRPerm(owner *azcorearm.ResourceID, credName string) []client.Object` — `credName` is the credential's 16-char name; the helper returns a `ClusterRole` + `ClusterRoleBinding` pair named `system-admin-credential-give-csr-perm-<credName>`.
- `internal/systemadmincredential.BuildRBACCSRA(owner *azcorearm.ResourceID, credName string) []client.Object` — `Role` + `RoleBinding` named `system-admin-credential-csra-perm-<credName>`.
- `internal/systemadmincredential.BuildRBACRevocation(owner *azcorearm.ResourceID, credName string) []client.Object` — `Role` + `RoleBinding` named `system-admin-credential-revocation-perm-<credName>`.

Every `Build*` helper that returns an object that will end up inside
an `ApplyDesire.Spec.KubeContent` writes
`metadata.annotations["aro-hcp.openshift.io/owner"] = strings.ToLower(owner.String())`
on the object it returns. Callers cannot opt out — the parameter is
required and the helpers panic on nil. See the "Owner annotation"
subsection above.

For this plan the `owner` parameter is always the parent cluster's
ARM resource ID — every k8s object we write (per-credential CSR,
CSRA, RBAC bundles, and the per-revoke CRR) is cluster-scoped on the
MC, so its ARO-HCP owner is the cluster. The parameter is
`*azcorearm.ResourceID` rather than a string so a nodepool-scoped
future caller can pass a nodepool ID without a type cast.

There is intentionally no `BuildPrivateKeySecret` helper and no
`GetCachedPrivateKeyForSystemAdminCredential` helper. The private key
is read directly off `SystemAdminCredential.Spec.PrivateKeyPEM`.

The `Build*` helpers are unit-tested in isolation. The `GetCached*`
helpers are thin wrappers around the existing `ReadDesireLister.GetForCluster`
/ `GetForNodePool` machinery, exactly like `GetCachedHostedClusterForCluster`.

Note: the K8s objects these helpers build still use the HyperShift signer
name `customer-break-glass` — that string is HyperShift's contract with
its control-plane-pki-operator, not ours to rename. It only ever appears
as a string literal inside the `Build*` helpers; nothing in our type
graph, controller graph, or Cosmos schema carries it.

## Cutover strategy

Single PR. The PR:

1. Adds the `SystemAdminCredential` type, deepcopy, conversion, frontend
   admission, and validation.
2. Adds the nine new controllers above and wires them in
   `backend/pkg/app/backend.go`. Wires the cluster-deletion gate
   (controller 6) into the existing cluster-cleanup precondition stack
   so the cluster-deletion finalizer blocks until
   `ServiceProviderCluster.Status.SystemAdminCredentialContentDeleted=True`.
   Extends `createClusterScopedReadDesiresSyncer` to seed the new CA
   ReadDesire alongside the existing HostedCluster mirror. Wires the
   revoked-doc janitor (controller 9) on a 1-hour cadence per cluster.
3. Modifies the two frontend action handlers only to choose the new
   target field for `Operation.InternalID` (now a `SystemAdminCredential`
   resource ID, not a cluster-service HREF). The ARM contract is
   unchanged.
4. Modifies `OperationResult` to assemble the kubeconfig locally.
5. Deletes:
   - `dispatch_request_credential.go` / `_test.go`
   - `dispatch_revoke_credentials.go` / `_test.go`
   - `operation_request_credential.go` / `_test.go`
   - `operation_revoke_credentials.go` / `_test.go`
   - The four `BreakGlassCredential*` methods from
     `internal/ocm/client.go` and their mock fakes.
6. Updates the integration fixtures under `test-integration/admin/...`
   to round-trip via the new path. The existing fixture paths (e.g.
   `test-integration/admin/artifacts/AdminCRUD/HCP/breakglass/`) keep
   their on-disk names because they predate this work and the test
   discovery code reads them by path; rename in a follow-up if desired.

No feature flag. No dual-write. The change is structurally
incompatible — the same Operation document points at either a
cluster-service HREF (old) or a `SystemAdminCredential` resource ID
(new), and any in-flight operations at PR-merge time continue down
whichever path created them only if we keep both. We have explicitly
decided not to do that. See "Migration" below.

### Migration (the 24-hour drain)

At PR-merge time, every cluster-service-issued credential that is still
in-flight resolves on whichever side it started on:

- Old request flow: the dispatcher already wrote the cluster-service
  HREF into `Operation.InternalID`. Without the old poller these will
  hang. **We accept this** — the customer can retry, and on the new
  path it just works. The window is bounded by max credential TTL
  (24h). Add a *one-shot* short-lived cleanup that flips any
  `RequestCredential` operation whose `InternalID` starts with
  `/api/clusters_mgmt/v1/clusters/` to `Failed` so we surface the
  break clearly rather than silently hang.

- Old revoke flow: revokes are idempotent. If a customer's revoke is
  half-done at cutover, the new dispatcher will look at the cluster's
  `RevokeCredentialsOperationID` sentinel, see it's set, find no
  `SystemAdminCredential` rows in `AwaitingRevocation` (because the
  pre-cutover dispatch never wrote any), apply the CRR anyway, and
  drive the operation to completion. Make sure the dispatcher tolerates
  "found nothing to mark, write the CRR anyway."

Both edges are handled inside the new controllers; they do not require
a separate migration controller.

## Testing strategy

Each of the new controllers gets a table-driven test under
`backend/pkg/controllers/systemadmincredentialcontrollers/` using the same
`MockResourcesDBClient` + `SliceReadDesireLister` + `SliceApplyDesireLister`
+ `SliceDeleteDesireLister` pattern that the upgrade controllers use today.
Specifically:

- `OperationRequestCredentialDispatch`: cases for already-issued,
  duplicate dispatch, missing cluster, revoke-in-flight (must cancel).
- `SystemAdminCredentialIssuanceObserver`: cases for CSR not yet mirrored,
  CSR mirrored but no cert, CSR mirrored with cert, CSR denied, signer
  failure.
- `OperationRequestCredentialPoll`: each Phase → ARM state mapping.
- `OperationRevokeCredentialsDispatch`: no credentials present
  (cutover edge), multiple credentials, sentinel-mismatch.
- `OperationRevokeCredentialsPoll`: CRR not yet mirrored, CRR
  PreviousCertificatesRevoked=True, CRR failure mode.
- `SystemAdminCredentialClusterDeletionCleanup`: no credential desires
  (already clean — must report ready), some ApplyDesires still present
  with no DeleteDesire yet (must create them), DeleteDesires pending
  (must wait), DeleteDesires all Successful (must delete Cosmos docs
  and flip the ServiceProviderCluster condition). Include a test that
  the controller is idempotent across restart by running `SyncOnce`
  twice in sequence with a partial state in between.
- `SystemAdminCredentialPostIssuanceCleanup`: credential in
  `Phase=Issued` with non-empty `OutstandingDesires` (must drive
  teardown); credential in `Phase=Issued` with empty
  `OutstandingDesires` (must no-op); credential in `Phase=Requested`
  (must no-op — the issuance observer has not flipped Phase yet);
  credential in `Phase=Failed` (must drive teardown). Include a test
  that the `OutstandingDesires` list ends empty after a full sweep and
  that no live `ApplyDesire` / `ReadDesire` / `DeleteDesire` Cosmos doc
  remains for the credential.
- `SystemAdminCredentialCABundleSync`: ReadDesire absent (no-op);
  ReadDesire present but `KubeContent` unobserved (no-op); ReadDesire
  observed with a CA bundle matching the SPC's stored value
  (no Replace expected); ReadDesire observed with a different CA
  bundle (must write the new bytes onto
  `ServiceProviderClusterStatus.ServingCABundle`); malformed Secret
  (must not crash, must surface via a Condition or log without
  rewriting good state).
- `SystemAdminCredentialRevokedGC`: `Phase=Revoked` doc with
  `RevokedAt + 48h > now` (must be kept); doc with
  `RevokedAt + 48h <= now` (must be deleted); `Phase=Revoked` doc
  with `RevokedAt` unset (must be kept — defensive: never delete a
  doc we cannot age); non-`Revoked` docs (must be ignored
  regardless of `RevokedAt`).
- `BuildKubeconfig`: round-trip against a known cert/key pair, assert
  shape matches what cluster-service's `GenerateKubeconfig` emits today
  (golden-file diff vs. a captured cluster-service output).
- `GenerateKeypair`: smoke-test that the PEM encoding round-trips through
  `BuildCSR` → x509 parse → `BuildKubeconfig` cleanly. We do not assert
  randomness; the standard library handles that.

Frontend `OperationResult`: extend the existing
`test-integration/admin/artifacts/AdminCRUD/HCP/breakglass/...` fixtures
to drive the new flow end-to-end against the mock kube-applier informer.

Where possible, capture real Cosmos docs from a live `cspr` cluster
running the new code (the same "snapshot a real cluster" trick we used
for `TestNodePoolActiveVersionSyncer_RealCosmosFixture`) and embed them
as artifact fixtures.

## Open questions to settle before implementation

1. **Where does the credential doc live in the resource ID tree?**
   **Resolved**: `…/clusters/<cluster>/systemAdminCredentials/<name>`.
   This keeps cleanup tied to cluster deletion and makes the resource
   type indexable by the existing cluster informer; the alternative
   (nesting under `ServiceProviderCluster`) was rejected.

2. **Credential name choice.** **Resolved**: generate a 16-character
   random suffix from a UUIDv4 at dispatch time —
   `strings.ReplaceAll(uuid.New().String(), "-", "")[:16]` — and use
   that as the credential name. Idempotency on the dispatch side is
   keyed on `Operation.OperationID` (the dispatcher checks whether a
   `SystemAdminCredential` already exists with
   `Spec.OperationID == Operation.OperationID` before creating a new
   one), not on the name itself. 16 hex chars = 64 bits of entropy;
   collisions inside a single cluster's `systemAdminCredentials`
   namespace are negligible.

3. **GC policy for `Phase=Revoked` docs.** **Resolved**: delete the
   `SystemAdminCredential` Cosmos document **48 hours** after it
   enters `Phase=Revoked`. 48h is deliberately longer than the
   certificate's 24h TTL, so by the time the row is gone the cert it
   tracked is guaranteed already expired — a leaked Cosmos read of a
   stale `Phase=Revoked` row cannot recover a still-valid kubeconfig.
   We record the revocation time at the same point we flip the phase
   (controller #5's Phase R-2), and a small janitor controller —
   `SystemAdminCredentialRevokedGC` — sweeps each per-cluster bucket
   once per hour, deleting any doc whose `Status.RevokedAt + 48h <= now`.
   Add a `RevokedAt metav1.Time` field on `SystemAdminCredentialStatus`
   for this. (This is a 9th controller; the cutover section already
   reflects it.)

4. **Do we need a separate `SystemAdminCredentialIssuanceObserver`, or can
   the `OperationRequestCredentialPoll` do double duty?** **Resolved**:
   keep the separation. The issuance signal is event-driven off a
   ReadDesire informer; the operation poll is a status mapper. Different
   triggering cadences, different inputs. Collapsing them would couple
   two unrelated state machines into one reconcile and obscure the test
   surface.

5. **RBAC bundles**: **Resolved**: per-credential, not cluster-scoped.
   The dispatcher (controller 1) creates the three RBAC bundle
   ApplyDesires alongside the CSR and CSRA, with k8s object names
   suffixed by the credential's 16-char `<credName>`. Cleanup falls
   out of the per-credential teardown path
   (`OutstandingDesires` walk in controllers #5 and #7) with no
   special handling. `createClusterScopedReadDesiresSyncer` does not
   seed RBAC.

6. **Tracing & metrics.** **Resolved**: we don't need a dedicated
   per-controller Prometheus counter set. Dashboards can be built from
   the existing Cosmos object counts per resource type — the
   `SystemAdminCredential` doc count, broken down by `Status.Phase`,
   carries the same information cluster-service's status-tagged
   counter did. Dashboard owner only needs the new resource type
   added to the existing object-count pipeline.

7. **Envelope encryption of `Spec.PrivateKeyPEM`.** **Resolved**: Cosmos
   at-rest encryption is sufficient. We do not layer additional
   envelope encryption (no Key Vault-managed KEK wrap). The private
   key sits in `Spec.PrivateKeyPEM` in plaintext at rest from Cosmos's
   perspective and is protected at the storage layer the same way
   every other ARO-HCP per-cluster document is. `BuildKubeconfig`
   reads it directly. If a future threat model demands envelope
   encryption we can layer it inside `BuildKubeconfig` and the
   dispatcher without disturbing the controller graph.

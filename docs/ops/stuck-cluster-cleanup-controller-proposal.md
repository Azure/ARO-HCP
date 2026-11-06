# Stuck HostedCluster Cleanup: NamespaceDeletion

**Status:** Proposed design for the next revision of PR #7762; implementation and architecture review remain pending.
**Tracking:** [AROSLSRE-2477](https://redhat.atlassian.net/browse/AROSLSRE-2477).

**Revision note (this pass):** scope was narrowed by requester decision and the design
collapsed accordingly. The default path never initiates a deletion — it only pushes an
already-wedged one over the line. Active namespaces are out of scope, and the single new
mutation on that path is clearing a namespace's own `kubernetes` finalizer.

One addition in this revision: HostedCluster deletion is available, but **only** when the
request explicitly opts in via `deleteHostedCluster`. Absent that field the behaviour is
exactly as described above and a HostedCluster that is not already deleting is refused.
This reverses the previous revision's "no HostedCluster deletion" decision in a bounded
way — the authorization is recorded per request rather than implied by which controller
version is deployed. See [HostedCluster deletion](#hostedcluster-deletion-opt-in).

Claims below are grounded in named files; what remains unproven is marked as an open
question rather than asserted.

## Problem and scope

A fleet investigation on 19 October 2026 found 20 HostedCluster-related namespaces stuck
in `Terminating`. Sixteen had no matching customer cluster record in the available Cosmos
snapshots, leaving no cluster document to drive normal deletion. The SRE supplies the
missing trigger through the admin API: a management cluster, an exact namespace, and an
exact HostedCluster name. Kusto helps SRE identify candidates, but the backend makes its
safety decision from live Cosmos and Kubernetes observations.

### Why the trigger is an operator action, not automated detection

Review raised an alternative worth answering directly: the evidence of a stuck namespace
lives on the management cluster, so mgmt-agent could detect these namespaces, report them
via a CR, and have fleet or backend turn that into the document driving this controller —
removing the human from the loop.

That is a better *detection* mechanism and this design does not preclude it. The reason it
is not the v1 entry point is that detection is not the hard part. A namespace that is
`Terminating`, unowned and dwelled is cheap to find; what the design spends its effort on
is deciding whether clearing its finalizer is safe, and that decision currently rests on
an assumption the design states but cannot verify — that Azure was already reclaimed (see
[The Azure assumption](#the-azure-assumption-and-its-accepted-risk)). Automating the
trigger would mean automating acceptance of that risk across every namespace that matches,
at machine speed, with no named operator and no `requestReason` attached to the audit
record.

The capability is also expected to be rare and finite: 20 namespaces from a historical
population, most of them created by routes that no longer exist. Building a detection
pipeline for a backlog is the wrong shape of investment, and a standing detector for a
condition that should stop occurring is a liability.

**Where this becomes right:** once the Azure assumption is replaced by a positive check —
or once the opt-in path (which does not rely on the assumption) is the normal case — the
decision is mechanical and automating the trigger is a natural follow-up. The resource
shape is already compatible: a mgmt-agent-driven CR would create exactly the same
`NamespaceDeletion` document an SRE creates, and every gate in this design would still
run. Nothing here has to be redesigned for that; `requestReason` would simply name the
detector instead of a person.

### Operating principle

**By default this API does not start deletions. It finishes them.**

Every admission rule below follows from that one sentence. A namespace that is not
already `Terminating` is not this API's problem. A HostedCluster that has not already
been asked to delete is not this API's problem either, *unless the request explicitly
asks for it to be deleted*. Acting on them otherwise would mean originating a destructive
operation that no customer or controller requested — a materially different and riskier
capability than unblocking one that is already underway and stuck.

The `deleteHostedCluster` opt-in is how that difference is made explicit rather than
erased. A request without it cannot originate anything; a request with it is an operator
statement, recorded durably, that originating this particular deletion is intended. The
namespace rules are unaffected: an `Active` namespace is still always out of scope, since
no field opts into starting a namespace deletion.

The normal backend deletion chain deletes the HostedCluster before its two namespaces
(`backend/pkg/controllers/clusterresources/apply_desire_removal_chain.go`, the ordered
`applyDesireRemovalChain`: `hostedClusterRemovalStep` precedes `namespacesRemovalStep`).
This proposal does not reproduce that chain; it picks up where the chain has already run
and stalled, or where the chain never existed to run.

## Admission rules

Each table below lists the **blocking observation** and what it produces. A request that
matches no row proceeds. Every rejection names the observed value and the operator's next
step.

The split between the two tables is architectural, not stylistic. **The admin handler
cannot read Kubernetes.** Per the boundary in `CLAUDE.md`, nothing on the API side talks to
a management cluster directly; the only sanctioned path to management-cluster state is a
ReadDesire mirrored through kube-applier into Cosmos, and no such observation exists until
*after* the request has been created. So POST can check everything that is answerable from
Cosmos — request shape, namespace grammar, the denylist, and ownership — while every check
that needs Kubernetes truth (namespace phase, HostedCluster identity, UIDs) necessarily
happens later, in the controller. An accepted POST is therefore a recorded request, never a
statement that the cleanup is safe.

The second table is evaluated by a new backend controller, `NamespaceDeletion`
(package `backend/pkg/controllers/namespacedeletion/`, constant
`NamespaceDeletionControllerName = "NamespaceDeletion"`). Per the repository convention
that one constant is the sole source for the struct's `name` field, the workqueue `Name`
that surfaces as a Prometheus label, `utils.ContextWithControllerName`, and
`utils.LogValues{}.AddControllerName`.

**At POST, before anything is stored:**

| Blocking observation | Response | Why |
| --- | --- | --- |
| Namespace name does not match the supported grammar for the supplied `hostedClusterName` | `400 InvalidRequestContent` | The request cannot be mapped to a HostedCluster or control plane namespace role. |
| Namespace is on the protected-namespace denylist | `400 ProtectedNamespace` | Platform namespaces are never valid targets, whatever their state. |
| A live Cosmos record owns the namespace or the HostedCluster | `409 ClusterRecordPresent` | The cluster still exists. Use the normal deletion flow; this API must not touch it. |
| A foreign ApplyDesire still maintains the namespace or the HostedCluster | `409 ActiveDesirePresent` | Another controller is actively asserting this object should exist. Resolve that owner first. |
| The ownership query failed, timed out, or could not read every page | `503 OwnershipCheckUnavailable` | A failed lookup is never evidence of absence. Retry once the database is healthy. |
| Records exist but their identity or placement is contradictory | `409 OwnershipIndeterminate` | Ownership could not be decided either way; investigate before resubmitting. |

**After acceptance, during reconciliation:**

| Blocking observation | Outcome | Why |
| --- | --- | --- |
| Namespace is `Active` — no `deletionTimestamp` | `Stopped`, reason `NamespaceNotTerminating` | This API finishes deletions; it does not start them. |
| Namespace is `Terminating` but the dwell period has not elapsed | `Complete=False`, reason `DwellNotElapsed` | **Retryable, not a stop.** The deletion may still be making normal progress. |
| A HostedCluster in the related namespace has **no** `deletionTimestamp`, and `deleteHostedCluster` is **not** set | `Stopped`, reason `HostedClusterNotDeleting` | HyperShift's finalizer never started, so its Azure infrastructure is provably still live, and nothing authorized originating a deletion. |
| A HostedCluster in the related namespace has **no** `deletionTimestamp`, and `deleteHostedCluster` **is** set | Issue `Type=Delete`; `Complete=False`, reason `DeletingHostedCluster` | The operator authorized originating this deletion. The finalize waits for the HostedCluster to be observed absent — see [HostedCluster deletion](#hostedcluster-deletion-opt-in). |
| A HostedCluster in the related namespace looks **live** (available / completed version / endpoint), whatever `deleteHostedCluster` says | `Stopped`, reason `HostedClusterLive` | A working cluster is never a cleanup target. This gate cannot be overridden by any request field. |
| `deleteHostedCluster` was set, the delete was issued, and the removal timeout elapsed | `Stopped`, reason `HostedClusterRemovalTimedOut` | HyperShift did not complete. Azure was live when the attempt began, so finalizing now would strand it. Back to SRE. |
| The observed HostedCluster name differs from `hostedClusterName` | `Stopped`, reason `HostedClusterIdentityMismatch` | The request probably names the wrong cluster. |
| The namespace UID differs from the pinned value | `Stopped`, reason `NamespaceUIDChanged` | The namespace was replaced after binding; this attempt is void. |
| A Cosmos owner appears after acceptance | `Stopped`, reason `ClusterRecordAppeared` | A cluster now claims this namespace. Use normal deletion. |
| A Cosmos or Kubernetes read fails | `Degraded=True`, retried indefinitely with backoff | Never treated as absence, and never advances a mutation. |

`ClusterRecordAppeared` is not primarily about a new cluster seizing the namespace — that
is near-impossible, since the name embeds a 32-character Cluster Service cluster ID. The
realistic case is a record that existed at POST but did not yet *match*:
`Status.HostedClusterNamespace` is populated by `ServiceProviderClusterPropertiesSync`, so
a record caught mid-population matches nothing on the first query and matches on a later
one. A restore or migration has the same effect. The check is cheap and it is the only
thing between a partially written record and an irreversible mutation.

`HostedClusterNotDeleting` is the important one and remains a deliberate refusal, not a
gap, for any request that does not opt in. A HostedCluster with no `deletionTimestamp`
means HyperShift's finalizer never started, so its Azure infrastructure is provably still
live. That namespace goes back to SRE for manual investigation; see
[the Azure assumption](#the-azure-assumption-and-its-accepted-risk). With
`deleteHostedCluster` set, the same observation instead becomes the trigger for the
opt-in path below rather than a stop.

### HostedCluster deletion (opt-in)

`deleteHostedCluster` is a boolean on the request, immutable after creation and defaulting
to false. It exists for exactly one observation: **a HostedCluster that has no
`deletionTimestamp`.** When one is already deleting, issuing a delete is a no-op, so the
field changes nothing on the majority path; it is not a general "also delete things" flag.

When it is set and that observation holds, the controller issues an
`ApplyDesire Type=Delete` for the HostedCluster. This reuses the existing union member
(`kubeapplierapi.ApplyDesireTypeDelete`, `internal/api/kubeapplierapi/types_apply_desire.go`)
and the existing kube-applier behaviour — the same mechanism `hostedClusterRemovalStep`
uses in the normal chain, which "issues the delete and waits out its finalizers" and
reports `SuccessfullyDeleted`. No new union member is required.

**The finalize waits for the HostedCluster to be observed absent.** This is the one place
in the design where one action waits on another, and the exception is narrow and
justified:

- It is a **within-request** wait on an observation, not a dependency between the two
  namespace requests. Requests remain independent; GET on one still says nothing about
  the other.
- It **cannot deadlock**, unlike the sequencing rejected in
  [The two requests are independent](#the-two-requests-are-independent--there-is-no-sequencing).
  That argument turned on nothing ever deleting the HostedCluster, so "HostedCluster gone"
  was unreachable. Here the request issues the delete itself, so the condition is reachable
  by construction.
- Racing them instead would be the **most destructive thing this API could do.** On this
  path Azure is provably live. Finalizing the namespace while HyperShift is deprovisioning
  destroys the control plane's etcd and kube-apiserver mid-flight and strands the entire
  Azure footprint — manufacturing precisely the orphan this capability exists to clean up,
  at a larger scale than the orphan it was asked to remove.

The wait is bounded by `hostedClusterRemovalTimeout`, a backend flag with the same
discipline as the dwell: it travels to kube-applier on the desire and is re-checked there,
so a request cannot shorten its own safety margin. **On expiry the request stops**
(`HostedClusterRemovalTimedOut`) and does **not** fall back to finalizing. That is
deliberate and differs from the default path: there, dwell has already established that
Azure is beyond saving, whereas here Azure was live when the attempt began and HyperShift
merely failed to finish. Falling back would convert a recoverable situation into a
guaranteed leak. Such a request returns to SRE exactly as `HostedClusterNotDeleting` does
today.

Opting in does not weaken any other gate. The protected-namespace denylist, the Cosmos
ownership gate, the namespace grammar, the UID pin and the dwell all apply unchanged and
all run *before* the delete, and the live ownership query runs immediately before the
HostedCluster delete as well as before the finalize — a delete is at least as irreversible
as a finalize.

#### The liveness gate

One gate exists only on this path, and it is the one that keeps the opt-in from being able
to destroy a working cluster.

Without the opt-in, "HostedCluster has no `deletionTimestamp`" is itself the refusal, so a
live cluster is safe by construction. The opt-in converts that refusal into an action, and
therefore removes the very check that made a live cluster safe. Something has to replace
it, because the Cosmos ownership gate cannot: a live cluster normally *does* have a
ServiceProviderCluster record and would be rejected at POST — but this API exists precisely
for the case where Cosmos records are missing or stale, so "no record" must never be read
as "no cluster."

**Before issuing the delete, the controller inspects the observed HostedCluster and refuses
if it looks alive.** The ReadDesire already mirrors the whole object into
`Status.KubeContent`, so this needs no new plumbing. Treat as live, and stop with
`HostedClusterLive`:

- `status.conditions` contains `Available=True` — the constant is
  `hyperv1.HostedClusterAvailable` (`"Available"`,
  `vendor/github.com/openshift/hypershift/api/hypershift/v1beta1/hostedcluster_conditions.go`),
  documented there as indicating a healthy control plane. This is the clearest single
  signal;
- `status.version.history` contains an entry in state `Completed` — `History` is
  `[]configv1.UpdateHistory` on `ClusterVersionStatus`
  (`.../v1beta1/hostedcluster_types.go`), whose own comment states that completed rollouts
  are recorded as `Completed` and partial ones as `Partial`. A completed entry means the
  control plane reached a working version and nothing has torn it down;
- `status.controlPlaneEndpoint` is non-empty (`APIEndpoint`, same file).

All three fields exist in the vendored HyperShift API at the revision pinned across this
repo's modules (`github.com/openshift/hypershift/api v0.0.0-20261002220447-d1eba2a7a57c`),
so the gate needs no dependency change. Read them out of the ReadDesire's `KubeContent`
rather than typing the whole HostedCluster if that keeps the backend's dependency surface
smaller.

Absence of these is not proof of death, and it does not need to be — it only has to be
true that *presence* of them blocks. The gate is deliberately one-directional: it can only
ever refuse, never authorize. A HostedCluster whose status is empty, stale or unreadable
is not treated as live, but it is also not treated as confirmed-dead; the dwell and the
operator's explicit opt-in carry that decision, exactly as they do on the default path.

`HostedClusterLive` is a latched `Stopped` reason and is not overridable by resubmitting
with the same inputs. Correcting it means establishing why a namespace belonging to a
live cluster was deleted in the first place — which is an incident, not a cleanup.

### Dwell

**Dwell is how long the namespace has already been stuck in `Terminating`** — the elapsed
time from its `metadata.deletionTimestamp` to now. It is not a delay this controller
imposes and not a timeout; it is an age measured on the namespace itself, so a namespace
that has been Terminating for three weeks satisfies it the moment the request is created.

It exists to separate **"this deletion is wedged"** from **"this deletion is still
working."** A normal HostedCluster teardown takes minutes, because HyperShift's finalizer
deprovisions Azure infrastructure before the namespace cascade can complete. Clearing the
namespace finalizer 30 seconds into a perfectly healthy teardown would cut that
deprovisioning off mid-flight and strand the Azure resources — manufacturing exactly the
orphan this capability exists to clean up. Requiring the namespace to have sat in
`Terminating` for a while is what makes "wedged" a defensible conclusion rather than a
guess.

It is also the load-bearing half of the Azure assumption: see
[The Azure assumption](#the-azure-assumption-and-its-accepted-risk). A namespace Terminating
for weeks means HyperShift either finished its Azure work or has been failing continuously
and will not recover. Waiting longer does not change the outcome.

**One hour is a chosen initial policy, not evidence of safety.** It is the first commit's
`minimumTerminatingDwell`, and it is far shorter than the real orphans' age; it is set to
be comfortably longer than a healthy teardown, not tuned to the observed population.

**It must be a backend flag, not a constant.** Every end-to-end test gates on it, and a
one-hour wait per iteration makes personal-dev verification impractical. The value travels
to kube-applier on the finalize desire as `MinimumDwell` and is re-checked there against
the live namespace immediately before the mutation, so a request cannot shorten its own
safety margin — only the operator deploying the backend can.

## What this API cannot do

Two invariants govern this design. They are stated here as testable claims rather than
left implicit, because they are the basis on which the capability is acceptable at all.

**Invariant 1 — it cannot delete a healthy namespace.** Not "is unlikely to"; it
structurally cannot. Every path requires `metadata.deletionTimestamp` to be set on the
target namespace, which means *someone else already issued the delete*. A healthy
namespace is `Active`, and an `Active` namespace is refused with `NamespaceNotTerminating`
with no field able to override it — the opt-in covers HostedClusters only, and there is
deliberately no equivalent for namespaces. The API has no `DELETE` verb for namespaces at
all: the only namespace mutation is clearing a `kubernetes` finalizer on an object that is
already being deleted. Beyond that, the protected-namespace denylist is evaluated first
and independently of the grammar, the dwell requires the deletion to have been stuck for a
configured period, the UID is pinned so a replaced namespace voids the attempt, and
kube-applier re-checks the UID, the dwell and the finalizer set against the live API
immediately before acting.

**Invariant 2 — it cannot delete a healthy cluster.** This one is enforced, not
structural, and the distinction matters:

| Path | What prevents it |
| --- | --- |
| Default (no opt-in) | Structural. A HostedCluster without a `deletionTimestamp` is the refusal `HostedClusterNotDeleting`. Nothing can proceed past a live cluster. |
| Opt-in (`deleteHostedCluster`) | Enforced by three independent gates: the Cosmos ownership gate, the [liveness gate](#the-liveness-gate), and explicit per-request operator authorization that cannot be added to an existing request. |

The honest statement of residual risk: **the opt-in is the only part of this design capable
of destroying a working cluster.** It requires a stale or missing Cosmos record, a
HostedCluster whose status does not look live, a namespace already deleted by someone else
and stuck past the dwell, *and* an operator who set the field — simultaneously. The
default path has no such combination available to it at any setting.

**What it does not touch.** `ApplyDesireSpec` is unmodified and `evaluateDelete` is reused
as-is, so `applyDesireRemovalChain` and the normal cluster deletion path are untouched by
this change. The new `FinalizeNamespace` member is additive to the `ApplyDesireType`
discriminator, and the existing switch in `kube-applier/pkg/controllers/apply_desire/controller.go`
has a `default` branch that reports `unknown desire type` as a pre-check error rather than
falling through to an action — so a kube-applier that has not yet been rolled out responds
to a `FinalizeNamespace` desire by going Degraded and mutating nothing. Version skew during
rollout is therefore fail-safe in the correct direction, and the backend may be deployed
first.

Two consequences of being additive that must still be handled in code:
`recordApplyDesireOperation` (`kube-applier/pkg/app/desire_operation_metrics.go`) currently
labels anything that is not `Type=Delete` as an *apply*, so a `FinalizeNamespace` desire
would be silently mis-labelled in metrics unless that function is extended — which would
falsify this design's claim that the distinct type keeps the operation unconfusable in
metrics. And the `namespaces/finalize` RBAC grant is cluster-wide for kube-applier as a
whole; nothing in RBAC scopes it to these namespaces, so the entire restriction lives in
the desire content and its preconditions. That is the same trust model as kube-applier's
existing `delete` on namespaces, but it should be an explicit review item rather than an
assumed one.

## What this capability fixes

Two Kubernetes facts shape the whole design:

- A `DELETE` on a namespace that is already `Terminating` succeeds and changes nothing.
  There is no second, stronger delete. This is why v1 issues no namespace `DELETE` at all.
- A namespace stays in `Terminating` only because the namespace controller in
  kube-controller-manager cannot finish. It has already issued deletes for all content it
  could discover, and reports why it is stuck in `status.conditions`:
  `NamespaceDeletionDiscoveryFailure`, `NamespaceDeletionContentFailure`,
  `NamespaceContentRemaining`, `NamespaceFinalizersRemaining`.

Clearing the namespace's own `kubernetes` finalizer is therefore the only action that
changes anything *about a namespace* — the opt-in HostedCluster delete is the one other
mutation this design issues, and it acts on a different object. Because clearing the
finalizer bypasses the namespace controller entirely, it works regardless of which of
those conditions is set:

| Observed state of a Terminating, unowned, dwelled namespace | v1 action | Unsticks it? |
| --- | --- | --- |
| Empty; namespace controller wedged or discovery failing | clear `kubernetes` finalizer | **Yes** |
| Content remaining behind resource finalizers | clear `kubernetes` finalizer | **Yes** — see the etcd-orphan tradeoff below |
| HostedCluster present with `deletionTimestamp`, HyperShift finalizer wedged | clear `kubernetes` finalizer | **Yes** — this is the expected majority case |
| HostedCluster present with **no** `deletionTimestamp`, no opt-in | reject, `HostedClusterNotDeleting` | n/a — nothing authorized originating a deletion |
| HostedCluster present with **no** `deletionTimestamp`, `deleteHostedCluster` set | delete the HostedCluster, wait for its absence, then clear `kubernetes` finalizer | **Yes**, if HyperShift completes; otherwise `HostedClusterRemovalTimedOut` and no finalize |

**The etcd-orphan tradeoff.** Clearing `spec.finalizers` via the `/finalize` subresource
deletes the namespace object. Content still inside is *not* deleted — it lingers in etcd
keyed under a namespace that no longer exists. Those objects are inert (their controllers
will not act on a namespace-less object) and invisible to most tooling, and they resurface
if the namespace name is ever recreated. v1 accepts this in exchange for not needing an
allowlist of resource finalizers, per-kind `patch` grants for CAPI/CAPZ, or a proof that
the namespace is empty. The audit record below is what makes it traceable.

This replaces an earlier draft requirement to prove the namespace empty before finalizing.
That requirement needed `list` on effectively every namespaced kind, and it was
self-defeating: the most likely reason an empty namespace is wedged is a discovery
failure, which is exactly the condition under which the proof cannot be completed. The
Cosmos ownership gate, not an emptiness proof, is what authorizes the mutation.

## The Azure assumption and its accepted risk

**v1 assumes that a namespace which is `Terminating` and has no live Cosmos owner has
already had its Azure resources reclaimed.** This is a requester decision and it is
load-bearing. It should be reviewed on its merits, not assumed safe.

Where it is sound: in the normal deletion path the assumption is not merely true but
tautological. `hostedClusterRemovalStep` completes — meaning HyperShift's finalizer ran
and deprovisioned Azure — before `namespacesRemovalStep` issues the namespace delete; and
`clusterDeletionController.SyncOnce`
(`backend/pkg/controllers/cluster/deletion/cluster_deletion_controller.go`) removes the
Cluster document only after `deletePreconditionAllApplyDesiresGone`, which requires the
namespace delete desire to have reported `SuccessfullyDeleted`. So "no Cosmos record while
the namespace is still Terminating" is **unreachable** through the normal path.

Where it is unverified: every namespace in this state therefore got there abnormally —
Cosmos documents removed out of band, teardown that predates the current chain, or an
incomplete snapshot in the investigation. None of those routes guarantees Azure ran.

What actually carries the assumption is the **dwell**, not the Cosmos check. A namespace
that has been `Terminating` for weeks means HyperShift's finalizer either completed its
Azure work or has been failing continuously and will not recover. Waiting longer does not
change the outcome.

The one state that would falsify the assumption outright — a HostedCluster whose finalizer
never started, so Azure is fully live — is **rejected at reconciliation**
(`HostedClusterNotDeleting`) rather than acted on, unless the request opted in.

**What the opt-in changes, and what it does not.** On the opt-in path the assumption is
not relied on at all: rather than assuming Azure was already reclaimed, the request causes
a real deprovisioning attempt and refuses to finalize until it succeeds. That path is
therefore *stricter* than the default, not a relaxation — the timeout stops the request
instead of proceeding. The accepted risk below is unchanged, because it belongs to the
default path, where the HostedCluster is already deleting and opting in would be a no-op.
Opting in buys nothing there, and the field should not be set out of habit.

**Accepted residual risk, stated plainly:** in the expected majority case a HostedCluster
is present *with* a `deletionTimestamp` and a wedged HyperShift finalizer. Clearing the
namespace finalizer abandons that deprovisioning mid-flight. Azure resources belonging to
that control plane may survive, unreferenced, and will block later resource-group cleanup.
v1 accepts this and compensates with the audit record rather than with an Azure API check.

**Audit record.** Before finalizing, the request must durably persist: the namespace UID
and `deletionTimestamp`; the namespace's `status.conditions` verbatim; the observed
HostedCluster name, UID, `deletionTimestamp` and remaining `metadata.finalizers` if one is
present; and the finalizer strings removed. This is the only artifact an SRE will have if
the assumption turns out wrong for a given cluster, so it is a requirement, not telemetry.

## Contract

Create one request per target namespace. The management cluster comes from the URL.
`default` is the Fleet singleton name within a stamp
(`fleetapi.ManagementClusterResourceName`, `internal/api/fleetapi/registry.go`); it is not
a customer cluster name or an AKS cluster name. The route carries a
`{managementClusterName}` segment for consistency with its sibling stamp routes, and the
handler rejects anything but the singleton, matching the existing
`validateManagementClusterName` in `admin/server/handlers/stamp/`.

```http
POST /admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/namespacedeletions
Content-Type: application/json

{
  "namespaceName": "<exact target namespace>",
  "hostedClusterName": "<exact HostedCluster metadata.name>",
  "requestReason": "AROSLSRE-2477",
  "deleteHostedCluster": false
}
```

`deleteHostedCluster` is optional, defaults to false, and is immutable after creation —
including across an explicit restart, which may correct only `hostedClusterName`. Changing
it requires a new request, so an authorization to originate a deletion can never be added
to an attempt that was accepted without one. It has no effect unless the observed
HostedCluster lacks a `deletionTimestamp`; see
[HostedCluster deletion](#hostedcluster-deletion-opt-in).

The resource name is exactly `namespaceName`. GET uses
`/admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/namespacedeletions/{namespaceName}`
and returns the request's conditions. This replaces the first commit's
`StuckClusterCleanupNameFor` hash (`ns-<16 hex>`): a Kubernetes namespace name is at most
63 characters of `[a-z0-9-]`, which is a valid single ARM path segment and a valid Cosmos
resource name, so the hash buys nothing and costs readability. Validation enforces that
bound explicitly rather than relying on it.

`hostedClusterName` does **not** by itself drive a deletion — only `deleteHostedCluster`
does that, and only for a HostedCluster that is not already deleting. Its two standing
purposes are deriving and validating the control-plane namespace name, and confirming that
the HostedCluster observed in the related namespace is the one the SRE believes is there.
Both matter — a mistyped name is the main way a request lands on the wrong namespace, and
with the opt-in set it is also the difference between deleting the intended HostedCluster
and deleting someone else's.

The Fleet resource ID identifies the management cluster and request, so there is no
customer cluster ID or customer ARM ID in the request body. Wildcard targets are invalid.
The admin API writes the desire and returns promptly; the backend and kube-applier do the
asynchronous work.

The POST example intentionally uses placeholders. The exact HostedCluster `metadata.name`
must be verified before creating a real request; a customer ARM cluster name is not
automatically the HostedCluster name.

For example, if `ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` is a HostedCluster
namespace and `ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-hcp1` is its control plane
namespace, SRE sends one POST for each namespace that is stuck, with
`hostedClusterName: "hcp1"`. The example ID and name are illustrative, not a request to
delete an actual cluster.

### The two requests are independent — there is no sequencing

**Nothing waits on anything.** The two requests share no state, run in either order or
simultaneously, and neither blocks on the other's completion. This is a change from the
original draft and it follows directly from the narrowed scope, so it is worth stating why
rather than asserting it.

Ordering existed to protect HyperShift. While the design still deleted the HostedCluster,
the namespaces had to go last, because pulling a namespace out from under an in-flight
finalizer is what strands Azure resources and wedges namespaces in the first place — the
same reason `applyDesireRemovalChain` puts `namespacesRemovalStep` after
`hostedClusterRemovalStep` in the normal path.

On the default path no HostedCluster is deleted, and the dwell plus
[the Azure assumption](#the-azure-assumption-and-its-accepted-risk) have already concluded
that HyperShift's finalizer will not complete. There is nothing left for an ordering rule
to protect. The two orders are also equivalent in effect: whichever namespace is finalized
first, the HostedCluster CR is orphaned in etcd and HyperShift stops acting on it. No order
preserves a path to a graceful finish, because that path was given up with the Azure
assumption — not by the choice of sequence.

This reasoning is exactly what the opt-in inverts, and why that path needs a wait: there,
HyperShift has *not* been written off, so the order does protect something.

**On the default path, waiting for the HostedCluster to disappear would deadlock.** This is
the decisive argument, and it is easy to miss: without the opt-in, nothing ever removes the
HostedCluster. The controller does not delete it, HyperShift's finalizer is wedged by
definition — that is why the namespace is stuck — and clearing a namespace's `kubernetes`
finalizer orphans the contained objects rather than deleting them. "HostedCluster is gone"
is therefore a condition that is never satisfied, and a control plane request that waited
on it would hang indefinitely.

Cross-request sequencing and "this API never initiates a deletion" are mutually exclusive.
For `HostedCluster gone` to become reachable, something must delete the HostedCluster and
HyperShift must finish.

**That is exactly, and only, what the opt-in path does.** It makes the condition reachable
within a single request by issuing the delete itself, which is why
[HostedCluster deletion](#hostedcluster-deletion-opt-in) can wait where the default path
cannot. Note what it still does not introduce: no request waits on *another request*. The
two namespace requests remain independent in both modes, each owning its own desires
(including its own read of the shared HostedCluster), and neither observes the other's
conditions. The wait is internal to one request's own reconciliation.

This independence holds only while the one-request-per-namespace shape is kept; merging
both namespaces into a single request would be a breaking API change rather than an
extension, and would also turn the internal wait into genuine cross-target sequencing.

**What the control plane request does observe about the other namespace.** It reads the
HostedCluster in the related HostedCluster namespace, and a HostedCluster with no
`deletionTimestamp` either stops it (`HostedClusterNotDeleting`) or, with the opt-in,
triggers the delete-and-wait path. Either way this is driven by an observation of the
*HostedCluster*, **not a wait on the other request** — it never resolves by the other
request making progress, and it never blocks on the other request existing. Both namespace
requests read the same HostedCluster through their own separate ReadDesires.

A consequence worth stating: if both requests opt in, both may issue a `Type=Delete` for
the same HostedCluster. That is safe and deliberate rather than a race — `DELETE` is
idempotent, the desires are separately owned so retiring one cannot strip the other, and
both resolve on the same observed absence. Do not add cross-request coordination to
prevent it.

Its purpose is to refuse the most dangerous mistake this API could make: finalizing the
control plane namespace of a cluster that is still alive, destroying its etcd and
kube-apiserver. This check is not redundant with the Cosmos ownership gate. A live cluster
normally has a ServiceProviderCluster carrying `Status.ControlPlaneNamespace`, so the gate
rejects at POST — but the entire orphan premise is that Cosmos records are missing or
stale, which is precisely when that gate cannot be trusted. The HostedCluster observation
is an independent second opinion taken from the management cluster itself.

**Partial and asymmetric outcomes are normal.** Submitting only one of the two requests is
valid; the other namespace simply remains for a later request. One request may complete
while the other stops on `HostedClusterNotDeleting` or `NamespaceUIDChanged`. There is no
aggregate or cross-request status: each request reports only its own target, and GET on one
says nothing about the other.

POST checks request shape, management-cluster identity, the protected-namespace denylist,
and live Cosmos ownership before creating a desire. It does not read Kubernetes. After
creation the backend observes the namespace and HostedCluster through kube-applier; GET
reports those checks and any blocker. An accepted POST means the request was recorded,
not that finalization is already authorized.

The admin server already holds what the gate needs: `NewAdminAPI` in
`admin/server/server/admin.go` is constructed with `resourcesDBClient`
(`corecosmosstorage.ResourcesDBClient`) and `kubeApplierDBClients` alongside
`fleetDBClient`, so the handler can run the live ownership query without new plumbing.

## Validation and operator messages

Use stable HTTP error codes and condition `reason` values for automation. Every message
names the requested object, the conflicting observation when one exists, and the
operator's next action. Avoid a generic "validation failed" or "preflight failed" message.
Do not treat a failed lookup as proof that an object is absent.

| POST result | HTTP response | Message content and operator action |
| --- | --- | --- |
| Valid new request | `201 Created` with request name and GET location | Poll GET for safety checks and progress. |
| Identical existing request | `200 OK` with the persisted request | Include whether the attempt is active, stopped, or complete. An ordinary duplicate does not restart work. |
| Invalid namespace or HostedCluster name, unsupported namespace relationship, or malformed body | `400 Bad Request`, `InvalidRequestContent` | Identify the invalid field and expected form. For example: `namespaceName "ocm-arohcpprod-...-other" is not the HostedCluster namespace or its control plane namespace for hostedClusterName "hcp1"`. |
| Target is a platform namespace | `400 Bad Request`, `ProtectedNamespace` | Name the namespace and state that platform namespaces are never valid targets. |
| Unknown stamp or management cluster | `404 Not Found` | Name the stamp or management cluster that could not be found. |
| Same namespace has an active or complete request with different details | `409 Conflict` | Identify the stored request and differing field; use GET to inspect it. Do not replace it. A corrected HostedCluster name is allowed only through an explicit restart of a stopped request. |
| A live Cosmos cluster record owns the namespace or HostedCluster | `409 Conflict`, `ClusterRecordPresent` | Name the matching Cosmos resource ID and whether it is active or Deleting: `Namespace "<namespace>" is still owned by cluster "<ARM resource ID>" (state Deleting); use the normal cluster deletion flow. No NamespaceDeletion request was created.` Do not create or restart a request. See the fully worked example below. |
| Another controller's ApplyDesire still maintains the target | `409 Conflict`, `ActiveDesirePresent` | Identify the desire and target; resolve that owner before retrying. |
| Cosmos query fails, times out, or cannot read every page | `503 Service Unavailable`, `OwnershipCheckUnavailable` | State which check failed, with a request or correlation ID. Retry after the database issue is resolved. Never report "no record." |
| Cosmos records exist but their identity or placement is ambiguous | `409 Conflict`, `OwnershipIndeterminate` | Identify the record(s) and missing or contradictory fields; investigate ownership before resubmitting. Do not create a request. |
| Explicit restart with a stale `If-Match` | `412 Precondition Failed` | GET the current request and ETag before retrying. |

### Error envelope

Use the repository's normal `CloudError` envelope for POST failures —
`coreapi.NewCloudError` (`internal/api/coreapi/error.go`), as the existing
`admin/server/handlers/stamp/stuckclustercleanup.go` already does. Every row in the table
above serializes into this one envelope; only `code`, `message` and `target` differ.

The following is a **fully worked example of one row** — the `ClusterRecordPresent` row —
not a specification of that row alone and not a template with blanks to fill in. It uses
the running example from the Contract section: HostedCluster namespace
`ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` with `hostedClusterName: "hcp1"`. The
subscription GUID, resource group and ARM cluster name are illustrative values in valid
ARM form; they do not identify a real cluster.

Request:

```http
POST /admin/v1/stamps/eastus/managementclusters/default/namespacedeletions
Content-Type: application/json

{
  "namespaceName": "ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "hostedClusterName": "hcp1",
  "requestReason": "AROSLSRE-2477"
}
```

Response — the preflight ownership query found a live Cosmos cluster record for this
namespace, so no request was created:

```http
HTTP/1.1 409 Conflict
Content-Type: application/json
```

```json
{
  "error": {
    "code": "ClusterRecordPresent",
    "message": "Namespace \"ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\" is still owned by cluster \"/subscriptions/2f1b7c0e-9a44-4d3b-8c61-5e7a0d9f3b21/resourceGroups/aro-hcp-eastus-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/prod-cluster-07\" (state Deleting); use the normal cluster deletion flow. No NamespaceDeletion request was created.",
    "target": "namespaceName"
  }
}
```

Note that the ARM cluster name (`prod-cluster-07`) is deliberately **not** the
HostedCluster name (`hcp1`) in this example. They are independent, as the Contract section
warns; a response that showed them equal would be misleading.

Exactly the following produces that body:

```go
return coreapi.NewCloudError(
    http.StatusConflict,
    CloudErrorCodeClusterRecordPresent, "namespaceName",
    "Namespace %q is still owned by cluster %q (state %s); use the normal cluster deletion flow. No NamespaceDeletion request was created.",
    namespaceName, owner.ResourceID, owner.State,
)
```

Three properties of the envelope are easy to get wrong and are fixed by the type, not by
convention:

- **The HTTP status is not in the body.** `CloudError.StatusCode` is tagged `json:"-"`
  (`internal/api/coreapi/error.go:57`); it is carried only by the HTTP status line. Clients
  must not look for a `status` field.
- **`target` is the JSON field name of the offending request-body field**, matching the
  existing handler's use of `"clusterResourceID"` and `"managementClusterName"`. Use `""`
  when a failure is not attributable to one field.
- **`code` is the stable contract; `message` is not.** Automation matches on `code` and on
  condition `reason` values. Message wording may be reworded without notice, so the exact
  strings in this document are illustrative of content and tone, not frozen.

The codes this design introduces — `ClusterRecordPresent`, `ProtectedNamespace`,
`ActiveDesirePresent`, `OwnershipCheckUnavailable`, `OwnershipIndeterminate` — do not exist
yet. Add them as `CloudErrorCode*` constants in `internal/api/coreapi/error.go` alongside
`CloudErrorCodeInvalidRequestContent` and the rest, rather than passing string literals at
the call sites, so the invariant set is greppable and cannot drift.

GET returns the request identity, conditions, the audit record, and an ETag for
concurrency control. The ETag is `CosmosMetadata.CosmosETag`, which the CRUD layer already
round-trips (`internal/api/coreapi/arm_types_cosmosdata.go`); `azcosmos.ItemOptions.IfMatchEtag`
is the matching write-side precondition. Conditions describe a single current blocker, with
the observed value and the next action. Keep prior `LastTransitionTime` when the condition
is unchanged, and avoid rewriting an unchanged status.

| Backend observation after POST | GET conditions and example message |
| --- | --- |
| ReadDesire has not reported a successful observation | `Complete=False`, reason `ObservationPending`: `Waiting for namespace "..." to be observed on management cluster "..."; nothing has been modified.` |
| Target namespace is already absent after a successful read | `Complete=True`, reason `NamespaceAbsent`: `Namespace "..." is absent; no action was needed.` |
| Namespace is `Active` | `Stopped=True`, reason `NamespaceNotTerminating`: `Namespace "..." is Active. This API only unblocks deletions already in progress; it does not start them. No change was made.` |
| Namespace phase and deletionTimestamp contradict each other | `Degraded=True`, reason `NamespaceStateInconsistent`: show both; take no action until the state reads consistently. |
| Namespace is Terminating but has not reached the dwell period | `Complete=False`, reason `DwellNotElapsed`: show `deletionTimestamp`, elapsed time and the required dwell. Retryable, not a stop. |
| A HostedCluster in the related namespace has no `deletionTimestamp`, no opt-in | `Stopped=True`, reason `HostedClusterNotDeleting`: `HostedCluster "hcp1" exists and has not been asked to delete, so its Azure infrastructure is still live. This request did not set deleteHostedCluster, so no deletion was initiated. Investigate manually, or submit a new request with deleteHostedCluster set. No change was made.` |
| Same, with `deleteHostedCluster` set | `Complete=False`, reason `DeletingHostedCluster`: `Deleted HostedCluster "hcp1" as authorized by deleteHostedCluster; waiting for HyperShift to finish deprovisioning before the namespace is finalized. Timeout <duration>, <elapsed> elapsed.` |
| The removal timeout elapsed with the HostedCluster still present | `Stopped=True`, reason `HostedClusterRemovalTimedOut`: `HostedCluster "hcp1" was deleted <elapsed> ago but still exists; HyperShift has not finished. The namespace was NOT finalized, because its Azure infrastructure was live when this attempt began. The HostedCluster now carries a deletionTimestamp set by this request. Investigate manually.` |
| Supplied HostedCluster name is wrong or another HostedCluster exists in the related namespace | `Stopped=True`, reason `HostedClusterIdentityMismatch`: `Requested HostedCluster "hcp1" in namespace "...", but observed HostedCluster "hcp2". No change was made. Verify the name, then submit a corrected POST with If-Match to start a new attempt.` Paired with `Complete=False`, reason `BlockedByIdentityMismatch`. |
| Namespace labels contradict the supplied identity | `Stopped=True`, reason `NamespaceIdentityMismatch`: name the label, expected value, and observed value. Missing legacy CS labels alone do not cause this result. Paired with `Complete=False`, reason `BlockedByIdentityMismatch`. |
| Namespace UID changed after binding | `Stopped=True`, reason `NamespaceUIDChanged`: show the pinned and observed UIDs; a new attempt is required. |
| Cosmos owner appears after creation | `Stopped=True`, reason `ClusterRecordAppeared`: name the owning record and state; use normal deletion. Retire request-owned pending desires, while acknowledging that an already accepted finalize cannot be undone. |
| Cosmos lookup or Kubernetes observation fails after creation | `Degraded=True`, reason `OwnershipCheckFailed` or `ObservationFailed`: include the failed check and correlation ID; retry automatically without issuing a mutation. |
| All gates pass; finalize issued | `Complete=False`, reason `FinalizingNamespace`: name the namespace, its pinned UID, and the finalizer being cleared. Include the audit record. |
| Finalize rejected on a precondition at execution time | `Complete=False`, reason `FinalizePreconditionFailed`: name the precondition (UID, resourceVersion conflict, dwell, HostedCluster state) and that it will be re-observed and retried. |
| Namespace is observed absent after finalization | `Complete=True`, reason `NamespaceFinalized`: name the target, confirm its absence, and restate what was cleared. |

## Namespace relationship and identity

The two-role design needs a deterministic way to distinguish the HostedCluster namespace
from its control plane namespace when neither HostedCluster nor customer Cosmos data
exists.

The naming rule is already encoded in the repository and must be reused rather than
re-invented — `backend/pkg/utils/controllerutils/cs_maestro_utils.go`:

- HostedCluster namespace: `ocm-<envIdentifier>-<csClusterID>`
  (`controllerutils.HostedClusterNamespace`). Cluster Service owns this name.
- Control plane namespace: `ocm-<envIdentifier>-<csClusterID>-<csClusterDomainPrefix>`
  (`controllerutils.HostedControlPlaneNamespace`). HyperShift builds the same string as
  `<hostedClusterNamespace>-<strings.ReplaceAll(hostedClusterName, ".", "-")>`, which is
  also what `coreapi.ServiceProviderClusterStatus.ControlPlaneNamespace` documents. The two
  agree because Cluster Service names the HostedCluster after the cluster's domain prefix —
  **the SRE-supplied `hostedClusterName` is that domain prefix.** The existing helper does
  no dot normalization because a domain prefix is a DNS label; the parser must still apply
  HyperShift's `ReplaceAll(".", "-")` rather than assume the helper's simpler form.

Classification of the supplied `namespaceName`, given `hostedClusterName`:

1. If `namespaceName` matches `^ocm-<env>-[a-z0-9]{32}$`, it is the **HostedCluster namespace**.
2. Else if `namespaceName == base + "-" + ReplaceAll(hostedClusterName, ".", "-")` for a
   `base` satisfying rule 1, it is the **control plane namespace**.
3. Otherwise reject with `400 InvalidRequestContent`.

The two rules are mutually exclusive because a Cluster Service cluster ID is a
32-character lowercase alphanumeric string with no dashes, while a domain prefix is a short
DNS label — a control plane namespace can never satisfy rule 1. This invariant is verified
against production namespace names before implementation lands (open question 2).

Reject unsupported or ambiguous forms rather than guessing. Do not use an arbitrary
string-prefix match.

**Protected-namespace denylist.** Keep the denylist from
`backend/pkg/controllers/stuckcleanup/eligibility.go` (`protectedNamespaceExact`,
`protectedNamespacePrefixes`) and evaluate it at POST, before anything else. The grammar
above already excludes `kube-system` and friends, but the denylist is cheap, independent
of the grammar being right, and definitive rather than provisional.

**This is not a dependency on Cluster Service.** Review was explicit that nothing may rely
on the Cluster Service ID, because Cluster Service is almost gone and is no longer
authoritative — and that constraint is respected here. The 32-character segment is treated
as an **opaque string already present in a name on the management cluster**. It is never
resolved against Cluster Service, never sent to it, never required as a label, never used
to look anything up, and never used as the Cosmos ownership match. No code path becomes
incorrect when Cluster Service is retired, because no code path asks it anything.

What the grammar actually asserts is narrower than it looks: *a name whose last segment is
32 lowercase alphanumerics is the HostedCluster namespace, and a name that equals such a
name plus `-<hostedClusterName>` is its control plane namespace.* That is a statement about
string shape on existing namespaces, which is why open question 2 validates it against
production names rather than against any Cluster Service contract. If Cluster Service stops
minting such names tomorrow, already-stuck namespaces keep the names they have, and those
are the only namespaces this API ever acts on.

This is still an explicit compatibility limit. If arbitrary namespace names must be
supported, the three inputs cannot always identify both roles after objects have
disappeared. That requires a different contract; do not silently broaden the parser.

Read the target namespace and pin its UID before creating the finalize desire. Persist that
binding first. Replacement of the pinned namespace stops the attempt; never repin
automatically. These are controller-observed safety values, not SRE-supplied identity
fields — matching the first commit's rule that `spec.namespaceUID` is forbidden on create
and pinned once by the controller (`internal/validation/validate_stuck_cluster_cleanup.go`).

If identity labels are present and contradict the request, report the mismatch. Missing
legacy Cluster Service labels alone must not block a known supported orphan. Name patterns
and labels do not establish Cosmos absence.

## Cosmos ownership gate

POST and reconciliation use the same ownership-matching implementation, with live database
queries rather than absence inferred from an unsynced informer cache. Matching is scoped by
management cluster and exact Kubernetes identity.

Inspect ServiceProviderCluster desired and observed identity — the fields exist today in
`internal/api/coreapi/types_serviceprovider_cluster.go`:

- `Spec.ManagementClusterResourceID` and `Status.ManagementClusterResourceID` (placement),
- `Spec.DesiredHostedCluster.metadata.namespace` / `.metadata.name` (desired identity,
  populated before the namespace exists on the management cluster),
- `Status.HostedClusterNamespace` and `Status.ControlPlaneNamespace` (observed identity).

A matching namespace is a blocker even if the SRE supplied a different HostedCluster name.
Do not rely on name alone across namespaces or management clusters. Include records in
Deleting and related customer records found through that identity.

### Why absence is meaningful

The gate is sound only if "no live record" cannot also mean "normal deletion is still in
flight." The deletion-ordering argument in
[The Azure assumption](#the-azure-assumption-and-its-accepted-risk) establishes that the
Cluster document outlives both namespaces in the normal path, so the dangerous direction is
prevented by construction for any cluster deleted through the current chain. The converse is
not guaranteed and does not need to be: clusters deleted before this chain existed, or whose
documents were removed out of band, are precisely the orphan population.

Pair this with the ApplyDesire half of the gate for the **creation** race: a cluster being
created has a live `Type=ServerSideApply` desire for its namespace before its
ServiceProviderCluster status is populated, so the foreign-desire check covers the window
the ServiceProviderCluster check does not.

### Query shape

Two implementation facts constrain this and are easy to get wrong:

- `cosmosstorageutils.CosmosGlobalLister` appends `AND (NOT IS_DEFINED(c.deletionTimestamp))`
  to every query (`internal/database/cosmosstorage/cosmosstorageutils/global_lister.go`).
  That is the **Cosmos soft-delete tombstone** (`SetSoftDeleteFields`, TTL
  `SoftDeleteTTLSeconds = 30`), not the ARM lifecycle. "Deleting" in the operator messages
  above means a live document with `Cluster.ServiceProviderProperties.DeletionTimestamp`
  set — the same field `clusterResourcesController.SyncOnce` branches on. Soft-deleted
  documents are deliberately invisible, and within 30 seconds they are gone; the gate must
  not be written as though it can observe them.
- Reusing the stock `ResourcesGlobalListers().ServiceProviderClusters()` means a full
  cross-partition `SELECT *` over every ServiceProviderCluster in the fleet, per call.
  Acceptable once per POST; unacceptable on every backend resync.

Therefore: add a purpose-built global lister in `corecosmosstorage` that filters server-side
on the candidate namespaces, following the existing `cosmosActiveOperationsGlobalLister`
pattern (`internal/database/cosmosstorage/corecosmosstorage/global_lister.go`), which already
demonstrates `c.properties.<path>` predicates in a cross-partition query. And split the read
by purpose:

- The backend's **steady-state** check may use the existing ServiceProviderCluster informer
  (`coreinformers.ServiceProviderClusters()`) as a fast blocker: a cache hit means stop. A
  cache miss proves nothing and never authorizes a mutation.
- The **finalize transition** is always preceded by the live query.

Consume every query page. Errors, unsupported legacy shapes needed for a decision, or
conflicting/incomplete placement evidence must produce an unknown/blocking result, not
"absent." Before rollout, demonstrate that the deployed document formats let this lookup
identify healthy clusters. An ARM-only record with no recoverable Kubernetes mapping cannot
be proved unrelated merely by querying newer ServiceProviderCluster fields; document the
necessary lifecycle/migration invariant or add a supported mapping.

Also block on surviving foreign ApplyDesires that actively maintain the exact HostedCluster
or either namespace. These are Cosmos desired ownership, so they belong in the same safety
gate. Exclude this request's own desires and observation-only ReadDesires. Do not
automatically delete another controller's intent. The first commit's `survivingApplyDesires`
already implements the matching rule and should be lifted into the shared gate.

POST returns 409 without storing a new request when ownership is found; database failures
return a retryable server error. The gate uses no Cluster Service, Maestro or Kusto call.

## Reconciliation

1. Validate the request's management cluster and namespace relationship. Establish exact
   ReadDesires for the target namespace and the related HostedCluster. Wait for valid
   observations. In the existing contract, `Successful=True` with nil `KubeContent` means
   observed absence; an unready or errored observation does not — exactly what
   `kubeapplierapi.ReadDesireStatus.KubeContent` documents. A cluster-scoped ReadDesire for
   a Namespace is supported: `read_desire_kubernetes/controller.go` keys off
   `len(target.Namespace) > 0`.
2. If the target namespace is already absent on a valid observation, complete the request.
3. If the namespace is not `Terminating`, stop with `NamespaceNotTerminating`.
4. If a HostedCluster is present in the related namespace with a different name than
   requested, stop with `HostedClusterIdentityMismatch`.
5. Pin the namespace UID, run the live Cosmos ownership gate, and persist the binding.
   Query failures retry without advancing.
6. If the dwell has not elapsed since the namespace's `deletionTimestamp`, requeue with
   `DwellNotElapsed`. **The dwell gates every mutation, including the opt-in HostedCluster
   delete below** — nothing destructive happens on a namespace that has only just entered
   `Terminating`, whichever path the request is on.
7. If a HostedCluster in the related namespace has no `deletionTimestamp`:
   - without `deleteHostedCluster`, stop with `HostedClusterNotDeleting`;
   - if the HostedCluster appears **live** (see the liveness gate below), stop with
     `HostedClusterLive` regardless of the opt-in;
   - otherwise write the audit record, re-run the live Cosmos ownership gate, then issue
     `Type=Delete` for the HostedCluster and hold at `Complete=False`, reason
     `DeletingHostedCluster`, until it is observed absent. If `hostedClusterRemovalTimeout`
     elapses first, stop with `HostedClusterRemovalTimedOut` and issue no finalize.
8. Write the audit record, then issue the finalize desire with the pinned UID.
9. Set `Complete=True` only when the namespace is observed absent for this attempt. Retire
   all request-owned desires so the old request cannot affect a future same-named namespace.

The ordering of steps 5–7 is deliberate and is a safety property, not sequencing
convenience: **the UID pin, the ownership gate and the dwell all precede the first
destructive act on every path.** An implementation that reorders them to delete the
HostedCluster earlier would defeat the invariant in
[What this API cannot do](#what-this-api-cannot-do).

Note what is absent: no namespace `DELETE` — a no-op on a Terminating namespace, which is
why both namespaces are removed by `FinalizeNamespace` and never by `Type=Delete` — and no
cross-request dependency in either mode. The only deletion this design issues is the
opt-in HostedCluster delete in step 5.

ReadDesire is the observation mechanism; it does not provide a guaranteed fresh snapshot at
the instant a mutation executes. Execution preconditions therefore belong in kube-applier.
This is a narrow cleanup capability, not a new general workflow engine.

## kube-applier: namespace finalize

This design adds exactly one capability to kube-applier: `FinalizeNamespace`. The opt-in
HostedCluster delete needs **no** kube-applier change — `ApplyDesireTypeDelete` already
exists (`internal/api/kubeapplierapi/types_apply_desire.go`), already reports
`SuccessfullyDeleted`, and is already the mechanism `hostedClusterRemovalStep` uses in the
normal removal chain. `evaluateDelete` is reused as-is.

The previously proposed `DeleteConfig` union member carrying UID preconditions is **not
reinstated.** Its purpose was to stop a stale desire deleting a recreated HostedCluster,
and that risk is negligible here: the namespace name embeds a 32-character Cluster Service
cluster ID, so the same identity reappearing under a new object is near-impossible (the
same reasoning as `ClusterRecordAppeared` above). If review disagrees, adding `Delete
*DeleteConfig` is additive — but it would touch the union validation that currently
requires every non-`ServerSideApply` field to be nil when `Type=Delete`, and therefore the
normal removal chain. Leaving it out keeps this change off that path entirely.

Add a third union member to the `ApplyDesireType` discriminator
(`internal/api/kubeapplierapi/types_apply_desire.go`):

- `Type=FinalizeNamespace`, with `FinalizeNamespace *FinalizeNamespaceConfig` carrying the
  pinned namespace `UID` and the required `MinimumDwell`.

A distinct type rather than a flag on Delete keeps the operation impossible to confuse with
an ordinary deletion in logs, metrics and review.

Immediately before issuing `/finalize`, the controller re-reads the namespace from the live
API and requires, in order: the pinned UID still matches; `deletionTimestamp` is set and
older than `MinimumDwell`; the only entry in `spec.finalizers` is `kubernetes`; and no
HostedCluster in the related namespace lacks a `deletionTimestamp`. Any failure is a
non-terminal precondition failure reported as `FinalizePreconditionFailed`, never a success.
Use the freshly read `resourceVersion`; retry a conflict by re-observing, never by
overwriting a newer namespace.

It may remove only the built-in `kubernetes` entry. It strips no resource finalizers, force
deletes no content, and has no "ignore errors" path.

**RBAC:** one rule, `namespaces/finalize` with `update`, added to
`kube-applier/deploy/templates/clusterrole.yaml`. The chart already grants `get`/`list`/
`watch`/`delete` on `namespaces` and `patch` on `hypershift.openshift.io/*`, so nothing else
is required. Notably v1 needs **no** `list` on arbitrary namespaced kinds and **no** CAPI /
CAPZ grant — both of which earlier drafts implied. Regenerate the Helm fixtures
(`cd config && make materialize`).

## Conditions, retries and lifecycle

Keep the Fleet spec small: HostedCluster name, request reason and the `deleteHostedCluster`
opt-in, with the namespace encoded by the resource name (or retained once in spec if API
conventions require it, with equality validated). Use conditions for progress and blockers, plus the audit record. Avoid phases,
modes, a separate Message field, detailed preflight structs, or a mirrored namespace
inventory. This removes `Status.Phase`, `Status.Message`, `Status.Preflight` and
`Status.ObservedNamespace` from the first commit's type.

Condition vocabulary:

- `Complete`: whether this request's target namespace is absent. False reasons:
  `ObservationPending`, `DwellNotElapsed`, `DeletingHostedCluster`, `FinalizingNamespace`,
  `FinalizePreconditionFailed`, `BlockedByIdentityMismatch`.
- `Degraded`: a retryable error prevents progress — database, observation, Kubernetes.
  **Retries are unbounded**, using the workqueue's standard rate-limited exponential
  backoff. There is deliberately no attempt cap: a capped retry would strand a request in
  a dead state after a transient Cosmos blip and force the operator to resubmit, which is
  the defect flagged on the first commit and contradicts the rule that a controller keeps
  trying. Unbounded retry is safe here because `Degraded` gates no mutation — every
  finalize precondition is re-evaluated from scratch on each pass, so a recovered error
  resumes exactly where a healthy run would be. What is bounded is observation
  *staleness*, never the attempt count.
- `Stopped`: a latched safety stop — `NamespaceNotTerminating`, `HostedClusterNotDeleting`,
  `HostedClusterLive`, `HostedClusterRemovalTimedOut`, `HostedClusterIdentityMismatch`,
  `NamespaceIdentityMismatch`, `NamespaceUIDChanged`, `ClusterRecordAppeared`. The
  controller still reconciles status and retires desires, but issues no further mutation
  for that attempt.

`HostedClusterRemovalTimedOut` is the one `Stopped` reason that latches *after* this
design has already mutated something — the HostedCluster carries a `deletionTimestamp`
this request set. Retiring the Delete desire does not undo that, and the stop must say so
plainly, since the cluster is now in a state no operator chose directly. Record it in the
audit record alongside the finalize evidence.

`Complete=False` is normal while work progresses; `Stopped=True` means the attempt cannot
resume without deliberate operator action; `Degraded=True` is retryable and does not itself
stop the attempt. Keep reason and message stable while the underlying observation is
unchanged, and clear `Degraded` after recovery.

When ownership appears, stop creating work and retire pending request-owned desires. Do not
claim this cancels an already accepted finalize. Record the stop durably so restart cannot
reactivate the attempt.

**Restart contract.** An ordinary duplicate POST returns the stored request and never clears
`Stopped` or `Complete`. A deliberate restart of a stopped request uses POST plus `If-Match`
from GET. It is permitted only after prior owned desires are retired and the live Cosmos gate
passes. The operator may correct `hostedClusterName` for an identity mismatch; `namespaceName`
and `requestReason` stay immutable. Validate the corrected name against the namespace
relationship and repeat all checks. Create a fresh internal attempt identity and new desire
identities under the same public request name. A stale precondition returns 412; changed
immutable details return 409. Retain the prior attempt's audit record. This relaxes the first
commit's validation, where every spec field was immutable on update — the update validator
must be rewritten to permit exactly this one field change, and only on a stopped attempt.
Restart may land as a follow-up commit; POST/GET/reconcile do not depend on it.

Never reuse old desire status for a new attempt. Each request owns separate desires,
including its read of a shared HostedCluster, so completing one namespace request cannot
remove the other's observation. Use stable names within an attempt and an incarnation
identifier across attempts. On concurrent POST/create conflicts, read and return the stored
winner.

Unchanged status must not trigger a Cosmos write loop. Backend restart or client
unavailability must retain the durable request and retry. Retain settled requests for 14 days
after their desires have been retired; failure to retire is not success. Do not let retention
rearm an old attempt or let a completed request pursue a replacement namespace. The reaper
must be controller-driven: these documents are stamp-rooted, so `DeleteOrphanedCosmosResources`
cannot reach them and no other sweeper will. The first commit's `reconcileTerminal` already
implements the right shape — release the ReadDesire first, stamp `CompletedAt`, delete the
document after the retention window — and carries over.

## Remaining concurrency limit

The Cosmos check and the Kubernetes mutation are not atomic. UID preconditions protect
against a replaced namespace; they do not prevent a new Cosmos record from adopting the same
Kubernetes UID after a check. Retiring desires is also asynchronous.

The deletion-ordering invariant closes the *deletion* race by construction. What remains is
the *adoption/creation* race: a cluster create beginning between the gate and the finalize.
The foreign-ApplyDesire check narrows it to the window before the creating controller writes
its first desire — and a namespace in `Terminating` cannot be adopted by a new cluster
anyway, which narrows it further than in the earlier draft.

Reviewers must either accept that residual window or require coordination with cluster
creation. A second check in kube-applier narrows it; it cannot make two systems atomic. Do
not add a global locking subsystem without that architectural decision, and do not present
this proposal as proving the race impossible.

## Implementation on top of PR #7762's first commit

- Replace `types_stuck_cluster_cleanup.go` with `types_namespace_deletion.go`; rename
  `StuckClusterCleanupResourceTypeName` to `namespaceDeletions` in
  `internal/api/fleetapi/registry.go`; update validation, Fleet registration,
  runtime/deepcopy generation, Cosmos CRUD/mocks, lister and Fleet change-feed wiring.
  Helpers belong in helper/controller packages, not the API type file.
- Replace admin inspection handlers/routes with NamespaceDeletion POST/GET and the shared
  Cosmos gate; drop `StuckClusterCleanupNameFor` in favour of the namespace name as the
  resource name.
- Replace the preflight controller with NamespaceDeletion reconciliation and request-owned
  ReadDesire/finalize-desire creation. Update registry, cache synchronization and watcher
  plumbing. Reuse Cosmos informers; introduce no direct Kubernetes informers in the backend.
- Carry over rather than rewrite: the protected-namespace denylist, `survivingApplyDesires`,
  `reconcileTerminal`'s retention/desire-release ordering, the controller-pins-UID validation
  rule, and `minimumTerminatingDwell`.
- Adapt rather than delete `evaluateEligibility`: its `NamespaceNotTerminating` and
  `NoDeletionTimestamp` verdicts are now correct and central. Its `HostedClusterPresent`
  verdict is replaced by the narrower `HostedClusterNotDeleting` test.
- Add the `FinalizeNamespace` union member and its kube-applier handler, the
  `namespaces/finalize` RBAC rule, and regenerated Helm fixtures.
- Add a purpose-built ServiceProviderCluster ownership query to `corecosmosstorage` rather
  than reusing the unfiltered global lister.
- Add the `deleteHostedCluster` field, the `hostedClusterRemovalTimeout` backend flag, the
  liveness gate, the `DeletingHostedCluster` / `HostedClusterLive` /
  `HostedClusterRemovalTimedOut` reasons, and issuance of an `ApplyDesire Type=Delete` for
  the HostedCluster. **No kube-applier or `ApplyDesireSpec` change is required for this** —
  `Type=Delete` and `evaluateDelete` already exist and are reused unmodified, and `delete`
  on `hypershift.openshift.io/*` is already granted in
  `kube-applier/deploy/templates/clusterrole.yaml`.
- Extend `recordApplyDesireOperation`
  (`kube-applier/pkg/app/desire_operation_metrics.go`) to label `FinalizeNamespace`
  distinctly. It currently treats everything that is not `Type=Delete` as an apply, so
  without this the new operation is invisible in metrics under an apply label.
- Remove the original proposal's inspection, CS checks and deferred-mutation narrative.

**Suggested commit split.** The opt-in path is separable and should land second: commits
one through five above are the whole default capability and are independently reviewable
and deployable. Landing the opt-in separately also keeps the riskiest review conversation
— originating a deletion — off the critical path for the stuck namespaces that motivated
the work, none of which are known to need it (open question 7).

## Test plan

Unit tests for matching and reconciliation, plus real API tests for Kubernetes semantics.
Dynamic fake clients do not by themselves prove UID preconditions, resourceVersion conflicts,
`/finalize`, or namespace controller behaviour.

Required cases:

- Each namespace request alone and both together; target already absent; no cross-request
  desire deletion; no ordering dependency between the two.
- Active namespace rejected; contradictory phase/deletionTimestamp; dwell boundary either
  side; HostedCluster absent, present-with-deletionTimestamp, present-without; wrong
  HostedCluster name; namespace UID replacement.
- Correct and unsupported/ambiguous namespace forms; dot normalization;
  protected-namespace rejection; contradictory labels; missing legacy labels.
- Healthy and Deleting Cosmos records; desired identity populated while observed status is
  empty; spec/status placement mismatch; legacy record coverage; foreign ApplyDesire;
  late-page query failure; no records; owner appearing after acceptance; soft-deleted
  document invisible to the query.
- ReadDesire pending, failed, valid absence and stale status; client outage/restart; cache
  readiness; desires under management-cluster scope; unchanged status produces no writes.
- Finalize: pinned UID match and mismatch; resourceVersion conflict and retry; extra
  namespace finalizers present (must refuse); dwell enforced at execution time, not only at
  decision time; audit record written before the mutation.
- Duplicate and conflicting POSTs; concurrent create winner; restart precondition; changed
  immutable details; stopped-attempt persistence; desire retirement; retention when clients
  are unavailable. Assert exact POST error codes and GET condition reasons for every result.
- Opt-in path: `deleteHostedCluster` absent on a not-deleting HostedCluster still stops with
  `HostedClusterNotDeleting`; set on an *already deleting* HostedCluster issues no second
  delete and changes nothing; set on a not-deleting one issues `Type=Delete` and holds at
  `DeletingHostedCluster`. **Assert no finalize desire is created while the HostedCluster is
  still observed present** — this is the test that protects a live control plane. Timeout
  elapsed yields `HostedClusterRemovalTimedOut` with still no finalize. Attempted mutation
  of `deleteHostedCluster` on update is rejected, including via restart. Both requests
  opting in produce two independent Delete desires and retiring one leaves the other intact.
- **Invariant tests, asserted directly rather than inferred.** An `Active` namespace is
  refused with `NamespaceNotTerminating` at every combination of request fields including
  `deleteHostedCluster=true`, and no desire of any type is created. A HostedCluster that is
  `Available=True`, or has a `Completed` version-history entry, or a populated
  `controlPlaneEndpoint`, stops with `HostedClusterLive` and issues no Delete — with the
  opt-in set and with the Cosmos gate returning no owner, since that is the only
  combination in which the gate could otherwise proceed. Dwell not elapsed blocks the
  HostedCluster delete and not only the finalize. These assert
  [What this API cannot do](#what-this-api-cannot-do) and should fail loudly if a later
  change reorders the reconciliation steps.

Focused tests and builds after implementation:

```sh
(cd internal && go test ./validation ./api/fleetapi ./api/kubeapplierapi ./database/cosmosstorage/fleetcosmosstorage ./database/cosmosstorage/corecosmosstorage)
(cd admin/server && go test ./handlers/stamp ./server)
(cd backend && go test ./pkg/controllers/namespacedeletion)
(cd kube-applier && go test ./pkg/controllers/apply_desire)
make -C admin build
make -C backend build
make -C kube-applier build
make verify
```

Set up envtest assets with `export KUBEBUILDER_ASSETS=$(make -s envtest-setup)`. Envtest does
not run the Kubernetes namespace controller or HyperShift: use it to verify `/finalize`
semantics, UID preconditions and resourceVersion conflicts, and a disposable personal fixture
for end-to-end namespace removal. Regenerate API/mocks/Helm fixtures before verification; do
not regenerate unrelated generated architecture documentation.

### Personal-dev verification

Deploy the backend and kube-applier images to pers with the dwell flag lowered. Access
admin through the service-cluster kubeconfig and the existing admin helper/authentication
flow; port forwarding alone does not replace authentication. Read the live `ocm-<env>-`
prefix off the management cluster (`kubectl get ns | grep '^ocm-'`) rather than deriving it
from config — the namespace must match the grammar or POST rejects it for the wrong reason.

Build the wedged-namespace fixture synthetically. It reproduces the stuck state exactly,
with no Azure resources and no HyperShift involvement:

```sh
NS=ocm-<env>-$(openssl rand -hex 16)          # must match ^ocm-<env>-[a-z0-9]{32}$
kubectl create ns "$NS"
kubectl -n "$NS" create configmap wedge
kubectl -n "$NS" patch configmap wedge --type=merge \
  -p '{"metadata":{"finalizers":["test.aro-hcp/never"]}}'
kubectl delete ns "$NS" --wait=false          # -> Terminating, stuck indefinitely
```

Sequence:

1. **Live owner rejected.** POST against a *real healthy cluster's* namespace → `409
   ClusterRecordPresent`, raised at POST before any Kubernetes read. Assert no request and
   no desire were stored. Do not fabricate or delete Cosmos documents for this: a real
   cluster already provides a correct ServiceProviderCluster, and removing a healthy
   cluster's record is how orphans get manufactured.
2. **Deletion is never initiated.** Synthetic namespace, still `Active`. POST is accepted
   (no owner) and reconciliation stops with `NamespaceNotTerminating`.
3. **Happy path.** Same namespace, now wedged per the recipe above. After the dwell the
   finalize lands and the namespace disappears. Then confirm
   `kubectl get configmap -n "$NS"` still returns `wedge` — the etcd-orphan tradeoff this
   design accepts, verified once explicitly rather than assumed.
4. **Two namespaces in parallel.** Create `ocm-<env>-<32hex>` and
   `ocm-<env>-<32hex>-<hostedClusterName>`, wedge both, and POST both at once with the same
   `hostedClusterName`. Both must finalize independently, in either order, with neither
   waiting on the other.

Removing a Cosmos record removes the *blocker*; it never causes cleanup on its own. The
namespace must independently be wedged in `Terminating` for the request to have anything
to do. Steps 1 and 3 above exercise both sides of that gate through the same code path,
using different namespaces.

To demonstrate the gate flipping on a *single* namespace — 409 while owned, success once
the owner is gone — plant a synthetic `ServiceProviderCluster` document pointing at the
synthetic namespace and then delete it. The gate matches on ServiceProviderCluster
placement and namespace fields, so an HCPCluster document alone will not trigger it. The
minimum fields are `CosmosMetadata.ResourceID` (child resource name `default`, per
`coreapi.ServiceProviderClusterResourceName`), `Spec.ManagementClusterResourceID` and
`Status.ManagementClusterResourceID` set to the target management cluster, and
`Status.HostedClusterNamespace` (or `Status.ControlPlaneNamespace`) set to the synthetic
namespace. There is no admin API for this write, so it needs a small `hack/` fixture tool
built on `corecosmosstorage`; the same tool serves the ownership-gate tests.

Do **not** create a real HostedCluster CR on a personal environment to exercise
`HostedClusterNotDeleting`: HyperShift will reconcile it and begin provisioning Azure
infrastructure. Cover that path in envtest and unit tests, where no HyperShift controller
is running.

## Open questions

Settled by requester decision: one request per namespace; Terminating-only scope;
namespace `kubernetes` finalizer only; Azure reclamation assumed complete on the default
path. **Revised this pass:** HostedCluster deletion is no longer categorically out of
scope — it is available behind the `deleteHostedCluster` opt-in, which supersedes the
previous revision's blanket exclusion.

1. **Review the Azure assumption on its merits.** It is the one place where the default
   path knowingly accepts a possible resource leak. See
   [The Azure assumption](#the-azure-assumption-and-its-accepted-risk). Confirm the accepted
   risk and that the audit record is sufficient for manual reclamation.
2. Confirm the 32-character Cluster Service cluster-ID invariant that makes the two
   namespace-classification rules mutually exclusive, against production namespace names.
   *Resolvable from data; does not block coding.*
3. Confirm the ownership query's coverage of legacy and ARM-only records that lack complete
   desired or observed Kubernetes identity. Code treats unrecognized shapes as indeterminate
   and blocks, so this gates rollout rather than coding.
4. Where does the admin handler get `hostedClusterNamespaceEnvIdentifier` for the
   `ocm-<env>-…` grammar check? It is a backend flag today and `NewAdminAPI` does not receive
   it. Default taken: validate structure only at POST and anchor the environment check in the
   backend. Cheap to change.
5. Confirm the conditional POST restart contract, including correction of a mistaken
   HostedCluster name. May land as a follow-up commit.
6. Accept or coordinate the residual adoption window in
   [Remaining concurrency limit](#remaining-concurrency-limit).
7. Distribution of blocking conditions across the 20 namespaces is still worth collecting —
   not to gate the design, but to establish how many (if any) are in the
   `HostedClusterNotDeleting` state and therefore need the opt-in at all. If none are, the
   opt-in path can land as a later commit without delaying the cleanup it was built for.
8. **Confirm the opt-in's stop-on-timeout behaviour.** `HostedClusterRemovalTimedOut`
   deliberately does *not* fall back to finalizing, on the grounds that Azure was live when
   the attempt began. The cost is that the request leaves a HostedCluster carrying a
   `deletionTimestamp` it set and then stops, which is a state no operator chose directly.
   The alternative — finalize anyway after the timeout — guarantees a leak but leaves no
   half-deleted object. Confirm the choice; it is the one genuinely new safety tradeoff in
   this revision.
9. ~~Choose an initial `hostedClusterRemovalTimeout`.~~ **Default taken: 30 minutes**, by
   the same reasoning as the dwell's one hour — comfortably longer than a healthy
   HyperShift deprovision (minutes), short enough that a wedged one is reported the same
   working day. Unlike the dwell it is a real wait imposed by this controller, so it is a
   flag and can be tuned without a code change. Not a coding blocker.

## Mapping to PR review feedback

| Feedback | Design response |
| --- | --- |
| Clarify DeleteNamespace vs DeleteHostedCluster | Both are now answered explicitly. **Namespaces are never deleted** — `DELETE` is a no-op on a Terminating namespace, so both namespaces are removed by clearing the `kubernetes` finalizer (`FinalizeNamespace`). **The HostedCluster is deleted only on explicit opt-in** (`deleteHostedCluster`), using the existing `Type=Delete`; without it, a HostedCluster that is not already deleting still causes a refusal. |
| Remove phase-based failure and preflight | Continuous reconciliation with conditions; no observation/preflight phases. A safety stop is explicit and persistent, not a transient-error phase. |
| Explain retry after a live record disappears | A rejected POST can be submitted again. An accepted request later stopped uses the explicit conditional restart contract. |
| No helpers in API package | Helpers live outside API type definitions. |
| Use namespace name directly; provide an example | Namespace is the resource name; the URL/body example identifies each input. The `ns-<hash>` name is dropped. |
| Do not depend on Cluster Service ID | No CS input, query, lookup or required CS label; no code path asks Cluster Service anything, so none breaks when it is retired. The 32-character segment is treated as an opaque string already present in a name on the management cluster, asserting only string shape. See [Namespace relationship and identity](#namespace-relationship-and-identity). |
| UID field not valuable as originally proposed | Removed from caller input/spec. Retained only as a controller-pinned precondition on the finalize operation. |
| Customer Cosmos ID is unavailable | Caller supplies none; live ownership matching uses management cluster and Kubernetes identity. |
| HostedClusterName is missing | Required. Its job is deriving the control-plane namespace name and confirming observed identity; it drives a deletion only when `deleteHostedCluster` is also set. |
| Use conditions for messages/check results | Small condition-based status plus an audit record; no preflight report or top-level message. |
| Remove duplicated namespace representation | Observe through ReadDesire; retain only the UID binding, audit record and conditions. |
| Why have a mode other than deletion? | Agreed — the inspection mode is gone. There is now exactly one operation and no `mode` field at all: an accepted request expresses intent to remove a wedged namespace. On the naming: the review suggested `DeleteTheHostedCluster`, but the HostedCluster is not what blocks these 20 namespaces, and deleting it is neither sufficient (the namespace stays `Terminating`) nor usually necessary (it is already deleting). The resource is therefore named for what it removes — `NamespaceDeletion` — and HostedCluster deletion is available on it via `deleteHostedCluster` for the one case where it is the missing step. |

| Detect stuck namespaces via mgmt-agent + CR instead of an operator trigger | Not precluded, and better once the safety decision is mechanical. Deferred because detection is the easy half; the hard half is accepting the Azure assumption, which should not be automated across a finite historical backlog with no named requester. See [Why the trigger is an operator action](#why-the-trigger-is-an-operator-action-not-automated-detection). |

The table records proposed design resolutions. Implementation, tests and individual PR
replies are still required.

### Carried-over defects found in review

Two findings against the first commit are **not** resolved by redesign and must be fixed in
the code that carries over. Both are verified against the current branch:

- `backend/pkg/controllers/stuckcleanup/registration.go` passes `usesUnion=false` to
  `controllerconfig.WithCacheSyncs` while the controller consumes both
  `UnionKubeApplierInformers.ReadDesires()` and `.ApplyDesires()`. `WithCacheSyncs` only
  adds `UnionKubeApplierInformers.HasSynced` to the readiness barrier when that flag is
  true (`backend/pkg/controllers/controllerconfig/cache_sync.go`), so after a restart the
  controller can read a cold union cache. This matters more in this design than it did in
  an inspection-only one: a false "absent" reading is exactly what authorizes an
  irreversible mutation. Set it to `true`.
- The terminal cleanup path passes a full resource ID string to `crud.Delete`, which takes
  a **leaf name** and rebuilds the path itself via `makeResourceIDPath`
  (`internal/database/cosmosstorage/cosmosstorageutils/crud_nested_resource.go`). The
  delete therefore targets a doubled path, returns NotFound, and NotFound is swallowed as
  success — so the ReadDesire is never retired. Pass `ReadDesireNameFor(key.Name)`. This
  directly violates this design's rule that failure to retire is not success.

# Stuck HostedCluster Cleanup

**Status:** Approved shape. Verified against the source tree on 25 October 2026.
**Review:** Presented to the ARO HCP architecture review board on 21 October 2026 and refined in subsequent design discussion with the backend and kube-applier maintainers.
**Tracking:** [AROSLSRE-2477](https://redhat.atlassian.net/browse/AROSLSRE-2477)

## 1. Problem statement

The fleet sweep of 19 October 2026 identified 20 HostedCluster namespaces stuck in `Terminating`: eight in production and twelve in stage. Four retained both Cosmos and Clusters Service (CS) state; **sixteen appeared in neither**. A further production HostedCluster exhibits a stalled deletion while its namespaces remain intact, which establishes that a namespace-only detector would be incomplete.

These cases are presently cleared by means of individual JIT breakglass sessions and manual `kubectl patch` of finalizers. This represents recurring operational burden, and the associated orphaned Azure resources continue to consume capacity and deny-assignment quota.

Existing controllers do not address this case:

- `ClusterServiceMatchingClusters` is driven from CS records.
- `DeleteOrphanedCosmosResources` ([`backend/pkg/controllers/mismatch/delete_orphaned_cosmos.go`](../../backend/pkg/controllers/mismatch/delete_orphaned_cosmos.go)) is driven from Cosmos documents.

For the sixteen namespace-only orphans neither a Cosmos document nor a CS record exists, and consequently **no state remains from which a controller could be triggered**. The review therefore concluded that the entry point must be an explicit operator action rather than automated detection: the triggering event is an admin API request asserting that the cluster occupying a named namespace requires cleanup.

Known root causes are tracked separately: the backend/kube-applier deletion-acknowledgement race (AROSLSRE-2442), the orphan sweeper and namespace teardown race (ARO-30112), blocked PodDisruptionBudget teardown, and failed creates leaving a managed resource group together with deny assignments (AROSLSRE-2377). Resolving these prevents recurrence but cannot retire objects already stranded.

### 1.1 Scope of reclamation

This warrants explicit statement, since the motivation above refers to Azure capacity.

The deletion chain's own documentation ([`apply_desire_removal_chain.go:80-86`](../../backend/pkg/controllers/clusterresources/apply_desire_removal_chain.go)) records that deleting a Namespace out from under the HostedCluster garbage-collects the HostedCluster CR before its finalizer is able to deprovision Azure, thereby "stranding Azure resources and, because the namespace cascade honors finalizers, likely wedging the namespace in Terminating."

This describes the most probable mechanism by which the twenty namespaces reached their current state. It follows that in most cases the HyperShift deprovision finalizer never executed, and that **the Azure resources are already orphaned independently of the Kubernetes objects**. Clearing the namespace retires the Kubernetes-side remnants; it does not reclaim the Azure resources or the deny assignments.

The deliverable of the first release is therefore the retirement of stranded Kubernetes objects and the elimination of the associated operational burden. Azure reclamation constitutes a separate workstream, for which open question 2 (§9) is the entry point. The findings produced by the first release will determine its scope.

## 2. Design overview

The design comprises four parts, as agreed in review:

1. A **Cosmos API object** expressing the intent to clean up one namespace on one management cluster.
2. A **backend controller** reconciling that object, routing all management-cluster access through kube-applier desires.
3. An **admin API POST** endpoint to create the object.
4. An **admin API GET** endpoint to read its status.

The admin API does not hold a request open for the duration of the work and does not communicate with a management cluster directly. It writes desired state to Cosmos and reads status back.

### 2.1 Precedent

The pattern is already in production. `PATCH /admin/v1/.../backupschedules` writes desired state into Cosmos ([`admin/server/handlers/hcp/backups.go:211-230`](../../admin/server/handlers/hcp/backups.go)), a controller acts upon it, and `GET /backups` reads status back from the per-management-cluster kube-applier container ([`backups.go:275`](../../admin/server/handlers/hcp/backups.go)). This design reuses that precedent rather than introducing a new mechanism.

The admin API is an established Geneva Actions entry point with authentication, caller identity and audit middleware already in place ([`admin/README.md:148-150`](../../admin/README.md)), and already holds ARM-resource Cosmos, Fleet Cosmos, CS and per-management-cluster kube-applier clients. A stamp-scoped route family exists ([`admin/server/server/admin.go:153-167`](../../admin/server/server/admin.go)).

Regarding authentication wiring: the client-principal middleware is `MiddlewareClientPrincipal`, registered **pre-mux** ([`admin.go:95-101`](../../admin/server/server/admin.go)), and therefore executes on every admin route prior to pattern matching. New routes inherit authentication, audit and caller identity without per-route opt-in.

### 2.2 Controller placement

The preflight requires a live point read of the ARM-resources Cosmos container. Of the two candidate services:

- `fleet` holds `FleetDBClient`, `ClustersServiceClient` and `KubeApplierDBClients`, but **not `ResourcesDBClient`** ([`fleet/pkg/controllers/controllerconfig/context.go:34-50`](../../fleet/pkg/controllers/controllerconfig/context.go)).
- `backend` holds all four, together with `FleetInformers` ([`backend/pkg/controllers/controllerconfig/context.go:50-79`](../../backend/pkg/controllers/controllerconfig/context.go)).

Only the backend is capable of executing the full preflight, and the controller is sited there accordingly. `NewControlPlaneVersionRolloutWatchingController` ([`backend/pkg/utils/controllerutils/control_plane_version_rollout_watching_controller.go`](../../backend/pkg/utils/controllerutils/control_plane_version_rollout_watching_controller.go)) provides the closest precedent: a backend controller watching a Fleet-container type, with a key implementing `AddLoggerValues` and no nested `Controllers` status document.

## 3. Management-cluster-scoped desires

The availability of management-cluster-scoped desires is what makes this design inexpensive, and it resolves the principal difficulty — driving cleanup for a cluster that has no cluster document:

- Desires partition by **management cluster** resource ID rather than by HCP cluster ([`internal/api/kubeapplierapi/partition.go`](../../internal/api/kubeapplierapi/partition.go)).
- `ApplyDesiresForManagementCluster(stampIdentifier)` and `ReadDesiresForManagementCluster` already exist ([`internal/database/cosmosstorage/kubeappliercosmosstorage/kube_applier_client.go:98-101`](../../internal/database/cosmosstorage/kubeappliercosmosstorage/kube_applier_client.go)), backed by `ManagementClusterScope` and `StampAncestry` in [`desire_scope.go`](../../internal/database/cosmosstorage/kubeappliercosmosstorage/desire_scope.go).
- `ManagementClusterScopedApplyDesireResourceType` is provider-rooted beneath `stamps/{stamp}/managementClusters/{mc}` ([`internal/api/kubeapplierapi/registry.go`](../../internal/api/kubeapplierapi/registry.go)), whereas `DeleteOrphanedCosmosResources` traverses each management-cluster container by **subscription** resource-ID prefix (`delete_orphaned_cosmos.go:121,308`). The orphan sweeper is therefore structurally unable to collect these documents.

No new scoping concept is required.

The management cluster is a **singleton per stamp**: `ManagementClusterResourceName` is the constant `"default"`, and `ManagementClusterScope` accepts only a stamp identifier. The `{managementClusterName}` route segment exists for consistency with the sibling stamp routes and is validated against `"default"`, following the precedent in [`managementclusterscheduling.go:50`](../../admin/server/handlers/stamp/managementclusterscheduling.go).

### 3.1 Known limitation: ApplyDesire informer registration

Verified 25 October 2026. This affects the mutation release and not the inspection release.

`ManagementClusterScopedApplyDesireResourceType` is present in the Cosmos **lister** ([`kube_applier_client.go:313`](../../internal/database/cosmosstorage/kubeappliercosmosstorage/kube_applier_client.go)) but **absent from the ApplyDesire informer's change-feed type list** ([`informers.go:60-63`](../../internal/database/informers/kubeapplierinformers/informers.go)). Its ReadDesire counterpart is correctly registered ([`informers.go:99`](../../internal/database/informers/kubeapplierinformers/informers.go)). The change feed silently discards documents whose type is not listed ([`changefeed_list_watch.go:422-431`](../../internal/database/informers/informerutils/changefeed_list_watch.go)), while the List path applies no such filter.

The resulting behaviour is that a management-cluster-scoped `ApplyDesire` is observed only on the 30-minute relist, and subsequent changes to it are never streamed. The condition is intermittent rather than a clean failure, which increases diagnostic difficulty. The remedy is to register the type at `informers.go:60-63`.

This is a latent defect affecting any future management-cluster-scoped ApplyDesire producer and is not specific to this design. It should be tracked and remedied independently.

Management-cluster-scoped **ReadDesire** exhibits no equivalent gap and is in production use by two fleet controllers (`capacityreporting` and `sharedingress`). The inspection release depends only on ReadDesire.

## 4. Operating modes

The object carries `spec.mode`, with permitted values `Inspect` and `Execute`.

In `Inspect` the controller performs the full preflight and records findings to `status` without mutating any management-cluster state. This keeps the admin API off every management-cluster read path, provides a dry run at no additional cost, and satisfies the requirement for an admin API status endpoint with a single object rather than two endpoints.

## 5. Safety controls

The review board required explicit protection against the deletion of a desired HostedCluster arising from operator error.

Two independent gates are applied, in order. Both must pass.

### 5.1 Eligibility

Eligibility establishes whether the named namespace is an object this action may touch at all. It is evaluated first, because its refusals are definitive rather than provisional and because it is the gate that protects against a mistaken request.

The distinction from ownership matters. The ownership checks in §5.2 ask whether anything still owns the namespace. An operator who mistypes a namespace name may land on a perfectly healthy namespace that no cluster record happens to refer to, in which case **every ownership check passes**. Ownership alone therefore provides no protection against a mistyped or mistaken request.

A namespace is eligible only where all of the following hold:

1. **It is not a platform namespace.** A denylist of exact names and prefixes (`kube-*`, `openshift-*`, `hypershift`, `default`, and others) is rejected unconditionally. This is a denylist and not an attempt to identify an HCP namespace positively: a prefix remains inadmissible as proof that a namespace belongs to a cluster, but is sound evidence that a namespace is not a candidate.
2. **Its phase is `Terminating`.** An `Active` namespace is refused. This is the decisive check against a mistyped name.
3. **It carries a `deletionTimestamp`.** A namespace reporting `Terminating` without one is contradictory, and the preflight refuses on ambiguous data.
4. **It has been deleting for longer than one hour.** Normal HostedCluster teardown takes minutes, during which HyperShift's finalizer deprovisions Azure infrastructure. Accepting a namespace that has only just begun deleting would race that teardown — the very mechanism that strands Azure resources and wedges namespaces (§1.1).
5. **No HostedCluster CR survives inside it.** A surviving HostedCluster indicates a stalled deletion rather than an orphan, which is out of scope for the first release (§9.1), and retiring the namespace beneath a live HostedCluster would garbage-collect the CR before its finalizer completes.

Condition 5 is determined from the remaining-content findings already collected (§7.1) and requires no additional read.

The verdict is published as `status.preflight.namespaceEligible`, with `status.preflight.ineligibleReason` naming the condition that refused.

### 5.2 Ownership

The ownership preflight executes before `Execute` is accepted, and again immediately prior to every mutation:

1. Live point read of ARM Cosmos for the cluster document.
2. Live point read of CS.
3. Scan for surviving `ServerSideApply` desires on the management cluster.

Where any of the three indicates a live record or an active publisher, the operation is refused and the reason recorded. The preflight **fails closed** on unavailable or ambiguous data.

The ADX query associated with AROSLSRE-2477 infers `inCosmos` from a 30-minute Cosmos snapshot and `csActive` from one hour of CS logs. These are discovery signals for constructing the work list and do not constitute proof of absence. Only live point reads may gate a mutation.

### 5.3 Identifier resolution

A namespace-only orphan has neither a Cosmos document nor a CS record, so the only input intrinsically available to the preflight is the namespace name.

The implemented behaviour is that **no cluster identifier is derived from the namespace name**. The ARM Cosmos and CS point reads execute only where the operator supplies `spec.clusterResourceID` or `spec.clusterServiceID`; otherwise those checks remain unset.

The rationale is asymmetry of risk. A derived key would be used for a negative lookup — establishing absence rather than ownership — and an incorrect derivation yields a spurious `NotFound`, which presents as authorisation to delete. This is the sole error direction of consequence, and it is silent. Requiring the operator to identify the cluster renders the identifier a verified value rather than an inference.

The cost of this choice is that a request naming only a namespace can never reach a preflight pass. This is the correct trade-off for the inspection release: the value of inspection lies in the namespace findings, which require no cluster identifier, and the surviving-desire scan likewise executes unconditionally and is the check most likely to detect a live cluster. Since the preflight fails closed, an incomplete check set is reported as `Inspected` with `PreflightPassed=Unknown` together with an explicit enumeration of which checks could not be performed and why. It is never reported as a pass.

Should the findings of the inspection release demonstrate that operators cannot reliably supply identifiers for these orphans, a documented derivation rule becomes the follow-up item, to be delivered with the mutation release rather than in advance of it.

### 5.4 Residual time-of-check-to-time-of-use exposure

`ResourceReference` ([`internal/api/kubeapplierapi/types_resource_reference.go`](../../internal/api/kubeapplierapi/types_resource_reference.go)) carries group, version, resource, namespace and name, but **no UID**, and the kube-applier issues deletions with a bare `metav1.DeleteOptions{}` ([`apply_desire/controller.go:467`](../../kube-applier/pkg/controllers/apply_desire/controller.go)). No mechanism pins object identity between the controller's preflight and the kube-applier's subsequent DELETE.

Re-running the preflight immediately prior to every mutation is therefore achievable for the act of *writing the desire*, but not for the Kubernetes-level deletion that follows asynchronously. A namespace of the same name recreated within that window would be deleted by name.

The remedy is small and self-contained: add `UID` to `ResourceReference` and set `Preconditions.UID` on the deletion. This constitutes the concrete implementation of the operator-error protection required in review, and must be delivered with the first mutation.

## 6. Mutation primitives

The review direction was to build upon the existing `ApplyDesire` and `DeleteDesire` primitives, supporting precisely the cases required by the present set of stuck clusters and extending to further flows as the need is demonstrated.

The mutation release writes management-cluster-scoped `ApplyDesire{Type: Delete}` ([`internal/api/kubeapplierapi/types_apply_desire.go:39`](../../internal/api/kubeapplierapi/types_apply_desire.go)) for objects that are not yet deleting and must be removed. Objects already deleting require resolution of their blocker rather than a second DELETE; the kube-applier's deletion path already distinguishes these cases ([`apply_desire/controller.go:441-494`](../../kube-applier/pkg/controllers/apply_desire/controller.go)). The namespace is already `Terminating` and is never re-sent.

No new kube-applier desire type is to be introduced until the inspection phase demonstrates that the observed blockers require one. Should targeted finalizer removal prove unavoidable, it is to be designed at that point, keyed to an exact object UID and a named, allowlisted case. It must never accept an arbitrary GVR or finalizer string from the request, and must never perform a blanket `metadata.finalizers: null`.

### 6.1 RBAC as the effective constraint

The characterisation of the mutation release as using only existing primitives holds for the desire *types* and does not hold for the *permissions*.

The kube-applier apply controller accepts the GVR verbatim from a desire and applies no code-side allowlist. [`kube-applier/deploy/templates/clusterrole.yaml`](../../kube-applier/deploy/templates/clusterrole.yaml) is consequently the sole constraint on what may be acted upon.

Currently granted and relevant: `core/namespaces` (full verbs, which covers the namespace read performed by the inspection release), `core/secrets`, `core/configmaps`, `hypershift.openshift.io/*`, `multitenancy.acn.azure.com/podnetworks` and `podnetworkinstances`, `cluster.open-cluster-management.io/managedclusters`, and `velero.io/schedules` and `backups`.

**Not granted**, and plausibly implicated in a wedged namespace:

- `policy/poddisruptionbudgets` — blocked PodDisruptionBudget teardown is identified in §1 as a known root cause, and the mutation release could not delete one.
- `cluster.x-k8s.io` and `infrastructure.cluster.x-k8s.io` — CAPI and CAPZ machines and Azure infrastructure objects, which are probable finalizer holders on a namespace leaking Azure resources.
- `apps` (deployments, statefulsets, replicasets), `core/pods`, `core/serviceaccounts`, `rbac.authorization.k8s.io`, `batch`, and `monitoring.coreos.com`.
- The `namespaces/finalize` subresource, with the consequence that the operation presently performed manually by SREs cannot be performed by the kube-applier at all.

The mutation release is therefore contingent upon a ClusterRole expansion whose blast radius belongs to the kube-applier maintainers. That consultation should be initiated in parallel with the inspection release rather than subsequent to it, as it may prove slower to conclude than the implementation.

## 7. Delivery plan

### 7.1 First release — inspection, no management-cluster mutation

1. A new management-cluster-scoped Cosmos API type under `internal/api/fleetapi`, with deepcopy, registry entry, validators and `// Written by:` annotations.
2. Admin API POST and GET handlers on the routes defined in §8, inheriting the existing pre-mux authentication and audit middleware.
3. A backend controller reconciling the object: preflight only, comprising ARM Cosmos and CS point reads, the surviving-desire scan, and findings written to `status`.
4. A management-cluster-scoped `ReadDesire` on the target Namespace, enabling the controller to observe the live object.
5. Terminal-state lifecycle handling per §8.1: release of the `ReadDesire` and retention-based deletion of the request itself.

Item 4 warrants explicit note: the inspection release does write one document to the kube-applier container. A `ReadDesire` constitutes a read of the management cluster and mutates nothing there, but a characterisation of the release as writing nothing beyond status would be inaccurate. It is also the sole means by which the release delivers its stated value, since without observing the namespace there are no findings to report.

**Findings granularity.** A `ReadDesire` reads exactly one object by exact name — the kube-applier runs a per-instance list/watch and informer scoped to that name ([`types_read_desire.go`](../../internal/api/kubeapplierapi/types_read_desire.go)). No list or enumeration primitive exists. Findings are therefore derived from the Namespace's own `status.conditions` (`NamespaceDeletionContentFailure`, `NamespaceDeletionDiscoveryFailure`, `NamespaceContentRemaining`, `NamespaceFinalizersRemaining`), which report remaining content at **group/resource granularity**. Individual object names and their finalizers would require a new list-capable primitive and are out of scope for this release.

The `status` schema is accordingly structured around a list of group/resource pairs rather than a list of objects. This is the most consequential decision in the release: a Cosmos API type, once shipped, is the most expensive element of this design to change subsequently.

Each remaining group/resource is annotated with whether the kube-applier ClusterRole presently grants delete upon it. The annotation is advisory and gates nothing; a corresponding test parses the live manifest and fails on divergence in either direction, so the annotation cannot silently become stale.

This release carries no risk to any management cluster, is independently reviewable, and produces the evidence the design still requires: the per-namespace blocker inventory for the twenty stuck namespaces.

### 7.2 Second release — mutation

Delivered one proven case at a time and contingent upon the findings of the first release. This release carries the `informers.go` registration fix (§3.1), the `ResourceReference.UID` precondition (§5.2), and whatever ClusterRole expansion the findings demonstrate to be necessary (§6.1). It emits a metric and an audited action log per cleanup, and alerts on elevated cleanup frequency. Manual cleanup remains the fallback throughout.

**Sequencing.** The second release is gated on data that does not exist until the first release has been merged and exercised against stage. The two releases are therefore sequential and must not be combined into a single change set. The informer registration fix and the UID precondition are the exceptions: both are independently verifiable defects requiring no findings, and either may be delivered ahead of or in parallel with the first release.

### 7.3 Rollout and validation

**Rollout:** stage cases first, followed by a production canary.

**Testing:** end-to-end on personal dev against the admin API directly, using [`test/util/framework/admin_api_helpers.go`](../../test/util/framework/admin_api_helpers.go) and the patterns established in [`test/e2e/admin_api.go`](../../test/e2e/admin_api.go). Geneva is not a prerequisite.

**Geneva:** the Action definition is registered in the Geneva portal under Azure-UnifiedGASO, outside this repository. [`docs/ops/delete-node-pool-via-geneva.md`](delete-node-pool-via-geneva.md) illustrates the form such a registration takes. It is authored once the endpoint shape is stable, and invokes a single command rather than a sequence of raw `kubectl patch` calls.

## 8. API surface

| Method | Route | Result |
| --- | --- | --- |
| `POST` | `/admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/stuckclustercleanups` | Creates, or returns, the cleanup object. Returns its name. |
| `GET` | `/admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/stuckclustercleanups/{name}` | Returns phase, findings, current blocker and final verification. |

Input comprises the stamp, the management cluster, and the exact terminating namespace name. Optional CS cluster ID, ARM ID or Azure resource identifiers strengthen ownership proof. A namespace prefix is never accepted as ownership proof.

Deduplication is performed on management cluster, namespace name and namespace UID. The UID is knowable only after the namespace has been read, and the POST handler therefore cannot deduplicate upon it synchronously: the handler deduplicates on management cluster and namespace name, and the controller records and enforces the UID once observed.

`spec.namespaceUID` is present in the type from the first release notwithstanding that the first release performs no mutation, on the grounds that it serves both as the deduplication key and as the deletion precondition of the second release, and that its addition to a live type at a later point is materially more expensive than carrying it from the outset.

### 8.1 Object lifecycle

Because the object is stamp-rooted, `DeleteOrphanedCosmosResources` cannot collect it by its subscription-prefix traversal. The property that makes the design viable also entails that nothing garbage-collects the object, and the same applies to the management-cluster-scoped desires the controller creates. Lifecycle management is therefore the controller's own responsibility.

`Inspected` and `Refused` are terminal phases. `Failed` is not: it denotes an out-of-band error expected to be transient, and the next resync retries.

On reaching a terminal phase the controller performs two actions, in this order:

1. **Releases the `ReadDesire`** it created for the target namespace. Without this, every invocation would leave a permanent informer watch on a namespace that no longer exists. The desire is deleted before the document, because the document holds the only reference to it and deleting the document first would strand the desire permanently.
2. **Records `status.completedAt`**, which starts the retention clock. The timestamp is written once and never re-stamped; re-stamping on each subsequent pass would continually defer expiry and the document would never be retired.

Once `completedAt` has aged past the retention period the controller deletes the document. The period is 14 days: long enough that findings from an inspection remain readable into the following week, and short enough that a fleet-wide sweep does not accumulate without bound.

A terminal document carrying no `completedAt` — one written before the field existed — is given a fresh clock rather than being retired on sight.

## 9. Scope and open questions

### 9.1 First release scope

The sixteen **namespace-only orphans**: ARM Cosmos and CS records both absent, with a management-cluster namespace already `Terminating`.

Out of scope for the first release, each requiring a separate case definition:

- The four record-backed clusters.
- The production cluster whose HostedCluster still exists with a stalled deletion.
- Azure resource reclamation (§1.1).
- Unattended detection. A subsequent detector may reuse this object and controller without alteration to the safety contract.

### 9.2 Open questions

1. Can CS provide a reliable point lookup confirming that no record and no active publisher remains for a namespace-only orphan? The action does not call CS DELETE.
2. Which Azure resources are owned by CS as against HyperShift, CAPZ and SWIFT, and which identifiers survive once both ARM Cosmos and CS records are absent? This is the entry point for the reclamation workstream and is not a blocker for the first release.
3. Which of the current twenty are genuinely namespace-only, and which GVRs remain within each namespace? The first release answers this at group/resource granularity.

## Appendix: The normal deletion path

Retained as the baseline from which this cleanup deviates.

The frontend DELETE handler writes a `DeletionTimestamp`, a delete-operation document and `Deleting` state within a single Cosmos transaction. Backend controllers react: `ClusterClusterServiceDeleteDispatch` calls CS DELETE, while `ClusterResources` ceases fetching CS manifests and runs its [apply-desire removal chain](../../backend/pkg/controllers/clusterresources/apply_desire_removal_chain.go) — dropping NodePool desires first to halt reapplication, then requesting deletion of the ManagedCluster, ingress manifests, HostedCluster, PodNetworkInstance, PodNetwork and finally the two cluster namespaces. The per-management-cluster kube-applier consumes those desires and reports success only once the object is absent. `ClusterClusterServiceIDClearer`, `ClusterChildResourcesCleanup` and `ClusterDeletionController` converge and remove the cluster document, and `OperationClusterDelete` marks the ARM operation `Succeeded`.

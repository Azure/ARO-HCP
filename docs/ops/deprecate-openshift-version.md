# Deprecate an OpenShift Minor Version

This procedure follows the current discovery, catalog and retirement
[implementation](../cosmos-data-flow.md#backend-fleet-control-plane-version-rollout).
Deprecation has two separate stages: stop new admission and catalog publication
first, then retire internal channel state after the fleet drains. Neither stage
upgrades or deletes customer clusters.

**Current implementation boundary:** public-floor validation and backend catalog
publication/retirement are implemented. Frontend version GET/LIST still query
Cluster Service. Their cutover to the Resources catalog is upcoming, so a public
floor change does not yet remove versions from those endpoints. Do not declare
endpoint deprecation complete based only on catalog persistence.

## Policy Controls

Both controls are compiled constants in
[`internal/versionpolicy/policy.go`](../../internal/versionpolicy/policy.go),
currently `4.20`. They compare major/minor, ignoring patch and prerelease; they are
not runtime configuration or emergency feature flags.

| Constant | Current effect | When to advance |
|---|---|---|
| `MinimumPublicVersion` | Frontend rejects creates and changed cluster-version requests below the floor; backend catalog publication hides those minors. Frontend GET/LIST filtering is part of the upcoming catalog cutover. | First, after manually proving every affected cluster retains a valid next upgrade target. |
| `MinimumBackendVersion` | Discovery stops seeding unreferenced older channels; retirement may delete older rollout documents only when unreferenced. | Separately, after old-version dependencies have drained. Keep it at or below the public floor. |

These are distinct from the selector's per-channel exact-version minimums in
[`RolloutConfig.MinimumVersions`](../../backend/pkg/controllers/cluster/version/rollout/config.go).
Changing a policy floor does not select an exact release or force a minor upgrade.

## 1. Prove a Valid Next Target

Before raising the public floor, inventory affected clusters in **every region
and environment receiving the change**. Record the proposed floor, current build,
requested channel/minor, observed control-plane and node-pool versions, provider
desired version, SRE pins/release thresholds, and experimental exact overrides.
Include deleting resources and in-flight upgrades; rollout aggregate counts alone
are insufficient because they exclude deleting clusters.

For every remaining cluster, prove that at least one target at/above the proposed
public floor is valid under the existing admission and upgrade rules. Check the
one-minor-step constraint, active control-plane history, node-pool skew (including
deleting pools), channel constraints, and the backend-mirrored SPC
`Status.DesiredVersionChannels`. Confirm that the target channel has a usable
Fleet `Spec.BestExactVersion`; public listing alone does not prove upgrade
compatibility. Relevant sources are
[cluster validation](../../internal/validation/validate_cluster.go),
[cluster admission](../../internal/admission/admit_cluster.go), and
[minor assignment](../../backend/pkg/controllers/cluster/version/rollout/minor_upgrade_normal_desired_version_controller.go).

**Do not jump the floor past a required intermediate minor.** For example, raising
the public floor from `4.20` to `4.22` while a cluster still needs `4.21` strands its
ordinary upgrade path: `4.21` becomes inadmissible and `4.22` violates one-minor skew.
Migrate those clusters through valid intermediate versions first or defer the floor
change. Advancing one minor at a time still requires checking channel availability
and node-pool compatibility.

This is a **manual fleet prerequisite**, not an implemented runtime global gate.
The frontend does not scan the fleet to approve a floor change, and catalog
publication does not wait for fleet-wide drain or health. Do not interpret an empty
catalog or zero rollout counts as proof that this prerequisite passed.

An empty `DesiredVersionChannels` currently skips that admission check, and
candidate/nightly requests bypass it. Those exceptions are not evidence of a valid
upgrade path; obtain actual compatibility evidence before approving deprecation.

Removing the numeric major-version ceiling allows creation on future minor versions
in the stable/fast channel groups; it does not authorize every cross-major upgrade.
Existing node-pool cross-major upgrade checks still require experimental release
features. Verify the entire control-plane and node-pool upgrade path before
selecting a cross-major target for an ordinary subscription; catalog membership
alone is insufficient.

## 2. Advance the Public Floor

1. Change only `MinimumPublicVersion` in a reviewed code change; leave the backend
   floor unchanged so old channels continue to be discovered and processed.
2. Deploy both frontend and backend builds carrying that policy. Frontend enforces
   admission; backend filters the persisted catalog. Frontend GET/LIST remain on
   Cluster Service until the upcoming cutover. Account for mixed builds during
   deployment. For Int/Stage/Prod, promote through `sdp-pipelines` and the deployment
   pipelines at [aka.ms/arohcp-pipelines](https://aka.ms/arohcp-pipelines); a source
   merge alone does not apply the change.
3. Verify that new creates and changed version requests below the floor are
   rejected, and that the Resources catalog no longer contains the retired minor.
   Repeat admission checks with ordinary and experimental-release subscriptions.
   GET/LIST withdrawal is a separate verification step after the frontend cutover,
   not expected behavior of the current handlers.
4. Verify unrelated updates to existing below-floor clusters still work when their
   stored version profile **and** experimental exact override remain unchanged.
   This grandfathering bypasses only the new floor check, not other validation.
   Changing channel group, patch/exact override or nightly build is not an
   unchanged-version exemption.

Existing clusters are not automatically moved by this change. Continue their
normal upgrade or deletion workflow, retaining internal rollout state throughout.

## 3. Drain Internal References

Use the [reference scanner](../../backend/pkg/controllers/cluster/version/rollout/rollout_references.go)
as the checklist, not only the public cluster version or current desired version:

- Cluster `CustomerProperties.Version.ID` and `.ChannelGroup`.
- Cluster `ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion`.
- Every Cluster `Status.ActiveVersions[].Version` entry.
- SPC `Spec.ControlPlaneVersion.DesiredVersion`.
- SPC `Spec.PinnedVersion.ExactVersion`.
- Every SPC `Status.ControlPlaneVersion.ActiveVersions[].Version` entry.

`Spec.PinnedVersion.UntilExactVersion` is a comparison threshold against best in
the pinned channel, not a dependency on the threshold's minor version. It is not
used for seeding or retirement references, though it remains relevant when planning
pin release and upgrades.

SPCs derive their channel group from the parent Cluster. Deleting documents still
count until they disappear; every stored active-version entry counts even if the
current desired version has advanced. This is not all historical HostedCluster
history: the observation controller replaces the active set using history through
the first Completed entry (normally only that entry in steady state). The scanner
uses the stored set, not a fresh management-cluster read. Nightly references follow
the same structured-profile seeding and retention policy as other allowed groups.

NodePool and SPNP versions are not control-plane rollout references. Their requested
and observed versions still matter for the manual valid-target/skew prerequisite.
Node-pool active status reflects observed per-node OCP versions, not append-only
history; missing or empty usable observations leave previously stored status
unchanged. Do not mistake those retained observations for proof of current drain.

Malformed versions and SPCs whose parent channel group cannot be resolved produce
errors **alongside known references**. Additive seeding still queues known profiles
and logs enumeration errors for the next periodic pass. Retirement fails closed
on any scan error and deletes nothing in that pass. Cluster/SPC add events and
version-reference-changing updates use targeted cache reads to enqueue normalized
`coreapi.VersionProfile` values; unrelated status churn does not enqueue work.

The seeder's `Run` waits for cache sync, then starts two independent producer loops:
HTTP discovery, and cached reference inventory plus legacy-profile backfill and
Controller-status cleanup. Each runs immediately and then on a five-minute ticker;
calls to the same producer never overlap. Producers share the seeder's controller
identity; enumeration failures are logged for the next tick and do not increment
queue retry/reconcile metrics. Workers live-read and create/backfill rollouts from
the profile-only queue; write failures, including conflicts, use rate-limited queue
retries. Missing below-floor rollouts require a fresh cached reference check before
creation.

Fleet rollouts store their minor version and channel group in
[`Spec.Version`](../../internal/api/fleetapi/types_control_plane_version_rollout.go)
as canonical major/minor `ID` and `ChannelGroup`, matching the document's Cincinnati
channel name. The reference producer also inventories legacy documents with missing
profiles, even unused below-floor rollouts. The
[`NormalizeRolloutVersion` adapter](../../internal/apihelpers/fleetapihelpers/rollout_version.go)
derives those profiles from old names. Workers persist backfill with the live ETag,
preserving metadata, selected best and status. Publication and retirement adapt
legacy identities at their read boundaries and then use structured profiles.
Partial profiles, unsupported groups and name/profile mismatches are errors.

Retirement supersedes backfill for below-backend-floor, unreferenced documents.
Their legacy identity can be validated and the document deleted directly. A queued
backfill rechecks references before recreating a missing below-floor document.

After an error-free inventory, the reference producer removes a cluster's legacy
seeder Controller status only when the cache shows valid structured rollouts for
all that cluster's references. This prevents stale Degraded conditions from
surviving the controller transition while retaining health state until repair is
observed. Cleanup errors are logged and retried at the next producer tick.
See the [migration tests](../../internal/apihelpers/fleetapihelpers/rollout_version_test.go)
and [seeder lifecycle tests](../../backend/pkg/controllers/cluster/version/rollout/rollout_seeding_controller_test.go)
for preservation, concurrency and cleanup behavior.

Wait for normal convergence/deletion and confirm that all references below the
proposed backend floor are gone across the target fleet. Do not erase pins, active
history or deleting documents merely to make GC pass; investigate remaining
dependencies and allow their owning lifecycle controllers to resolve them.

## 4. Advance the Backend Floor

1. After the drain evidence is complete, raise `MinimumBackendVersion` in a separate
   reviewed and deployed change, no higher than the public floor.
2. Observe `ControlPlaneVersionRolloutSeeding`, `ControlPlaneVersionRolloutRetirement`,
   and `OpenShiftVersionCatalog` logs/reconcile metrics. Maintenance starts after
   cache sync and repeats every five minutes. Seeder producer/cleanup failures
   appear in logs and retry at the next tick; seeder worker writes, retirement
   passes and catalog reconciliation use queue error retries. Informer lag and
   errors can delay completion beyond that interval.
3. Confirm that below-floor, unreferenced Fleet `ControlPlaneVersionRollout`
   documents disappear. Referenced channels must remain, even below the floor;
   seeding can repair a missing referenced channel. At/above-floor channels remain
   even if the graph-data archive no longer contains them.
4. Confirm catalog convergence in the regional **Resources** singleton
   `/providers/microsoft.redhatopenshift/openshiftversioncatalogs/default`, partition
   `microsoft.redhatopenshift`. It is backend-owned internal data, not an ARM
   resource to delete or edit manually.

Retirement is internal garbage collection, not a fleet-wide stop switch. It
validates references and the entire structured rollout inventory before deleting
anything in a pass. Unsupported groups, malformed profiles and name/profile
mismatches block the pass; nightly follows the shared policy. It uses eventually
consistent informer inventories, not a transactional global lock. The
reference-retention safeguard does not replace the manual drain prerequisite.
Deletes tolerate 404; another delete error stops the pass and retries without
rolling back earlier deletions. See the
[retirement controller](../../backend/pkg/controllers/cluster/version/rollout/rollout_retirement_controller.go).

## API Verification

For all registered API versions, the regional endpoints are:

```text
GET /subscriptions/{subscriptionId}/providers/Microsoft.RedHatOpenShift/locations/{location}/hcpOpenShiftVersions?api-version={apiVersion}
GET /subscriptions/{subscriptionId}/providers/Microsoft.RedHatOpenShift/locations/{location}/hcpOpenShiftVersions/{name}?api-version={apiVersion}
```

**Current behavior:** the
[`ArmResourceListVersion` and `GetOpenshiftVersions` handlers](../../frontend/pkg/frontend/frontend.go)
call Cluster Service `ListVersions`/`GetVersion`. They do not read the Resources
catalog, and their responses cannot verify catalog publication or floor filtering.
Validate current admission behavior and inspect the backend-owned snapshot
separately. A missing, empty or unresolved catalog does not currently impose
catalog-derived HTTP status codes on these endpoints.

**Upcoming frontend cutover:** after the scheduled change to catalog-backed reads
is implemented and deployed, verify GET/LIST withdrawal below the public floor,
subscription visibility for experimental channels, canonical public names, and
the missing/empty/unresolved-catalog response contract against that implementation.
Those endpoint checks are not implemented behavior in this revision. Do not apply
the later catalog-backed API's bootstrap/status expectations to the current
Cluster Service handlers.

For backend verification now, availability means a non-nil Fleet
`Spec.BestExactVersion`, not completed upgrades or recent successful discovery.
The publisher waits for initial Fleet informer sync, not discovery's first
successful pass, so it can persist `entries: []` before rollouts are seeded.
Unresolved entries remain present with `Available: false`; the publisher has no
all-channel resolution gate. The generic projection honors any selected nightly
rollout, but the existing Cincinnati selector does not resolve nightly releases,
graph-data typically has no nightly definitions, and no CI releasestream downloader
is added. The nightly exact-pin admission rule is unchanged.

If the snapshot does not converge, trace the seeder's archive fetch/validation and
owned queue, best-version selection, Fleet informer sync, and catalog publication
as diagnostic dependencies, not a guaranteed controller execution schedule.
Existing resolved state can survive graph outages; there is no freshness TTL.
Do not manually populate the catalog or delete reference state to force progress.

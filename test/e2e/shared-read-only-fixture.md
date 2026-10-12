# Run-owned read-only cluster

Specs labeled `labels.RequireSharedReadOnlyCluster` borrow one default 20240610
cluster per `aro-hcp-tests run-suite` or standalone `run-test` invocation. The
extension runner's selected-spec BeforeAll callback triggers provisioning;
listing and runs with no selected borrower do not create anything. Multiple
borrowers use the same fixture, including when the extension starts a separate
`run-test` process for each spec. This does not use Ginkgo suite hooks.

The outer RunE wrapper owns setup and teardown. It assigns a random run ID and
owner PID before executing the command. Only that owner can provision. Workers
with inherited run metadata validate their parent PID and read the manifest;
missing, unready, expired, cross-run, or mismatched-environment manifests fail
closed. Do not set the coordination environment variables manually or carry
them into a later run.

Setup uses an independent, error-returning `RunResourceContext`, not
`NewTestContext`, and reuses the existing infrastructure, managed-identity, and
cluster creation helpers. Identities are dedicated to the fixture customer RG,
regardless of `POOLED_IDENTITIES`; no identity-pool slots are leased. Owner setup
also creates one default node pool with one replica, using the restriction-aware
VM SKU selector. This adds one shared worker and node-pool provisioning time to
every fixture invocation, not a new cluster or worker per borrower. No external
authentication or workload is installed.

## Publication and borrowing

An atomic, mode-0600 JSON manifest lives in a unique directory beneath
`ARTIFACT_DIR` (the system temporary directory if unset for local runs).
`ARO_HCP_SHARED_FIXTURE_MANIFEST`, `ARO_HCP_TEST_RUN_ID`, and
`ARO_HCP_TEST_RUN_OWNER_PID` identify it to subprocesses. The manifest includes
the subscription, cluster ID/name/location, node pool ID/name, customer and managed RGs, routing
environment, owner, expiration, and readiness. It contains no credentials.
Manifest version 2 requires the node pool identity and rejects older cluster-only
manifests. Readiness is published only after cluster provisioning succeeds, its
managed RG exists, and node pool provisioning succeeds with the expected ID.
Node-pool setup failure fails the run and invokes owner cleanup, including the
already-created cluster. Listing and runs without borrowers still provision nothing.

`framework.SharedReadOnlyCluster(ctx)` returns a value-copy manifest and an
RG-bound client exposing only GET and LIST. Every operation revalidates the
manifest. Borrowers must not instantiate an unrestricted SDK client for fixture
mutations. They never register the parent RG with a test context or delete it.
`framework.SharedGuardedNegativeCluster(ctx)` exposes only
`RejectPatch20240610`, `RejectNodePoolPatch20240610`, and
`RejectExternalAuthIssuer20240610`, and `RejectNegativeNodePoolReplicas20240610`,
not an unrestricted mutation client. The PATCH helpers take a PATCH body
and the required Azure error status, code, target, message, and optional error
details. `GetNodePool20240610` reads only the manifest's shared node pool. A context-bounded
file lock next to the manifest serializes the entire GET/PATCH/GET sequence across
worker processes, using the same lock for cluster and node-pool writes. The helper checks the typed Azure rejection and compares the
complete decoded GET responses, including read-only fields. Unexpected acceptance,
an incorrect rejection, a failed post-GET, or changed state quarantines the
manifest by setting readiness false; subsequent borrowers fail closed. It does
not attempt to repair the cluster. The outer runner still owns cleanup.

The guarded client uses the standard HCP Azure pipeline and API version, but
sends an explicit JSON PATCH because the generated SDK update model omits
immutable fields. P1-2 changes `properties.dns.baseDomainPrefix` and expects HTTP
400 / `InvalidRequestContent`, targeting that property with `field is immutable`.
P1-3 changes `properties.platform.osDisk.sizeGiB` to another valid size and expects
HTTP 400 / `MultipleErrorsOccurred` with two `InvalidRequestContent` immutable
details targeting `properties.platform` and `properties.platform.osDisk`. It
compares the complete node-pool GET before and after the attempt. The node pool
belongs to the owner: borrowers never delete it, lease identities, or register its
parent RG for cleanup. Standard owner cluster/RG teardown removes the pool.
P1-4 uses the same fixture lock to PUT an external-auth child with a unique name
and `invalid-url` issuer. It first requires typed HTTP 404 / `ResourceNotFound`
for that child, then requires HTTP 400 / `InvalidRequestContent` targeting
`properties.issuer.url` with `must be https URL`. Success also requires the child
to remain absent and the complete parent GET response to remain unchanged.
An unexpectedly present child triggers bounded, cancellation-independent cleanup
of only that child; any failed assertion quarantines the fixture even if cleanup
succeeds. Cleanup errors retain the resource ID and last observation, and the
outer owner remains responsible for the tagged parent RG. No borrower registers
the shared RG for test cleanup or enables cluster-wide external authentication.

P2-1 reuses this child-create guard for a unique node pool with `replicas: -1`.
The borrower copies only the known-good platform, version, and auto-repair
configuration from the owner-provisioned node pool into a new CREATE payload;
it never PUTs or deletes the shared node pool. The RP must return typed HTTP
400 / `InvalidRequestContent`, targeting `properties.replicas` with
`must be greater than or equal to 0`. Typed HTTP 404 / `ResourceNotFound` is
required for the new child both before and after the attempt, and the parent
GET must remain unchanged. Both child kinds use the same fixture lock, quarantine,
and bounded failure-cleanup implementation; initial child absence is required
before the helper can take ownership of any cleanup.

Read-only borrowers do not acquire the write lock: they cannot change state, but
may fail if a regression unexpectedly accepts a guarded write. General child-owning
and successful parent mutations remain unsupported. A killed worker can leave state
uncertain before quarantine is written; do not reuse the fixture after a worker
crash. OS process exit releases its file lock, not Azure resources.

## Teardown and failures

The wrapper invalidates readiness then runs cleanup with a fresh bounded context,
even after partial provisioning or test failure. Cleanup verifies the exact
`runID.aro-hcp-ci.redhat.com` ownership tag before invoking the existing standard
RP/RG cleanup workflow. Setup and cleanup errors both fail the command. The
initial RG PUT includes run ownership, E2E, 24-hour expiration, and (in Prow) job
tags. `SHARED_DIR/tracked-resource-group_<name>` records the RG for existing CI
backup cleanup. Retained manifests identify the owner and resources for debugging.

SIGINT/SIGTERM cancel setup and allow bounded cleanup. SIGKILL, pod loss, or Azure
cleanup failure cannot guarantee immediate deletion: existing expired-E2E/job
resource cleanup must recover the tagged RG. Local operators without that
periodic cleanup must remove a leaked tagged RG themselves. Do not delete an RG
whose run tag does not match the retained manifest.

## Selection and validation

The first consumer is `cluster_get_rp.go` (P0-2). Its labels are
`RequireSharedReadOnlyCluster`, `High`, `Positive`, `AroRpApiCompatible`, and
`MIContainers(0)`. It is selected by the DEV RP-compatible parallel gate without
`Slow`, `CreateCluster`, or `DevelopmentOnly`. Suites explicitly restricted to
`RequireNothing` do not automatically select it; their policies are unchanged.
The zero MI demand describes the borrower, not the dedicated run-owned identities.

The stacked P1-2 consumer `cluster_update_immutable_reject.go` uses the same labels
except `Negative` instead of `Positive`, and shares this cluster without creating
any additional resources.

The P1-3 consumer `nodepool_update_immutable_reject.go` uses the same negative
labels and `MIContainers(0)`, with no `Slow` label. Node pool provisioning happens
in owner setup, before the borrower runs, and is shared by all consumers.

The P1-4 consumer `external_auth_invalid_issuer_reject.go` has the same negative
labels and `MIContainers(0)`, without `Slow`. It creates no additional fixture
resources: its unique child PUT must be rejected.

The P2-1 consumer `nodepool_create_validation_reject.go` uses the same negative
labels and `MIContainers(0)`, without `Slow`. Negative replica CREATE validation
is distinct from the existing upper-bound and autoscaling-limit tests; this
borrower does not create another cluster or a successful node pool.

When adding consumers, run the pinned `golangci-lint fmt`, `make lint`, framework
unit tests, and `UPDATE=true go test ./test/cmd/aro-hcp-tests/...` followed by the
same command without UPDATE. Review suite-listing golden changes and prove
selection with the compiled runner. Offline coordination tests exercise actual
extension commands and child processes without Azure. Live provisioning still
requires the normal E2E environment and Azure credentials, including local runs.

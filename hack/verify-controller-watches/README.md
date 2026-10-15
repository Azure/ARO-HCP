# Controller watch invariants

This static checker follows backend and Fleet bootstrap wiring to typed controller
registrations, includes conditionally enabled registrations, and compares Go
document types:

- `missing-watch`: a lister/indexer/CRUD read or write has no corresponding watch.
- `unused-watch`: a watch has no corresponding lister/indexer/CRUD read or write.

Admin API is loaded but currently has no configured controller bootstrap entrypoint.
HTTP handlers are not controllers. Polling controllers with no watches
are included. Non-Cosmos controllers naturally have empty sets.

## Commands

```sh
make verify-controller-watches
make update-controller-watches
go run ./hack/verify-controller-watches -all
go run ./hack/verify-controller-watches -json
go test ./hack/verify-controller-watches/...
```

Verification compares current findings with `baseline.txt`. Additions and removals
both require regenerating and reviewing the baseline. The file contains only
`service/controller | invariant | fully-qualified Go document type`; moving code
or adding another call site for the same violation does not change it. New baseline
entries are allowed through normal review. Verification does not edit the file.

The normal root `make verify` target depends on `verify-controller-watches` and
uses the checked-in `baseline.txt`. Prow's `aro-hcp-verify` step invokes that target.
The module is included in `go.work`, so the normal `make test-unit` workspace
discovery also runs the real-library end-to-end suite. Baseline regeneration is
only performed by the explicit `make update-controller-watches` target.

Diagnostics show every distinct offending site and the shortest discovered call
path to it. Missing watches show the actual typed access operation. Unused watches
show the registration/constructor path to the watch call. `-all` includes findings
already in the baseline; `-json` includes complete accessed/watched inventories,
findings, and coverage gaps and does not perform baseline verification.

## Analysis model

`analyzer.New()` returns a `go/analysis.Analyzer`. Package facts hold serializable
function/type summaries. The bootstrap package specializes those summaries
using constructor parameters, object fields, closures, generic arguments, and
concrete interface receivers. The standalone driver loads packages once and
computes each package's facts once. It does not execute application code or
connect to Cosmos, Azure, or Kubernetes.

Production roots are `backend/cmd.RunRootCmd` and `fleet/cmd/controller.run`.
The evaluator follows options completion, concrete storage construction, context
creation, and registry iteration. It intercepts the invocation of each typed
`Registration.Instantiate`, using the arguments actually assembled by its caller.
Thus backend's per-controller context copy and storage-field replacement are part
of the analyzed path. Struct assignments copy fields while pointer/map bindings
retain identity. The `Enabled` field is treated as absent during bootstrap so all
registered controllers are included. Startup storage accesses establish bindings
but are not charged to a controller's access set.

Recognized boundaries include typed Cosmos CRUD/listers, generic transaction
create/replace calls, informer/lister accessors, indexers, the generic watching
controller, event-handler registration, and Kubernetes worker/retry callbacks.
Typed CRUD operations and lister/informer contracts are trusted endpoints: their
implementations are not traversed for additional resource dependencies. Returning
a client is not an endpoint. Client-returning helper bodies are followed, so a
helper such as `KubeApplierDBClients.For` exposes its `ManagementCluster` lister
read without a method-name special case. Shared informer list/watch plumbing is
not attributed to every consumer. Event-routing metadata is not a read.

The narrow bookkeeping exemption is for `Controller` document accesses beneath
framework calls to `WriteController` / `DegradedControllerPanicHandler` in backend
controllerutils and Fleet base wrappers. A concrete controller's own call to those
helpers, and aggregators reading other controllers' documents, still count.

Go type identity ignores parent scope, resource names, container instance, event
filters, and ARM resource-type strings. Typed constructors resolve explicit nil
optional informers. Runtime branches are combined; this is a type-dependency
check, not proof that every configuration or event filter triggers reconciliation.
Potential transaction operations count even if execution later fails or is skipped.

## Coverage and limitations

This is a best-effort, registration-specialized analysis, not a sound whole-program
pointer analysis. Loops are traversed over known collection elements or one symbolic
element, recursion is bounded, and runtime control flow is overapproximated. The
analysis follows reachable methods rather than every method on a controller type.
It retains concrete return alternatives and function callbacks in collections.
Package import reachability prunes bodies that cannot reach the endpoint packages.
Import reachability is not sufficient for indirect calls: in-repo function/interface
dispatch and its local forwarding chains are retained, and supplied function
callbacks are followed even when a third-party body is unavailable. Such external
callbacks are conservatively treated as potentially invoked; arbitrary third-party
interface protocols are not inferred.

Client implementation bindings come from bootstrap construction and injection.
There is no sole-factory fallback or `KubeApplierDBClients.For` special case.
Unresolved non-endpoint storage interface calls produce coverage diagnostics.
Typed CRUD/lister accessors retain their trusted result types even when a fixture
supplies only their public interface. New service bootstrap entrypoints must be
added explicitly; arbitrary command/plugin frameworks are not automatically discovered.

Reflection, dynamically assembled wiring, arbitrary third-party callback frameworks,
and arbitrary pointer aliasing are not modeled. Recognized boundaries are maintained
in `analyzer/evaluate.go`; new infrastructure patterns need fixtures.

Untyped Cosmos queries/deletes do not identify a Go document type. These produce
explicit **analysis incomplete** diagnostics with call paths. Incomplete controller
findings are labeled in detailed reports. Gaps are not fabricated resource types
or silently added to the invariant baseline. They are currently warnings while
coverage is assessed; a passing baseline check does not assert complete coverage.
Load/type-check failures fail the command.
Inferring resource types from untyped queries is explicitly deferred. The separate
incomplete fixture checks their diagnostics without pretending to resolve them.

The baseline against `a7458c817` with the Fleet registration precursor discovers 124 controllers
(112 backend, 12 Fleet), 305 findings, and 32 untyped-access sites across 11
controllers. Representative checks were reviewed against cluster creation,
operation controllers, deletion callback maps, metrics indexer/lister consumers,
Fleet polling and shared informer management. The baseline records
the current analyzer's results, not a claim that every existing finding is a bug.

## Real-library regression fixture

`TestRealLibraryRegistrationAndInjection` loads `testdata/controllers` with the
actual registry, backend wrapper, generic watching controller, Cosmos CRUDs,
informers, listers, and API types. No library definitions are copied into this test.
Nine registrations pin independently written expectations:

- `first`: reads Cluster and Subscription, writes NodePool; watches Cluster and
  ServiceProviderCluster.
- `second`: the same controller implementation, injected with a different lookup;
  reads Cluster and ExternalAuth, writes NodePool; watches the same two types.
- `inherited`: retains the shared context's lookup, proving a previous entry's
  `instanceContext := shared` mutation does not leak into the shared context.
- `wrapped`: uses the actual backend wrapper, reads Cluster and
  ServiceProviderCluster, and inherits watches for those types and
  ManagementClusterContent. Framework status writes must remain exempt.
- `operations`: generic Subscription reads through two helper paths, transaction
  create/replace sites, and an explicit Controller status write with no watches.
  Evidence retains all distinct sites and the shortest path to the shared helper.
- `callbacks`: alternative concrete implementations, promoted methods, callbacks
  stored under dynamic map keys, and a real Kubernetes wait callback. The forwarding
  helper package has no imports of storage; interface dispatch must survive pruning.
  An uncalled method must not add its resource type to the inventory.
- `indexed`: real informer event registration and indexer reads, with event-routing
  metadata handling that does not introduce spurious resource accesses.
- `optional-nil` / `optional-watch`: the same promoted syncer reads provider state;
  only the non-nil constructor argument installs its optional watch.

The test asserts exact inventories, exact findings, evidence paths, and zero gaps.
These expectations are not regenerated with the production baseline. Core-library
refactors must preserve them or deliberately revise the fixture's stated intent.

All regression tests run end-to-end through package loading, real-library facts,
bootstrap, injection, controller execution, and reporting. There are no unit tests
that construct private evaluator values or reproduce core-library interfaces.
The suite round-trips actual package facts through serialization and builds the
CLI to verify baseline generation, known-finding suppression, missing-entry errors,
stale-entry rejection, and unchanged baseline identity with extra evidence sites.
`BootstrapIncomplete` separately checks unknown-informer and untyped-CRUD warnings.
Use `-entrypoints` with fully qualified function names to run a fixture bootstrap
through the command; pass its package and relevant library packages explicitly.

## Performance and lint integration

The runner reports package-loading and analysis durations separately. Initial local
loading measured about 5.3 seconds; warm runs were approximately 0.8 seconds loading
and 0.7 seconds analysis. These are local observations, not CI guarantees. Using
the prebuilt runner, one measured warm run took 1.52 seconds wall time and about
493 MiB peak RSS. The initial load was not an isolated empty-cache benchmark.
With warm loading no longer dominating the total, standalone verification is the initial
integration. It uses existing Go build/export caches; no additional persistent cache
is required.

The analyzer exports package facts and returns structured results, so a future
golangci-lint module plugin can use the host's analysis passes instead of invoking
`packages.Load` again. A plugin adapter must handle baseline suppression and stale
entries, and retain full evidence in diagnostics. The standalone runner remains
useful for baseline generation and JSON reports.

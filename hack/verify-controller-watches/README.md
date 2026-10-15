# Controller watch invariants

This static checker starts at the typed `newControllerRegistry` entrypoints in
backend and Fleet, includes conditionally enabled registrations, and compares Go
document types:

- `missing-watch`: a lister/indexer/CRUD read or write has no corresponding watch.
- `unused-watch`: a watch has no corresponding lister/indexer/CRUD read or write.

Admin API is included in package discovery; it currently has no applicable
registry. HTTP handlers are not controllers. Polling controllers with no watches
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

Diagnostics show every distinct offending site and the shortest discovered call
path to it. Missing watches show the actual typed access operation. Unused watches
show the registration/constructor path to the watch call. `-all` includes findings
already in the baseline; `-json` includes complete accessed/watched inventories,
findings, and coverage gaps and does not perform baseline verification.

## Analysis model

`analyzer.New()` returns a `go/analysis.Analyzer`. Package facts hold serializable
function/type summaries. The registration package specializes those summaries
using constructor parameters, object fields, closures, generic arguments, and
concrete interface receivers. The standalone driver loads packages once and
computes each package's facts once. It does not execute application code or
connect to Cosmos, Azure, or Kubernetes.

Recognized boundaries include typed Cosmos CRUD/listers, generic transaction
create/replace calls, informer/lister accessors, indexers, the generic watching
controller, event-handler registration, and Kubernetes worker/retry callbacks.
Client navigation alone is not an access. `KubeApplierDBClients.For` is an important
exception: its production implementation is followed because resolving a container
reads `ManagementCluster` through a lister. Shared informer list/watch plumbing is
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
Reflection, dynamically assembled wiring, arbitrary third-party callback frameworks,
and arbitrary pointer aliasing are not modeled. Recognized boundaries are maintained
in `analyzer/evaluate.go`; new infrastructure patterns need fixtures.

Untyped Cosmos queries/deletes do not identify a Go document type. These produce
explicit **analysis incomplete** diagnostics with call paths. Incomplete controller
findings are labeled in detailed reports. Gaps are not fabricated resource types
or silently added to the invariant baseline. They are currently warnings while
coverage is assessed; a passing baseline check does not assert complete coverage.
Load/type-check failures fail the command.

The initial run at the Fleet registration precursor discovers 124 controllers
(112 backend, 12 Fleet), 303 findings, and 32 untyped-access sites across 11
controllers. Representative checks were reviewed against cluster creation,
operation controllers, deletion callback maps, metrics indexer/lister consumers,
StackDesires, Fleet polling, and shared informer management. The baseline records
the current analyzer's results, not a claim that every existing finding is a bug.

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

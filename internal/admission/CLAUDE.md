# internal/admission

This package runs admission for ARM resources (cluster, node pool, …) after
static validation in `internal/validation/`. Each resource has two entry
points:

- `MutateXxx(ctx, admissionContext, op, newObj, oldObj) field.ErrorList` —
  applies admission-time mutations (defaulting, generated fields, projecting
  tags into internal state). Runs before validation.
- `AdmitXxx(ctx, admissionContext, op, newObj, oldObj) field.ErrorList` —
  performs the non-static checks that require server-side state.

## File and function organization

Mirror `admit_nodepool.go` when adding admission for a new resource:

- **One exported entry point per phase**: `MutateXxx` / `AdmitXxx`. Both take
  `op operation.Operation` and handle CREATE and UPDATE in the same call —
  branch on `op.Type` inside the per-struct helpers, not at the top level.
- **One function per struct in the resource tree**: top-level functions
  delegate to `mutateXxxProperties` → `mutateXxxPlatform` → … each owning
  exactly the struct passed in. Use `safe.Field(oldObj, validation.ToXxx)`
  to thread the old object down one struct at a time.
- **Aggregate errors as you go**: each helper returns `field.ErrorList`; the
  caller `errs = append(errs, helper(...)...)`. Never short-circuit on the
  first error — admission should report every problem in one pass so the
  user can fix them all.
- **Field paths**: each helper takes the `fldPath *field.Path` for the
  struct it is admitting; nested helpers extend with `.Child(...)`. Paths
  must match what static validation reports so users can correlate errors.

## Admission-context constructor signatures

Constructors that build the admission context for a resource (e.g.
`frontend.newNodePoolAdmissionContext`, `frontend.newClusterAdmissionContext`)
must keep this signature even when the current body is trivial:

```go
func (f *Frontend) newXxxAdmissionContext(
    ctx context.Context,
    op operation.Operation,
    /* required inputs */ ...,
) (*admission.XxxAdmissionContext, error)
```

Keep `ctx` and the `error` return even if neither is used right now. These
constructors are the place where future context-dependent or fallible setup
(DB lookups, feature gates) will land. Trimming the signature forces a wider
edit later and breaks callers that already thread `ctx`/`err`.

If a bot review (Copilot, etc.) suggests dropping `ctx` or the `error`
return, leave the signature as is and treat the comment as
resolved-by-rejection.

## Build context ahead of time; never reach for the DB during admission

`internal/admission` must not depend on the database client. All server-side
state that admission needs is **prefetched by the frontend** and passed in
via the `XxxAdmissionContext` struct.

Concretely:

- `XxxAdmissionContext` carries plain data: the parent cluster, the
  service-provider cluster, the list of related node pools, the
  subscription, etc. — not interfaces and not DB handles.
- The frontend's `newXxxAdmissionContext` constructor performs every DB
  lookup needed for admission (the cluster, the service-provider records,
  the node pool list with their service-provider counterparts, …) and
  populates the struct. It is the single chokepoint where admission's
  prerequisites are loaded.
- Admission code reads only from the context struct. If `AdmitXxx` finds it
  needs more state, add the field to the context struct and load it in the
  constructor — do not add a DB client parameter.

This keeps admission deterministic and unit-testable: tests build a
`XxxAdmissionContext` literal and call `AdmitXxx` directly, with no DB
mock in scope. It also keeps the cost of admission predictable — every
lookup happens once, before validation, instead of being scattered through
the admit functions.

## The frontend must never reach the management cluster; mirror through ServiceProviderCluster

The frontend (and therefore admission, which the frontend drives) must
**never** have direct access to kube-applier, a `ReadDesireLister`, Maestro,
or any management-cluster / HostedCluster Kubernetes API. The frontend is an
ARM-facing service in the service cluster; giving it a path to the management
clusters would widen its blast radius and break the isolation boundary between
the RP and the clusters it manages. Do not add such a client to
`Frontend`, and do not import `backend/pkg/kubeapplierhelpers` (or anything
that reads ReadDesires) from `frontend/` or `internal/admission/`.

When admission needs data that only exists on the management cluster — for
example the observed `HostedCluster.status.version.desired.channels` — the
**backend** (which legitimately watches the management clusters via the
kube-applier ReadDesire mirror) must observe it and copy a distilled form onto
the `ServiceProviderCluster` document in Cosmos. The frontend then prefetches
`ServiceProviderCluster` in `newClusterAdmissionContext` like any other
server-side state, and admission reads it from the context struct.

Concrete example in this package: `admitClusterVersionID` validates a
version.id change against `ServiceProviderCluster.Status.DesiredVersionChannels`.
That field is populated by the backend `ControlPlaneActiveVersions` controller
from the observed HostedCluster — the frontend never talks to the management
cluster to obtain it. Any future "admission needs live cluster state" case must
follow the same pattern: backend observes → mirrors onto `ServiceProviderCluster`
→ frontend prefetches → admission reads from context.

## Tests

- Unit tests live next to the implementation (`admit_xxx_test.go`).
- Build the admission context as a struct literal — do not call the
  frontend constructor from admission tests. The constructor is the
  frontend's responsibility; admission tests cover the per-struct logic.
- Cover both CREATE and UPDATE in the same test where the logic differs,
  driven off `op.Type`.

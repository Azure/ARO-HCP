# 05 &mdash; Controllers

The `kube-applier` binary runs four controllers. Three are conventional
informer-driven controllers. The fourth (`ReadDesireInformerManagingController`)
is a controller-of-controllers that owns lifecycles of per-resource sub
controllers.

All four live under `kube-applier/pkg/controllers/`.

## Shared infrastructure

### Reuse from `backend/pkg/controllers/controllerutils`

- `controllerutils.Controller` interface
  (`backend/pkg/controllers/controllerutils/util.go:37-40`):

  ```go
  type Controller interface {
      SyncOnce(ctx context.Context, keyObj any) error
      Run(ctx context.Context, threadiness int)
  }
  ```

- `controllerutils.GenericSyncer[T]` and `genericWatchingController[T]`
  (`generic_watching_controller.go:48-245`) &mdash; we reuse the workqueue,
  worker, and event-handler scaffolding wholesale.

- `controllerutils.CooldownChecker`
  (`controllerutils/cooldown.go:29-60`) &mdash; used by the per-instance
  ReadDesire kubernetes controller for the unconditional 1-minute resync.

### Replace `Controller`-based Degraded with conditions on the desire

The existing controllerutils path writes `Degraded` to a Cosmos
`api.Controller` document via `ReportSyncError` /
`WriteController`. For kube-applier we instead set
`Degraded` directly on the relevant `*Desire.Status.Conditions`.

Add a small helper, `kube-applier/pkg/controllers/conditions/conditions.go`:

```go
func SetSuccessful(conds *[]metav1.Condition, err error)
func SetSuccessfulWaitingForDeletion(conds *[]metav1.Condition, deletionTime metav1.Time, uid types.UID)
func SetDegraded(conds *[]metav1.Condition, err error)
```

Each helper:

- Uses `meta.SetStatusCondition` (which preserves the timestamp on no-op
  changes).
- Assigns `Reason`/`Message` per the readme's table:
  - kube-apiserver call failed &rarr; `Reason: KubeAPIError`,
    `Message: <kube error>`.
  - call could not be issued &rarr; `Reason: PreCheckFailed`,
    `Message: <reason>`.

### Status writeback

Add a generic `kube-applier/pkg/controllers/statuswriter` helper:

```go
type StatusWriter[T any] interface {
    UpdateStatus(ctx context.Context, lister Lister[T], key string, mutate func(*T)) error
}
```

The implementation:

1. Reads the latest object from the local lister cache.
2. Applies `mutate(obj)` on a deep copy.
3. Skips the write if `reflect.DeepEqual(old.Status, new.Status)` &mdash; the
   common case after a successful no-op sync.
4. Calls `database.ResourceCRUD[T].Replace(...)` with the cached etag.
5. On 412 (etag mismatch), drops the update and returns nil &mdash; the
   informer will requeue.

This mirrors `controllerutils.WriteController`
(`util.go:239-279`) but is generic over the desire type and skips the
"create if missing" branch &mdash; the kube-applier never creates desires.

## 5.1 ApplyDesireController

Trigger sources:

- `ApplyDesire` informer events (Add/Update only; Delete is a no-op since
  we're not the deleter).
- Workqueue retries with `DefaultTypedControllerRateLimiter` (cap exponential
  backoff at 5 minutes).

Per the readme, sync logic is:

```
1. Read the ApplyDesire from lister, decode .spec.kubeContent into an
   *unstructured.Unstructured.
2. Resolve GVR via RESTMapper from the parsed kubeContent's GVK.
3. Server-side-apply with Force=true and FieldManager="kube-applier" via
   the dynamic client:
       dyn.Resource(gvr).Namespace(ns).Apply(ctx, name, obj, applyOpts)
4. On success: SetSuccessful(conds, nil).
   On error:   SetSuccessful(conds, err)   // err -> KubeAPIError
   On a pre-check failure (RESTMapper miss, namespace mismatch, malformed
   kubeContent): SetSuccessful(conds, err with PreCheckFailed reason).
5. Write status via statuswriter.
```

Notes:

- Use `k8s.io/client-go/dynamic.NewForConfig(rest.InClusterConfig())` once at
  binary startup.
- Build a `RESTMapper` from `discovery.NewDiscoveryClientForConfig` &mdash; a
  `restmapper.DeferredDiscoveryRESTMapper` so that newly-installed CRDs work
  on the next call without restart.
- `ApplyOptions.FieldManager = "kube-applier"`. Force = true.
- The pre-check vs. kube-API split is done in the helper:
  - `meta.NoKindMatchError`, JSON parse errors, malformed `TargetItem` &rarr;
    `PreCheckFailed`.
  - Anything returned by `dyn.Apply` &rarr; `KubeAPIError`.

## 5.2 DeleteDesireController

Trigger sources:

- `DeleteDesire` informer events.
- 60-second resync (per readme: "must resync every 60 seconds"). This is the
  `SharedIndexInformer.ResyncPeriod` plus a per-key requeue loop &mdash;
  pick one. Recommended: rely on the informer's resync alone, set to 60s for
  this informer specifically.

Sync logic (from readme):

```
get target
  not found             -> SetSuccessful(true)
  has deletion timestamp -> SetSuccessful(false, "WaitingForDeletion",
                            msg="deletionTimestamp=<t> uid=<u>")
  no deletion timestamp -> issue Delete; if delete fails -> KubeAPIError
                            re-issue get
                              still not found -> SetSuccessful(true)
                              has deletion timestamp -> WaitingForDeletion
```

Implementation tip: a single pass through this state machine should never
need more than one delete call.

UID + deletionTimestamp must be carried in the message verbatim (the readme
specifies this explicitly so that consumers can correlate without separately
querying the cluster).

## 5.3 ReadDesireInformerManagingController

A controller-of-controllers. There is no existing equivalent in the repo, so
this is the most novel piece.

State held:

```go
type ReadDesireInformerManager struct {
    readDesireLister listers.ReadDesireLister
    kubeConfig       *rest.Config
    dynamicClient    dynamic.Interface
    restMapper       meta.RESTMapper
    statusWriter     statuswriter.StatusWriter[kubeapplier.ReadDesire]
    crud             database.ResourceCRUD[kubeapplier.ReadDesire]
    mgmtCluster      string

    mu           sync.Mutex
    runningByKey map[string]*runningReadDesire
}

type runningReadDesire struct {
    targetItem kubeapplier.ResourceReference
    cancel     context.CancelFunc
    controller *ReadDesireKubernetesController
}
```

Sync logic for a `ReadDesire` key:

```
1. Fetch ReadDesire from lister.
   - missing -> stop and discard runningByKey[key], return.
2. Compare ReadDesire.Spec.TargetItem with runningByKey[key].targetItem.
   - same         -> nothing to do.
   - different    -> stop existing controller; create + Run new one.
   - missing      -> create + Run new one.
3. Construction-failure path only (factory.Build returned PreCheckError or
   similar): write Successful=False with reason PreCheckFailed. No
   per-launch condition is written on the success path — steady-state
   Successful comes from the per-instance controller.
```

The "stop and discard" path **must** wait for the goroutine to actually
exit before launching the new one, otherwise we leak listers/informers. Use
`controller.Run(ctx)` and rely on `ctx.Done()` for shutdown.

The manager itself is a `genericWatchingController[string]` keyed by the
ReadDesire's resource ID. On binary shutdown it iterates `runningByKey` and
cancels each child context.

## 5.4 ReadDesireKubernetesController (per ReadDesire)

One instance per `ReadDesire`. Holds:

```go
type ReadDesireKubernetesController struct {
    readDesireKey string
    targetItem    kubeapplier.ResourceReference
    readLister    listers.ReadDesireLister  // shared
    crud          database.ResourceCRUD[kubeapplier.ReadDesire]

    // Per-instance kube cache.
    informer cache.SharedIndexInformer
    lister   cache.GenericLister
    queue    workqueue.TypedRateLimitingInterface[string]
}
```

Construction:

- Build a single-item `ListWatch` against `dynamic.Resource(gvr).Namespace(ns)`
  with `metav1.SingleObject(metav1.ObjectMeta{Name: name})` so we are *not*
  list-watching all objects of the type.
- Wrap in a `cache.SharedIndexInformer` using
  `cache.NewSharedIndexInformerWithOptions` &mdash; we get reflector,
  resync, and store for free.
- Add an event handler that always queues the (single) key.

Run loop (per readme):

```
- Trigger conditions:
    a) Informer event (Add/Update/Delete).
    b) Unconditional 60-second tick (so a missing object can be reflected
       in status).

- On each sync:
    1. Read the live object from the kube lister (may be nil if absent).
    2. Read the ReadDesire from the readDesireLister.
    3. Marshal the live object to RawExtension. If absent, leave a sentinel
       (e.g. RawExtension{Raw: nil}).
    4. If new RawExtension differs from ReadDesire.Status.KubeContent
       (byte-equal compare), write the new status and SetSuccessful(true).
       Otherwise no-op.
    5. On any kube error en route, SetSuccessful(false, "KubeAPIError"/"PreCheckFailed").
```

Stop behaviour: when the parent manager calls `cancel()`, the workqueue is
shut down and the informer goroutine returns. The manager's `runningByKey`
entry is then removed.

## Wiring summary

```
kube-applier main:
  cfg          := rest.InClusterConfig()
  dyn          := dynamic.NewForConfig(cfg)
  rm           := restmapper.DeferredDiscoveryRESTMapper(...)
  cosmos       := database.NewDBClient(...)
  scopedListers:= newKubeApplierScopedListers(cosmos, mgmtCluster)
  informers    := informers.NewKubeApplierInformers(ctx, scopedListers)

  applyCtl     := NewApplyDesireController(informers, dyn, rm, cosmos, mgmtCluster)
  deleteCtl    := NewDeleteDesireController(informers, dyn, rm, cosmos, mgmtCluster)
  readMgr      := NewReadDesireInformerManagingController(informers, dyn, rm, cosmos, mgmtCluster)

  go informers.RunWithContext(ctx)
  go applyCtl.Run(ctx, 4)
  go deleteCtl.Run(ctx, 4)
  go readMgr.Run(ctx, 4)
```

Threadiness defaults to 4 across the board, matching backend conventions.
The `ReadDesireInformerManager` itself only needs threadiness 1 since it is
not parallelism-sensitive (the per-instance controllers run independently
in their own goroutines).

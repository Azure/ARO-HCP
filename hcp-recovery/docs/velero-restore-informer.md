# Velero Restore Informer

## Problem

After the hcp-recovery controller creates a Velero Restore, it is never notified when the restore completes. The controller only watches HCPRecovery custom resources via a generated informer. With no watch on Velero Restores, the controller only re-reconciles on the informer relist interval (~5 minutes), leaving the `VeleroRestoreCompleted` condition unset until then.

The restore name is deterministic (`restore-{recoveryName}`), so mapping a Restore event back to the owning HCPRecovery is straightforward — strip the `restore-` prefix to get the HCPRecovery name, and the namespace is the controller's configured namespace.

## Why Velero's generated informers are not available

The idiomatic client-go approach would be to import Velero's generated clientset and `SharedInformerFactory`, the same way we import our own generated `hcprecoveryinformers.SharedInformerFactory`. However, Velero v1.17.2 migrated its internals to controller-runtime and **no longer publishes generated clientset or informer code**. The packages `github.com/vmware-tanzu/velero/pkg/generated/clientset/versioned` and `github.com/vmware-tanzu/velero/pkg/generated/informers/externalversions` do not exist. The `pkg/generated/` directory only contains gRPC plugin protobuf stubs.

This means we need to build a custom informer for Velero Restores.

## Options

### 1. Dynamic informer

Use `dynamicinformer.NewFilteredDynamicSharedInformerFactory` from `k8s.io/client-go/dynamic/dynamicinformer`, scoped to the `velero` namespace, watching the `restores.velero.io/v1` GVR.

```go
import (
    "k8s.io/client-go/dynamic"
    dynamicinformer "k8s.io/client-go/dynamic/dynamicinformer"
    "k8s.io/apimachinery/pkg/runtime/schema"
)

dynamicClient, _ := dynamic.NewForConfig(kubeConfig)
factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
    dynamicClient, 300*time.Second, "velero", nil,
)

restoreGVR := schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "restores"}
restoreInformer := factory.ForResource(restoreGVR).Informer()
```

- Returns `*unstructured.Unstructured`, but the event handler only needs the object name to derive the HCPRecovery key, so no type safety is lost in practice.
- No new dependencies — `k8s.io/client-go/dynamic` is already transitively available.
- Simplest to set up.

### 2. Custom typed ListWatch informer

Build a `cache.ListWatch` using a REST client configured for the `velero.io` API group, then create a `cache.NewSharedIndexInformerWithOptions` with typed `velerov1api.Restore`.

```go
import (
    velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/cache"
)

restClient, _ := rest.RESTClientFor(&rest.Config{
    // ...configured for velero.io/v1 API group
})

lw := cache.NewFilteredListWatchFromClient(
    restClient, "restores", "velero", /* tweakListOptions */ nil,
)

informer := cache.NewSharedIndexInformerWithOptions(
    lw, &velerov1api.Restore{},
    cache.SharedIndexInformerOptions{ResyncPeriod: 300 * time.Second},
)
```

- Returns typed `*velerov1api.Restore` objects.
- Follows the pattern used in `backend/pkg/informers/informers.go` (custom `ListWatch` + `cache.NewSharedIndexInformerWithOptions`).
- Requires manually constructing a REST client for the `velero.io/v1` API group with the correct serialization config (content type, negotiated serializer, group version).

### 3. controller-runtime cache

Create a controller-runtime `cache.Cache` scoped to the `velero` namespace, then get an `Informer` for `velerov1api.Restore`. The Velero types are already registered via `recovery.AddToScheme()`.

```go
import (
    "sigs.k8s.io/controller-runtime/pkg/cache"
    ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
    velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

restoreCache, _ := cache.New(kubeConfig, cache.Options{
    Scheme:            drScheme,
    DefaultNamespaces: map[string]cache.Config{"velero": {}},
})

informer, _ := restoreCache.GetInformer(ctx, &velerov1api.Restore{})
```

- Returns typed objects and leverages the scheme already registered in `recovery.AddToScheme()`.
- Mixes controller-runtime caching with the client-go workqueue pattern used by the rest of the controller. The cache has its own lifecycle (Start, WaitForCacheSync) that must be managed alongside the existing informer factories.
- controller-runtime is already a dependency (used for `ctrlclient.Client`), so no new module dependency.

## Event handler mapping (shared across all options)

Regardless of which informer approach is chosen, the event handler maps Restore events to HCPRecovery workqueue keys using the naming convention:

```go
func keyForVeleroRestore(controllerNamespace string) func(obj interface{}) (cache.ObjectName, error) {
    return func(obj interface{}) (cache.ObjectName, error) {
        key, err := cache.DeletionHandlingObjectToName(obj)
        if err != nil {
            return cache.ObjectName{}, err
        }
        recoveryName, ok := strings.CutPrefix(key.Name, "restore-")
        if !ok {
            return cache.ObjectName{}, fmt.Errorf("restore %s not managed by controller", key.Name)
        }
        return cache.ObjectName{Namespace: controllerNamespace, Name: recoveryName}, nil
    }
}
```

This function plugs directly into the existing generic `registerInformer` helper in `helpers.go`, which silently skips restores that don't match the naming convention (key function errors cause early return).

// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package read_desire_kubernetes implements the per-ReadDesire kubernetes
// reflector. One instance is created for each ReadDesire by the manager
// (see ../read_desire_manager). It list/watches a single named object via
// the dynamic client and mirrors its observed state into the ReadDesire's
// .status.kubeContent.
package read_desire_kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

// ResyncDuration is how often a ReadDesireKubernetesController re-evaluates
// even without a fresh kube event, so a missing target object can be reflected
// into status. Aligns with the readme's "every 60 seconds" requirement.
const ResyncDuration = 60 * time.Second

// listWatchWithoutWatchListSemantics opts out of the WatchList streaming mode
// enabled by default in client-go v0.35+. Mirrors the unexported wrapper in
// client-go/tools/cache/listwatch.go. The dynamic client's Watch (whether
// against an apiserver without WatchList support or against a fake) does not
// emit the bookmark events WatchList requires, so the reflector would never
// reach Synced.
type listWatchWithoutWatchListSemantics struct {
	*cache.ListWatch
}

func (listWatchWithoutWatchListSemantics) IsWatchListSemanticsUnSupported() bool { return true }

// ReadDesireKubernetesController reflects a single named kube object into a
// ReadDesire's status. One instance per ReadDesire is owned by the manager.
type ReadDesireKubernetesController struct {
	key        keys.ReadDesireKey
	target     kubeapplier.ResourceReference
	gvr        schema.GroupVersionResource
	namespaced bool

	dyn      dynamic.Interface
	informer cache.SharedIndexInformer
	fetcher  *readDesireFetcher
	writer   desirestatuswriter.StatusWriter[kubeapplier.ReadDesire, keys.ReadDesireKey]

	queue workqueue.TypedRateLimitingInterface[keys.ReadDesireKey]
}

// NewReadDesireKubernetesController constructs a per-ReadDesire kubernetes
// reflector. It builds a single-object ListWatch so the per-instance informer
// touches only the named object — never the whole resource type.
//
// We deliberately do not consult the RESTMapper here: the GVR is taken
// straight from the ReadDesire's targetItem, and the dynamic client is
// trusted to surface a kube error at list/watch time if it does not resolve
// in the cluster. Cluster-vs-namespace scoping is decided by whether
// targetItem.namespace is non-empty.
//
// crudByParent provides a parent-scoped ResourceCRUD per ReadDesire so status
// replaces can be issued under the desire's own cluster/nodepool resource ID
// rather than a sentinel parent.
func NewReadDesireKubernetesController(
	key keys.ReadDesireKey,
	target kubeapplier.ResourceReference,
	dyn dynamic.Interface,
	crudByParent database.KubeApplierReadDesireCRUD,
) (*ReadDesireKubernetesController, error) {
	if len(target.Resource) == 0 || len(target.Version) == 0 || len(target.Name) == 0 {
		return nil, conditions.NewPreCheckError(errors.New("spec.targetItem requires version, resource, and name"))
	}

	fetcher := &readDesireFetcher{crudByParent: crudByParent}
	c := &ReadDesireKubernetesController{
		key:    key,
		target: target,
		gvr: schema.GroupVersionResource{
			Group: target.Group, Version: target.Version, Resource: target.Resource,
		},
		namespaced: len(target.Namespace) > 0,
		dyn:        dyn,
		fetcher:    fetcher,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ReadDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ReadDesireKey]{
				// Underscores rather than slashes: this name surfaces as a
				// Prometheus label and slashes complicate downstream tooling.
				Name: fmt.Sprintf("ReadDesireKubernetesController_%s_%s_%s", key.ClusterName, key.NodePoolName, key.Name),
			},
		),
		writer: desirestatuswriter.New[kubeapplier.ReadDesire, keys.ReadDesireKey, *kubeapplier.ReadDesire](
			fetcher,
			&readDesireReplacer{crudByParent: crudByParent},
		),
	}

	c.informer = cache.NewSharedIndexInformerWithOptions(
		// listWatchWithoutWatchListSemantics opts out of the v0.35+ default that
		// uses streaming WatchList. The dynamic client's Watch on top of the
		// fake doesn't speak the bookmark protocol that mode requires, so the
		// reflector would never reach Synced. Production hits the same issue
		// with non-watchable backends, hence this wrapper for both paths.
		&listWatchWithoutWatchListSemantics{ListWatch: c.singleObjectListWatch()},
		&unstructured.Unstructured{},
		cache.SharedIndexInformerOptions{ResyncPeriod: ResyncDuration},
	)

	// Register the event handler at construction so the SharedIndexInformer
	// has it before its reflector pumps the first List response. Adding it
	// in Run() races with the initial sync.
	if _, err := c.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.queue.Add(c.key) },
		UpdateFunc: func(_, _ any) { c.queue.Add(c.key) },
		DeleteFunc: func(obj any) { c.queue.Add(c.key) },
	}); err != nil {
		return nil, fmt.Errorf("register informer handler: %w", err)
	}
	return c, nil
}

// Run starts the per-instance informer and worker. It blocks until ctx is cancelled.
func (c *ReadDesireKubernetesController) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx).WithValues("readDesire", c.key.Name, "cluster", c.key.ClusterName)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("starting ReadDesireKubernetesController")
	defer logger.Info("stopped ReadDesireKubernetesController")

	go c.informer.RunWithContext(ctx)

	// Wait for the per-instance informer to sync before letting the worker
	// pull from the queue. Standard client-go pattern: a worker that runs
	// against an unsynced cache will see the target as absent and incorrectly
	// publish an empty Status.KubeContent until events finally arrive.
	if !cache.WaitForCacheSync(ctx.Done(), c.informer.HasSynced) {
		logger.Info("per-instance informer cache failed to sync; exiting controller",
			"gvr", c.gvr.String(),
			"namespace", c.target.Namespace,
			"name", c.target.Name)
		return
	}

	// Periodic tick so a missing target gets reported even when no event fires.
	ticker := time.NewTicker(ResyncDuration)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.queue.Add(c.key)
			}
		}
	}()

	// Seed the queue so the first sync runs without waiting for the first
	// event or tick. Safe to do here: the informer's AddEventHandler was
	// registered in the constructor and has already replayed cache contents
	// once HasSynced returned true above.
	c.queue.Add(c.key)

	go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	<-ctx.Done()
}

func (c *ReadDesireKubernetesController) runWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *ReadDesireKubernetesController) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)
	if err := c.SyncOnce(ctx); err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "sync error; requeuing", "key", key)
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	return true
}

// SyncOnce reads the live object from the per-instance informer cache and
// updates the ReadDesire's status if its KubeContent differs.
//
// The HasSynced check below is the same defensive guard kube core controllers
// use: until the reflector has finished its initial List, GetByKey returns
// "does not exist" indistinguishably from a real absence. Acting on that
// would publish a "kube object missing" status for an object that simply
// hasn't been observed yet. Skipping until HasSynced lets us tell those two
// cases apart — once true, GetByKey's exists==false really means the object
// is not there.
func (c *ReadDesireKubernetesController) SyncOnce(ctx context.Context) error {
	// we wait until we've synced at least once so that we can be sure a "not found" means the content doesn't exist
	// instead of "we haven't observed the content yet".
	if !c.informer.HasSynced() {
		utils.LoggerFromContext(ctx).Info("per-instance informer not yet synced; skipping",
			"gvr", c.gvr.String(),
			"namespace", c.target.Namespace,
			"name", c.target.Name)
		return nil
	}

	desire, err := c.fetcher.Fetch(ctx, c.key)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if desire == nil {
		return nil
	}

	// Pull the live object from the per-instance informer cache. The store key
	// for an Unstructured is namespace/name (or just name for cluster-scoped).
	//
	// We read from the informer cache rather than re-Getting against
	// kube-apiserver so a noisy ReadDesire doesn't load the apiserver. The
	// cache is fed by an informer scoped to a single (gvr, namespace, name),
	// so it tracks at most one object. Don't change the resource pointed at
	// by spec.targetItem out from under a running ReadDesire — but if you do,
	// the next published .status.kubeContent will reflect what was observed
	// for the new target.
	storeKey := c.target.Name
	if c.namespaced {
		storeKey = c.target.Namespace + "/" + c.target.Name
	}
	rawObj, exists, err := c.informer.GetStore().GetByKey(storeKey)
	if err != nil {
		return c.writer.UpdateStatus(ctx, c.key, func(d *kubeapplier.ReadDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, fmt.Errorf("read cache: %w", err))
		})
	}

	// newRaw left nil when exists==false. A nil slice is the signal for
	// "kube object does not exist": the .status.kubeContent publish branch
	// below clears the pointer to nil, which the API contract treats as
	// "absent or not yet observed" (disambiguated by the Successful
	// condition).
	var newRaw []byte
	if exists {
		obj, ok := rawObj.(*unstructured.Unstructured)
		if !ok {
			return c.writer.UpdateStatus(ctx, c.key, func(d *kubeapplier.ReadDesire) {
				conditions.SetSuccessful(&d.Status.Conditions, conditions.NewPreCheckError(
					fmt.Errorf("informer cached unexpected type %T", rawObj)))
			})
		}
		newRaw, err = json.Marshal(obj)
		if err != nil {
			return c.writer.UpdateStatus(ctx, c.key, func(d *kubeapplier.ReadDesire) {
				conditions.SetSuccessful(&d.Status.Conditions, fmt.Errorf("marshal observed object: %w", err))
			})
		}
	}

	// No-op if the new payload is byte-equal to the existing status. A nil
	// pointer collapses to a nil Raw for the comparison so "absent stays
	// absent" doesn't trip the publish branch.
	var existingRaw []byte
	if desire.Status.KubeContent != nil {
		existingRaw = desire.Status.KubeContent.Raw
	}
	if bytes.Equal(newRaw, existingRaw) {
		// Still ensure Successful=True so a freshly-launched controller flips
		// the condition out of Unknown into True on the first cycle.
		return c.writer.UpdateStatus(ctx, c.key, func(d *kubeapplier.ReadDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, nil)
		})
	}

	return c.writer.UpdateStatus(ctx, c.key, func(d *kubeapplier.ReadDesire) {
		if newRaw == nil {
			d.Status.KubeContent = nil
		} else {
			d.Status.KubeContent = &runtime.RawExtension{Raw: append([]byte(nil), newRaw...)}
		}
		conditions.SetSuccessful(&d.Status.Conditions, nil)
	})
}

// singleObjectListWatch builds a ListWatch scoped to the single named object
// using metav1.SingleObject. The reflector treats this as a 1-element collection
// and only reflects that object.
func (c *ReadDesireKubernetesController) singleObjectListWatch() *cache.ListWatch {
	resource := c.dyn.Resource(c.gvr)
	var kubeResourceAccessor dynamic.ResourceInterface = resource
	if c.namespaced {
		kubeResourceAccessor = resource.Namespace(c.target.Namespace)
	}
	fieldSelector := metav1.SingleObject(metav1.ObjectMeta{Name: c.target.Name}).FieldSelector
	return &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return kubeResourceAccessor.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			return kubeResourceAccessor.Watch(ctx, options)
		},
	}
}

// readDesireFetcher implements desirestatuswriter.Fetcher by going to a
// live Cosmos client per call. See the apply_desire counterpart for why
// the lister cache is the wrong source here.
type readDesireFetcher struct {
	crudByParent database.KubeApplierReadDesireCRUD
}

var _ desirestatuswriter.Fetcher[kubeapplier.ReadDesire, keys.ReadDesireKey] = &readDesireFetcher{}

func (f *readDesireFetcher) Fetch(ctx context.Context, key keys.ReadDesireKey) (*kubeapplier.ReadDesire, error) {
	crud, err := f.crudByParent.ReadDesires(key.ResourceParent())
	if err != nil {
		return nil, fmt.Errorf("crud for parent %v: %w", key.ResourceParent(), err)
	}
	return crud.Get(ctx, key.Name)
}

// readDesireReplacer implements desirestatuswriter.Replacer over a
// KubeApplierReadDesireCRUD. See the apply_desire counterpart for why
// the parent must be derived per-call instead of fixed at construction.
type readDesireReplacer struct {
	crudByParent database.KubeApplierReadDesireCRUD
}

var _ desirestatuswriter.Replacer[kubeapplier.ReadDesire] = &readDesireReplacer{}

func (r *readDesireReplacer) Replace(ctx context.Context, desired *kubeapplier.ReadDesire) error {
	key, err := keys.ReadDesireKeyFromResourceID(desired.GetResourceID())
	if err != nil {
		return fmt.Errorf("derive key for replace: %w", err)
	}
	crud, err := r.crudByParent.ReadDesires(key.ResourceParent())
	if err != nil {
		return fmt.Errorf("crud for parent %v: %w", key.ResourceParent(), err)
	}
	if _, err := crud.Replace(ctx, desired, nil); err != nil {
		return err
	}
	return nil
}

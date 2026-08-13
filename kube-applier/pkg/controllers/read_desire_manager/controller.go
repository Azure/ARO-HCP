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

// Package read_desire_manager implements the ReadDesireInformerManagingController.
//
// It watches the ReadDesire informer and, for every key, owns the lifecycle of a
// per-ReadDesire ReadDesireKubernetesController. When a ReadDesire's TargetItem
// changes, the manager stops the old per-instance controller (waiting for its
// goroutine to exit) and starts a fresh one.
package read_desire_manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/read_desire_kubernetes"
)

// DefaultResyncPeriod is the maximum interval between two reconciles of a
// ReadDesire whose Cosmos etag has not changed. If content changes, the
// controller reconciles immediately; otherwise it schedules a resync after
// this duration. 10 minutes matches apply_desire's default.
const DefaultResyncPeriod = 10 * time.Minute

// ReadDesireInformerManagingControllerName is the per-controller identifier
// emitted in the "controller_name" log key and used as the workqueue name.
// Mirrors the backend convention.
const ReadDesireInformerManagingControllerName = "ReadDesireInformerManagingController"

// Config tunes the manager's resync behavior. Zero-valued fields take the
// Default* constants; tests pass shorter durations.
type Config struct {
	// ResyncPeriod is the maximum time between re-reconciles for a desire
	// whose Cosmos etag has not changed. See DefaultResyncPeriod.
	ResyncPeriod time.Duration
}

func (c Config) withDefaults() Config {
	if c.ResyncPeriod == 0 {
		c.ResyncPeriod = DefaultResyncPeriod
	}
	return c
}

// PerInstanceController abstracts the per-ReadDesire kube reflector so the
// manager can be tested with a fake.
type PerInstanceController interface {
	Run(ctx context.Context)
}

// PerInstanceFactory builds a per-ReadDesire controller. The default factory
// constructs a ReadDesireKubernetesController via realPerInstanceFactory;
// tests pass a recording fake.
type PerInstanceFactory interface {
	Build(key keys.ReadDesireKey, target kubeapplierapi.ResourceReference) (PerInstanceController, error)
}

// ReadDesireInformerManagingController watches ReadDesires and manages the
// per-instance kubernetes reflectors.
//
// Reconcile cadence:
//
//   - Add, Update, and Delete events queue immediately.
//   - The informer's ResyncPeriod (set to cfg.ResyncPeriod) controls how
//     often unchanged items are re-delivered, guaranteeing periodic
//     reconciliation.
//   - On error the workqueue's rate limiter requeues the key with backoff.
type ReadDesireInformerManagingController struct {
	name               string
	readDesireInformer cache.SharedIndexInformer
	fetcher            *readDesireFetcher
	factory            PerInstanceFactory
	writer             desirestatuswriter.StatusWriter[kubeapplierapi.ReadDesire, keys.ReadDesireKey]
	queue              workqueue.TypedRateLimitingInterface[keys.ReadDesireKey]

	cfg Config

	// running tracks the live per-instance ReadDesireKubernetesController for
	// each ReadDesire by its key. The map is mutated only under mu. SyncOnce
	// reads it to decide whether to spawn a fresh per-instance controller,
	// stop+respawn when TargetItem changed, or no-op when the running entry
	// already matches the desire.
	mu      sync.Mutex
	running map[keys.ReadDesireKey]*runningInstance
}

type runningInstance struct {
	target kubeapplierapi.ResourceReference
	cancel context.CancelFunc
	done   chan struct{}
}

// NewReadDesireInformerManagingController constructs a manager that uses the
// supplied dynamic client for every per-instance controller it spawns.
//
// crudByParent provides a parent-scoped ResourceCRUD per ReadDesire so status
// replaces from each spawned per-instance controller can be issued under
// each desire's own cluster/nodepool resource ID rather than a sentinel
// parent. The manager itself only writes status on construction failure
// (Successful=False with reason PreCheckFailed); steady-state status
// — including KubeContent — comes from the per-instance controllers.
//
// cfg's zero values get the Default* constants. Production callers may pass
// Config{} directly; tests substitute shorter durations and a fake clock.
func NewReadDesireInformerManagingController(
	readDesireInformer cache.SharedIndexInformer,
	dyn dynamic.Interface,
	crudByParent kubeappliercosmosstorage.KubeApplierReadDesireCRUD,
	cfg Config,
) (*ReadDesireInformerManagingController, error) {
	cfg = cfg.withDefaults()
	fetcher := &readDesireFetcher{crudByParent: crudByParent}
	c := &ReadDesireInformerManagingController{
		name:               ReadDesireInformerManagingControllerName,
		readDesireInformer: readDesireInformer,
		fetcher:            fetcher,
		factory:            &realPerInstanceFactory{dyn: dyn, crudByParent: crudByParent},
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ReadDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ReadDesireKey]{Name: ReadDesireInformerManagingControllerName},
		),
		writer: desirestatuswriter.New[kubeapplierapi.ReadDesire, keys.ReadDesireKey, *kubeapplierapi.ReadDesire](
			fetcher,
			&readDesireReplacer{crudByParent: crudByParent},
		),
		cfg:     cfg,
		running: map[keys.ReadDesireKey]*runningInstance{},
	}

	logger := utils.DefaultLogger()
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(ReadDesireInformerManagingControllerName)...)

	if _, err := readDesireInformer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.handleAdd(obj) },
		UpdateFunc: func(oldObj, newObj any) { c.handleUpdate(oldObj, newObj) },
		DeleteFunc: func(obj any) { c.handleDelete(obj) },
	}, cache.HandlerOptions{
		Logger:       &logger,
		ResyncPeriod: &cfg.ResyncPeriod,
	}); err != nil {
		return nil, fmt.Errorf("register informer handler: %w", err)
	}
	return c, nil
}

// SetFactory swaps the per-instance controller factory. Intended for tests.
func (c *ReadDesireInformerManagingController) SetFactory(f PerInstanceFactory) { c.factory = f }

// realPerInstanceFactory is the production PerInstanceFactory: it builds a
// real ReadDesireKubernetesController against the supplied dynamic client
// and CRUD provider.
type realPerInstanceFactory struct {
	dyn          dynamic.Interface
	crudByParent kubeappliercosmosstorage.KubeApplierReadDesireCRUD
}

var _ PerInstanceFactory = &realPerInstanceFactory{}

func (f *realPerInstanceFactory) Build(
	key keys.ReadDesireKey, target kubeapplierapi.ResourceReference,
) (PerInstanceController, error) {
	return read_desire_kubernetes.NewReadDesireKubernetesController(key, target, f.dyn, f.crudByParent)
}

// Run starts the workers. Threadiness > 1 is supported but not necessary —
// the manager's work is bookkeeping, while the per-instance controllers run
// in their own goroutines.
func (c *ReadDesireInformerManagingController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()
	defer c.stopAll()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("starting controller")
	defer logger.Info("stopped controller")

	if threadiness < 1 {
		threadiness = 1
	}
	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	<-ctx.Done()
}

// handleAdd queues every observed Add unconditionally.
func (c *ReadDesireInformerManagingController) handleAdd(obj any) {
	d, ok := obj.(*kubeapplierapi.ReadDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

// handleUpdate enqueues the key unconditionally. The informer's
// ResyncPeriod controls how often unchanged items are re-delivered.
func (c *ReadDesireInformerManagingController) handleUpdate(_, newObj any) {
	newD, newOK := newObj.(*kubeapplierapi.ReadDesire)
	if !newOK {
		return
	}
	c.enqueue(newD)
}

// handleDelete queues every observed Delete unconditionally so the
// per-instance controller stops promptly. The DeleteFinalStateUnknown
// wrapper appears when the cache evicted the object before delivery, and
// we still want to drive a stop in that case.
func (c *ReadDesireInformerManagingController) handleDelete(obj any) {
	d, ok := obj.(*kubeapplierapi.ReadDesire)
	if !ok {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			d, _ = t.Obj.(*kubeapplierapi.ReadDesire)
		}
	}
	if d == nil {
		return
	}
	c.enqueue(d)
}

func (c *ReadDesireInformerManagingController) enqueue(d *kubeapplierapi.ReadDesire) {
	key, err := keys.ReadDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.queue.Add(key)
}

func (c *ReadDesireInformerManagingController) runWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *ReadDesireInformerManagingController) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	// Seed the per-reconcile logger with the key's identifying fields so every
	// log line from SyncOnce carries subscription_id / resource_group /
	// resource_id, matching the backend generic worker loop's behavior.
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)

	if err := c.SyncOnce(ctx, key); err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "sync error; requeuing", "key", key)
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	return true
}

// SyncOnce reconciles one ReadDesire by ensuring its per-instance controller
// is running with the desired TargetItem.
func (c *ReadDesireInformerManagingController) SyncOnce(ctx context.Context, key keys.ReadDesireKey) error {
	desire, err := c.fetcher.Fetch(ctx, key)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return err
	}
	if desire == nil {
		c.stopByKey(key)
		return nil
	}

	c.mu.Lock()
	cur, exists := c.running[key]
	c.mu.Unlock()

	target := desire.Spec.TargetItem
	if exists && cur.target == target {
		// Already running with the right target — nothing to do.
		return nil
	}
	if exists {
		c.stopByKey(key)
	}

	per, err := c.factory.Build(key, target)
	if err != nil {
		// PreCheckError or any other construction failure: record it on status,
		// don't enter a Running state.
		return c.writer.UpdateStatus(ctx, key, func(d *kubeapplierapi.ReadDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
		})
	}

	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c.mu.Lock()
	c.running[key] = &runningInstance{target: target, cancel: cancel, done: done}
	c.mu.Unlock()

	go func() {
		defer utilruntime.HandleCrash()
		defer close(done)
		per.Run(childCtx)
	}()

	// No status write here. The manager used to publish a WatchStarted
	// condition on every (re)launch, but that timestamp turned out to be
	// uninterpretable to consumers — they cannot distinguish "the watcher
	// just (re)launched because the desire changed" from "the kube-applier
	// process restarted." Steady-state ReadDesire status comes from the
	// per-instance controller's Successful/KubeContent writes; on
	// construction failure the early return above records Successful=False.
	return nil
}

func (c *ReadDesireInformerManagingController) stopByKey(key keys.ReadDesireKey) {
	c.mu.Lock()
	cur, ok := c.running[key]
	if ok {
		delete(c.running, key)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	cur.cancel()
	<-cur.done // wait for the goroutine to actually exit before returning.
}

func (c *ReadDesireInformerManagingController) stopAll() {
	c.mu.Lock()
	allKeys := make([]keys.ReadDesireKey, 0, len(c.running))
	for k := range c.running {
		allKeys = append(allKeys, k)
	}
	c.mu.Unlock()
	for _, k := range allKeys {
		c.stopByKey(k)
	}
}

// Running returns true when key has a per-instance controller in flight. Test-only.
func (c *ReadDesireInformerManagingController) Running(key keys.ReadDesireKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.running[key]
	return ok
}

// readDesireFetcher implements desirestatuswriter.Fetcher by going to a
// live Cosmos client per call. See the apply_desire counterpart for why
// the lister cache is the wrong source here. Defined here to keep the
// manager self-contained; the per-instance controller package has its
// own equivalent struct.
type readDesireFetcher struct {
	crudByParent kubeappliercosmosstorage.KubeApplierReadDesireCRUD
}

var _ desirestatuswriter.Fetcher[kubeapplierapi.ReadDesire, keys.ReadDesireKey] = &readDesireFetcher{}

func (f *readDesireFetcher) Fetch(ctx context.Context, key keys.ReadDesireKey) (*kubeapplierapi.ReadDesire, error) {
	crud, err := key.CRUD(f.crudByParent)
	if err != nil {
		return nil, fmt.Errorf("crud for key %v: %w", key, err)
	}
	return crud.Get(ctx, key.Name)
}

// readDesireReplacer implements desirestatuswriter.Replacer over a
// KubeApplierReadDesireCRUD. The manager uses its writer only on the
// construction-failure path (Successful=False with reason
// PreCheckFailed). Spawned per-instance controllers have their own
// writer for KubeContent and steady-state Successful. Both writers go
// through a Replacer like this one.
type readDesireReplacer struct {
	crudByParent kubeappliercosmosstorage.KubeApplierReadDesireCRUD
}

var _ desirestatuswriter.Replacer[kubeapplierapi.ReadDesire] = &readDesireReplacer{}

func (r *readDesireReplacer) Replace(ctx context.Context, desired *kubeapplierapi.ReadDesire) error {
	key, err := keys.ReadDesireKeyFromResourceID(desired.GetResourceID())
	if err != nil {
		return fmt.Errorf("derive key for replace: %w", err)
	}
	crud, err := key.CRUD(r.crudByParent)
	if err != nil {
		return fmt.Errorf("crud for key %v: %w", key, err)
	}
	if _, err := crud.Replace(ctx, desired, nil); err != nil {
		return err
	}
	return nil
}

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
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/read_desire_kubernetes"
)

// DefaultCooldownPeriod is the minimum interval between two reconciles of a
// ReadDesire whose Cosmos etag has not changed. The manager's per-key
// reconcile is bookkeeping (start/stop of the per-instance kube reflector),
// so the periodic re-check is much less time-sensitive than apply or delete;
// 10 minutes matches apply_desire's default and avoids needless churn on the
// per-instance controllers.
//
// Real content changes (Add events, Update events with a different etag,
// and Delete events) bypass this gate so per-instance controllers are
// (re)launched or stopped promptly.
const DefaultCooldownPeriod = 10 * time.Minute

// Config tunes the manager's cooldown behavior. Zero-valued fields take the
// Default* constants; tests pass shorter durations and a fake clock.
type Config struct {
	// CooldownPeriod gates re-reconciles for a desire whose Cosmos etag has
	// not changed. See DefaultCooldownPeriod.
	CooldownPeriod time.Duration
	// Clock is the time source used by the cooldown gate. nil =
	// utilsclock.RealClock{}.
	Clock utilsclock.PassiveClock
}

func (c Config) withDefaults() Config {
	if c.CooldownPeriod == 0 {
		c.CooldownPeriod = DefaultCooldownPeriod
	}
	if c.Clock == nil {
		c.Clock = utilsclock.RealClock{}
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
	Build(key keys.ReadDesireKey, target kubeapplier.ResourceReference) (PerInstanceController, error)
}

// ReadDesireInformerManagingController watches ReadDesires and manages the
// per-instance kubernetes reflectors.
//
// Reconcile cadence (mirrors apply_desire and backend's GenericWatchingController):
//
//   - Add events queue immediately.
//   - Update events whose Cosmos etag differs from the previous version queue
//     immediately. Etag-unchanged updates (informer resyncs, or our own
//     status writes feeding back) are routed through the cooldown gate.
//   - Delete events queue immediately so the per-instance controller stops
//     promptly when a ReadDesire is removed from Cosmos.
//   - On error the workqueue's rate limiter requeues the key with backoff.
type ReadDesireInformerManagingController struct {
	readDesireInformer cache.SharedIndexInformer
	fetcher            *readDesireFetcher
	factory            PerInstanceFactory
	writer             desirestatuswriter.StatusWriter[kubeapplier.ReadDesire, keys.ReadDesireKey]
	queue              workqueue.TypedRateLimitingInterface[keys.ReadDesireKey]

	cfg      Config
	cooldown controllerutil.CooldownChecker

	// running tracks the live per-instance ReadDesireKubernetesController for
	// each ReadDesire by its key. The map is mutated only under mu. SyncOnce
	// reads it to decide whether to spawn a fresh per-instance controller,
	// stop+respawn when TargetItem changed, or no-op when the running entry
	// already matches the desire.
	mu      sync.Mutex
	running map[keys.ReadDesireKey]*runningInstance
}

type runningInstance struct {
	target kubeapplier.ResourceReference
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
	crudByParent database.KubeApplierReadDesireCRUD,
	cfg Config,
) (*ReadDesireInformerManagingController, error) {
	cfg = cfg.withDefaults()
	fetcher := &readDesireFetcher{crudByParent: crudByParent}
	cooldownChecker := controllerutil.NewTimeBasedCooldownChecker(cfg.CooldownPeriod)
	cooldownChecker.SetClock(cfg.Clock)
	c := &ReadDesireInformerManagingController{
		readDesireInformer: readDesireInformer,
		fetcher:            fetcher,
		factory:            &realPerInstanceFactory{dyn: dyn, crudByParent: crudByParent},
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ReadDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ReadDesireKey]{Name: "ReadDesireInformerManagingController"},
		),
		writer: desirestatuswriter.New[kubeapplier.ReadDesire, keys.ReadDesireKey, *kubeapplier.ReadDesire](
			fetcher,
			&readDesireReplacer{crudByParent: crudByParent},
		),
		cfg:      cfg,
		cooldown: cooldownChecker,
		running:  map[keys.ReadDesireKey]*runningInstance{},
	}

	if _, err := readDesireInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.handleAdd(obj) },
		UpdateFunc: func(oldObj, newObj any) { c.handleUpdate(oldObj, newObj) },
		DeleteFunc: func(obj any) { c.handleDelete(obj) },
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
	crudByParent database.KubeApplierReadDesireCRUD
}

var _ PerInstanceFactory = &realPerInstanceFactory{}

func (f *realPerInstanceFactory) Build(
	key keys.ReadDesireKey, target kubeapplier.ResourceReference,
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

	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName("ReadDesireInformerManagingController")...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("starting ReadDesireInformerManagingController")
	defer logger.Info("stopped ReadDesireInformerManagingController")

	if threadiness < 1 {
		threadiness = 1
	}
	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	<-ctx.Done()
}

// handleAdd queues every observed Add unconditionally — a new ReadDesire
// has never been reconciled, so the cooldown gate has nothing to compare
// against.
func (c *ReadDesireInformerManagingController) handleAdd(obj any) {
	d, ok := obj.(*kubeapplier.ReadDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

// handleUpdate queues immediately when the Cosmos etag differs — that is
// the signal that something the manager cares about (TargetItem, Spec, or
// our own status write) actually moved. Etag-unchanged updates are
// informer resyncs of an already-running instance: route them through the
// cooldown gate so we don't churn through bookkeeping for nothing.
func (c *ReadDesireInformerManagingController) handleUpdate(oldObj, newObj any) {
	oldD, oldOK := oldObj.(*kubeapplier.ReadDesire)
	newD, newOK := newObj.(*kubeapplier.ReadDesire)
	if !oldOK || !newOK {
		return
	}
	if oldD.GetEtag() != newD.GetEtag() {
		c.enqueue(newD)
		return
	}
	key, err := keys.ReadDesireKeyFromResourceID(newD.GetResourceID())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	if !c.cooldown.CanSync(context.TODO(), key) {
		return
	}
	c.queue.Add(key)
}

// handleDelete queues every observed Delete unconditionally so the
// per-instance controller stops promptly. The DeleteFinalStateUnknown
// wrapper appears when the cache evicted the object before delivery, and
// we still want to drive a stop in that case.
func (c *ReadDesireInformerManagingController) handleDelete(obj any) {
	d, ok := obj.(*kubeapplier.ReadDesire)
	if !ok {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			d, _ = t.Obj.(*kubeapplier.ReadDesire)
		}
	}
	if d == nil {
		return
	}
	c.enqueue(d)
}

func (c *ReadDesireInformerManagingController) enqueue(d *kubeapplier.ReadDesire) {
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
	if err != nil && !database.IsNotFoundError(err) {
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
		return c.writer.UpdateStatus(ctx, key, func(d *kubeapplier.ReadDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
		})
	}

	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c.mu.Lock()
	c.running[key] = &runningInstance{target: target, cancel: cancel, done: done}
	c.mu.Unlock()

	go func() {
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
// KubeApplierReadDesireCRUD. The manager uses its writer only on the
// construction-failure path (Successful=False with reason
// PreCheckFailed). Spawned per-instance controllers have their own
// writer for KubeContent and steady-state Successful. Both writers go
// through a Replacer like this one.
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

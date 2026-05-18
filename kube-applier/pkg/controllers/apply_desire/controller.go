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

// Package apply_desire implements the ApplyDesireController.
//
// On every sync the controller reads the named ApplyDesire from a live
// Cosmos client, decodes spec.kubeContent into an unstructured object, and
// issues a server-side-apply with Force=true and FieldManager from this
// package's FieldManager const via the dynamic client. The outcome is
// recorded on .status.conditions["Successful"] / ["Degraded"] and persisted
// via the StatusWriter.
package apply_desire

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

// FieldManager is the SSA field-manager name the kube-applier uses when
// applying ApplyDesires. All on-cluster ownership of fields written by the
// kube-applier traces back to this string. The "aro-hcp-" prefix exists so
// an operator inspecting fieldsV1 metadata can tell at a glance that ARO-HCP
// is the owner, distinct from any native Kubernetes "kube-..." manager.
const FieldManager = "aro-hcp-kube-applier"

// DefaultCooldownPeriod is the minimum interval between two reconciles
// of an unchanged ApplyDesire. The informer's handler resync fires
// frequently (at the informer's check period); the cooldown gate is what
// turns that into a slow re-reconcile. 10 minutes matches the bot
// directive on PR #5076: "resync without change relatively slow (say 10
// minutes on a resync)".
//
// Real content changes — Add events and Update events with a different
// Cosmos etag — bypass this gate so users see their content reflected fast.
const DefaultCooldownPeriod = 10 * time.Minute

// Config tunes the ApplyDesireController's cooldown behavior. Zero-valued
// fields take the Default* constants below; tests pass shorter durations
// and a fake clock.
type Config struct {
	// CooldownPeriod is the minimum time between two reconciles of an
	// unchanged desire. See DefaultCooldownPeriod for the rationale.
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

// ApplyDesireController reconciles ApplyDesires by SSA-applying spec.kubeContent.
//
// Reconcile cadence (mirrors backend's GenericWatchingController):
//
//   - Add events queue immediately.
//   - Update events whose Cosmos etag differs from the previous version
//     queue immediately. Etag-unchanged updates (informer resyncs, or our
//     own status writes feeding back) are routed through the cooldown gate.
//   - The cooldown gate (controllerutils.TimeBasedCooldownChecker) lets each key through
//     at most once per CooldownPeriod, so unchanged desires reconcile on
//     a slow cadence regardless of how often the informer resyncs.
//   - On error the workqueue's rate limiter requeues the key with backoff.
type ApplyDesireController struct {
	name                string
	applyDesireInformer cache.SharedIndexInformer
	fetcher             desirestatuswriter.Fetcher[kubeapplier.ApplyDesire, keys.ApplyDesireKey]
	dyn                 dynamic.Interface
	writer              desirestatuswriter.StatusWriter[kubeapplier.ApplyDesire, keys.ApplyDesireKey]
	queue               workqueue.TypedRateLimitingInterface[keys.ApplyDesireKey]

	cfg      Config
	cooldown controllerutils.CooldownChecker
}

// NewApplyDesireController wires up the informer event handler and returns a
// ready-to-Run controller. SSA writes go through dyn; we don't consult a
// RESTMapper — see applyDesired for the GVR-from-GVK convention.
//
// crudByParent provides a parent-scoped ResourceCRUD per ApplyDesire so
// status replaces can be issued under the desire's own cluster/nodepool
// resource ID rather than a sentinel parent.
//
// cfg's zero values get the Default* constants. Production callers may pass
// Config{} directly; tests substitute shorter durations and a fake clock.
func NewApplyDesireController(
	applyDesireInformer cache.SharedIndexInformer,
	dyn dynamic.Interface,
	crudByParent database.KubeApplierApplyDesireCRUD,
	cfg Config,
) (*ApplyDesireController, error) {
	cfg = cfg.withDefaults()
	fetcher := &applyDesireFetcher{crudByParent: crudByParent}
	cooldownChecker := controllerutils.NewTimeBasedCooldownChecker(cfg.CooldownPeriod)
	cooldownChecker.SetClock(cfg.Clock)
	c := &ApplyDesireController{
		name:                "ApplyDesireController",
		applyDesireInformer: applyDesireInformer,
		fetcher:             fetcher,
		dyn:                 dyn,
		writer: desirestatuswriter.New[kubeapplier.ApplyDesire, keys.ApplyDesireKey, *kubeapplier.ApplyDesire](
			fetcher,
			&applyDesireReplacer{crudByParent: crudByParent},
		),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ApplyDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ApplyDesireKey]{Name: "ApplyDesireController"},
		),
		cfg:      cfg,
		cooldown: cooldownChecker,
	}

	// Register the event handler at construction so events are delivered to
	// the queue before the informer starts pumping. Adding it inside Run()
	// races with the initial sync.
	if _, err := applyDesireInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.handleAdd(obj) },
		UpdateFunc: func(oldObj, newObj any) { c.handleUpdate(oldObj, newObj) },
	}); err != nil {
		return nil, fmt.Errorf("register informer handler: %w", err)
	}
	return c, nil
}

// Run starts threadiness workers. It returns when ctx is cancelled.
//
// There is no separate poll goroutine: the informer's handler resync
// (configured via the informer factory's ResyncPeriod) fires periodic
// Update events for every cached desire, and handleUpdate routes those
// through the cooldown gate.
func (c *ApplyDesireController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("starting ApplyDesireController")
	defer logger.Info("stopped ApplyDesireController")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	<-ctx.Done()
}

// handleAdd queues every observed Add unconditionally. A new ApplyDesire
// has never been reconciled, so the cooldown gate has nothing to compare
// against; treat Adds the same way the backend's GenericWatchingController
// does — as "changed" and immediate.
func (c *ApplyDesireController) handleAdd(obj any) {
	d, ok := obj.(*kubeapplier.ApplyDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

// handleUpdate queues immediately when the Cosmos etag differs (real
// content change) and consults the cooldown gate when it doesn't (informer
// resync or our own status-write feedback). Etag is the right signal for
// "changed" because Cosmos bumps it on every persisted mutation, including
// the status writes the controller itself produces — those still re-trigger
// reconcile (we want to see Successful conditions converge), but only at
// cooldown cadence, not in a tight feedback loop.
func (c *ApplyDesireController) handleUpdate(oldObj, newObj any) {
	oldD, oldOK := oldObj.(*kubeapplier.ApplyDesire)
	newD, newOK := newObj.(*kubeapplier.ApplyDesire)
	if !oldOK || !newOK {
		return
	}
	changed := oldD.GetEtag() != newD.GetEtag()
	c.enqueueWithCooldown(newD, changed)
}

// enqueue is the unconditional path used for Add events.
func (c *ApplyDesireController) enqueue(d *kubeapplier.ApplyDesire) {
	key, err := keys.ApplyDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		// Should not happen for a desire produced by our own informers, but
		// don't poison the queue if it does.
		utilruntime.HandleError(err)
		return
	}
	c.queue.Add(key)
}

// enqueueWithCooldown queues unconditionally on changed=true and consults
// the cooldown gate otherwise. A cooldown rejection is silent; the next
// resync (or a real change) will get its turn.
func (c *ApplyDesireController) enqueueWithCooldown(d *kubeapplier.ApplyDesire, changed bool) {
	key, err := keys.ApplyDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	if changed {
		c.queue.Add(key)
		return
	}
	if !c.cooldown.CanSync(context.TODO(), key) {
		return
	}
	c.queue.Add(key)
}

func (c *ApplyDesireController) runWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *ApplyDesireController) processNext(ctx context.Context) bool {
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

// SyncOnce performs a single reconcile pass for the named ApplyDesire.
// It is idempotent; concurrent invocations on different keys are safe.
func (c *ApplyDesireController) SyncOnce(ctx context.Context, key keys.ApplyDesireKey) error {
	desire, err := c.fetcher.Fetch(ctx, key)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if desire == nil {
		return nil
	}

	syncErr := c.applyDesired(ctx, desire)

	return c.writer.UpdateStatus(ctx, key, func(d *kubeapplier.ApplyDesire) {
		conditions.SetSuccessful(&d.Status.Conditions, syncErr)
		conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(syncErr))
	})
}

// applyDesired performs the kubeContent decode and SSA call. The GVR comes
// straight from spec.targetItem; we don't consult a RESTMapper or guess. The
// dynamic client surfaces a kube error if the GVR doesn't resolve, and that
// lands in SetSuccessful as KubeAPIError.
//
// PreCheckError is returned for pre-flight failures (parse, missing fields)
// so they classify as PreCheckFailed; everything else is treated as a
// kube-apiserver error.
func (c *ApplyDesireController) applyDesired(ctx context.Context, d *kubeapplier.ApplyDesire) error {
	target := d.Spec.TargetItem
	if len(target.Resource) == 0 || len(target.Version) == 0 || len(target.Name) == 0 {
		return conditions.NewPreCheckError(errors.New("spec.targetItem requires version, resource, and name"))
	}
	if d.Spec.KubeContent == nil || len(d.Spec.KubeContent.Raw) == 0 {
		return conditions.NewPreCheckError(errors.New("spec.kubeContent is empty"))
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(d.Spec.KubeContent.Raw); err != nil {
		return conditions.NewPreCheckError(fmt.Errorf("decode kubeContent: %w", err))
	}

	gvr := schema.GroupVersionResource{Group: target.Group, Version: target.Version, Resource: target.Resource}
	resource := c.dyn.Resource(gvr)
	var kubeResourceAccessor dynamic.ResourceInterface = resource
	if len(target.Namespace) > 0 {
		kubeResourceAccessor = resource.Namespace(target.Namespace)
	}

	_, applyErr := kubeResourceAccessor.Apply(ctx, target.Name, obj, metav1.ApplyOptions{
		FieldManager: FieldManager,
		Force:        true,
	})
	if applyErr != nil {
		// Wrap with a contextual prefix; keep the original kind so SetSuccessful
		// classifies it as a kube-apiserver error (NOT a *PreCheckError).
		return fmt.Errorf("server-side apply: %w", applyErr)
	}
	return nil
}

// classifyAsDegraded picks which sync errors should bubble to the Degraded
// condition. PreCheck failures are status-only signals, not controller-health
// problems, so we suppress them here.
func classifyAsDegraded(err error) error {
	if err == nil {
		return nil
	}
	var preCheck *conditions.PreCheckError
	if errors.As(err, &preCheck) {
		return nil
	}
	// 4xx errors from the apiserver are also user-input problems, not
	// controller wedges. Only 5xx and unclassified errors register as Degraded.
	if isClientError(err) {
		return nil
	}
	return err
}

func isClientError(err error) bool {
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		c := statusErr.ErrStatus.Code
		return c >= 400 && c < 500
	}
	return false
}

// applyDesireFetcher implements desirestatuswriter.Fetcher by going to a
// live Cosmos client per call. The desirestatuswriter package contract
// requires a live read so the etag passed to Replace is fresh; reading
// from the lister cache here would lose the second of two back-to-back
// status writes to a PreconditionFailed.
type applyDesireFetcher struct {
	crudByParent database.KubeApplierApplyDesireCRUD
}

var _ desirestatuswriter.Fetcher[kubeapplier.ApplyDesire, keys.ApplyDesireKey] = &applyDesireFetcher{}

func (f *applyDesireFetcher) Fetch(ctx context.Context, key keys.ApplyDesireKey) (*kubeapplier.ApplyDesire, error) {
	crud, err := f.crudByParent.ApplyDesires(key.ResourceParent())
	if err != nil {
		return nil, fmt.Errorf("crud for parent %v: %w", key.ResourceParent(), err)
	}
	return crud.Get(ctx, key.Name)
}

// applyDesireReplacer implements desirestatuswriter.Replacer over a
// KubeApplierApplyDesireCRUD. It derives the (cluster, [nodepool]) parent
// from each desire's resourceID at Replace time so a single Replacer can
// serve desires across many parents.
type applyDesireReplacer struct {
	crudByParent database.KubeApplierApplyDesireCRUD
}

var _ desirestatuswriter.Replacer[kubeapplier.ApplyDesire] = &applyDesireReplacer{}

func (r *applyDesireReplacer) Replace(ctx context.Context, desired *kubeapplier.ApplyDesire) error {
	key, err := keys.ApplyDesireKeyFromResourceID(desired.GetResourceID())
	if err != nil {
		return fmt.Errorf("derive key for replace: %w", err)
	}
	crud, err := r.crudByParent.ApplyDesires(key.ResourceParent())
	if err != nil {
		return fmt.Errorf("crud for parent %v: %w", key.ResourceParent(), err)
	}
	if _, err := crud.Replace(ctx, desired, nil); err != nil {
		return err
	}
	return nil
}

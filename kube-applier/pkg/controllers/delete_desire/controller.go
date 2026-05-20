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

// Package delete_desire implements the DeleteDesireController.
//
// On every sync the controller resolves spec.targetItem to a GVR via the
// supplied RESTMapper, gets the object, and either:
//   - reports Successful=True if the object is gone,
//   - reports Successful=False (WaitingForDeletion) if it's there and has a
//     deletionTimestamp (or after issuing a delete that succeeded but the
//     object hasn't fully gone away yet because of finalizers), or
//   - issues a delete and re-checks the same way.
package delete_desire

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
)

// DefaultCooldownPeriod is the minimum interval between two reconciles of a
// DeleteDesire whose Cosmos etag has not changed. Real content changes (Add
// events and Update events with a different etag) bypass this gate.
//
// 1 minute is shorter than apply_desire's 10-minute default because a
// DeleteDesire in the WaitingForDeletion state needs to keep checking the
// kube apiserver to see whether finalizers have completed; once they
// complete, the user wants Successful=True to land within a reasonable
// window. 60s matches the previous fixed informer resync period and keeps
// kube-apiserver Get traffic bounded.
const DefaultCooldownPeriod = 1 * time.Minute

// Config tunes the DeleteDesireController's cooldown behavior. Zero-valued
// fields take the Default* constants; tests pass shorter durations and a
// fake clock.
type Config struct {
	// CooldownPeriod gates poll-driven re-reconciles for a desire whose
	// Cosmos etag has not changed. See DefaultCooldownPeriod.
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

// DeleteDesireController reconciles DeleteDesires by deleting their target items
// and reporting WaitingForDeletion until the items actually disappear.
//
// Reconcile cadence (mirrors apply_desire and backend's GenericWatchingController):
//
//   - Add events queue immediately.
//   - Update events whose Cosmos etag differs from the previous version queue
//     immediately. Etag-unchanged updates (informer resyncs, or our own status
//     writes feeding back) are routed through the cooldown gate so a stuck
//     WaitingForDeletion does not hammer the kube apiserver.
//   - On error the workqueue's rate limiter requeues the key with backoff.
type DeleteDesireController struct {
	name                 string
	deleteDesireInformer cache.SharedIndexInformer
	fetcher              *deleteDesireFetcher
	dyn                  dynamic.Interface
	writer               desirestatuswriter.StatusWriter[kubeapplier.DeleteDesire, keys.DeleteDesireKey]
	queue                workqueue.TypedRateLimitingInterface[keys.DeleteDesireKey]

	cfg      Config
	cooldown controllerutil.CooldownChecker
}

// NewDeleteDesireController wires up the informer event handler and returns a
// ready-to-Run controller. Deletes go through dyn; the GVR comes straight from
// spec.targetItem, no RESTMapper consultation.
//
// crudByParent provides a parent-scoped ResourceCRUD per DeleteDesire so
// status replaces can be issued under the desire's own cluster/nodepool
// resource ID rather than a sentinel parent.
//
// cfg's zero values get the Default* constants. Production callers may pass
// Config{} directly; tests substitute shorter durations and a fake clock.
func NewDeleteDesireController(
	deleteDesireInformer cache.SharedIndexInformer,
	dyn dynamic.Interface,
	crudByParent database.KubeApplierDeleteDesireCRUD,
	cfg Config,
) (*DeleteDesireController, error) {
	cfg = cfg.withDefaults()
	fetcher := &deleteDesireFetcher{crudByParent: crudByParent}
	cooldownChecker := controllerutil.NewTimeBasedCooldownChecker(cfg.CooldownPeriod)
	cooldownChecker.SetClock(cfg.Clock)
	c := &DeleteDesireController{
		name:                 "DeleteDesireController",
		deleteDesireInformer: deleteDesireInformer,
		fetcher:              fetcher,
		dyn:                  dyn,
		writer: desirestatuswriter.New[kubeapplier.DeleteDesire, keys.DeleteDesireKey, *kubeapplier.DeleteDesire](
			fetcher,
			&deleteDesireReplacer{crudByParent: crudByParent},
		),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.DeleteDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.DeleteDesireKey]{Name: "DeleteDesireController"},
		),
		cfg:      cfg,
		cooldown: cooldownChecker,
	}

	if _, err := deleteDesireInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.handleAdd(obj) },
		UpdateFunc: func(oldObj, newObj any) { c.handleUpdate(oldObj, newObj) },
	}); err != nil {
		return nil, fmt.Errorf("register informer handler: %w", err)
	}
	return c, nil
}

// Run starts threadiness workers. The informer's handler resync (configured
// via the informer factory's ResyncPeriod) fires periodic Update events for
// every cached desire, and handleUpdate routes those through the cooldown
// gate. That is what turns a stuck WaitingForDeletion into Successful=True
// once finalizers complete.
func (c *DeleteDesireController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("starting DeleteDesireController")
	defer logger.Info("stopped DeleteDesireController")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	<-ctx.Done()
}

// handleAdd queues every observed Add unconditionally. A new DeleteDesire
// has never been reconciled, so the cooldown gate has nothing to compare
// against.
func (c *DeleteDesireController) handleAdd(obj any) {
	d, ok := obj.(*kubeapplier.DeleteDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

// handleUpdate queues immediately when the Cosmos etag differs (real
// content change, including a status write the controller itself just
// produced) and consults the cooldown gate when it doesn't. The latter
// path is the periodic re-check that drives WaitingForDeletion to
// Successful once finalizers complete.
func (c *DeleteDesireController) handleUpdate(oldObj, newObj any) {
	oldD, oldOK := oldObj.(*kubeapplier.DeleteDesire)
	newD, newOK := newObj.(*kubeapplier.DeleteDesire)
	if !oldOK || !newOK {
		return
	}
	if oldD.GetEtag() != newD.GetEtag() {
		c.enqueue(newD)
		return
	}
	key, err := keys.DeleteDesireKeyFromResourceID(newD.GetResourceID())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	if !c.cooldown.CanSync(context.TODO(), key) {
		return
	}
	c.queue.Add(key)
}

func (c *DeleteDesireController) enqueue(d *kubeapplier.DeleteDesire) {
	key, err := keys.DeleteDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.queue.Add(key)
}

func (c *DeleteDesireController) runWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *DeleteDesireController) processNext(ctx context.Context) bool {
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

// SyncOnce performs a single reconcile pass for the named DeleteDesire.
func (c *DeleteDesireController) SyncOnce(ctx context.Context, key keys.DeleteDesireKey) error {
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

	mutate, _ := c.evaluate(ctx, desire)
	// The error returned by evaluate is already encoded into the status mutation
	// via SetSuccessful, so we don't propagate it back to the workqueue — the
	// next informer event or resync will redrive the loop if needed.
	return c.writer.UpdateStatus(ctx, key, mutate)
}

// evaluate runs the state machine for one DeleteDesire and returns the status
// mutation function that records the outcome.
//
// State machine (from readme):
//
//	get target
//	  not found             -> SetSuccessful(true)
//	  has deletion timestamp -> WaitingForDeletion
//	  no deletion timestamp -> issue Delete; on error -> KubeAPIError
//	                           re-issue get
//	                             still not found       -> SetSuccessful(true)
//	                             has deletion timestamp -> WaitingForDeletion
//
// We don't consult a RESTMapper: the GVR is taken straight from
// spec.targetItem, and scope is decided by namespace presence. If the GVR
// doesn't resolve, the dynamic client surfaces a kube error that lands in
// SetSuccessful as KubeAPIError.
func (c *DeleteDesireController) evaluate(ctx context.Context, d *kubeapplier.DeleteDesire) (desirestatuswriter.MutateFunc[kubeapplier.DeleteDesire], error) {
	target := d.Spec.TargetItem
	if len(target.Resource) == 0 || len(target.Version) == 0 || len(target.Name) == 0 {
		err := conditions.NewPreCheckError(errors.New("spec.targetItem requires version, resource, and name"))
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}

	gvr := schema.GroupVersionResource{Group: target.Group, Version: target.Version, Resource: target.Resource}
	resource := c.dyn.Resource(gvr)
	var kubeResourceAccessor dynamic.ResourceInterface = resource
	if len(target.Namespace) > 0 {
		kubeResourceAccessor = resource.Namespace(target.Namespace)
	}

	got, getErr := kubeResourceAccessor.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		// Target is gone. We do not try to distinguish "kube-applier deleted
		// it just now" from "it was already absent before we ran" — neither
		// the apiserver nor any prior status field carries the necessary
		// signal, and the desired post-condition (target absent) is the same
		// in both cases.
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, nil)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}, nil
	}
	if getErr != nil {
		err := fmt.Errorf("get target: %w", getErr)
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}

	if dt := got.GetDeletionTimestamp(); dt != nil {
		uid := got.GetUID()
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessfulWaitingForDeletion(&d.Status.Conditions, *dt, uid)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}, nil
	}

	if delErr := kubeResourceAccessor.Delete(ctx, target.Name, metav1.DeleteOptions{}); delErr != nil {
		// 404 just before delete is fine — the object disappeared between Get and Delete.
		if apierrors.IsNotFound(delErr) {
			return func(d *kubeapplier.DeleteDesire) {
				conditions.SetSuccessful(&d.Status.Conditions, nil)
				conditions.SetDegraded(&d.Status.Conditions, nil)
			}, nil
		}
		err := fmt.Errorf("delete target: %w", delErr)
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}

	// Re-read post-delete to capture the deletion-timestamp + UID for the
	// "waiting for finalizers" message — readme requires this verbatim.
	//
	// At-least-once semantic: a controller crash between the Delete above
	// and any of the writes that follow is harmless. The next reconcile
	// re-Gets, finds the object either gone or terminating, and publishes
	// the right status. A duplicate Delete on an already-terminating object
	// is a no-op at the apiserver.
	post, postErr := kubeResourceAccessor.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(postErr) {
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, nil)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}, nil
	}
	if postErr != nil {
		err := fmt.Errorf("post-delete get: %w", postErr)
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}
	dt := post.GetDeletionTimestamp()
	uid := post.GetUID()
	if dt == nil {
		// Should not happen — apiserver always sets DT on a successful delete that's not
		// immediate. If it does, treat as still-present.
		now := metav1.NewTime(time.Now())
		dt = &now
	}
	return func(d *kubeapplier.DeleteDesire) {
		conditions.SetSuccessfulWaitingForDeletion(&d.Status.Conditions, *dt, uid)
		conditions.SetDegraded(&d.Status.Conditions, nil)
	}, nil
}

func classifyAsDegraded(err error) error {
	if err == nil {
		return nil
	}
	var preCheck *conditions.PreCheckError
	if errors.As(err, &preCheck) {
		return nil
	}
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		c := statusErr.ErrStatus.Code
		if c >= 400 && c < 500 {
			return nil
		}
	}
	return err
}

// deleteDesireFetcher implements desirestatuswriter.Fetcher by going to a
// live Cosmos client per call. See the apply_desire counterpart for why
// the lister cache is the wrong source here.
type deleteDesireFetcher struct {
	crudByParent database.KubeApplierDeleteDesireCRUD
}

var _ desirestatuswriter.Fetcher[kubeapplier.DeleteDesire, keys.DeleteDesireKey] = &deleteDesireFetcher{}

func (f *deleteDesireFetcher) Fetch(ctx context.Context, key keys.DeleteDesireKey) (*kubeapplier.DeleteDesire, error) {
	crud, err := f.crudByParent.DeleteDesires(key.ResourceParent())
	if err != nil {
		return nil, fmt.Errorf("crud for parent %v: %w", key.ResourceParent(), err)
	}
	return crud.Get(ctx, key.Name)
}

// deleteDesireReplacer implements desirestatuswriter.Replacer over a
// KubeApplierDeleteDesireCRUD. See the apply_desire counterpart for why
// the parent must be derived per-call instead of fixed at construction.
type deleteDesireReplacer struct {
	crudByParent database.KubeApplierDeleteDesireCRUD
}

var _ desirestatuswriter.Replacer[kubeapplier.DeleteDesire] = &deleteDesireReplacer{}

func (r *deleteDesireReplacer) Replace(ctx context.Context, desired *kubeapplier.DeleteDesire) error {
	key, err := keys.DeleteDesireKeyFromResourceID(desired.GetResourceID())
	if err != nil {
		return fmt.Errorf("derive key for replace: %w", err)
	}
	crud, err := r.crudByParent.DeleteDesires(key.ResourceParent())
	if err != nil {
		return fmt.Errorf("crud for parent %v: %w", key.ResourceParent(), err)
	}
	if _, err := crud.Replace(ctx, desired, nil); err != nil {
		return err
	}
	return nil
}

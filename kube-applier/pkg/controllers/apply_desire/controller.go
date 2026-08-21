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
// ApplyDesire uses a discriminated union on .spec.type:
//
//   - Type=ServerSideApply: decodes .spec.serverSideApply.kubeContent into
//     an unstructured object and issues a server-side-apply with Force=true
//     and FieldManager from this package's FieldManager const via the dynamic
//     client.
//   - Type=Delete: deletes .spec.targetItem from the management cluster and
//     reports WaitingForDeletion until the target disappears (finalizers
//     complete).
//
// The outcome is recorded on .status.conditions: ["SuccessfullyApplied"] for
// ServerSideApply or ["SuccessfullyDeleted"] for Delete, the legacy
// ["Successful"] (retained for backwards compatibility), and ["Degraded"]; it is
// persisted via the StatusWriter.
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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
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

// ApplyDesireControllerName is the per-controller identifier emitted in the
// "controller_name" log key, used as the workqueue name (so it surfaces as a
// Prometheus label), and threaded into ctx via utils.ContextWithControllerName.
// Mirrors the backend convention (e.g. NodepoolVersionControllerName).
const ApplyDesireControllerName = "ApplyDesireController"

// DefaultResyncPeriod is the maximum interval between two reconciles of an
// unchanged ApplyDesire. If content changes (etag differs), the controller
// reconciles immediately; otherwise the informer re-delivers the item
// after this duration so drift from the desired state is detected.
const DefaultResyncPeriod = 10 * time.Minute

// Config tunes the ApplyDesireController's resync behavior. Zero-valued
// fields take the Default* constants below; tests pass shorter durations.
type Config struct {
	// ResyncPeriod is the maximum time between two reconciles of an
	// unchanged desire. See DefaultResyncPeriod for the rationale.
	ResyncPeriod time.Duration
}

func (c Config) withDefaults() Config {
	if c.ResyncPeriod == 0 {
		c.ResyncPeriod = DefaultResyncPeriod
	}
	return c
}

// ApplyDesireController reconciles ApplyDesires by SSA-applying spec.kubeContent.
//
// Reconcile cadence:
//
//   - Add and Update events queue immediately.
//   - The informer's ResyncPeriod (set to cfg.ResyncPeriod) controls how
//     often unchanged items are re-delivered, guaranteeing periodic
//     reconciliation.
//   - On error the workqueue's rate limiter requeues the key with backoff.
type ApplyDesireController struct {
	name                string
	applyDesireInformer cache.SharedIndexInformer
	fetcher             desirestatuswriter.Fetcher[kubeapplierapi.ApplyDesire, keys.ApplyDesireKey]
	dyn                 dynamic.Interface
	writer              desirestatuswriter.StatusWriter[kubeapplierapi.ApplyDesire, keys.ApplyDesireKey]
	queue               workqueue.TypedRateLimitingInterface[keys.ApplyDesireKey]

	cfg Config
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
// Config{} directly; tests substitute shorter durations.
func NewApplyDesireController(
	applyDesireInformer cache.SharedIndexInformer,
	dyn dynamic.Interface,
	crudByParent kubeappliercosmosstorage.KubeApplierApplyDesireCRUD,
	cfg Config,
) (*ApplyDesireController, error) {
	cfg = cfg.withDefaults()
	fetcher := &applyDesireFetcher{crudByParent: crudByParent}
	c := &ApplyDesireController{
		name:                ApplyDesireControllerName,
		applyDesireInformer: applyDesireInformer,
		fetcher:             fetcher,
		dyn:                 dyn,
		writer: desirestatuswriter.New[kubeapplierapi.ApplyDesire, keys.ApplyDesireKey, *kubeapplierapi.ApplyDesire](
			fetcher,
			&applyDesireReplacer{crudByParent: crudByParent},
		),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ApplyDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ApplyDesireKey]{Name: ApplyDesireControllerName},
		),
		cfg: cfg,
	}

	logger := utils.DefaultLogger()
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(ApplyDesireControllerName)...)

	if _, err := applyDesireInformer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.handleAdd(obj) },
		UpdateFunc: func(oldObj, newObj any) { c.handleUpdate(oldObj, newObj) },
	}, cache.HandlerOptions{
		Logger:       &logger,
		ResyncPeriod: &cfg.ResyncPeriod,
	}); err != nil {
		return nil, fmt.Errorf("register informer handler: %w", err)
	}
	return c, nil
}

// Run starts threadiness workers. It returns when ctx is cancelled.
func (c *ApplyDesireController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("starting controller")
	defer logger.Info("stopped controller")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	<-ctx.Done()
}

// handleAdd queues every observed Add unconditionally.
func (c *ApplyDesireController) handleAdd(obj any) {
	d, ok := obj.(*kubeapplierapi.ApplyDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

// handleUpdate enqueues the key unconditionally. The informer's
// ResyncPeriod controls how often unchanged items are re-delivered.
func (c *ApplyDesireController) handleUpdate(_, newObj any) {
	newD, newOK := newObj.(*kubeapplierapi.ApplyDesire)
	if !newOK {
		return
	}
	c.enqueue(newD)
}

func (c *ApplyDesireController) enqueue(d *kubeapplierapi.ApplyDesire) {
	key, err := keys.ApplyDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		utilruntime.HandleError(err)
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

// SyncOnce performs a single reconcile pass for the named ApplyDesire.
// It is idempotent; concurrent invocations on different keys are safe.
//
// The desire's Type field discriminates the operation:
//   - ServerSideApply: SSA-applies .spec.serverSideApply.kubeContent.
//   - Delete: deletes .spec.targetItem and reports WaitingForDeletion
//     until the target disappears.
func (c *ApplyDesireController) SyncOnce(ctx context.Context, key keys.ApplyDesireKey) error {
	desire, err := c.fetcher.Fetch(ctx, key)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if desire == nil {
		return nil
	}

	switch desire.Spec.Type {
	case kubeapplierapi.ApplyDesireTypeServerSideApply:
		applied, syncErr := c.applyDesired(ctx, desire)

		// Capture the metadata.generation of the Kubernetes object returned by
		// the SSA apply call so the closure below records the right value.
		var appliedKubeGeneration *int64
		if syncErr == nil && applied != nil {
			gen := applied.GetGeneration()
			appliedKubeGeneration = &gen
		}

		return c.writer.UpdateStatus(ctx, key, func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessfullyApplied(&d.Status.Conditions, syncErr)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(syncErr))
			d.Status.AppliedKubeGeneration = appliedKubeGeneration
		})
	case kubeapplierapi.ApplyDesireTypeDelete:
		mutate := c.evaluateDelete(ctx, desire)
		return c.writer.UpdateStatus(ctx, key, mutate)
	default:
		syncErr := conditions.NewPreCheckError(fmt.Errorf("unknown desire type %q", desire.Spec.Type))
		return c.writer.UpdateStatus(ctx, key, func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, syncErr)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(syncErr))
		})
	}
}

// applyDesired performs the kubeContent decode and SSA call. The GVR comes
// straight from spec.targetItem; we don't consult a RESTMapper or guess. The
// dynamic client surfaces a kube error if the GVR doesn't resolve, and that
// lands in SetSuccessfullyApplied as KubeAPIError.
//
// PreCheckError is returned for pre-flight failures (parse, missing fields)
// so they classify as PreCheckFailed; everything else is treated as a
// kube-apiserver error.
func (c *ApplyDesireController) applyDesired(ctx context.Context, d *kubeapplierapi.ApplyDesire) (*unstructured.Unstructured, error) {
	target := d.Spec.TargetItem
	if len(target.Resource) == 0 || len(target.Version) == 0 || len(target.Name) == 0 {
		return nil, conditions.NewPreCheckError(errors.New("spec.targetItem requires version, resource, and name"))
	}
	if d.Spec.ServerSideApply == nil || d.Spec.ServerSideApply.KubeContent == nil || len(d.Spec.ServerSideApply.KubeContent.Raw) == 0 {
		return nil, conditions.NewPreCheckError(errors.New("spec.serverSideApply.kubeContent is empty"))
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(d.Spec.ServerSideApply.KubeContent.Raw); err != nil {
		return nil, conditions.NewPreCheckError(fmt.Errorf("decode kubeContent: %w", err))
	}

	gvr := schema.GroupVersionResource{Group: target.Group, Version: target.Version, Resource: target.Resource}
	resource := c.dyn.Resource(gvr)
	var kubeResourceAccessor dynamic.ResourceInterface = resource
	if len(target.Namespace) > 0 {
		kubeResourceAccessor = resource.Namespace(target.Namespace)
	}

	result, applyErr := kubeResourceAccessor.Apply(ctx, target.Name, obj, metav1.ApplyOptions{
		FieldManager: FieldManager,
		Force:        true,
	})
	if applyErr != nil {
		// Wrap with a contextual prefix; keep the original kind so
		// SetSuccessfullyApplied classifies it as a kube-apiserver error (NOT a
		// *PreCheckError).
		return nil, fmt.Errorf("server-side apply: %w", applyErr)
	}
	return result, nil
}

// evaluateDelete runs the state machine for one ApplyDesire with Type=Delete
// and returns the status mutation function that records the outcome.
//
// State machine:
//
//	get target
//	  not found             -> SuccessfullyDeleted=True
//	  has deletion timestamp -> WaitingForDeletion
//	  no deletion timestamp -> issue Delete; on error -> KubeAPIError
//	                           re-issue get
//	                             not found              -> SuccessfullyDeleted=True
//	                             has deletion timestamp  -> WaitingForDeletion
func (c *ApplyDesireController) evaluateDelete(ctx context.Context, d *kubeapplierapi.ApplyDesire) desirestatuswriter.MutateFunc[kubeapplierapi.ApplyDesire] {
	target := d.Spec.TargetItem
	if len(target.Resource) == 0 || len(target.Version) == 0 || len(target.Name) == 0 {
		err := conditions.NewPreCheckError(errors.New("spec.targetItem requires version, resource, and name"))
		return func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessfullyDeleted(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}
	}

	gvr := schema.GroupVersionResource{Group: target.Group, Version: target.Version, Resource: target.Resource}
	resource := c.dyn.Resource(gvr)
	var kubeResourceAccessor dynamic.ResourceInterface = resource
	if len(target.Namespace) > 0 {
		kubeResourceAccessor = resource.Namespace(target.Namespace)
	}

	got, getErr := kubeResourceAccessor.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessfullyDeleted(&d.Status.Conditions, nil)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}
	}
	if getErr != nil {
		err := fmt.Errorf("get target: %w", getErr)
		return func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessfullyDeleted(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}
	}

	if dt := got.GetDeletionTimestamp(); dt != nil {
		uid := got.GetUID()
		return func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetWaitingForDeletion(&d.Status.Conditions, *dt, uid)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}
	}

	if delErr := kubeResourceAccessor.Delete(ctx, target.Name, metav1.DeleteOptions{}); delErr != nil {
		if apierrors.IsNotFound(delErr) {
			return func(d *kubeapplierapi.ApplyDesire) {
				conditions.SetSuccessfullyDeleted(&d.Status.Conditions, nil)
				conditions.SetDegraded(&d.Status.Conditions, nil)
			}
		}
		err := fmt.Errorf("delete target: %w", delErr)
		return func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessfullyDeleted(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}
	}

	// Re-read post-delete to capture the deletion-timestamp + UID for the
	// "waiting for finalizers" message.
	post, postErr := kubeResourceAccessor.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(postErr) {
		return func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessfullyDeleted(&d.Status.Conditions, nil)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}
	}
	if postErr != nil {
		err := fmt.Errorf("post-delete get: %w", postErr)
		return func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetSuccessfullyDeleted(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}
	}
	dt := post.GetDeletionTimestamp()
	uid := post.GetUID()
	if dt == nil {
		now := metav1.NewTime(time.Now())
		dt = &now
	}
	return func(d *kubeapplierapi.ApplyDesire) {
		conditions.SetWaitingForDeletion(&d.Status.Conditions, *dt, uid)
		conditions.SetDegraded(&d.Status.Conditions, nil)
	}
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
	crudByParent kubeappliercosmosstorage.KubeApplierApplyDesireCRUD
}

var _ desirestatuswriter.Fetcher[kubeapplierapi.ApplyDesire, keys.ApplyDesireKey] = &applyDesireFetcher{}

func (f *applyDesireFetcher) Fetch(ctx context.Context, key keys.ApplyDesireKey) (*kubeapplierapi.ApplyDesire, error) {
	crud, err := key.CRUD(f.crudByParent)
	if err != nil {
		return nil, fmt.Errorf("crud for key %v: %w", key, err)
	}
	return crud.Get(ctx, key.Name)
}

// applyDesireReplacer implements desirestatuswriter.Replacer over a
// KubeApplierApplyDesireCRUD. It derives the (cluster, [nodepool]) parent
// from each desire's resourceID at Replace time so a single Replacer can
// serve desires across many parents.
type applyDesireReplacer struct {
	crudByParent kubeappliercosmosstorage.KubeApplierApplyDesireCRUD
}

var _ desirestatuswriter.Replacer[kubeapplierapi.ApplyDesire] = &applyDesireReplacer{}

func (r *applyDesireReplacer) Replace(ctx context.Context, desired *kubeapplierapi.ApplyDesire) error {
	key, err := keys.ApplyDesireKeyFromResourceID(desired.GetResourceID())
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

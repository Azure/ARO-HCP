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

package azuredeploymentstackdesires

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/azureresourcesinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const AzureDeploymentStackDesiresControllerName = "AzureDeploymentStackDesires"
const defaultInterval = 6 * time.Hour
const pendingInterval = 30 * time.Second

var errExecutionFailed = errors.New("deployment stack execution failed")

// Key identifies a single DeploymentStackDesire by its parent resource ID and
// name. ParentResourceID is parent-agnostic (mirrors
// kube-applier's keys.ApplyDesireKey) so this controller does not assume the
// parent is specifically a Cluster — today only Cluster-scoped desires exist,
// but a future scope needs no change here.
type Key struct {
	ParentResourceID string
	Name             string
}

// CRUD returns the right per-scope CRUD for this key's parent.
func (k Key) CRUD(client azureresourcescosmosstorage.AzureResourcesDeploymentStackDesireCRUD) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	id, err := azcorearm.ParseResourceID(k.ParentResourceID)
	if err != nil {
		return nil, err
	}
	parent, err := azureresourcescosmosstorage.ParseDeploymentStackDesireScope(id)
	if err != nil {
		return nil, err
	}
	return client.DeploymentStackDesiresFor(parent)
}

// GetResourceID returns the desire's full resource ID.
func (k Key) GetResourceID() *azcorearm.ResourceID {
	s := strings.ToLower(path.Join(k.ParentResourceID, azureresourcesapi.DeploymentStackDesireResourceTypeName, k.Name))
	return metadataapi.Must(azcorearm.ParseResourceID(s))
}

func (k Key) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

// KeyFromResourceID parses a Key out of a DeploymentStackDesire's resource ID.
// The desire is the leaf; everything above it is the parent. Mirrors
// kube-applier's keys.ApplyDesireKeyFromResourceID.
func KeyFromResourceID(id *azcorearm.ResourceID) (Key, error) {
	if id == nil {
		return Key{}, errors.New("resource ID is nil")
	}
	if id.Parent == nil {
		return Key{}, fmt.Errorf("desire %q has no parent in its resource ID", id.String())
	}
	return Key{ParentResourceID: strings.ToLower(id.Parent.String()), Name: id.Name}, nil
}

// controller reconciles DeploymentStackDesires directly off its informer's
// event stream, mirroring kube-applier's ApplyDesireController rather than
// backend's generic Cluster-aggregator framework (GenericWatchingController):
// a single informer already scoped to this document type is the only source
// of truth for what to reconcile, so there is no resource-type gate or
// interesting-resource filter to maintain. Adding a future scope means
// widening the informer's GlobalLister, not this controller.
type controller struct {
	controllerutil.CacheSyncWaiter
	name         string
	crudByParent azureresourcescosmosstorage.AzureResourcesDeploymentStackDesireCRUD
	lister       azureresourceslisters.DeploymentStackDesireLister
	fpa          azureclient.FirstPartyApplicationClientBuilder
	smi          azureclient.ServiceManagedIdentityClientBuilder
	clock        clock.PassiveClock
	queue        workqueue.TypedRateLimitingInterface[Key]
}

// NewController wires up the informer event handler and returns a
// ready-to-Run controller.
func NewController(crudByParent azureresourcescosmosstorage.AzureResourcesDeploymentStackDesireCRUD, informers azureresourcesinformers.AzureResourcesInformers,
	fpa azureclient.FirstPartyApplicationClientBuilder, smi azureclient.ServiceManagedIdentityClientBuilder,
	clk clock.PassiveClock,
) *controller {
	deploymentStackDesiresInformer, deploymentStackDesiredLister := informers.DeploymentStackDesires()
	c := &controller{
		name:         AzureDeploymentStackDesiresControllerName,
		crudByParent: crudByParent,
		lister:       deploymentStackDesiredLister,
		fpa:          fpa,
		smi:          smi,
		clock:        clk,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[Key](),
			workqueue.TypedRateLimitingQueueConfig[Key]{Name: AzureDeploymentStackDesiresControllerName},
		),
	}

	logger := utils.DefaultLogger()
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(AzureDeploymentStackDesiresControllerName)...)

	registration, err := deploymentStackDesiresInformer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.handleAdd(obj) },
		UpdateFunc: func(_, newObj any) { c.handleUpdate(newObj) },
	}, cache.HandlerOptions{
		Logger:       &logger,
		ResyncPeriod: ptr.To(time.Minute),
	})
	if err != nil {
		panic(err)
	}
	c.AddCacheSyncs(deploymentStackDesiresInformer.HasSynced, registration.HasSynced)
	return c
}

// handleAdd queues every observed Add unconditionally.
func (c *controller) handleAdd(obj any) {
	d, ok := obj.(*azureresourcesapi.DeploymentStackDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

// handleUpdate enqueues the key unconditionally. The informer's ResyncPeriod
// controls how often unchanged items are re-delivered.
func (c *controller) handleUpdate(newObj any) {
	d, ok := newObj.(*azureresourcesapi.DeploymentStackDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

func (c *controller) enqueue(d *azureresourcesapi.DeploymentStackDesire) {
	key, err := KeyFromResourceID(d.GetResourceID())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.queue.Add(key)
}

// Run starts threadiness workers. It returns when ctx is cancelled.
func (c *controller) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	if !c.WaitForCacheSync(ctx) {
		return
	}

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue. It returns false
// when it's time to quit.
func (c *controller) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, key)
	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", key)
	c.queue.AddRateLimited(key)
	return true
}

func (c *controller) SyncOnce(ctx context.Context, key Key) error {
	logger := utils.LoggerFromContext(ctx)
	desire, err := c.lister.GetByResourceID(ctx, key.GetResourceID().String())
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	result := decideStackDesire(logger, desire, c.clock.Now(), func() (*armdeploymentstacks.DeploymentStack, error) {
		client, err := c.stackClient(ctx, desire)
		if err != nil {
			return nil, err
		}
		return getStack(ctx, client, desire.Spec.TargetResourceID)
	})
	result.validate()
	status, executionErr := result.status, result.err
	switch result.action {
	case applyStack, deleteStack:
		client, err := c.stackClient(ctx, desire)
		if err != nil {
			return err
		}
		hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
		if err != nil {
			return err
		}
		logger.Info(result.submissionMessage, "action", result.action, "desiredSpecHash", hash)
		if result.action == applyStack {
			stack, err := submitApply(ctx, client, desire.Spec)
			if err != nil {
				status, executionErr = stackDesireStatusForRejectedSubmission(hash, azureresourcesapi.DeploymentStackDesireConditionSuccessfullyApplied, err)
			} else {
				status, executionErr = stackDesireStatusForAssociatedApply(hash, stack)
				properties := ptr.Deref(stack.Properties, armdeploymentstacks.DeploymentStackProperties{})
				logger.Info("Azure returned an Apply outcome", "state", ptr.Deref(properties.ProvisioningState, ""), "observedSpecHash", hash, "reason", status.Conditions[0].Reason)
			}
		} else {
			err := submitDelete(ctx, client, desire.Spec)
			if err == nil {
				logger.Info("Azure accepted stack deletion; waiting for GET to confirm absence", "observedSpecHash", hash)
			}
			status, executionErr = stackDesireStatusForDeleteSubmission(hash, err)
		}
		if status == nil {
			logger.Info("Azure stack submission outcome is unknown; reconciliation will observe Azure again", "action", result.action, "desiredSpecHash", hash)
		} else if status.Error != nil {
			logger.Info("Azure rejected the stack operation", "action", result.action, "desiredSpecHash", hash, "reason", status.Conditions[0].Reason)
		}
		if status != nil {
			// Every new attempt gets a clock, even an identical rejection after cooldown.
			now := c.clock.Now()
			status = normalizeStatusTimes(desire.Status, *status, now)
			status.ObservedAt = ptr.To(metav1.NewTime(now))
		}
	case noOp:
		if result.requeue > 0 {
			c.queue.AddAfter(key, result.requeue)
		}
		return result.err
	}
	if status != nil {
		updated := desire.DeepCopy()
		updated.Status = *status
		crud, err := key.CRUD(c.crudByParent)
		if err != nil {
			logger.Info("Could not resolve DeploymentStackDesire CRUD; reconciliation will observe Azure again", "observedSpecHash", status.ObservedSpecHash)
			return errors.Join(executionErr, err)
		}
		_, err = crud.Replace(ctx, updated, &azcosmos.ItemOptions{IfMatchEtag: &desire.CosmosETag})
		if err != nil {
			logger.Info("Could not persist stack observation; reconciliation will observe Azure again", "observedSpecHash", status.ObservedSpecHash)
		}
		// Never reread or merge an observation into a newer producer document.
		return errors.Join(executionErr, err)
	}
	return executionErr
}

// stackClient assumes the desire's routing and authentication have been validated.
func (c *controller) stackClient(ctx context.Context, desire *azureresourcesapi.DeploymentStackDesire) (*armdeploymentstacks.Client, error) {
	authentication := desire.Spec.Authentication
	if authentication.Type == azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI {
		return c.smi.DeploymentStacksClient(ctx, authentication.ClusterIdentityURL, authentication.SMIResourceID, desire.Spec.TargetResourceID.SubscriptionID)
	}
	return c.fpa.DeploymentStacksClient(authentication.TenantID, desire.Spec.TargetResourceID.SubscriptionID)
}

type action string

const (
	noOp               action = "NoOp"
	updateDesireStatus action = "UpdateDesireStatus"
	applyStack         action = "ApplyStack"
	deleteStack        action = "DeleteStack"
)

type decision struct {
	action  action
	status  *azureresourcesapi.DeploymentStackDesireStatus
	err     error
	requeue time.Duration
	// Only logged at submission, after the client and hash are ready.
	submissionMessage string
}

// Invalid combinations are programmer errors, not retryable reconciliation errors.
func (result decision) validate() {
	switch result.action {
	case updateDesireStatus:
		if result.status != nil && result.requeue == 0 && result.submissionMessage == "" {
			return
		}
	case applyStack, deleteStack:
		if result.status == nil && result.requeue == 0 && result.err == nil {
			return
		}
	case noOp:
		if result.status == nil && result.requeue >= 0 && (result.requeue == 0 || result.err == nil) && result.submissionMessage == "" {
			return
		}
	}
	panic(fmt.Sprintf("invalid stack desire decision: %+v", result))
}

// All events use this decision. GET is lazy and occurs at most once.
func decideStackDesire(logger logr.Logger, desire *azureresourcesapi.DeploymentStackDesire, now time.Time, lazyGET func() (*armdeploymentstacks.DeploymentStack, error)) decision {
	var desiredConditionType string
	var mutation action
	switch desire.Spec.Action {
	case azureresourcesapi.DeploymentStackDesireActionApply:
		desiredConditionType, mutation = azureresourcesapi.DeploymentStackDesireConditionSuccessfullyApplied, applyStack
	case azureresourcesapi.DeploymentStackDesireActionDelete:
		desiredConditionType, mutation = azureresourcesapi.DeploymentStackDesireConditionSuccessfullyDeleted, deleteStack
	}
	var validationErr error
	target := desire.Spec.TargetResourceID
	switch {
	case target == nil || target.Parent == nil:
		validationErr = errors.New("stack target and scope are required")
	case !strings.EqualFold(target.ResourceType.String(), "Microsoft.Resources/deploymentStacks"):
		validationErr = errors.New("stack target must be a Microsoft.Resources/deploymentStacks resource")
	case desire.Spec.Action != azureresourcesapi.DeploymentStackDesireActionApply && desire.Spec.Action != azureresourcesapi.DeploymentStackDesireActionDelete:
		validationErr = fmt.Errorf("unsupported stack action %q", desire.Spec.Action)
	case desire.Spec.Authentication.Type != azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA && desire.Spec.Authentication.Type != azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI:
		validationErr = fmt.Errorf("unsupported stack authentication %q", desire.Spec.Authentication.Type)
	case desire.Spec.Authentication.Type == azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI && desire.Spec.Authentication.SMIResourceID == nil:
		validationErr = errors.New("SMI authentication requires smiResourceID")
	default:
		switch strings.ToLower(target.Parent.ResourceType.String()) {
		case "microsoft.resources/resourcegroups", "microsoft.resources/subscriptions", "microsoft.management/managementgroups":
		default:
			validationErr = fmt.Errorf("unsupported stack scope %q", target.Parent.ResourceType)
		}
	}
	hash, hashErr := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	validationErr = errors.Join(validationErr, hashErr)
	if validationErr != nil {
		status := desire.Status
		if desiredConditionType == "" {
			status.Conditions = []metav1.Condition{
				{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
			}
		} else {
			status.Conditions = []metav1.Condition{
				{Type: desiredConditionType, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
				{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
			}
		}
		if normalized := changedObservationStatus(desire.Status, status, now); normalized != nil {
			logger.Info("Stack intent failed local validation", "desiredConditionType", desiredConditionType, "desiredSpecHash", hash)
			return decision{action: updateDesireStatus, status: normalized}
		}
		return decision{action: noOp}
	}
	// Bound repeated repair of unchanged successful or invalid intent without
	// delaying new intent, in-flight observations, or retryable execution failures.
	interval := desire.Spec.ReconcileInterval.Duration
	if interval <= 0 {
		interval = defaultInterval
	}
	condition := meta.FindStatusCondition(desire.Status.Conditions, desiredConditionType)
	if desire.Status.ObservedSpecHash == hash && desire.Status.ObservedAt != nil && condition != nil &&
		(condition.Status == metav1.ConditionTrue || condition.Reason == "InvalidIntent") && now.Before(desire.Status.ObservedAt.Add(interval)) {
		return decision{action: noOp}
	}
	stack, err := lazyGET()
	if err != nil {
		return decision{action: noOp, err: err}
	}
	if stack == nil {
		if mutation == applyStack {
			return decision{action: applyStack, submissionMessage: "Attempting to create the deployment stack because it is absent"}
		}
		status := azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, ObservedAt: ptr.To(metav1.NewTime(now)), Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionSuccessfullyDeleted, Status: metav1.ConditionTrue, Reason: "StackAbsent", Message: "The deployment stack is absent"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}}
		if normalized := changedObservationStatus(desire.Status, status, now); normalized != nil {
			logger.Info("Confirmed deployment stack deletion", "desiredSpecHash", hash)
			return decision{action: updateDesireStatus, status: normalized}
		}
		return decision{action: noOp}
	}
	properties := ptr.Deref(stack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	state := ptr.Deref(properties.ProvisioningState, "")
	correlation := ptr.Deref(properties.CorrelationID, "")
	previousStack := ptr.Deref(desire.Status.Stack, armdeploymentstacks.DeploymentStack{})
	previousProperties := ptr.Deref(previousStack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	previousCorrelation := ptr.Deref(previousProperties.CorrelationID, "")
	associatedWithRecordedApply := correlation != "" && correlation == previousCorrelation && desire.Status.ObservedSpecHash != ""
	terminal := state == armdeploymentstacks.DeploymentStackProvisioningStateSucceeded ||
		state == armdeploymentstacks.DeploymentStackProvisioningStateFailed || state == armdeploymentstacks.DeploymentStackProvisioningStateCanceled
	if terminal && !associatedWithRecordedApply {
		return decision{action: mutation, submissionMessage: "Attempting the desired stack operation because the completed Azure operation is not associated with the recorded Apply"}
	}
	var status *azureresourcesapi.DeploymentStackDesireStatus
	var executionErr error
	if associatedWithRecordedApply {
		// Only Apply submissions establish a correlated Stack/hash pair. Delete
		// acceptance has no Stack, and unassociated GETs never acquire a hash.
		// Conditions may have been replaced by prechecks for a different intent.
		status, executionErr = stackDesireStatusForAssociatedApply(desire.Status.ObservedSpecHash, stack)
		if status.ObservedSpecHash != hash {
			executionErr = nil
		}
	} else {
		status = &azureresourcesapi.DeploymentStackDesireStatus{Stack: stack, Conditions: []metav1.Condition{
			{Type: desiredConditionType, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}}
	}
	status.ObservedAt = ptr.To(metav1.NewTime(now))
	if normalized := changedObservationStatus(desire.Status, *status, now); normalized != nil {
		if associatedWithRecordedApply {
			logger.Info("Observed associated Apply outcome", "state", state, "reason", status.Conditions[0].Reason, "observedSpecHash", status.ObservedSpecHash, "desiredSpecHash", hash)
		} else {
			logger.Info("Waiting for unassociated stack operation", "state", state, "desiredConditionType", desiredConditionType, "desiredSpecHash", hash)
		}
		return decision{action: updateDesireStatus, status: normalized, err: executionErr}
	}
	if !terminal {
		return decision{action: noOp, requeue: pendingInterval}
	}
	var submissionMessage string
	switch {
	case status.ObservedSpecHash != hash:
		submissionMessage = "Attempting the new desired stack intent because the recorded Apply has completed"
	case executionErr != nil:
		submissionMessage = "Attempting the stack operation again because the recorded Apply failed with a retryable error"
	default:
		submissionMessage = "Attempting the desired stack operation because the configured repair/retry interval has expired"
	}
	return decision{action: mutation, submissionMessage: submissionMessage}
}

// Unchanged observations do not write; completed-operation metadata and
// diagnostics updates do not renew the cooldown.
func changedObservationStatus(previous, status azureresourcesapi.DeploymentStackDesireStatus, now time.Time) *azureresourcesapi.DeploymentStackDesireStatus {
	if cmp.Equal(previous, status,
		cmpopts.IgnoreFields(azureresourcesapi.DeploymentStackDesireStatus{}, "ObservedAt"),
		cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")) {
		return nil
	}
	return normalizeStatusTimes(previous, status, now)
}

// Preserve condition age while truth values match and preserve the attempt
// clock for metadata-only changes to the same completed operation.
func normalizeStatusTimes(previous, status azureresourcesapi.DeploymentStackDesireStatus, now time.Time) *azureresourcesapi.DeploymentStackDesireStatus {
	// Own the conditions slice without changing the desire or SDK objects.
	status.Conditions = append([]metav1.Condition(nil), status.Conditions...)
	for index := range status.Conditions {
		condition := &status.Conditions[index]
		oldCondition := meta.FindStatusCondition(previous.Conditions, condition.Type)
		condition.LastTransitionTime = metav1.NewTime(now)
		if oldCondition != nil && oldCondition.Status == condition.Status {
			condition.LastTransitionTime = oldCondition.LastTransitionTime
		}
	}
	if len(status.Conditions) > 0 {
		condition := status.Conditions[0]
		oldCondition := meta.FindStatusCondition(previous.Conditions, condition.Type)
		stack := ptr.Deref(status.Stack, armdeploymentstacks.DeploymentStack{})
		properties := ptr.Deref(stack.Properties, armdeploymentstacks.DeploymentStackProperties{})
		correlation := ptr.Deref(properties.CorrelationID, "")
		oldStack := ptr.Deref(previous.Stack, armdeploymentstacks.DeploymentStack{})
		oldProperties := ptr.Deref(oldStack.Properties, armdeploymentstacks.DeploymentStackProperties{})
		oldCorrelation := ptr.Deref(oldProperties.CorrelationID, "")
		if status.ObservedSpecHash == previous.ObservedSpecHash && correlation == oldCorrelation && oldCondition != nil &&
			condition.Status == oldCondition.Status && condition.Reason == oldCondition.Reason &&
			(condition.Status == metav1.ConditionTrue || condition.Reason == "InvalidIntent") {
			status.ObservedAt = previous.ObservedAt
		}
	}
	return &status
}

const (
	invalidTemplateCode                 = "InvalidTemplate"
	invalidRequestContentCode           = "InvalidRequestContent"
	deploymentStackDeploymentFailedCode = "DeploymentStackDeploymentFailed"
)

func classifyError(detail *armdeploymentstacks.ErrorDetail) (string, bool) {
	if detail == nil {
		return "Azure stack operation failed without error details", false
	}
	// Keep the entire diagnostic tree in the message as well as in typed Stack.
	payload, _ := json.Marshal(detail)
	code := ptr.Deref(detail.Code, "")
	if code == invalidTemplateCode || code == invalidRequestContentCode {
		return string(payload), true
	}
	// Only this demonstrated wrapper is inspected, and only one level deep.
	// InvalidTemplateDeployment can mean missing permission with no structured
	// authorization code; HTTP 400 and message text cannot establish invalid intent.
	if code != deploymentStackDeploymentFailedCode || len(detail.Details) == 0 {
		return string(payload), false
	}
	for _, child := range detail.Details {
		if child == nil || len(child.Details) != 0 ||
			(ptr.Deref(child.Code, "") != invalidTemplateCode && ptr.Deref(child.Code, "") != invalidRequestContentCode) {
			return string(payload), false
		}
	}
	return string(payload), true
}

func getStack(ctx context.Context, client *armdeploymentstacks.Client, target *azcorearm.ResourceID) (*armdeploymentstacks.DeploymentStack, error) {
	var stack armdeploymentstacks.DeploymentStack
	var err error
	switch strings.ToLower(target.Parent.ResourceType.String()) {
	case "microsoft.resources/resourcegroups":
		var response armdeploymentstacks.ClientGetAtResourceGroupResponse
		response, err = client.GetAtResourceGroup(ctx, target.ResourceGroupName, target.Name, nil)
		stack = response.DeploymentStack
	case "microsoft.resources/subscriptions":
		var response armdeploymentstacks.ClientGetAtSubscriptionResponse
		response, err = client.GetAtSubscription(ctx, target.Name, nil)
		stack = response.DeploymentStack
	case "microsoft.management/managementgroups":
		var response armdeploymentstacks.ClientGetAtManagementGroupResponse
		response, err = client.GetAtManagementGroup(ctx, target.Parent.Name, target.Name, nil)
		stack = response.DeploymentStack
	default:
		return nil, fmt.Errorf("unsupported stack scope %q", target.Parent.ResourceType)
	}
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &stack, nil
}

func submitApply(ctx context.Context, client *armdeploymentstacks.Client, spec azureresourcesapi.DeploymentStackDesireSpec) (*armdeploymentstacks.DeploymentStack, error) {
	target := spec.TargetResourceID
	var response *http.Response
	// BeginCreateOrUpdate returns a poller, whose Result cannot expose the initial
	// stack while the operation is nonterminal. Capture only this Apply response
	// to retain its body correlation ID for association on later GETs. The poller
	// is passive (no background work); the SDK buffers and closes the response body.
	// We neither poll nor persist a resume token.
	ctx = policy.WithCaptureResponse(ctx, &response)
	stack := ptr.Deref(spec.Stack, armdeploymentstacks.DeploymentStack{})
	var err error
	switch strings.ToLower(target.Parent.ResourceType.String()) {
	case "microsoft.resources/resourcegroups":
		_, err = client.BeginCreateOrUpdateAtResourceGroup(ctx, target.ResourceGroupName, target.Name, stack, nil)
	case "microsoft.resources/subscriptions":
		_, err = client.BeginCreateOrUpdateAtSubscription(ctx, target.Name, stack, nil)
	case "microsoft.management/managementgroups":
		_, err = client.BeginCreateOrUpdateAtManagementGroup(ctx, target.Parent.Name, target.Name, stack, nil)
	default:
		return nil, fmt.Errorf("unsupported stack scope %q", target.Parent.ResourceType)
	}
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("stack Apply returned no initial Azure response")
	}
	payload, err := runtime.Payload(response)
	if err != nil {
		return nil, err
	}
	var initialStack armdeploymentstacks.DeploymentStack
	if err := json.Unmarshal(payload, &initialStack); err != nil {
		return nil, err
	}
	return &initialStack, nil
}

func submitDelete(ctx context.Context, client *armdeploymentstacks.Client, spec azureresourcesapi.DeploymentStackDesireSpec) error {
	target := spec.TargetResourceID
	stack := ptr.Deref(spec.Stack, armdeploymentstacks.DeploymentStack{})
	properties := ptr.Deref(stack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	unmanage := ptr.Deref(properties.ActionOnUnmanage, armdeploymentstacks.ActionOnUnmanage{})
	var err error
	switch strings.ToLower(target.Parent.ResourceType.String()) {
	case "microsoft.resources/resourcegroups":
		_, err = client.BeginDeleteAtResourceGroup(ctx, target.ResourceGroupName, target.Name, &armdeploymentstacks.ClientBeginDeleteAtResourceGroupOptions{
			UnmanageActionResources:        (*armdeploymentstacks.UnmanageActionResourceMode)(unmanage.Resources),
			UnmanageActionResourceGroups:   (*armdeploymentstacks.UnmanageActionResourceGroupMode)(unmanage.ResourceGroups),
			UnmanageActionManagementGroups: (*armdeploymentstacks.UnmanageActionManagementGroupMode)(unmanage.ManagementGroups),
			BypassStackOutOfSyncError:      properties.BypassStackOutOfSyncError,
		})
	case "microsoft.resources/subscriptions":
		_, err = client.BeginDeleteAtSubscription(ctx, target.Name, &armdeploymentstacks.ClientBeginDeleteAtSubscriptionOptions{
			UnmanageActionResources:        (*armdeploymentstacks.UnmanageActionResourceMode)(unmanage.Resources),
			UnmanageActionResourceGroups:   (*armdeploymentstacks.UnmanageActionResourceGroupMode)(unmanage.ResourceGroups),
			UnmanageActionManagementGroups: (*armdeploymentstacks.UnmanageActionManagementGroupMode)(unmanage.ManagementGroups),
			BypassStackOutOfSyncError:      properties.BypassStackOutOfSyncError,
		})
	case "microsoft.management/managementgroups":
		_, err = client.BeginDeleteAtManagementGroup(ctx, target.Parent.Name, target.Name, &armdeploymentstacks.ClientBeginDeleteAtManagementGroupOptions{
			UnmanageActionResources:        (*armdeploymentstacks.UnmanageActionResourceMode)(unmanage.Resources),
			UnmanageActionResourceGroups:   (*armdeploymentstacks.UnmanageActionResourceGroupMode)(unmanage.ResourceGroups),
			UnmanageActionManagementGroups: (*armdeploymentstacks.UnmanageActionManagementGroupMode)(unmanage.ManagementGroups),
			BypassStackOutOfSyncError:      properties.BypassStackOutOfSyncError,
		})
	default:
		return fmt.Errorf("unsupported stack scope %q", target.Parent.ResourceType)
	}
	return err
}

func stackDesireStatusForAssociatedApply(hash string, stack *armdeploymentstacks.DeploymentStack) (*azureresourcesapi.DeploymentStackDesireStatus, error) {
	status := &azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, Stack: stack}
	associatedStack := ptr.Deref(stack, armdeploymentstacks.DeploymentStack{})
	properties := ptr.Deref(associatedStack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	state := ptr.Deref(properties.ProvisioningState, "")
	var err error
	switch state {
	case armdeploymentstacks.DeploymentStackProvisioningStateSucceeded:
		status.Conditions = []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionSuccessfullyApplied, Status: metav1.ConditionTrue, Reason: "Succeeded", Message: "Azure completed the stack operation"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}
	case armdeploymentstacks.DeploymentStackProvisioningStateFailed, armdeploymentstacks.DeploymentStackProvisioningStateCanceled:
		message, invalid := classifyError(properties.Error)
		if invalid {
			status.Conditions = []metav1.Condition{
				{Type: azureresourcesapi.DeploymentStackDesireConditionSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: message},
				{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: message},
			}
		} else {
			status.Conditions = []metav1.Condition{
				{Type: azureresourcesapi.DeploymentStackDesireConditionSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "ExecutionFailed", Message: message},
				{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionTrue, Reason: "ExecutionFailed", Message: message},
			}
			err = errExecutionFailed
		}
	default:
		status.Conditions = []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}
	}
	return status, err
}

func stackDesireStatusForDeleteSubmission(hash string, err error) (*azureresourcesapi.DeploymentStackDesireStatus, error) {
	if err != nil {
		return stackDesireStatusForRejectedSubmission(hash, azureresourcesapi.DeploymentStackDesireConditionSuccessfullyDeleted, err)
	}
	// Even a synchronous Delete response is only acceptance; GET proves absence.
	return &azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, Conditions: []metav1.Condition{
		{Type: azureresourcesapi.DeploymentStackDesireConditionSuccessfullyDeleted, Status: metav1.ConditionFalse, Reason: "WaitingForDeletion", Message: "Waiting for the deployment stack to disappear"},
		{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
	}}, nil
}

func stackDesireStatusForRejectedSubmission(hash, conditionType string, err error) (*azureresourcesapi.DeploymentStackDesireStatus, error) {
	var responseErr *azcore.ResponseError
	if !errors.As(err, &responseErr) {
		return nil, err
	}
	detail := &armdeploymentstacks.ErrorDetail{Code: ptr.To(responseErr.ErrorCode), Message: ptr.To(err.Error())}
	status := &azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, Error: detail}
	_, invalid := classifyError(detail)
	if invalid {
		status.Conditions = []metav1.Condition{
			{Type: conditionType, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: err.Error()},
			{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: err.Error()},
		}
		return status, nil
	}
	status.Conditions = []metav1.Condition{
		{Type: conditionType, Status: metav1.ConditionFalse, Reason: "RequestFailed", Message: err.Error()},
		{Type: azureresourcesapi.DeploymentStackDesireConditionDegraded, Status: metav1.ConditionTrue, Reason: "RequestFailed", Message: err.Error()},
	}
	return status, err
}

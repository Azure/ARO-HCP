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
	"strings"
	"time"

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

// defaultInterval is the cooldown before reconciling unchanged intent again after
// success or an invalid-intent outcome, used when the producer leaves
// Spec.ReconcileInterval unset (non-positive).
const defaultInterval = 6 * time.Hour

// pendingInterval is the requeue delay while waiting for a submitted Azure stack
// operation to reach a terminal state. It is independent of defaultInterval,
// which only bounds re-checks of already-settled (successful or invalid) intent.
const pendingInterval = 30 * time.Second

// errExecutionFailed is a stable sentinel distinguishing a genuine, retryable
// Azure execution failure from success, invalid intent, or a still-pending
// operation. SyncOnce propagates it so the workqueue retries with backoff, and
// decideStackDesire checks it separately to pick an accurate log/submission
// message when repairing a failed Apply, independent of the underlying Azure
// error's exact text.
var errExecutionFailed = errors.New("deployment stack execution failed")

// controller reconciles DeploymentStackDesires directly off its informer's
// event stream, mirroring kube-applier's ApplyDesireController rather than
// backend's generic Cluster-aggregator framework (GenericWatchingController):
// a single informer already scoped to this document type is the only source
// of truth for what to reconcile, so there is no resource-type gate or
// interesting-resource filter to maintain. Adding a future scope means
// widening the informer's GlobalLister, not this controller.
type controller struct {
	controllerutil.CacheSyncWaiter
	name string
	// crudByParent is the CRUD for the parent of the DeploymentStackDesire.
	crudByParent                        azureresourcescosmosstorage.AzureResourcesDeploymentStackDesireCRUD
	deploymentStackDesireLister         azureresourceslisters.DeploymentStackDesireLister
	firstPartyApplicationClientBuilder  azureclient.FirstPartyApplicationClientBuilder
	serviceManagedIdentityClientBuilder azureclient.ServiceManagedIdentityClientBuilder
	clock                               clock.PassiveClock
	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[Key]
}

// NewController wires up the informer event handler and returns a
// ready-to-Run controller.
func NewController(crudByParent azureresourcescosmosstorage.AzureResourcesDeploymentStackDesireCRUD, informers azureresourcesinformers.AzureResourcesInformers,
	firstPartyApplicationClientBuilder azureclient.FirstPartyApplicationClientBuilder, serviceManagedIdentityClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	clock clock.PassiveClock,
) *controller {
	deploymentStackDesiresInformer, deploymentStackDesiredLister := informers.DeploymentStackDesires()
	c := &controller{
		name:                                AzureDeploymentStackDesiresControllerName,
		crudByParent:                        crudByParent,
		deploymentStackDesireLister:         deploymentStackDesiredLister,
		firstPartyApplicationClientBuilder:  firstPartyApplicationClientBuilder,
		serviceManagedIdentityClientBuilder: serviceManagedIdentityClientBuilder,
		clock:                               clock,
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
	desire, err := c.deploymentStackDesireLister.GetByResourceID(ctx, key.GetResourceID().String())
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	getDeploymentStacksClient := c.newDeploymentStacksClientGetter(ctx, desire)
	result := decideDeploymentStackDesire(ctx, desire, c.clock.Now(), c.newLazyAzureDeploymentStackGet(ctx, desire, getDeploymentStacksClient))
	result.validate()
	status, executionErr := result.status, result.err
	switch result.kind {
	case decisionKindApplyStack, decisionKindDeleteStack:
		client, err := getDeploymentStacksClient()
		if err != nil {
			return err
		}
		hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
		if err != nil {
			return err
		}
		logger.Info(result.submissionMessage, "action", result.kind, "desiredSpecHash", hash)
		if result.kind == decisionKindApplyStack {
			stack, err := submitApply(ctx, client, desire.Spec)
			if err != nil {
				status, executionErr = deploymentStackDesireStatusForRejectedSubmission(hash, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, err)
			} else {
				status, executionErr = deploymentStackDesireStatusForAssociatedApply(hash, stack)
				properties := ptr.Deref(stack.Properties, armdeploymentstacks.DeploymentStackProperties{})
				logger.Info("Azure returned an Apply outcome", "state", ptr.Deref(properties.ProvisioningState, ""), "observedSpecHash", hash, "reason", status.Conditions[0].Reason)
			}
		} else {
			err := submitDelete(ctx, client, desire.Spec)
			if err == nil {
				logger.Info("Azure accepted stack deletion; waiting for GET to confirm absence", "observedSpecHash", hash)
			}
			status, executionErr = deploymentStackDesireStatusForDeleteSubmission(hash, err)
		}
		if status == nil {
			logger.Info("Azure stack submission outcome is unknown; reconciliation will observe Azure again", "action", result.kind, "desiredSpecHash", hash)
		} else if status.Error != nil {
			logger.Info("Azure rejected the stack operation", "action", result.kind, "desiredSpecHash", hash, "reason", status.Conditions[0].Reason)
		}
		if status != nil {
			// Every new attempt gets a clock, even an identical rejection after cooldown.
			now := c.clock.Now()
			status = normalizeStatusTimes(desire.Status, *status, now)
			status.ObservedAt = ptr.To(metav1.NewTime(now))
		}
	case decisionKindNoOp:
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

// newLazyAzureDeploymentStackGet returns a function that allows you to get the Azure deployment stack associated to the target in desired, using
// the authentication defined in desire.
func (c *controller) newLazyAzureDeploymentStackGet(ctx context.Context, desire *azureresourcesapi.DeploymentStackDesire, getClient func() (*armdeploymentstacks.Client, error)) func() (*armdeploymentstacks.DeploymentStack, error) {
	return func() (*armdeploymentstacks.DeploymentStack, error) {
		deploymentStacksClient, err := getClient()
		if err != nil {
			return nil, err
		}
		return getDeploymentStack(ctx, deploymentStacksClient, desire.Spec.TargetResourceID)
	}
}

// newDeploymentStacksClientGetter assumes the desire's routing and authentication have been
// validated. The returned function is scoped to a single SyncOnce call: for SMI it caches the
// built client (and the token fetch behind it) so it is reused across both the lazy GET and the
// submission path in one reconcile, instead of hitting ClusterIdentityURL twice.
func (c *controller) newDeploymentStacksClientGetter(ctx context.Context, desire *azureresourcesapi.DeploymentStackDesire) func() (*armdeploymentstacks.Client, error) {
	authentication := desire.Spec.Authentication
	if authentication.Type == azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI {
		return c.newCachedServiceManagedIdentityDeploymentStacksClientGetter(ctx, authentication.ServiceManagedIdentity, desire.Spec.TargetResourceID)
	}
	tenantID, subscriptionID := authentication.FirstPartyApplication.TenantID, desire.Spec.TargetResourceID.SubscriptionID
	return func() (*armdeploymentstacks.Client, error) {
		return c.firstPartyApplicationClientBuilder.DeploymentStacksClient(tenantID, subscriptionID)
	}
}

// newCachedServiceManagedIdentityDeploymentStacksClientGetter returns a getter that builds the
// SMI deployment stacks client at most once and caches it in the closure's own local variable for
// every later call. Only a successful build is cached; a failed build leaves the cache empty, so
// the next call retries the real builder instead of returning the cached error. Building this
// client exchanges the cluster identity for a token against ClusterIdentityURL, a real network
// round trip to the Managed Identities Data Plane Service. Without caching, a single SyncOnce
// that both calls the lazy GET and submits to Azure would pay that round trip twice. The cache
// lives only as long as the returned closure (i.e. one SyncOnce call). Callers must obtain a
// fresh getter per call rather than reusing one across reconciles or desires.
func (c *controller) newCachedServiceManagedIdentityDeploymentStacksClientGetter(ctx context.Context, authentication *azureresourcesapi.DeploymentStackDesireAuthenticationServiceManagedIdentity, targetResourceID *azcorearm.ResourceID) func() (*armdeploymentstacks.Client, error) {
	var deploymentStacksClient *armdeploymentstacks.Client
	return func() (*armdeploymentstacks.Client, error) {
		if deploymentStacksClient != nil {
			return deploymentStacksClient, nil
		}

		client, err := c.serviceManagedIdentityClientBuilder.DeploymentStacksClient(ctx, authentication.ClusterIdentityURL, authentication.ServiceManagedIdentityResourceID, targetResourceID.SubscriptionID)
		if err != nil {
			return nil, fmt.Errorf("failed to get Deployment Stacks Client: %w", err)
		}
		deploymentStacksClient = client
		return deploymentStacksClient, nil
	}
}

type decisionKind string

const (
	decisionKindNoOp               decisionKind = "NoOp"
	decisionKindUpdateDesireStatus decisionKind = "UpdateDesireStatus"
	decisionKindApplyStack         decisionKind = "ApplyStack"
	decisionKindDeleteStack        decisionKind = "DeleteStack"
)

type decision struct {
	kind    decisionKind
	status  *azureresourcesapi.DeploymentStackDesireStatus
	err     error
	requeue time.Duration
	// Only logged at submission, after the client and hash are ready.
	submissionMessage string
}

// Invalid combinations are programmer errors, not retryable reconciliation errors.
func (result decision) validate() {
	switch result.kind {
	case decisionKindUpdateDesireStatus:
		if result.status != nil && result.requeue == 0 && result.submissionMessage == "" {
			return
		}
	case decisionKindApplyStack, decisionKindDeleteStack:
		if result.status == nil && result.requeue == 0 && result.err == nil {
			return
		}
	case decisionKindNoOp:
		if result.status == nil && result.requeue >= 0 && (result.requeue == 0 || result.err == nil) && result.submissionMessage == "" {
			return
		}
	}
	panic(fmt.Sprintf("invalid stack desire decision: %+v", result))
}

// All events use this decision. GET is lazy and occurs at most once.
func decideDeploymentStackDesire(ctx context.Context, desire *azureresourcesapi.DeploymentStackDesire, now time.Time, lazyGET func() (*armdeploymentstacks.DeploymentStack, error)) decision {
	logger := utils.LoggerFromContext(ctx)
	userAssignedIdentityResourceType := metadataapi.Must(azcorearm.ParseResourceType("Microsoft.ManagedIdentity/userAssignedIdentities"))
	var desiredConditionType string
	var mutation decisionKind
	switch desire.Spec.Action {
	case azureresourcesapi.DeploymentStackDesireActionTypeApply:
		desiredConditionType, mutation = azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, decisionKindApplyStack
	case azureresourcesapi.DeploymentStackDesireActionTypeDelete:
		desiredConditionType, mutation = azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, decisionKindDeleteStack
	}
	var validationErrs []error
	target := desire.Spec.TargetResourceID
	if target == nil || target.Parent == nil {
		validationErrs = append(validationErrs, errors.New("stack target and scope are required"))
	} else {
		if !strings.EqualFold(target.ResourceType.String(), "Microsoft.Resources/deploymentStacks") {
			validationErrs = append(validationErrs, errors.New("stack target must be a Microsoft.Resources/deploymentStacks resource"))
		}
		switch strings.ToLower(target.Parent.ResourceType.String()) {
		case "microsoft.resources/resourcegroups", "microsoft.resources/subscriptions", "microsoft.management/managementgroups":
		default:
			validationErrs = append(validationErrs, fmt.Errorf("unsupported stack scope %q", target.Parent.ResourceType))
		}
	}
	if desire.Spec.Action != azureresourcesapi.DeploymentStackDesireActionTypeApply && desire.Spec.Action != azureresourcesapi.DeploymentStackDesireActionTypeDelete {
		validationErrs = append(validationErrs, fmt.Errorf("unsupported stack action %q", desire.Spec.Action))
	}
	switch desire.Spec.Authentication.Type {
	case azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI:
		if desire.Spec.Authentication.ServiceManagedIdentity == nil {
			validationErrs = append(validationErrs, errors.New("SMI authentication requires serviceManagedIdentity"))
		} else {
			smi := desire.Spec.Authentication.ServiceManagedIdentity
			switch {
			case smi.ServiceManagedIdentityResourceID == nil:
				validationErrs = append(validationErrs, errors.New("SMI authentication requires serviceManagedIdentityResourceID"))
			case !strings.EqualFold(smi.ServiceManagedIdentityResourceID.ResourceType.String(), userAssignedIdentityResourceType.String()):
				validationErrs = append(validationErrs, errors.New("SMI authentication requires a serviceManagedIdentityResourceID with userAssignedIdentity type"))
			}
			if smi.ClusterIdentityURL == "" {
				validationErrs = append(validationErrs, errors.New("SMI authentication requires clusterIdentityURL"))
			}
		}
	case azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA:
		if desire.Spec.Authentication.FirstPartyApplication == nil {
			validationErrs = append(validationErrs, errors.New("FPA authentication requires firstPartyApplication"))
		} else if desire.Spec.Authentication.FirstPartyApplication.TenantID == "" {
			validationErrs = append(validationErrs, errors.New("FPA authentication requires tenantID"))
		}
	default:
		validationErrs = append(validationErrs, fmt.Errorf("unsupported stack authentication %q", desire.Spec.Authentication.Type))
	}
	hash, hashErr := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	if hashErr != nil {
		validationErrs = append(validationErrs, hashErr)
	}
	validationErr := errors.Join(validationErrs...)
	if validationErr != nil {
		status := desire.Status
		if desiredConditionType == "" {
			status.Conditions = []metav1.Condition{
				{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
			}
		} else {
			status.Conditions = []metav1.Condition{
				{Type: desiredConditionType, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
				{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
			}
		}
		if normalized := changedObservationStatus(desire.Status, status, now); normalized != nil {
			logger.Info("Stack intent failed local validation", "desiredConditionType", desiredConditionType, "desiredSpecHash", hash)
			return decision{kind: decisionKindUpdateDesireStatus, status: normalized}
		}
		return decision{kind: decisionKindNoOp}
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
		return decision{kind: decisionKindNoOp}
	}
	deploymentStack, err := lazyGET()
	if err != nil {
		return decision{kind: decisionKindNoOp, err: err}
	}
	if deploymentStack == nil {
		if mutation == decisionKindApplyStack {
			return decision{kind: decisionKindApplyStack, submissionMessage: "Attempting to create the deployment stack because it is absent"}
		}
		status := azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, ObservedAt: ptr.To(metav1.NewTime(now)), Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue, Reason: "StackAbsent", Message: "The deployment stack is absent"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}}
		if normalized := changedObservationStatus(desire.Status, status, now); normalized != nil {
			logger.Info("Confirmed deployment stack deletion", "desiredSpecHash", hash)
			return decision{kind: decisionKindUpdateDesireStatus, status: normalized}
		}
		return decision{kind: decisionKindNoOp}
	}
	properties := ptr.Deref(deploymentStack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	state := ptr.Deref(properties.ProvisioningState, "")
	correlation := ptr.Deref(properties.CorrelationID, "")
	previousStack := ptr.Deref(desire.Status.Stack, armdeploymentstacks.DeploymentStack{})
	previousProperties := ptr.Deref(previousStack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	previousCorrelation := ptr.Deref(previousProperties.CorrelationID, "")
	associatedWithRecordedApply := correlation != "" && correlation == previousCorrelation && desire.Status.ObservedSpecHash != ""
	terminal := state == armdeploymentstacks.DeploymentStackProvisioningStateSucceeded ||
		state == armdeploymentstacks.DeploymentStackProvisioningStateFailed || state == armdeploymentstacks.DeploymentStackProvisioningStateCanceled
	if terminal && !associatedWithRecordedApply {
		return decision{kind: mutation, submissionMessage: "Attempting the desired stack operation because the completed Azure operation is not associated with the recorded Apply"}
	}
	var status *azureresourcesapi.DeploymentStackDesireStatus
	var executionErr error
	if associatedWithRecordedApply {
		// Only Apply submissions establish a correlated Stack/hash pair. Delete
		// acceptance has no Stack, and unassociated GETs never acquire a hash.
		// Conditions may have been replaced by prechecks for a different intent.
		status, executionErr = deploymentStackDesireStatusForAssociatedApply(desire.Status.ObservedSpecHash, deploymentStack)
		if status.ObservedSpecHash != hash {
			executionErr = nil
		}
	} else {
		status = &azureresourcesapi.DeploymentStackDesireStatus{Stack: deploymentStack, Conditions: []metav1.Condition{
			{Type: desiredConditionType, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}}
	}
	status.ObservedAt = ptr.To(metav1.NewTime(now))
	if normalized := changedObservationStatus(desire.Status, *status, now); normalized != nil {
		if associatedWithRecordedApply {
			logger.Info("Observed associated Apply outcome", "state", state, "reason", status.Conditions[0].Reason, "observedSpecHash", status.ObservedSpecHash, "desiredSpecHash", hash)
		} else {
			logger.Info("Waiting for unassociated stack operation", "state", state, "desiredConditionType", desiredConditionType, "desiredSpecHash", hash)
		}
		return decision{kind: decisionKindUpdateDesireStatus, status: normalized, err: executionErr}
	}
	if !terminal {
		return decision{kind: decisionKindNoOp, requeue: pendingInterval}
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
	return decision{kind: mutation, submissionMessage: submissionMessage}
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

// classifyError turns an Azure deployment-stack error into a diagnostic message
// and a verdict on whether the failure reflects invalid intent. The message is
// the full error tree marshaled to JSON, retained verbatim so the entire
// diagnostic is preserved in the condition alongside the typed Stack. The bool
// reports whether the failure is invalid intent (true) rather than a retryable
// execution failure (false), which determines whether the caller sets an
// InvalidIntent condition (settled, subject only to the long reconcile cooldown)
// or an ExecutionFailed condition that drives a workqueue retry.
//
// Classification is deliberately conservative: only error codes that
// unambiguously indicate a bad spec count as invalid intent. A top-level
// InvalidTemplate or InvalidRequestContent qualifies directly. The one wrapper
// inspected is DeploymentStackDeploymentFailed, and only one level deep: it is
// treated as invalid intent only when every child is itself a leaf (no further
// Details) carrying InvalidTemplate or InvalidRequestContent. Anything else,
// including a missing detail, an unrecognized code, or a deeper/mixed tree, is
// classified as retryable, because InvalidTemplateDeployment can also mean a
// missing permission with no structured authorization code, and HTTP 400 plus
// message text alone cannot establish invalid intent.
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

// getDeploymentStack gets the Azure deployment stack associated to target using client or an error.
// If the deployment stack is not found, it returns nil and no error.
// If the deployment stack is found, it returns the deployment stack and no error.
// If there's an error when trying to get the deployment stack, it returns nil and the error.
func getDeploymentStack(ctx context.Context, client *armdeploymentstacks.Client, target *azcorearm.ResourceID) (*armdeploymentstacks.DeploymentStack, error) {
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

func deploymentStackDesireStatusForAssociatedApply(hash string, stack *armdeploymentstacks.DeploymentStack) (*azureresourcesapi.DeploymentStackDesireStatus, error) {
	status := &azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, Stack: stack}
	associatedStack := ptr.Deref(stack, armdeploymentstacks.DeploymentStack{})
	properties := ptr.Deref(associatedStack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	state := ptr.Deref(properties.ProvisioningState, "")
	var err error
	switch state {
	case armdeploymentstacks.DeploymentStackProvisioningStateSucceeded:
		status.Conditions = []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionTrue, Reason: "Succeeded", Message: "Azure completed the stack operation"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}
	case armdeploymentstacks.DeploymentStackProvisioningStateFailed, armdeploymentstacks.DeploymentStackProvisioningStateCanceled:
		message, invalid := classifyError(properties.Error)
		if invalid {
			status.Conditions = []metav1.Condition{
				{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: message},
				{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: message},
			}
		} else {
			status.Conditions = []metav1.Condition{
				{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "ExecutionFailed", Message: message},
				{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "ExecutionFailed", Message: message},
			}
			err = errExecutionFailed
		}
	default:
		status.Conditions = []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation"},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}
	}
	return status, err
}

func deploymentStackDesireStatusForDeleteSubmission(hash string, err error) (*azureresourcesapi.DeploymentStackDesireStatus, error) {
	if err != nil {
		return deploymentStackDesireStatusForRejectedSubmission(hash, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, err)
	}
	// Even a synchronous Delete response is only acceptance; GET proves absence.
	return &azureresourcesapi.DeploymentStackDesireStatus{ObservedSpecHash: hash, Conditions: []metav1.Condition{
		{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, Status: metav1.ConditionFalse, Reason: "WaitingForDeletion", Message: "Waiting for the deployment stack to disappear"},
		{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
	}}, nil
}

func deploymentStackDesireStatusForRejectedSubmission(hash, conditionType string, err error) (*azureresourcesapi.DeploymentStackDesireStatus, error) {
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
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: err.Error()},
		}
		return status, nil
	}
	status.Conditions = []metav1.Condition{
		{Type: conditionType, Status: metav1.ConditionFalse, Reason: "RequestFailed", Message: err.Error()},
		{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "RequestFailed", Message: err.Error()},
	}
	return status, err
}

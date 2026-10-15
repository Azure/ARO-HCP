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

package stackdesires

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const StackDesiresControllerName = "StackDesires"
const defaultInterval = 6 * time.Hour
const pendingInterval = 30 * time.Second

var errExecutionFailed = errors.New("deployment stack execution failed")

type Key struct {
	SubscriptionID, ResourceGroupName, HCPClusterName, Name string
}

func (k Key) AddLoggerValues(logger logr.Logger) logr.Logger {
	cluster := controllerutils.HCPClusterKey{SubscriptionID: k.SubscriptionID, ResourceGroupName: k.ResourceGroupName, HCPClusterName: k.HCPClusterName}
	id, err := azcorearm.ParseResourceID(cluster.GetResourceID().String() + "/stackDesires/" + k.Name)
	if err != nil {
		return cluster.AddLoggerValues(logger)
	}
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(id)...)
}

type controller struct {
	resourcesDB corecosmosstorage.ResourcesDBClient
	lister      corelisters.StackDesireLister
	fpa         azureclient.FirstPartyApplicationClientBuilder
	smi         azureclient.ServiceManagedIdentityClientBuilder
	clock       clock.PassiveClock
	enqueue     controllerutil.AfterEnqueuer
}

func NewController(resourcesDB corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers,
	fpa azureclient.FirstPartyApplicationClientBuilder, smi azureclient.ServiceManagedIdentityClientBuilder,
	clk clock.PassiveClock,
) controllerutils.Controller {
	informer, lister := informers.StackDesires()
	syncer := &controller{resourcesDB: resourcesDB, lister: lister, fpa: fpa, smi: smi, clock: clk}
	watcher := controllerutil.NewGenericWatchingController(StackDesiresControllerName, coreapi.StackDesireResourceType, syncer, controllerutils.ReconcileTotal)
	syncer.enqueue = watcher
	if err := watcher.QueueForInformersWithMaxDepth(time.Minute, 0, informer); err != nil {
		panic(err)
	}
	return watcher
}

func (c *controller) MakeKey(id *azcorearm.ResourceID) Key {
	return Key{SubscriptionID: id.SubscriptionID, ResourceGroupName: id.ResourceGroupName, HCPClusterName: id.Parent.Name, Name: id.Name}
}

func (c *controller) CooldownChecker() controllerutil.CooldownChecker { return nil }

func (c *controller) SyncOnce(ctx context.Context, key Key) error {
	logger := utils.LoggerFromContext(ctx)
	desire, err := c.lister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.Name)
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
		hash, err := coreapi.StackDesireSpecHash(desire.Spec)
		if err != nil {
			return err
		}
		logger.Info(result.submissionMessage, "action", result.action, "desiredSpecHash", hash)
		if result.action == applyStack {
			stack, err := submitApply(ctx, client, desire.Spec)
			if err != nil {
				status, executionErr = stackDesireStatusForRejectedSubmission(hash, coreapi.StackDesireConditionSuccessfullyApplied, err)
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
			c.enqueue.EnqueueAfter(key, result.requeue)
		}
		return result.err
	}
	if status != nil {
		updated := desire.DeepCopy()
		updated.Status = *status
		_, err := c.resourcesDB.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, updated, &azcosmos.ItemOptions{IfMatchEtag: &desire.CosmosETag})
		if err != nil {
			logger.Info("Could not persist stack observation; reconciliation will observe Azure again", "observedSpecHash", status.ObservedSpecHash)
		}
		// Never reread or merge an observation into a newer producer document.
		return errors.Join(executionErr, err)
	}
	return executionErr
}

// stackClient assumes the desire's routing and authentication have been validated.
func (c *controller) stackClient(ctx context.Context, desire *coreapi.StackDesire) (*armdeploymentstacks.Client, error) {
	authentication := desire.Spec.Authentication
	if authentication.Type == coreapi.StackDesireAuthenticationTypeSMI {
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
	status  *coreapi.StackDesireStatus
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
func decideStackDesire(logger logr.Logger, desire *coreapi.StackDesire, now time.Time, lazyGET func() (*armdeploymentstacks.DeploymentStack, error)) decision {
	var desiredConditionType string
	var mutation action
	switch desire.Spec.Action {
	case coreapi.StackDesireActionApply:
		desiredConditionType, mutation = coreapi.StackDesireConditionSuccessfullyApplied, applyStack
	case coreapi.StackDesireActionDelete:
		desiredConditionType, mutation = coreapi.StackDesireConditionSuccessfullyDeleted, deleteStack
	}
	var validationErr error
	target := desire.Spec.TargetResourceID
	switch {
	case target == nil || target.Parent == nil:
		validationErr = errors.New("stack target and scope are required")
	case !strings.EqualFold(target.ResourceType.String(), "Microsoft.Resources/deploymentStacks"):
		validationErr = errors.New("stack target must be a Microsoft.Resources/deploymentStacks resource")
	case desire.Spec.Action != coreapi.StackDesireActionApply && desire.Spec.Action != coreapi.StackDesireActionDelete:
		validationErr = fmt.Errorf("unsupported stack action %q", desire.Spec.Action)
	case desire.Spec.Authentication.Type != coreapi.StackDesireAuthenticationTypeFPA && desire.Spec.Authentication.Type != coreapi.StackDesireAuthenticationTypeSMI:
		validationErr = fmt.Errorf("unsupported stack authentication %q", desire.Spec.Authentication.Type)
	case desire.Spec.Authentication.Type == coreapi.StackDesireAuthenticationTypeSMI && desire.Spec.Authentication.SMIResourceID == nil:
		validationErr = errors.New("SMI authentication requires smiResourceID")
	default:
		switch strings.ToLower(target.Parent.ResourceType.String()) {
		case "microsoft.resources/resourcegroups", "microsoft.resources/subscriptions", "microsoft.management/managementgroups":
		default:
			validationErr = fmt.Errorf("unsupported stack scope %q", target.Parent.ResourceType)
		}
	}
	hash, hashErr := coreapi.StackDesireSpecHash(desire.Spec)
	validationErr = errors.Join(validationErr, hashErr)
	if validationErr != nil {
		status := desire.Status
		if desiredConditionType == "" {
			status.Conditions = []metav1.Condition{
				{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
			}
		} else {
			status.Conditions = []metav1.Condition{
				{Type: desiredConditionType, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
				{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "PreCheckFailed", Message: validationErr.Error()},
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
		status := coreapi.StackDesireStatus{ObservedSpecHash: hash, ObservedAt: ptr.To(metav1.NewTime(now)), Conditions: []metav1.Condition{
			{Type: coreapi.StackDesireConditionSuccessfullyDeleted, Status: metav1.ConditionTrue, Reason: "StackAbsent", Message: "The deployment stack is absent"},
			{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
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
	var status *coreapi.StackDesireStatus
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
		status = &coreapi.StackDesireStatus{Stack: stack, Conditions: []metav1.Condition{
			{Type: desiredConditionType, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation"},
			{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
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
func changedObservationStatus(previous, status coreapi.StackDesireStatus, now time.Time) *coreapi.StackDesireStatus {
	if cmp.Equal(previous, status,
		cmpopts.IgnoreFields(coreapi.StackDesireStatus{}, "ObservedAt"),
		cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")) {
		return nil
	}
	return normalizeStatusTimes(previous, status, now)
}

// Preserve condition age while truth values match and preserve the attempt
// clock for metadata-only changes to the same completed operation.
func normalizeStatusTimes(previous, status coreapi.StackDesireStatus, now time.Time) *coreapi.StackDesireStatus {
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

func submitApply(ctx context.Context, client *armdeploymentstacks.Client, spec coreapi.StackDesireSpec) (*armdeploymentstacks.DeploymentStack, error) {
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

func submitDelete(ctx context.Context, client *armdeploymentstacks.Client, spec coreapi.StackDesireSpec) error {
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

func stackDesireStatusForAssociatedApply(hash string, stack *armdeploymentstacks.DeploymentStack) (*coreapi.StackDesireStatus, error) {
	status := &coreapi.StackDesireStatus{ObservedSpecHash: hash, Stack: stack}
	associatedStack := ptr.Deref(stack, armdeploymentstacks.DeploymentStack{})
	properties := ptr.Deref(associatedStack.Properties, armdeploymentstacks.DeploymentStackProperties{})
	state := ptr.Deref(properties.ProvisioningState, "")
	var err error
	switch state {
	case armdeploymentstacks.DeploymentStackProvisioningStateSucceeded:
		status.Conditions = []metav1.Condition{
			{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionTrue, Reason: "Succeeded", Message: "Azure completed the stack operation"},
			{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}
	case armdeploymentstacks.DeploymentStackProvisioningStateFailed, armdeploymentstacks.DeploymentStackProvisioningStateCanceled:
		message, invalid := classifyError(properties.Error)
		if invalid {
			status.Conditions = []metav1.Condition{
				{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: message},
				{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: message},
			}
		} else {
			status.Conditions = []metav1.Condition{
				{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "ExecutionFailed", Message: message},
				{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionTrue, Reason: "ExecutionFailed", Message: message},
			}
			err = errExecutionFailed
		}
	default:
		status.Conditions = []metav1.Condition{
			{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation"},
			{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
		}
	}
	return status, err
}

func stackDesireStatusForDeleteSubmission(hash string, err error) (*coreapi.StackDesireStatus, error) {
	if err != nil {
		return stackDesireStatusForRejectedSubmission(hash, coreapi.StackDesireConditionSuccessfullyDeleted, err)
	}
	// Even a synchronous Delete response is only acceptance; GET proves absence.
	return &coreapi.StackDesireStatus{ObservedSpecHash: hash, Conditions: []metav1.Condition{
		{Type: coreapi.StackDesireConditionSuccessfullyDeleted, Status: metav1.ConditionFalse, Reason: "WaitingForDeletion", Message: "Waiting for the deployment stack to disappear"},
		{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error"},
	}}, nil
}

func stackDesireStatusForRejectedSubmission(hash, conditionType string, err error) (*coreapi.StackDesireStatus, error) {
	var responseErr *azcore.ResponseError
	if !errors.As(err, &responseErr) {
		return nil, err
	}
	detail := &armdeploymentstacks.ErrorDetail{Code: ptr.To(responseErr.ErrorCode), Message: ptr.To(err.Error())}
	status := &coreapi.StackDesireStatus{ObservedSpecHash: hash, Error: detail}
	_, invalid := classifyError(detail)
	if invalid {
		status.Conditions = []metav1.Condition{
			{Type: conditionType, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: err.Error()},
			{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: err.Error()},
		}
		return status, nil
	}
	status.Conditions = []metav1.Condition{
		{Type: conditionType, Status: metav1.ConditionFalse, Reason: "RequestFailed", Message: err.Error()},
		{Type: coreapi.StackDesireConditionDegraded, Status: metav1.ConditionTrue, Reason: "RequestFailed", Message: err.Error()},
	}
	return status, err
}

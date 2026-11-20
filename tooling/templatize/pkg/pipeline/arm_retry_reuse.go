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

package pipeline

import (
	"context"
	"errors"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// pollExistingDeploymentOrOtherAttempt is pollAndGetOutputFromExistingDeployment for the step's own deployment,
// failing over to the deployments of the step's other retry attempts when its own had already failed or been
// cancelled at the first lookup: see findSucceededAttempt. inputs are the step's, or nil when its deployment name is
// not a digest of them; then there is nothing to fail over to.
func pollExistingDeploymentOrOtherAttempt(ctx context.Context, client *armresources.DeploymentsClient, getOperationsClient OperationsClientGetter, subscriptionID string, timeoutSeconds int, deploymentLevel, resourceGroup, deploymentName string, inputs *deploymentInputs) (bool, ArmOutput, DetailsProducer, error) {
	exists, output, details, err := pollAndGetOutputFromExistingDeployment(ctx, client, getOperationsClient, subscriptionID, timeoutSeconds, deploymentLevel, resourceGroup, deploymentName)
	var ended *endedDeploymentError
	if inputs == nil || !errors.As(err, &ended) {
		return exists, output, details, err
	}
	logger := logr.FromContextOrDiscard(ctx).WithValues("deployment", deploymentName)
	scope := newDeploymentScope(client, getOperationsClient, subscriptionID, deploymentLevel, resourceGroup)
	name, attemptOutput, found := findSucceededAttempt(ctx, logger, scope, otherAttemptDeploymentNames(*inputs))
	if !found {
		return exists, output, details, err
	}
	logger.Info("Reusing the outputs of another retry attempt's successful deployment with identical inputs; this attempt's deployment had ended unsuccessfully.", "provisioningState", ended.state, "succeededDeployment", name)
	return true, attemptOutput, scope.operations(name), nil
}

// maxRetryAttempts bounds the attempt numbers a step's deployments may have been made under: the retry loop runs
// attempts 0 to MaximumRetryCount-1 (see shouldExecuteStep), and MaximumRetryCount is at most 10. The bound doesn't
// follow the step's configured count, which may have changed since.
const maxRetryAttempts = 10

// otherAttemptDeploymentNames names the deployments the other attempts of a step would make, in attempt order.
// Deployment names are a digest of the step inputs, which include the retry attempt, so each retry deploys the same
// inputs under a name of its own.
func otherAttemptDeploymentNames(inputs deploymentInputs) []string {
	var names []string
	for attempt := range maxRetryAttempts {
		if attempt == inputs.RetryAttempt {
			continue
		}
		other := inputs
		other.RetryAttempt = attempt
		// the same inputs were just serialized for the step's own digest, so this can't fail
		if name, _, err := stepDigest(other); err == nil {
			names = append(names, name)
		}
	}
	return names
}

// findSucceededAttempt looks for a successful deployment among candidates, the deployments of the step's other retry
// attempts, and returns the first one's name and outputs.
//
// The step cache only records successes, so when an attempt fails and a retry succeeds, a later run with identical
// inputs (e.g. the upgrade phase of an upgrade test, for a step it doesn't change) finds the failed first attempt
// under its deployment name. The retry's success is just as valid for these inputs, so it is reused instead of
// failing the step, and the failed deployment is left as it is. The scope's successful deployments are listed once,
// rather than looking each candidate up: lookups of deployments that don't exist are retried for a while. Errors are
// only logged, without the request (which names the subscription): the step then fails on its own deployment.
func findSucceededAttempt(ctx context.Context, logger logr.Logger, scope deploymentScope, candidates []string) (string, ArmOutput, bool) {
	succeeded, err := scope.listSucceeded(ctx)
	if err != nil {
		logger.V(1).Info("Failed to list successful deployments to find another retry attempt's.", responseErrorFields(err)...)
		return "", nil, false
	}
	for _, name := range candidates {
		if !succeeded[name] {
			continue
		}
		// listed deployments may leave out their outputs
		output, err := scope.get(ctx, name)
		if err != nil {
			logger.V(1).Info("Failed to look up another retry attempt's successful deployment.", append(responseErrorFields(err), "otherDeployment", name)...)
			return "", nil, false
		}
		if output == nil || output.ProvisioningState == nil || *output.ProvisioningState != armresources.ProvisioningStateSucceeded {
			continue
		}
		return name, armOutputFromOutputs(output.Outputs), true
	}
	return "", nil, false
}

// deploymentScope is where a step's deployments live: its resource group, or its subscription for subscription-level
// steps. The SDK has separate calls for each scope.
type deploymentScope struct {
	client              *armresources.DeploymentsClient
	getOperationsClient OperationsClientGetter
	subscriptionID      string
	// resourceGroup is empty at subscription level.
	resourceGroup string
}

func newDeploymentScope(client *armresources.DeploymentsClient, getOperationsClient OperationsClientGetter, subscriptionID, deploymentLevel, resourceGroup string) deploymentScope {
	if deploymentLevel == "Subscription" {
		resourceGroup = ""
	}
	return deploymentScope{client: client, getOperationsClient: getOperationsClient, subscriptionID: subscriptionID, resourceGroup: resourceGroup}
}

func (s deploymentScope) get(ctx context.Context, name string) (*armresources.DeploymentPropertiesExtended, error) {
	if s.resourceGroup == "" {
		response, err := s.client.GetAtSubscriptionScope(ctx, name, nil)
		return response.Properties, err
	}
	response, err := s.client.Get(ctx, s.resourceGroup, name, nil)
	return response.Properties, err
}

// listSucceeded returns the names of the scope's successful deployments.
func (s deploymentScope) listSucceeded(ctx context.Context) (map[string]bool, error) {
	filter := to.Ptr("provisioningState eq 'Succeeded'")
	var nextPage func(ctx context.Context) ([]*armresources.DeploymentExtended, error)
	var more func() bool
	if s.resourceGroup == "" {
		pager := s.client.NewListAtSubscriptionScopePager(&armresources.DeploymentsClientListAtSubscriptionScopeOptions{Filter: filter})
		more = pager.More
		nextPage = func(ctx context.Context) ([]*armresources.DeploymentExtended, error) {
			page, err := pager.NextPage(ctx)
			return page.Value, err
		}
	} else {
		pager := s.client.NewListByResourceGroupPager(s.resourceGroup, &armresources.DeploymentsClientListByResourceGroupOptions{Filter: filter})
		more = pager.More
		nextPage = func(ctx context.Context) ([]*armresources.DeploymentExtended, error) {
			page, err := pager.NextPage(ctx)
			return page.Value, err
		}
	}
	succeeded := map[string]bool{}
	for more() {
		deployments, err := nextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, deployment := range deployments {
			if deployment != nil && deployment.Name != nil && deployment.Properties != nil && deployment.Properties.ProvisioningState != nil &&
				*deployment.Properties.ProvisioningState == armresources.ProvisioningStateSucceeded {
				succeeded[*deployment.Name] = true
			}
		}
	}
	return succeeded, nil
}

// operations lists the operations of one of the scope's deployments, for diagnostics.
func (s deploymentScope) operations(name string) DetailsProducer {
	if s.resourceGroup == "" {
		return DetermineOperationsForSubscriptionDeployment(s.getOperationsClient, s.subscriptionID, name)
	}
	return DetermineOperationsForResourceGroupDeployment(s.getOperationsClient, s.subscriptionID, s.resourceGroup, name)
}

// responseErrorFields describes an ARM request failure for logs without its request URL, which names the subscription.
func responseErrorFields(err error) []any {
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		return []any{"statusCode", responseErr.StatusCode, "errorCode", responseErr.ErrorCode}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return []any{"reason", "context done"}
	}
	// other errors, e.g. from the transport, may quote the URL, too
	return []any{"reason", "request failed"}
}

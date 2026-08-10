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

package validationutils

import (
	"context"
	"fmt"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// AzureClusterResourceGroupExistenceValidation validates that the Azure Resource
// Group part of the Cluster Resource being created exists beforehand.
type AzureClusterResourceGroupExistenceValidation struct {
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

func NewAzureClusterResourceGroupExistenceValidation(
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) *AzureClusterResourceGroupExistenceValidation {
	return &AzureClusterResourceGroupExistenceValidation{
		azureFPAClientBuilder: azureFPAClientBuilder,
	}
}

func (a *AzureClusterResourceGroupExistenceValidation) Name() string {
	return "AzureClusterResourceGroupExistenceValidation"
}

func (a *AzureClusterResourceGroupExistenceValidation) Validate(
	ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.HCPOpenShiftCluster,
) ValidationResult {
	// Full resource ID of the cluster's resource group. Falls back to just the name if Parent is nil.
	clusterResourceGroupStr := cluster.ID.ResourceGroupName
	if cluster.ID.Parent != nil {
		clusterResourceGroupStr = cluster.ID.Parent.String()
	}

	rgClient, err := a.azureFPAClientBuilder.ResourceGroupsClient(
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's resource group existence.",
			fmt.Sprintf("failed to get resource groups client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	_, err = rgClient.Get(ctx, cluster.ID.ResourceGroupName, nil)
	if azureclient.IsResourceGroupNotFoundErr(err) {
		internalAndUserMsg := fmt.Sprintf("Resource group %q does not exist.", clusterResourceGroupStr)
		return FailedValidation("ResourceGroupNotFound", internalAndUserMsg, internalAndUserMsg)
	}
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's resource group existence.",
			fmt.Sprintf("failed to get resource group: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	internalAndUserMsg := fmt.Sprintf("Resource group %q exists.", clusterResourceGroupStr)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalAndUserMsg, internalAndUserMsg)
}

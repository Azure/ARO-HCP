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

package validations

import (
	"context"
	"fmt"
	"time"

	"k8s.io/utils/ptr"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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
	ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster,
) *ValidationResult {
	rgClient, err := a.azureFPAClientBuilder.ResourceGroupsClient(
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return &ValidationResult{
			Outcome: OutcomeTypeUnknown,
			Unknown: &UnknownResult{
				Reason:                 "ClientError",
				ServiceProviderMessage: fmt.Sprintf("failed to get resource groups client: %s", err),
				UserMessage:            "Failed to check resource group existence.",
				ReportingPolicy:        ReportingPolicyTypeError,
			},
			EarliestRetryAfter: ptr.To(60 * time.Second),
		}
	}

	_, err = rgClient.Get(ctx, cluster.ID.ResourceGroupName, nil)
	if azureclient.IsResourceGroupNotFoundErr(err) {
		return &ValidationResult{
			Outcome: OutcomeTypeFailed,
			Failed: &FailedResult{
				Reason:                 "ResourceGroupNotFound",
				ServiceProviderMessage: fmt.Sprintf("resource group %q does not exist: %s", cluster.ID.ResourceGroupName, err),
				UserMessage:            fmt.Sprintf("Resource group %q does not exist.", cluster.ID.ResourceGroupName),
			},
			EarliestRetryAfter: ptr.To(60 * time.Second),
		}
	}
	if err != nil {
		return &ValidationResult{
			Outcome: OutcomeTypeUnknown,
			Unknown: &UnknownResult{
				Reason:                 "APIError",
				ServiceProviderMessage: fmt.Sprintf("failed to get resource group: %s", err),
				UserMessage:            "Failed to check resource group existence.",
				ReportingPolicy:        ReportingPolicyTypeError,
			},
			EarliestRetryAfter: ptr.To(60 * time.Second),
		}
	}

	return &ValidationResult{Outcome: OutcomeTypePassed}
}

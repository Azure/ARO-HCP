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
) ValidationResult {
	rgClient, err := a.azureFPAClientBuilder.ResourceGroupsClient(
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return ValidationResult{
			Outcome: ValidationOutcome{
				Type: OutcomeUnknown,
				Unknown: &UnknownResult{
					Reason:                 "InternalError",
					ServiceProviderMessage: fmt.Sprintf("failed to get resource groups client: %v", err),
					UserMessage:            "An internal error occurred while performing the validation.",
					ReportingPolicy:        ReportingPolicyReportError,
				},
			},
		}
	}

	_, err = rgClient.Get(ctx, cluster.ID.ResourceGroupName, nil)
	if azureclient.IsResourceGroupNotFoundErr(err) {
		return ValidationResult{
			Outcome: ValidationOutcome{
				Type: OutcomeFailed,
				Failed: &FailedResult{
					Reason:                 "ResourceGroupNotFound",
					ServiceProviderMessage: fmt.Sprintf("resource group does not exist: %v", err),
					UserMessage:            "The specified resource group does not exist.",
				},
			},
		}
	}
	if err != nil {
		return ValidationResult{
			Outcome: ValidationOutcome{
				Type: OutcomeUnknown,
				Unknown: &UnknownResult{
					Reason:                 "InternalError",
					ServiceProviderMessage: fmt.Sprintf("failed to get resource group: %v", err),
					UserMessage:            "An internal error occurred while performing the validation.",
					ReportingPolicy:        ReportingPolicyReportError,
				},
			},
		}
	}

	return ValidationResult{
		Outcome: ValidationOutcome{
			Type:   OutcomePassed,
			Passed: &PassedResult{},
		},
	}
}

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
	"strings"
	"time"

	"k8s.io/utils/ptr"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// The RpRegistrationValidation struct validates the states of several
// Azure Resource Providers associated with a clusters region, subscription, etc.
type AzureResourceProvidersRegistrationValidation struct {
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

func NewAzureResourceProvidersRegistrationValidation(
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) *AzureResourceProvidersRegistrationValidation {
	return &AzureResourceProvidersRegistrationValidation{
		azureFPAClientBuilder: azureFPAClientBuilder,
	}
}

func (v *AzureResourceProvidersRegistrationValidation) Name() string {
	return "AzureResourceProvidersRegistrationValidation"
}

func (v *AzureResourceProvidersRegistrationValidation) Validate(
	ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster,
) *ValidationResult {
	resourceProvidersToCheck := []string{
		"Microsoft.Authorization",
		"Microsoft.Compute",
		"Microsoft.Network",
		"Microsoft.Storage",
	}

	rpClient, err := v.azureFPAClientBuilder.ResourceProvidersClient(
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return &ValidationResult{
			Outcome: OutcomeTypeUnknown,
			Unknown: &UnknownResult{
				Reason:                 "ClientError",
				ServiceProviderMessage: fmt.Sprintf("failed to get resource providers client: %s", err),
				UserMessage:            "Failed to check resource provider registration status.",
				ReportingPolicy:        ReportingPolicyTypeError,
			},
			EarliestRetryAfter: ptr.To(60 * time.Second),
		}
	}

	var missingResourcesProviders []string
	for _, rp := range resourceProvidersToCheck {
		providerResp, err := rpClient.Get(ctx, rp, nil)
		if err != nil {
			return &ValidationResult{
				Outcome: OutcomeTypeUnknown,
				Unknown: &UnknownResult{
					Reason:                 "APIError",
					ServiceProviderMessage: fmt.Sprintf("failed to get resource provider %s: %s", rp, err),
					UserMessage:            "Failed to check resource provider registration status.",
					ReportingPolicy:        ReportingPolicyTypeError,
				},
				EarliestRetryAfter: ptr.To(60 * time.Second),
			}
		}
		if providerResp.RegistrationState == nil ||
			*providerResp.RegistrationState != "Registered" {
			missingResourcesProviders = append(missingResourcesProviders, rp)
		}
	}

	if len(missingResourcesProviders) > 0 {
		return &ValidationResult{
			Outcome: OutcomeTypeFailed,
			Failed: &FailedResult{
				Reason:                 "ResourceProvidersNotRegistered",
				ServiceProviderMessage: fmt.Sprintf("%d resource providers not registered: %s", len(missingResourcesProviders), strings.Join(missingResourcesProviders, ", ")),
				UserMessage:            fmt.Sprintf("The following Azure resource providers must be registered in the subscription: %s", strings.Join(missingResourcesProviders, ", ")),
			},
			EarliestRetryAfter: ptr.To(60 * time.Second),
		}
	}

	return &ValidationResult{Outcome: OutcomeTypePassed}
}

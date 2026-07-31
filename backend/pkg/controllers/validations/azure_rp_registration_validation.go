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

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
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
) error {
	resourceProvidersToCheck := []string{
		"Microsoft.Authorization",
		"Microsoft.Compute",
		"Microsoft.Network",
		"Microsoft.Storage",
	}

	missingResourcesProviders := []string{}

	rpClient, err := v.azureFPAClientBuilder.ResourceProvidersClient(
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get resource providers client: %w", err))
	}

	for _, rp := range resourceProvidersToCheck {
		providerResp, err := rpClient.Get(ctx, rp, nil)
		if err != nil {
			return err
		}
		if providerResp.RegistrationState == nil ||
			*providerResp.RegistrationState != "Registered" {
			missingResourcesProviders = append(missingResourcesProviders, rp)
		}
	}

	if len(missingResourcesProviders) > 0 {
		return utils.TrackError(fmt.Errorf("%v of the resource providers are not registered, or their state is empty: %s",
			len(missingResourcesProviders), strings.Join(missingResourcesProviders, ", ")))
	}

	return nil
}

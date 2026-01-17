package controllers

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
type AzureRPRegistrationValidation struct {
	azureFPAClientBuilder azureclient.FPAClientBuilder
}

func NewAzureRPRegistrationValidation(
	azureClientBuilder azureclient.FPAClientBuilder,
) *AzureRPRegistrationValidation {
	return &AzureRPRegistrationValidation{
		azureFPAClientBuilder: azureClientBuilder,
	}
}

func (v *AzureRPRegistrationValidation) Name() string {
	return "azure-rp-registration-validation"
}

func (v *AzureRPRegistrationValidation) Validate(
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

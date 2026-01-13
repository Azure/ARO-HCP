package controllers

import (
	"context"
	"fmt"
	"strings"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// RpRegistrationStateRegistered - Resource provider is registered
	azureRPRegistrationStateRegistered string = "Registered"
	azureRPRegistrationValidationName  string = "azure-rp-registration-validation"
)

// The RpRegistrationValidation struct validates the states of several
// Azure Resource Providers associated with a clusters region, subscription, etc.
type AzureRPRegistrationValidation struct {
	name                  string
	azureFPAClientBuilder azureclient.FPAClientBuilder
}

func NewAzureRPRegistrationValidation(
	name string,
	azureClientBuilder azureclient.FPAClientBuilder,
) *AzureRPRegistrationValidation {
	return &AzureRPRegistrationValidation{
		name:                  name,
		azureFPAClientBuilder: azureClientBuilder,
	}
}

func (v *AzureRPRegistrationValidation) Name() string {
	return v.name
}

func (v *AzureRPRegistrationValidation) Validate(ctx context.Context, cluster *api.HCPOpenShiftCluster) error {
	// TODO if we always get the logger from the context, a question that comes to my mind is: if we define a type
	// and we want that all of its methods always add the same decoration how would we do that? the context is per
	// method so we would need to call the With in every single method which seems a bit cumbersome and prone to errors.
	// An alternative could be to receive the context in a constructor function and then store it but it is not recommended
	// to store the context in a type in general. Even doing that the question would then become how would we combine the
	// information from the context received in the method than the one stored in the type. We would need to somehow
	// create a nwe logger in every method again which goes back to the same problem.
	logger := utils.LoggerFromContext(ctx)
	logger = logger.With("validation_name", azureRPRegistrationValidationName)
	// TODO should we always add the logger back to the context when we decorate it with With so it is
	// available just in case even if there are no functions that leverage the logger at the current point in time?
	ctx = utils.ContextWithLogger(ctx, logger)

	resourceProvidersToCheck := []string{
		"Microsoft.Authorization",
		"Microsoft.Compute",
		"Microsoft.Network",
		"Microsoft.Storage",
	}

	missingResourcesProviders := []string{}

	rpClient, err := v.getResourceProvidersClient(
		cluster.ID.SubscriptionID,
		// TODO is this the aro-hcp cluster tenant, is it always set, or do we need to get it somehow else? Figure out
		cluster.Identity.TenantID,
	)
	if err != nil {
		logger.Error("failed to get resource providers client", "error", err)
		return err
	}

	for _, rp := range resourceProvidersToCheck {
		providerResp, err := rpClient.Get(ctx, rp, nil)
		if err != nil {
			return err
		}
		if providerResp.RegistrationState == nil ||
			*providerResp.RegistrationState != azureRPRegistrationStateRegistered {
			missingResourcesProviders = append(missingResourcesProviders, rp)
		} else {
			logger.Debug(fmt.Sprintf("RP '%s' is registered", rp))
		}
	}

	if len(missingResourcesProviders) > 0 {
		return fmt.Errorf("%v of the resource providers are not registered, or their state is empty: %s",
			len(missingResourcesProviders), strings.Join(missingResourcesProviders, ", "))
	}

	logger.Debug("Validation success")
	return nil
}

func (v *AzureRPRegistrationValidation) getResourceProvidersClient(
	tenantID string, subscriptionID string,
) (azureclient.ResourceProvidersClient, error) {
	return v.azureFPAClientBuilder.ResourceProvidersClient(tenantID, subscriptionID)
}

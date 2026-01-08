package controllers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"

	"github.com/Azure/ARO-HCP/internal/api"

	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// RpRegistrationStateRegistered - Resource provider is registered
	azureRpRegistrationStateRegistered string = "Registered"
	azureRpRegistrationValidationName  string = "azure-rp-registration-validation"
)

// The RpRegistrationValidation struct validates the states of several
// Azure Resource Providers associated with a clusters region, subscription, etc.
type AzureRpRegistrationValidation struct {
	logger                           *slog.Logger
	name                             string
	resourceProvidersClientRetriever azureclient.ResourceProvidersClientRetriever
	fpaTokenCredRetriever            fpa.FirstPartyApplicationTokenCredentialRetriever
	azureCloudEnvironment            azureconfig.AzureCloudEnvironment
}

func NewAzureRpRegistrationValidation(
	l *slog.Logger,
	name string,
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	azureCloudEnvironment azureconfig.AzureCloudEnvironment,
) *AzureRpRegistrationValidation {
	logger := l.With("validation_name", azureRpRegistrationValidationName)
	return &AzureRpRegistrationValidation{
		logger:                logger,
		name:                  name,
		fpaTokenCredRetriever: fpaTokenCredRetriever,
		azureCloudEnvironment: azureCloudEnvironment,
	}
}

func (v *AzureRpRegistrationValidation) Name() string {
	return v.name
}

func (v *AzureRpRegistrationValidation) Validate(ctx context.Context, cluster *api.HCPOpenShiftCluster) error {
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
		v.azureCloudEnvironment.ArmClientOptions(),
	)
	if err != nil {
		v.logger.Error("failed to get resource providers client", "error", err)
		return err
	}

	for _, rp := range resourceProvidersToCheck {
		providerResp, err := rpClient.Get(ctx, rp, nil)
		if err != nil {
			return err
		}
		if providerResp.RegistrationState == nil ||
			*providerResp.RegistrationState != azureRpRegistrationStateRegistered {
			missingResourcesProviders = append(missingResourcesProviders, rp)
		} else {
			v.logger.Debug(fmt.Sprintf("RP '%s' is registered", rp))
		}
	}

	if len(missingResourcesProviders) > 0 {
		return fmt.Errorf("%v of the resource providers are not registered, or their state is empty: %s",
			len(missingResourcesProviders), strings.Join(missingResourcesProviders, ", "))
	}

	v.logger.Debug("Validation success")
	return nil
}

func (v *AzureRpRegistrationValidation) getResourceProvidersClient(subscriptionId string,
	tenantId string, clientOptions *arm.ClientOptions,
) (azureclient.ResourceProvidersClient, error) {
	credentials, err := v.fpaTokenCredRetriever.RetrieveCredential(tenantId)
	if err != nil {
		return nil, err
	}
	return azureclient.NewResourceProvidersClient(v.logger, subscriptionId, credentials, clientOptions)
}

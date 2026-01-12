package config

import (
	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
)

// AzureConfig represents Azure related configuration used by the service
type AzureConfig struct {
	// Cloud environment where the service is running on
	CloudEnvironment *AzureCloudEnvironment
	// AzureRuntimeConfig holds additional serialized configuration provided
	// to the service via a configuration file. This
	// is useful for pulling direct values from it.
	AzureRuntimeConfig *apisconfigv1.AzureRuntimeConfig

	// Other attributes in the future like the operators managed identities
	// configuration
	// OperatorsManagedIdentitiesConfig AzureOperatorsManagedIdentitiesConfig
}

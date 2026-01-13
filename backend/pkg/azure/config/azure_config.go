package config

import configv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"

// AzureConfig represents Azure related configuration used by the service
type AzureConfig struct {

	// Cloud environment where the service is running on
	CloudEnvironment AzureCloudEnvironment

	// Config holds the serialized config from the command line.  This is useful for pulling direct values (like URLs) from it.
	// If you need to do addition processing in order to be useful, consider creating something like AzureCloudEnvironment
	// that has logic for stitching the content together into useful interfaces.
	Config configv1.AzureRuntimeConfig

	// Other attributes in the future like the operators managed identities
	// configuration
	// OperatorsManagedIdentitiesConfig AzureOperatorsManagedIdentitiesConfig
}

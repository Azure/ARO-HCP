package config

// AzureRuntimeConfig represents Azure Runtime related configurations associated
// with the service.
type AzureRuntimeConfig struct {
	// Cloud environment where the service is running on
	CloudEnvironment AzureCloudEnvironment
}

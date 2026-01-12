package config

// AzureConfig represents Azure related configuration used by the service
type AzureConfig struct {
	RuntimeConfig AzureRuntimeConfig

	// Other attributes in the future like the operators managed identities
	// configuration
	// OperatorsManagedIdentitiesConfig AzureOperatorsManagedIdentitiesConfig
}

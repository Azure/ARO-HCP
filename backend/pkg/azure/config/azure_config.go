package config

// AzureConfig represents Azure related configuration used by the service
type AzureConfig struct {
	// Cloud environment where the service is running on
	CloudEnvironment AzureCloudEnvironment
	// The ID of the tenant where the service is running on
	ServiceTenantID string
	// The Azure Container Registry used to source relevant OCP images
	OCPImagesACR AzureContainerRegistry
	// Data plane identities OIDC configuration
	DataPlaneIdentitiesOIDCConfiguration AzureDataPlaneIdentitiesOIDCConfiguration
	// ManagedIdentitiesDataPlaneAudienceResource is the endpoint used to connect with the
	// Managed Identities Resource Provider (MI RP)
	ManagedIdentitiesDataPlaneAudienceResource string
	// TLSCertificatesConfig holds the configuration used to generate tls
	// certificates for user-facing apis, such as kube-apiserver and ingress.
	TLSCertificatesConfig TLSCertificatesConfig

	// Other attributes in the future like the operators managed identities
	// configuration
	// OperatorsManagedIdentitiesConfig AzureOperatorsManagedIdentitiesConfig
}

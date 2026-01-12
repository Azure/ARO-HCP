package v1

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/resourceid"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/urlvalidation"
)

// AzureRuntimeConfig represents user provided Azure related configuration for running the service
type AzureRuntimeConfig struct {
	// Cloud environment where the service is running on
	CloudEnvironment CloudEnvironment `json:"cloudEnvironment"`
	// The ID of the tenant where the service is running on
	ServiceTenantID string `json:"tenantID"`
	// Azure Container Registry containing OCP Images
	OCPImagesACR AzureContainerRegistry `json:"ocpImagesACR"`
	// Data plane identities OIDC configuration
	DataPlaneIdentitiesOIDCConfiguration DataPlaneIdentitiesOIDCConfiguration `json:"dataPlaneIdentitiesOIDCConfiguration"`
	// ManagedIdentitiesDataPlaneAudienceResource is the endpoint used to connect with the
	// Managed Identities Resource Provider (MI RP)
	ManagedIdentitiesDataPlaneAudienceResource string `json:"managedIdentitiesDataPlaneAudienceResource"`
	// TLSCertificatesConfig holds the configuration used to generate TLS
	// certificates for user-facing apis, such as kube-apiserver and ingress.
	// This config is optional. When provided (and with enabled: true), TLS
	// certificates will be provisioned in Azure Key Vault for the kube-apiserver
	// and ingress. When not provided (or when enabled: false), the default
	// Hypershift generated certificates are used instead, and Azure Key Vault
	// generation is skipped entirely.
	TLSCertificatesConfig TLSCertificatesConfig `json:"tlsCertificatesConfig"`
}

// Validate performs validation on the AzureRuntimeConfig properties
func (c AzureRuntimeConfig) Validate() error {
	if err := c.CloudEnvironment.Validate(); err != nil {
		return err
	}

	if err := c.OCPImagesACR.Validate(); err != nil {
		return err
	}

	if c.ServiceTenantID == "" {
		return fmt.Errorf("tenant_id is mandatory")
	}

	if err := c.validateManagedIdentitiesDataPlaneAudienceResource(); err != nil {
		return err
	}

	if err := c.DataPlaneIdentitiesOIDCConfiguration.Validate(); err != nil {
		return fmt.Errorf("failed to load 'data_plane_identities_oidc_configuration': %w", err)
	}

	if err := c.TLSCertificatesConfig.Validate(); err != nil {
		return err
	}

	return nil
}

func (c AzureRuntimeConfig) validateManagedIdentitiesDataPlaneAudienceResource() error {
	if c.ManagedIdentitiesDataPlaneAudienceResource == "" {
		return fmt.Errorf("managed_identities_data_plane_audience_resource is mandatory")
	}
	u, err := url.ParseRequestURI(c.ManagedIdentitiesDataPlaneAudienceResource)
	if err != nil {
		return fmt.Errorf("managed_identities_data_plane_audience_resource is invalid https url: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("managed_identities_data_plane_audience_resource must have a 'HTTPS' scheme")
	}
	return nil
}

// CloudEnvironment represents the cloud environment where the service is running on
// Accepted values are:
// - AzureChinaCloud
// - AzurePublicCloud
// - AzureUSGovernmentCloud
type CloudEnvironment string

func (c CloudEnvironment) String() string {
	return string(c)
}

// Validate - returns an error if the given cloud environment was not specified or is not supported
func (c CloudEnvironment) Validate() error {
	var supportedAzureCloudEnvironmentsStrings = []string{
		"AzureChinaCloud", "AzurePublicCloud", "AzureUSGovernmentCloud",
	}

	var supportedAzureCloudEnvironments []CloudEnvironment = []CloudEnvironment{
		CloudEnvironment(supportedAzureCloudEnvironmentsStrings[0]),
		CloudEnvironment(supportedAzureCloudEnvironmentsStrings[1]),
		CloudEnvironment(supportedAzureCloudEnvironmentsStrings[2]),
	}

	if c.String() == "" {
		return fmt.Errorf("cloud_environment is mandatory, please select one from the following list: %s",
			strings.Join(supportedAzureCloudEnvironmentsStrings, ","),
		)
	}

	isSupported := slices.Contains(supportedAzureCloudEnvironments, c)
	if !isSupported {
		return fmt.Errorf("cloud_environment '%s' is not supported, please select one from the following list: %s",
			c.String(), strings.Join(supportedAzureCloudEnvironmentsStrings, ","))
	}
	return nil
}

type DataPlaneIdentitiesOIDCConfiguration struct {
	// Name of the storage account blob container
	StorageAccountBlobContainerName string `json:"storage_account_blob_container_name"`
	// URL of the storage account blob service, e.g. https://<storage-account>.blob.core.windows.net/
	StorageAccountBlobServiceURL string `json:"storage_account_blob_service_url"`
	// OIDC base issuer URL, e.g. https://<storage-account>.z1.web.core.windows.net/
	// The system's certificate store is used to verify the OIDC issuer's certificate.
	OIDCIssuerBaseURL string `json:"oidc_issuer_base_url"`
}

type AzureContainerRegistry struct {
	// Resource Id of the Azure Container Registry
	ResourceID azcorearm.ResourceID `json:"resource_id"`
	// URL of the Azure Container Registry.
	// It should only contain the hostname, without any protocol, port or paths.
	URL string `json:"url"`
	// Scope map name for the Azure Container Registry repository
	ScopeMapName string `json:"scope_map_name"`
}

func (r AzureContainerRegistry) Validate() error {
	err := resourceid.ValidateACRResourceID(r.ResourceID)
	if err != nil {
		return err
	}

	if r.URL == "" {
		return fmt.Errorf("url for OCP images ACR required")
	}

	if strings.HasPrefix(r.URL, "https://") || strings.HasPrefix(r.URL, "http://") {
		return fmt.Errorf("url for OCP images ACR should not contain protocol")
	}
	// adds protocol for parsing to ensure that the host is set correctly when parsed, otherwise it is set as a
	// path in the parsed url
	parsedUrl, err := url.Parse("http://" + r.URL)
	if err != nil {
		return fmt.Errorf("url for OCP images ACR is not valid")
	}
	// the given acr url should be the same as the parsed url's hostname, which does not include any ports and paths
	if parsedUrl.Hostname() != r.URL {
		return fmt.Errorf("url for OCP images ACR should not contain port or paths")
	}

	splitUrl := strings.Split(r.URL, ".")
	nameFromUrl := splitUrl[0]
	if r.ResourceID.Name != nameFromUrl {
		return fmt.Errorf("url for OCP images ACR contains incorrect resource name")
	}

	if r.ScopeMapName == "" {
		return fmt.Errorf("scope_map_name for OCP images ACR required")
	}

	return nil
}

// Validate - returns an error if the given data plane OIDC configuration was not specified or is not supported
func (c DataPlaneIdentitiesOIDCConfiguration) Validate() error {
	if c.StorageAccountBlobContainerName == "" {
		return fmt.Errorf("'storage_account_blob_container_name' is mandatory")
	}
	if c.StorageAccountBlobServiceURL == "" {
		return fmt.Errorf("'storage_account_blob_service_url' is mandatory")
	}
	if c.OIDCIssuerBaseURL == "" {
		return fmt.Errorf("'oidc_issuer_base_url' is mandatory")
	}

	err := urlvalidation.ValidateAzureServiceURL(c.StorageAccountBlobServiceURL)
	if err != nil {
		return fmt.Errorf("attribute 'storage_account_blob_service_url' is invalid: %w", err)
	}
	err = urlvalidation.ValidateAzureServiceURL(c.OIDCIssuerBaseURL)
	if err != nil {
		return fmt.Errorf("attribute 'oidc_issuer_base_url' is invalid: %w", err)
	}

	return nil
}

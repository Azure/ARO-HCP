package v1

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/resourceid"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/urlvalidation"

	"k8s.io/apimachinery/pkg/util/validation/field"
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
func (c AzureRuntimeConfig) Validate() field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, c.CloudEnvironment.Validate(field.NewPath("cloudEnvironment"))...)

	if c.ServiceTenantID == "" {
		errs = append(errs, field.Required(field.NewPath("tenantID"), "attribute is required"))
	}

	errs = append(errs, c.OCPImagesACR.Validate(field.NewPath("ocpImagesACR"))...)

	errs = append(errs, c.DataPlaneIdentitiesOIDCConfiguration.Validate(field.NewPath("dataPlaneIdentitiesOIDCConfiguration"))...)

	errs = append(errs, c.validateManagedIdentitiesDataPlaneAudienceResource(
		field.NewPath("managedIdentitiesDataPlaneAudienceResource"))...,
	)

	errs = append(errs, c.TLSCertificatesConfig.Validate(field.NewPath("tlsCertificatesConfig"))...)

	return errs
}

func (c AzureRuntimeConfig) validateManagedIdentitiesDataPlaneAudienceResource(fldPath *field.Path) field.ErrorList {
	if c.ManagedIdentitiesDataPlaneAudienceResource == "" {
		return field.ErrorList{field.Required(fldPath, "attribute is required")}
	}

	u, err := url.ParseRequestURI(c.ManagedIdentitiesDataPlaneAudienceResource)
	if err != nil {
		return field.ErrorList{
			field.Invalid(fldPath, c.ManagedIdentitiesDataPlaneAudienceResource,
				fmt.Sprintf("attribute is not a valid url: %v", err)),
		}
	}

	if u.Scheme != "https" {
		return field.ErrorList{
			field.Invalid(fldPath, c.ManagedIdentitiesDataPlaneAudienceResource,
				"attribute must have a 'https' scheme",
			),
		}
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

func (c CloudEnvironment) Validate(fldPath *field.Path) field.ErrorList {
	var supportedAzureCloudEnvironmentsStrings = []string{
		"AzureChinaCloud", "AzurePublicCloud", "AzureUSGovernmentCloud",
	}

	var supportedAzureCloudEnvironments []CloudEnvironment = []CloudEnvironment{
		CloudEnvironment(supportedAzureCloudEnvironmentsStrings[0]),
		CloudEnvironment(supportedAzureCloudEnvironmentsStrings[1]),
		CloudEnvironment(supportedAzureCloudEnvironmentsStrings[2]),
	}

	if c.String() == "" {
		return field.ErrorList{
			field.Required(
				fldPath,
				fmt.Sprintf("attribute is required. Accepted values are: %s",
					strings.Join(supportedAzureCloudEnvironmentsStrings, ","),
				),
			),
		}

	}

	isSupported := slices.Contains(supportedAzureCloudEnvironments, c)
	if !isSupported {
		return field.ErrorList{
			field.Invalid(
				fldPath,
				c.String(),
				fmt.Sprintf("attribute is not supported. Accepted values are: %s",
					strings.Join(supportedAzureCloudEnvironmentsStrings, ","),
				),
			),
		}
	}

	return nil
}

type DataPlaneIdentitiesOIDCConfiguration struct {
	// Name of the storage account blob container
	StorageAccountBlobContainerName string `json:"storageAccountBlobContainerName"`
	// URL of the storage account blob service, e.g. https://<storage-account>.blob.core.windows.net/
	StorageAccountBlobServiceURL string `json:"storageAccountBlobServiceURL"`
	// OIDC base issuer URL, e.g. https://<storage-account>.z1.web.core.windows.net/
	// The system's certificate store is used to verify the OIDC issuer's certificate.
	OIDCIssuerBaseURL string `json:"oidcIssuerBaseURL"`
}

type AzureContainerRegistry struct {
	// Resource Id of the Azure Container Registry
	ResourceID azcorearm.ResourceID `json:"resourceID"`
	// URL of the Azure Container Registry.
	// It should only contain the hostname, without any protocol, port or paths.
	URL string `json:"url"`
	// Scope map name for the Azure Container Registry repository
	ScopeMapName string `json:"scopeMapName"`
}

func (r *AzureContainerRegistry) validateACRURL(fldPath *field.Path) field.ErrorList {
	if r.URL == "" {
		return field.ErrorList{field.Required(fldPath, "attribute is required")}
	}

	if strings.Contains(r.URL, "://") {
		return field.ErrorList{field.Invalid(fldPath, r.URL, "url scheme is not allowed")}
	}

	// adds protocol for parsing to ensure that the host is set correctly when parsed, otherwise it is set as a
	// path in the parsed url
	parsedUrl, err := url.Parse("http://" + r.URL)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, r.URL, fmt.Sprintf("url is not valid: %v", err))}
	}

	// the given acr url should be the same as the parsed url's hostname, which does not include any ports and paths
	if parsedUrl.Hostname() != r.URL {
		return field.ErrorList{field.Invalid(fldPath, r.URL, "url cannot contain port or paths")}
	}
	splitUrl := strings.Split(r.URL, ".")
	nameFromUrl := splitUrl[0]
	if r.ResourceID.Name != nameFromUrl {
		return field.ErrorList{field.Invalid(fldPath, r.URL, "url contains incorrect resource name")}
	}

	return nil
}

func (r AzureContainerRegistry) Validate(fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	err := resourceid.ValidateACRResourceID(r.ResourceID)
	if err != nil {
		errs = append(errs, field.Invalid(
			fldPath.Child("resourceID"), r.ResourceID, fmt.Sprintf("attribute is not a valid resource ID: %v", err)),
		)
	}

	errs = append(errs, r.validateACRURL(fldPath.Child("url"))...)

	if r.ScopeMapName == "" {
		errs = append(errs, field.Required(fldPath.Child("scopeMapName"), "attribute is required"))
	}

	return errs
}

// Validate - returns an error if the given data plane OIDC configuration was not specified or is not supported
func (c DataPlaneIdentitiesOIDCConfiguration) Validate(fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	if c.StorageAccountBlobContainerName == "" {
		errs = append(errs, field.Required(fldPath.Child("storageAccountBlobContainerName"), "attribute is required"))
	}
	if c.StorageAccountBlobServiceURL == "" {
		errs = append(errs, field.Required(fldPath.Child("storageAccountBlobServiceURL"), "attribute is required"))
	}

	errs = append(errs, c.validateStorageAccountBlobServiceURL(fldPath.Child("storageAccountBlobServiceURL"))...)

	errs = append(errs, c.validateOIDCIssuerBaseURL(fldPath.Child("oidcIssuerBaseURL"))...)

	return errs
}

func (c DataPlaneIdentitiesOIDCConfiguration) validateStorageAccountBlobServiceURL(fldPath *field.Path) field.ErrorList {
	if c.StorageAccountBlobServiceURL == "" {
		return field.ErrorList{field.Required(fldPath, "attribute is required")}
	}

	err := urlvalidation.ValidateAzureServiceURL(c.StorageAccountBlobServiceURL)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, c.StorageAccountBlobServiceURL, fmt.Sprintf("attribute is not a valid url: %v", err))}
	}

	return nil
}

func (c DataPlaneIdentitiesOIDCConfiguration) validateOIDCIssuerBaseURL(fldPath *field.Path) field.ErrorList {
	if c.OIDCIssuerBaseURL == "" {
		return field.ErrorList{field.Required(fldPath, "attribute is required")}
	}

	err := urlvalidation.ValidateAzureServiceURL(c.OIDCIssuerBaseURL)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, c.OIDCIssuerBaseURL, fmt.Sprintf("attribute is not a valid url: %v", err))}
	}

	return nil
}

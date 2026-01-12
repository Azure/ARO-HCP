// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/validation"
)

// AzureRuntimeConfig represents user provided Azure related configuration for running the service
type AzureRuntimeConfig struct {
	// Cloud environment where the service is running on
	CloudEnvironmentName CloudEnvironmentName `json:"cloudEnvironment"`
	// The ID of the tenant where the service is running on
	ServiceTenantID string `json:"tenantID"`
	// Azure Container Registry containing OCP Images
	OCPImagesACR AzureContainerRegistry `json:"ocpImagesACR"`
	// Data plane identities OIDC configuration
	DataPlaneIdentitiesOIDCConfiguration DataPlaneIdentitiesOIDCConfiguration `json:"dataPlaneIdentitiesOIDCConfiguration"`
	// ManagedIdentitiesDataPlaneAudienceResource is the endpoint used to connect with the
	// Managed Identities Resource Provider (MI RP). The scheme must be https.
	// The system's certificate store is used to verify the OIDC issuer's certificate.
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
func (c AzureRuntimeConfig) Validate(ctx context.Context, op operation.Operation) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, c.CloudEnvironmentName.Validate(ctx, op, field.NewPath("cloudEnvironment"))...)

	errs = append(errs, validate.RequiredValue(ctx, op, field.NewPath("tenantID"), &c.ServiceTenantID, nil)...)

	errs = append(errs, c.OCPImagesACR.Validate(ctx, op, field.NewPath("ocpImagesACR"))...)

	errs = append(errs, c.DataPlaneIdentitiesOIDCConfiguration.Validate(ctx, op, field.NewPath("dataPlaneIdentitiesOIDCConfiguration"))...)

	errs = append(errs, c.validateManagedIdentitiesDataPlaneAudienceResource(
		ctx, op, field.NewPath("managedIdentitiesDataPlaneAudienceResource"))...,
	)

	errs = append(errs, c.TLSCertificatesConfig.Validate(ctx, op, field.NewPath("tlsCertificatesConfig"))...)

	return errs
}

func (c AzureRuntimeConfig) validateManagedIdentitiesDataPlaneAudienceResource(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath, &c.ManagedIdentitiesDataPlaneAudienceResource, nil)...)

	if len(c.ManagedIdentitiesDataPlaneAudienceResource) > 0 {
		u, err := url.Parse(c.ManagedIdentitiesDataPlaneAudienceResource)
		if err == nil {
			if u.Scheme != "https" {
				errs = append(errs, field.Invalid(fldPath, c.ManagedIdentitiesDataPlaneAudienceResource,
					"attribute must have a 'https' scheme"))
			}
		} else {
			errs = append(errs, field.Invalid(fldPath, c.ManagedIdentitiesDataPlaneAudienceResource,
				fmt.Sprintf("attribute is not a valid url: %v", err)))
		}
	}

	return errs
}

// CloudEnvironmentName represents the cloud environment where the service is running on
// Accepted values are:
// - AzureChinaCloud
// - AzurePublicCloud
// - AzureUSGovernmentCloud
type CloudEnvironmentName string

const (
	AzureChinaCloud        CloudEnvironmentName = "AzureChinaCloud"
	AzurePublicCloud       CloudEnvironmentName = "AzurePublicCloud"
	AzureUSGovernmentCloud CloudEnvironmentName = "AzureUSGovernmentCloud"
)

var (
	// validCloudEnvironmentNames is a set of valid cloud environment names. As of now,
	// we have only verified AzurePublicCloud.
	validCloudEnvironmentNames = sets.New[CloudEnvironmentName](
		AzurePublicCloud,
		AzureUSGovernmentCloud,
		AzureChinaCloud,
	)
)

func (c CloudEnvironmentName) Validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	return validate.Enum(ctx, op, fldPath, &c, nil, validCloudEnvironmentNames)
}

type DataPlaneIdentitiesOIDCConfiguration struct {
	// Name of the storage account blob container
	StorageAccountBlobContainerName string `json:"storageAccountBlobContainerName"`
	// URL of the storage account blob service, e.g. https://<storage-account>.blob.core.windows.net/
	// The system's certificate store is used to verify the certificate.
	StorageAccountBlobServiceURL string `json:"storageAccountBlobServiceURL"`
	// OIDC base issuer URL, e.g. https://<storage-account>.z1.web.core.windows.net/
	// The system's certificate store is used to verify the certificate.
	OIDCIssuerBaseURL string `json:"oidcIssuerBaseURL"`
}

type AzureContainerRegistry struct {
	// Resource Id of the Azure Container Registry
	ResourceID *azcorearm.ResourceID `json:"resourceID"`
	// Hostname of the Azure Container Registry.
	// It should only contain the hostname, without any protocol, port or paths.
	// The system's certificate store is used to verify the certificate.
	Hostname string `json:"hostname"`
	// Scope map name for the Azure Container Registry repository
	ScopeMapName string `json:"scopeMapName"`
}

func (r *AzureContainerRegistry) validateACRHostname(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath, &r.Hostname, nil)...)

	if strings.Contains(r.Hostname, "://") {
		errs = append(errs, field.Invalid(fldPath, r.Hostname, "url scheme is not allowed"))
	}

	// adds protocol for parsing to ensure that the host is set correctly when parsed, otherwise it is set as a
	// path in the parsed url
	parsedURL, err := url.Parse("http://" + r.Hostname)
	if err == nil {
		// the given acr url should be the same as the parsed url's hostname, which does not include any ports and paths
		if parsedURL.Hostname() != r.Hostname {
			errs = append(errs, field.Invalid(fldPath, r.Hostname, "cannot contain port or paths"))
		}

		splitUrl := strings.Split(r.Hostname, ".")
		nameFromUrl := splitUrl[0]
		if r.ResourceID.Name != nameFromUrl {
			errs = append(errs, field.Invalid(fldPath, r.Hostname, "contains incorrect resource name"))
		}
	} else {
		errs = append(errs, field.Invalid(fldPath, r.Hostname, fmt.Sprintf("url is not valid: %v", err)))
	}

	return errs
}

func (r AzureContainerRegistry) Validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("resourceID"), r.ResourceID, nil)...)
	errs = append(errs, validation.ValidateACRResourceID(ctx, op, fldPath.Child("resourceID"), r.ResourceID)...)

	errs = append(errs, r.validateACRHostname(ctx, op, fldPath.Child("hostname"))...)

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("scopeMapName"), &r.ScopeMapName, nil)...)

	return errs
}

// Validate - returns an error if the given data plane OIDC configuration was not specified or is not supported
func (c DataPlaneIdentitiesOIDCConfiguration) Validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("storageAccountBlobContainerName"), &c.StorageAccountBlobContainerName, nil)...)

	errs = append(errs, c.validateStorageAccountBlobServiceURL(ctx, op, fldPath.Child("storageAccountBlobServiceURL"))...)

	errs = append(errs, c.validateOIDCIssuerBaseURL(ctx, op, fldPath.Child("oidcIssuerBaseURL"))...)

	return errs
}

func (c DataPlaneIdentitiesOIDCConfiguration) validateStorageAccountBlobServiceURL(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath, &c.StorageAccountBlobServiceURL, nil)...)
	if len(c.StorageAccountBlobServiceURL) > 0 {
		errs = append(errs, validation.ValidateAzureServiceURL(ctx, op, fldPath, c.StorageAccountBlobServiceURL)...)
	}

	return errs
}

func (c DataPlaneIdentitiesOIDCConfiguration) validateOIDCIssuerBaseURL(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath, &c.OIDCIssuerBaseURL, nil)...)
	if len(c.OIDCIssuerBaseURL) > 0 {
		errs = append(errs, validation.ValidateAzureServiceURL(ctx, op, fldPath, c.OIDCIssuerBaseURL)...)
	}

	return errs
}

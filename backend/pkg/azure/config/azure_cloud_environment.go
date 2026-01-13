package config

import (
	"fmt"

	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/tracing/azotel"
)

// AzureCloudEnvironment represents an Azure cloud environment.
type AzureCloudEnvironment struct {
	// cloudEnvironmentName of the cloud environment (AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud, ...)
	cloudEnvironmentName AzureCloudEnvironmentName
	// Configuration of the cloud environment
	configuration cloud.Configuration
	// RDBMS scope of the cloud environment
	rdbmsScope string
	// Check Access V2 environment of the cloud environment
	checkAccessV2Environment checkAccessV2Environment
	// Options for the Azure clients.
	clientOptions policy.ClientOptions
}

// checkAccessV2Environment represents the information associated to Microsoft's
// Check Access V2 API.
type checkAccessV2Environment struct {
	// domainSuffix is the domain suffix used as part of the domain name
	// of the endpoint of the Check Access V2 API.
	domainSuffix string
	// scope is the permission scope to be requested for the access token to
	// communicate with the Check Access V2 API.
	scope string
}

// AzureCloudEnvironmentName represents the name of an Azure cloud environment.
// Accepted values are:
// - AzureChinaCloud
// - AzurePublicCloud
// - AzureUSGovernmentCloud
type AzureCloudEnvironmentName string

const (
	AzureChinaCloud        AzureCloudEnvironmentName = "AzureChinaCloud"
	AzurePublicCloud       AzureCloudEnvironmentName = "AzurePublicCloud"
	AzureUSGovernmentCloud AzureCloudEnvironmentName = "AzureUSGovernmentCloud"
)

func NewAzureCloudEnvironment(cloudEnvironmentName string, tracerProvider trace.TracerProvider) (AzureCloudEnvironment, error) {
	if cloudEnvironmentName == "" {
		return AzureCloudEnvironment{}, fmt.Errorf("cloud environment cannot be empty")
	}

	var azureCloudEnvironmentConfigurationMapping = map[AzureCloudEnvironmentName]struct {
		cloud                    cloud.Configuration
		rdbmsScope               string
		checkAccessV2Environment checkAccessV2Environment
	}{
		AzureChinaCloud: {
			cloud:      cloud.AzureChina,
			rdbmsScope: "https://ossrdbms-aad.database.chinacloudapi.cn",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.cn",
				scope:        "https://authorization.azure.cn/.default",
			},
		},
		AzurePublicCloud: {
			cloud:      cloud.AzurePublic,
			rdbmsScope: "https://ossrdbms-aad.database.windows.net/.default",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.net",
				scope:        "https://authorization.azure.net/.default",
			},
		},
		AzureUSGovernmentCloud: {
			cloud:      cloud.AzureGovernment,
			rdbmsScope: "https://ossrdbms-aad.database.usgovcloudapi.net",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.us",
				scope:        "https://authorization.azure.us/.default",
			},
		},
	}

	typedAzureCloudEnvironmentName := AzureCloudEnvironmentName(cloudEnvironmentName)
	configuration, ok := azureCloudEnvironmentConfigurationMapping[typedAzureCloudEnvironmentName]
	if !ok {
		return AzureCloudEnvironment{},
			fmt.Errorf("cloud environment %q is not supported", cloudEnvironmentName)
	}

	clientOptions := policy.ClientOptions{
		Cloud: configuration.cloud,
	}
	if tracerProvider != nil {
		clientOptions.TracingProvider = azotel.NewTracingProvider(tracerProvider, nil)
	}

	return AzureCloudEnvironment{
		cloudEnvironmentName:     typedAzureCloudEnvironmentName,
		configuration:            configuration.cloud,
		rdbmsScope:               configuration.rdbmsScope,
		checkAccessV2Environment: configuration.checkAccessV2Environment,
		clientOptions:            clientOptions,
	}, nil
}

// AZCoreClientOptions returns an azcore.ClientOptions instance from the current
// Azure Cloud environment. The method returns the same result as calling
// PolicyClientOptions() because azcore.ClientOptions is a type alias of
// policy.ClientOptions.
func (a AzureCloudEnvironment) AZCoreClientOptions() azcore.ClientOptions {
	return a.clientOptions
}

// PolicyClientOptions returns a policy.ClientOptions instance from the current
// Azure Cloud environment. The method returns the same result as calling
// AZCoreClientOptions() because azcore.ClientOptions is a type alias of
// policy.ClientOptions.
func (a AzureCloudEnvironment) PolicyClientOptions() policy.ClientOptions {
	return a.clientOptions
}

// ArmClientOptions returns an arm.ClientOptions instance from the current
// Azure Cloud environment.
func (a AzureCloudEnvironment) ARMClientOptions() *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: a.clientOptions,
	}
}

func (a AzureCloudEnvironment) CloudEnvironmentName() AzureCloudEnvironmentName {
	return a.cloudEnvironmentName
}

func (a AzureCloudEnvironment) Configuration() cloud.Configuration {
	return a.configuration
}

func (a AzureCloudEnvironment) RDBMSScope() string {
	return a.rdbmsScope
}

func (a AzureCloudEnvironment) CheckAccessV2Scope() string {
	return a.checkAccessV2Environment.scope
}

func (a AzureCloudEnvironment) CheckAccessV2Endpoint(region string) string {
	// TODO: determine the latest stable version recommended by MSFT (ARO-18008).
	return fmt.Sprintf(
		"https://%s.authorization.%s/providers/Microsoft.Authorization/checkAccess?api-version=2021-06-01-preview",
		region, a.checkAccessV2Environment.domainSuffix)
}

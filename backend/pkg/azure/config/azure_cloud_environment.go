package config

import (
	"fmt"

	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/tracing/azotel"
	"github.com/pkg/errors"
)

// AzureCloudEnvironment represents an Azure cloud environment.
type AzureCloudEnvironment struct {
	// ID of the cloud environment (AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud, ...)
	id string
	// Configuration of the cloud environment
	configuration cloud.Configuration
	// RDBMS scope of the cloud environment
	rdbmsScope string
	// Check Access V2 environment of the cloud environment
	checkAccessV2Environment checkAccessV2Environment
	// Options for the Azure clients.
	clientOptions policy.ClientOptions
}

type checkAccessV2Environment struct {
	domainSuffix string
	scope        string
}

// AzureCloudEnvironmentBuilder can build an Azure cloud environment.
type AzureCloudEnvironmentBuilder struct {
	cloudEnvironment string
	tracerProvider   trace.TracerProvider
}

func NewAzureCloudEnvironmentBuilder() *AzureCloudEnvironmentBuilder {
	return &AzureCloudEnvironmentBuilder{}
}

func (a *AzureCloudEnvironmentBuilder) CloudEnvironment(cloudEnvironment string) *AzureCloudEnvironmentBuilder {
	a.cloudEnvironment = cloudEnvironment
	return a
}

func (a *AzureCloudEnvironmentBuilder) TracerProvider(
	tracerProvider trace.TracerProvider) *AzureCloudEnvironmentBuilder {
	a.tracerProvider = tracerProvider
	return a
}

func (a *AzureCloudEnvironmentBuilder) Build() (AzureCloudEnvironment, error) {
	if a.cloudEnvironment == "" {
		return AzureCloudEnvironment{}, errors.Errorf("cloud environment cannot be empty")
	}

	var azureCloudEnvironmentConfigurationMapping = map[string]struct {
		cloud                    cloud.Configuration
		rdbmsScope               string
		checkAccessV2Environment checkAccessV2Environment
	}{
		"AzureChinaCloud": {
			cloud:      cloud.AzureChina,
			rdbmsScope: "https://ossrdbms-aad.database.chinacloudapi.cn",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.cn",
				scope:        "https://authorization.azure.cn/.default",
			},
		},
		"AzurePublicCloud": {
			cloud:      cloud.AzurePublic,
			rdbmsScope: "https://ossrdbms-aad.database.windows.net/.default",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.net",
				scope:        "https://authorization.azure.net/.default",
			},
		},
		"AzureUSGovernmentCloud": {
			cloud:      cloud.AzureGovernment,
			rdbmsScope: "https://ossrdbms-aad.database.usgovcloudapi.net",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.us",
				scope:        "https://authorization.azure.us/.default",
			},
		},
	}

	configuration, ok := azureCloudEnvironmentConfigurationMapping[a.cloudEnvironment]
	if !ok {
		return AzureCloudEnvironment{},
			errors.Errorf("cloud environment %q is not supported", a.cloudEnvironment)
	}

	clientOptions := policy.ClientOptions{
		Cloud: configuration.cloud,
	}
	if a.tracerProvider != nil {
		clientOptions.TracingProvider = azotel.NewTracingProvider(a.tracerProvider, nil)
	}

	return AzureCloudEnvironment{
		id:                       a.cloudEnvironment,
		configuration:            configuration.cloud,
		rdbmsScope:               configuration.rdbmsScope,
		checkAccessV2Environment: configuration.checkAccessV2Environment,
		clientOptions:            clientOptions,
	}, nil
}

// PolicyClientOptions returns a policy.ClientOptions instance from the current
// Azure Cloud environment.
func (a AzureCloudEnvironment) PolicyClientOptions() policy.ClientOptions {
	return a.clientOptions
}

// ARMClientOptions returns an arm.ClientOptions instance from the current
// Azure Cloud environment.
func (a AzureCloudEnvironment) ARMClientOptions() *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: a.clientOptions,
	}
}

func (a AzureCloudEnvironment) ID() string {
	return a.id
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

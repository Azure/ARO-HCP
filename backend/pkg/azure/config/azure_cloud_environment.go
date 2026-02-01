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

package config

import (
	"fmt"

	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/tracing/azotel"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
)

// AzureCloudEnvironment represents an Azure cloud environment.
type AzureCloudEnvironment struct {
	// Configuration of the cloud environment
	configuration *cloud.Configuration
	// RDBMS scope of the cloud environment
	rdbmsScope string
	// Check Access V2 environment of the cloud environment
	checkAccessV2Environment *checkAccessV2Environment
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

func NewAzureCloudEnvironment(
	cloudEnvironmentName apisconfigv1.CloudEnvironmentName, tracerProvider trace.TracerProvider,
) (*AzureCloudEnvironment, error) {
	if len(cloudEnvironmentName) == 0 {
		return nil, fmt.Errorf("cloud environment cannot be empty")
	}

	var azureCloudEnvironmentConfigurationMapping = map[apisconfigv1.CloudEnvironmentName]struct {
		cloud                    cloud.Configuration
		rdbmsScope               string
		checkAccessV2Environment checkAccessV2Environment
	}{
		apisconfigv1.AzureChinaCloud: {
			cloud:      cloud.AzureChina,
			rdbmsScope: "https://ossrdbms-aad.database.chinacloudapi.cn",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.cn",
				scope:        "https://authorization.azure.cn/.default",
			},
		},
		apisconfigv1.AzurePublicCloud: {
			cloud:      cloud.AzurePublic,
			rdbmsScope: "https://ossrdbms-aad.database.windows.net/.default",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.net",
				scope:        "https://authorization.azure.net/.default",
			},
		},
		apisconfigv1.AzureUSGovernmentCloud: {
			cloud:      cloud.AzureGovernment,
			rdbmsScope: "https://ossrdbms-aad.database.usgovcloudapi.net",
			checkAccessV2Environment: checkAccessV2Environment{
				domainSuffix: "azure.us",
				scope:        "https://authorization.azure.us/.default",
			},
		},
	}

	configuration, ok := azureCloudEnvironmentConfigurationMapping[cloudEnvironmentName]
	if !ok {
		return nil,
			fmt.Errorf("cloud environment %q is not supported", cloudEnvironmentName)
	}

	clientOptions := policy.ClientOptions{
		Cloud: configuration.cloud,
	}
	if tracerProvider != nil {
		clientOptions.TracingProvider = azotel.NewTracingProvider(tracerProvider, nil)
	}

	return &AzureCloudEnvironment{
		configuration:            &configuration.cloud,
		rdbmsScope:               configuration.rdbmsScope,
		checkAccessV2Environment: &configuration.checkAccessV2Environment,
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
func (a AzureCloudEnvironment) ARMClientOptions() *azcorearm.ClientOptions {
	return &azcorearm.ClientOptions{
		ClientOptions: a.clientOptions,
	}
}

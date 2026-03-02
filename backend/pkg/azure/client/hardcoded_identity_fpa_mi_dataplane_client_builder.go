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

package client

import "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

// hardcodedIdentityFPAMIDataplaneClientBuilder is used to
// create Managed Identity Data Plane clients based on the
// hardcoded identity implementation of the Managed Identities
// Data Plane client hardcodedIdentityManagedIdentitiesDataplaneClient.
type hardcodedIdentityFPAMIDataplaneClientBuilder struct {
	cloudConfiguration *cloud.Configuration
	hardcodedIdentity  *HardcodedIdentity
}

var _ FPAMIDataplaneClientBuilder = (*hardcodedIdentityFPAMIDataplaneClientBuilder)(nil)

func (b *hardcodedIdentityFPAMIDataplaneClientBuilder) BuilderType() FPAMIDataplaneClientBuilderType {
	return FPAMIDataplaneClientBuilderTypeValue
}

// ManagedIdentitiesDataplane returns a new Managed Identity Data Plane client
// based on the hardcoded identity implementation of the Managed Identities
// Data Plane client hardcodedIdentityManagedIdentitiesDataplaneClient.
// The identity URL parameter is not used in the hardcoded identity implementation
// of the managed identities dataplane clientso we ignore it.
func (b *hardcodedIdentityFPAMIDataplaneClientBuilder) ManagedIdentitiesDataplane(_ string) (ManagedIdentitiesDataplaneClient, error) {
	return newHardcodedIdentityManagedIdentitiesDataPlaneClient(b.cloudConfiguration, b.hardcodedIdentity), nil
}

// NewHardcodedIdentityFPAMIDataplaneClientBuilder provides a new instance of
// FPAMIDataplaneClientBuilder that allows to retrieve Managed Identities Data Plane clients
// based on the hardcoded identity implementation of the Managed Identities Data Plane client
// hardcodedIdentityManagedIdentitiesDataplaneClient.
func NewHardcodedIdentityFPAMIDataplaneClientBuilder(cloudConfiguration *cloud.Configuration, hardcodedIdentity *HardcodedIdentity) FPAMIDataplaneClientBuilder {
	return &hardcodedIdentityFPAMIDataplaneClientBuilder{
		cloudConfiguration: cloudConfiguration,
		hardcodedIdentity:  hardcodedIdentity,
	}
}

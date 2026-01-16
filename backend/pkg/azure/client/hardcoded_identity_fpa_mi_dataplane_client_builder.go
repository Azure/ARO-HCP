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

package client

import "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

type hardcodedIdentityMIDataplaneClientBuilder struct {
	cloudConfiguration *cloud.Configuration
	identityStub       *HardcodedIdentity
}

var _ FPAMIDataplaneClientBuilder = (*hardcodedIdentityMIDataplaneClientBuilder)(nil)

func (b *hardcodedIdentityMIDataplaneClientBuilder) BuilderType() FPAMIDataplaneClientBuilderType {
	return FPAMIDataplaneClientBuilderTypeValue
}

// MIDataplane returns a new Managed Identity Data Plane client using the given tenant ID.
// The identity URL parameter is not used in the stub implementation as the stub MI dataplane client
// does not need that inforamtion.
// However, it is still part of the MIDataplane method signature because for the real MI Dataplane client
// it is needed.
func (b *hardcodedIdentityMIDataplaneClientBuilder) MIDataplane(tenantID string, _ string) (MIDataplaneClient, error) {
	return NewHardcodedIdentityMIDataPlaneClient(b.cloudConfiguration, b.identityStub, tenantID), nil
}

func NewHardcodedIdentityMIDataplaneClientBuilder(cloudConfiguration *cloud.Configuration, identityStub *HardcodedIdentity) FPAMIDataplaneClientBuilder {
	return &hardcodedIdentityMIDataplaneClientBuilder{
		cloudConfiguration: cloudConfiguration,
		identityStub:       identityStub,
	}
}

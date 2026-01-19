package client

import "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

type miDataplaneClientStubBuilder struct {
	cloudConfiguration *cloud.Configuration
	identityStub       *IdentityStub
}

var _ FPAMIDataplaneClientBuilder = (*miDataplaneClientStubBuilder)(nil)

func (b *miDataplaneClientStubBuilder) BuilderType() FPAMIDataplaneClientBuilderType {
	return FPAMIDataplaneClientBuilderTypeValue
}

// MIDataplane returns a new Managed Identity Data Plane client using the given tenant ID.
// The identity URL parameter is not used in the stub implementation as the stub MI dataplane client
// does not need that inforamtion.
// However, it is still part of the MIDataplane method signature because for the real MI Dataplane client
// it is needed.
func (b *miDataplaneClientStubBuilder) MIDataplane(tenantID string, _ string) (MIDataplaneClient, error) {
	return NewManagedIdentityClientStub(b.cloudConfiguration, b.identityStub, tenantID), nil
}

func NewFPAMIDataplaneClientStubBuilder(cloudConfiguration *cloud.Configuration, identityStub *IdentityStub) FPAMIDataplaneClientBuilder {
	return &miDataplaneClientStubBuilder{
		cloudConfiguration: cloudConfiguration,
		identityStub:       identityStub,
	}
}

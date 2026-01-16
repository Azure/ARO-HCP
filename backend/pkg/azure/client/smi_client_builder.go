package client

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

type SMIClientBuilderType string

const (
	SMIClientBuilderTypeValue SMIClientBuilderType = "SMI"
)

type SMIClientBuilder interface {
	BuilderType() SMIClientBuilderType
	UserAssignedIdentitiesClient(
		ctx context.Context, tenantID string, subscriptionID string,
		clusterIdentityURL string, smiResourceID *azcorearm.ResourceID,
	) (UserAssignedIdentitiesClient, error)
}

type smiClientBuilder struct {
	fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder
	azCoreARMClientOptions      *azcorearm.ClientOptions
}

var _ SMIClientBuilder = (*smiClientBuilder)(nil)

func (b *smiClientBuilder) BuilderType() SMIClientBuilderType {
	return SMIClientBuilderTypeValue
}

func (b *smiClientBuilder) UserAssignedIdentitiesClient(
	ctx context.Context, tenantID string, subscriptionID string,
	clusterIdentityURL string, smiResourceID *azcorearm.ResourceID,
) (UserAssignedIdentitiesClient, error) {
	miDataPlaneClient, err := b.fpaMIdataplaneClientBuilder.MIDataplane(tenantID, clusterIdentityURL)
	if err != nil {
		return nil, err
	}

	dataplaneRequest := dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{smiResourceID.String()},
	}

	resp, err := miDataPlaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplaneRequest)
	if err != nil {
		return nil, err
	}

	if len(resp.ExplicitIdentities) == 0 {
		return nil,
			fmt.Errorf("hcp cluster service managed identity %s not found in mi dataplane",
				smiResourceID.String(),
			)
	}

	userAssignedIdentityCredential := resp.ExplicitIdentities[0]

	// TODO will dataplane.GetCredential work
	// when MI Dataplane is not available? do we need
	// to make this an interface?
	creds, err := dataplane.GetCredential(b.azCoreARMClientOptions.ClientOptions, userAssignedIdentityCredential)
	if err != nil {
		return nil, err
	}

	return armmsi.NewUserAssignedIdentitiesClient(subscriptionID, creds, b.azCoreARMClientOptions)
}

func NewSMIClientBuilder(
	fpaMIdataplaneClientBuilder FPAMIDataplaneClientBuilder, options *azcorearm.ClientOptions,
) SMIClientBuilder {

	return &smiClientBuilder{
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
		azCoreARMClientOptions:      options,
	}
}

// type SMIClientBuilderFactory interface {
// 	NewSMIClientBuilder(smiResourceID *azcorearm.ResourceID) SMIClientBuilder
// }

// type smiClientBuilderFactory struct {
// 	fpaClientBuilder azureclient.FPAClientBuilder
// }

// var _ SMIClientBuilderFactory = (*smiClientBuilderFactory)(nil)

// func (f *smiClientBuilderFactory) NewSMIClientBuilder(smiResourceID *azcorearm.ResourceID) SMIClientBuilder {
// 	smiClientBuilder := &smiClientBuilder{
// 		fpaClientBuilder: f.fpaClientBuilder,
// 	}
// 	return smiClientBuilder
// }

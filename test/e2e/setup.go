package e2e

import (
	"context"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

var (
	clients *api.ClientFactory
)

const (
	subscriptionID = "00000000-0000-0000-0000-000000000000"
)

func setup(ctx context.Context) error {
	c := cloud.Configuration{
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Audience: "hcp-underlay-dev-svc",
				Endpoint: "https://localhost:8443",
			},
		},
	}
	opts := azcore.ClientOptions{
		Cloud:                           c,
		InsecureAllowCredentialWithHTTP: false,
	}

	/*envOptions := &azidentity.EnvironmentCredentialOptions{
		ClientOptions: opts,
	}
	creds, err := azidentity.NewEnvironmentCredential(envOptions)*/

	defaultOptions := &azidentity.DefaultAzureCredentialOptions{
		ClientOptions: opts,
	}
	creds, err := azidentity.NewDefaultAzureCredential(defaultOptions)
	if err != nil {
		return err
	}

	armOptions := &arm.ClientOptions{
		ClientOptions: opts,
	}
	clients, err = api.NewClientFactory(subscriptionID, creds, armOptions)
	if err != nil {
		return err
	}

	return nil
}

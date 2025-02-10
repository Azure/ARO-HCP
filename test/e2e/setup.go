package e2e

import (
	"context"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

var (
	clients *api.ClientFactory
)

const (
	subscriptionID = "00000000-0000-0000-0000-000000000000"
)

func setup(ctx context.Context) error {
	creds, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return err
	}

	armOptions := &arm.ClientOptions{}
	clients, err = api.NewClientFactory(subscriptionID, creds, armOptions)
	if err != nil {
		return err
	}

	return nil
}

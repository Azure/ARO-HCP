package azauth

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func SetupAzureAuth(ctx context.Context) error {
	if githubAuthSupported() {
		err := setupGithubAzureFederationAuthRefresher(ctx)
		if err != nil {
			return fmt.Errorf("failed to setup GitHub Azure Federation Auth Refresher: %w", err)
		}
	}
	return nil
}

func GetAzureTokenCredentials() (azcore.TokenCredential, error) {
	azCLI, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return nil, err
	}

	def, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}

	chain, err := azidentity.NewChainedTokenCredential([]azcore.TokenCredential{azCLI, def}, nil)
	if err != nil {
		return nil, err
	}
	return chain, nil
}

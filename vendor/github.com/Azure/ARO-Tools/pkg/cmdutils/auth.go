package cmdutils

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

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

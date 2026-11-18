package cmdutils

import (
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// GetAzureTokenCredentials returns an Azure TokenCredential.
// If running in GitHub Actions, it uses AzureCLICredential.
// Otherwise, it uses DefaultAzureCredential.
// This allows for the azure-cli to update the token expiry of the
// workload identity token used in the GitHub action as it currently
// does not refresh: https://github.com/Azure/azure-cli/issues/28708
func GetAzureTokenCredentials() (azcore.TokenCredential, error) {
	return GetAzureTokenCredentialsForCloud(cloud.AzurePublic)
}

// GetAzureTokenCredentialsForCloud returns an Azure TokenCredential configured for
// a specific Azure cloud. The cloud config determines the AAD authority host used
// when requesting tokens.
func GetAzureTokenCredentialsForCloud(cloudConfig cloud.Configuration) (azcore.TokenCredential, error) {
	if _, ok := os.LookupEnv("GITHUB_ACTIONS"); ok {
		return azidentity.NewAzureCLICredential(nil)
	}

	return azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		ClientOptions:                azcore.ClientOptions{Cloud: cloudConfig},
		RequireAzureTokenCredentials: true,
	})
}

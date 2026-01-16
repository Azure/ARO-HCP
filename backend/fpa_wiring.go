package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"time"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/fpa"
)

func getFPATokenCredentialRetriever(
	ctx context.Context, logger *slog.Logger, fpaCertBundlePath string,
	fpaClientID string, azureConfig *azureconfig.AzureConfig,
) (fpa.FirstPartyApplicationTokenCredentialRetriever, error) {
	if len(fpaCertBundlePath) == 0 || len(fpaClientID) == 0 {
		return nil, nil
	}

	// Create FPA TokenCredentials with watching
	certReader, err := fpa.NewWatchingFileCertificateReader(
		ctx,
		fpaCertBundlePath,
		1*time.Minute,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate reader: %w", err)
	}

	// We create the FPA token credential retriever here. Then we pass it to the cluster inflights controller,
	// which then is used to instantiate a validation that uses the FPA token credential retriever. And then the
	// validations uses the retriever to retrieve a token credential based on the information associated to the
	// cluster(the tenant of the cluster, the subscription id, ...)
	fpaTokenCredRetriever, err := fpa.NewFirstPartyApplicationTokenCredentialRetriever(
		logger,
		fpaClientID,
		certReader,
		*azureConfig.CloudEnvironment.AZCoreClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FPA token credential retriever: %w", err)
	}

	return fpaTokenCredRetriever, nil
}

func getFPAClientBuilder(
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	azureConfig *azureconfig.AzureConfig,
) azureclient.FPAClientBuilder {

	fpaClientBuilder := azureclient.NewFPAClientBuilder(
		fpaTokenCredRetriever, azureConfig.CloudEnvironment.ARMClientOptions(),
	)

	return fpaClientBuilder
}

func getFPAMIDataplaneClientBuilder(
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	azureMIMockSPCertBundlePath string, azureMIMockSPClientID string, azureMIMockSPPrincipalID string,
	azureConfig *azureconfig.AzureConfig,
) (azureclient.FPAMIDataplaneClientBuilder, error) {

	if len(azureMIMockSPCertBundlePath) == 0 || len(azureMIMockSPClientID) == 0 || len(azureMIMockSPPrincipalID) == 0 {
		// TODO if we want to support detecting when the cert bundle path content
		// changes, we could use a file watcher similar to the one used in the
		// fpa token credential retriever, and pass that retriever to the client
		// builder.
		bundle, err := os.ReadFile(azureMIMockSPCertBundlePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read bundle file: %w", err)
		}
		bundleBase64Encoded := base64.StdEncoding.EncodeToString(bundle)
		identityStub := &azureclient.IdentityStub{
			ClientID:     azureMIMockSPClientID,
			ClientSecret: bundleBase64Encoded,
			PrincipalID:  azureMIMockSPPrincipalID,
		}

		fpaMIdataplaneClientStubBuilder := azureclient.NewFPAMIDataplaneClientStubBuilder(
			azureConfig.CloudEnvironment.CloudConfiguration(),
			identityStub,
		)
		return fpaMIdataplaneClientStubBuilder, nil
	}

	fpaMIdataplaneClientBuilder := azureclient.NewFPAMIDataplaneClientBuilder(
		azureConfig.AzureRuntimeConfig.ServiceTenantID,
		fpaTokenCredRetriever,
		azureConfig.AzureRuntimeConfig.ManagedIdentitiesDataPlaneAudienceResource,
		azureConfig.CloudEnvironment.AZCoreClientOptions(),
	)

	return fpaMIdataplaneClientBuilder, nil
}

func getSMIClientBuilder(
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
	azureConfig *azureconfig.AzureConfig,
) azureclient.SMIClientBuilder {
	return azureclient.NewSMIClientBuilder(
		fpaMIdataplaneClientBuilder,
		azureConfig.CloudEnvironment.ARMClientOptions(),
	)
}

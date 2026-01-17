package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/fpa"
)

func getFPAClientBuilder(
	ctx context.Context, logger *slog.Logger, fpaCertBundlePath string, fpaClientID string,
	azureConfig *azureconfig.AzureConfig,
) (azureclient.FPAClientBuilder, error) {
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
		azureConfig.CloudEnvironment.AZCoreClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FPA token credential retriever: %w", err)
	}

	fpaClientBuilder := azureclient.NewFPAClientBuilder(
		fpaTokenCredRetriever, azureConfig.CloudEnvironment.ARMClientOptions(),
	)

	return fpaClientBuilder, nil
}

// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-logr/logr"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func getFirstPartyApplicationClientBuilder(
	ctx context.Context, fpaCertBundlePath string, fpaClientID string,
	azureConfig *azureconfig.AzureConfig,
) (azureclient.FirstPartyApplicationClientBuilder, error) {
	if len(fpaCertBundlePath) == 0 || len(fpaClientID) == 0 {
		return nil, nil
	}

	// TODO temporary until internal FPA types have been updated to
	// use logr.Logger or just receiving from context.
	logrLogger := utils.LoggerFromContext(ctx)
	slogLogger := slog.New(logr.ToSlogHandler(logrLogger))

	// Create FPA TokenCredentials with watching
	certReader, err := fpa.NewWatchingFileCertificateReader(
		ctx,
		fpaCertBundlePath,
		1*time.Minute,
		slogLogger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate reader: %w", err)
	}

	// We create the FPA token credential retriever here. Then we pass it to the cluster inflights controller,
	// which then is used to instantiate a validation that uses the FPA token credential retriever. And then the
	// validations uses the retriever to retrieve a token credential based on the information associated to the
	// cluster(the tenant of the cluster, the subscription id, ...)
	fpaTokenCredRetriever, err := fpa.NewFirstPartyApplicationTokenCredentialRetriever(
		slogLogger,
		fpaClientID,
		certReader,
		*azureConfig.CloudEnvironment.AZCoreClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FPA token credential retriever: %w", err)
	}

	fpaClientBuilder := azureclient.NewFirstPartyApplicationClientBuilder(
		fpaTokenCredRetriever, azureConfig.CloudEnvironment.ARMClientOptions(),
	)

	return fpaClientBuilder, nil
}

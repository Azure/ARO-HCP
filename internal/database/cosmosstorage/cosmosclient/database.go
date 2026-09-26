// Copyright 2025 Microsoft Corporation
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

package cosmosclient

import (
	"fmt"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosmetrics"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Options configures independently constructed container clients.
// Production uses DefaultAzureCredential; KeyCredential supports the emulator.
type Options struct {
	azcore.ClientOptions
	KeyCredential *azcosmos.KeyCredential
}

// NewCosmosDatabaseClient instantiates a generic Cosmos database client.
func NewCosmosDatabaseClient(url string, dbName string, options Options, bucket *cosmosratelimit.TokenBucket) (*azcosmos.DatabaseClient, error) {
	if bucket == nil {
		return nil, fmt.Errorf("cosmos database client requires a token bucket")
	}
	// Clone policy slices so independent container clients cannot overwrite
	// each other's buckets, or put Cosmos accounting in the credential pipeline.
	clientOptions := options.ClientOptions
	clientOptions.PerRetryPolicies = append(slices.Clone(options.PerRetryPolicies), cosmosmetrics.NewRequestChargePolicy(), cosmosratelimit.NewPolicy(bucket))
	cosmosOptions := &azcosmos.ClientOptions{ClientOptions: clientOptions}
	var client *azcosmos.Client
	var err error
	if options.KeyCredential != nil {
		client, err = azcosmos.NewClientWithKey(url, *options.KeyCredential, cosmosOptions)
	} else {
		credential, credentialErr := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
			ClientOptions:                options.ClientOptions,
			RequireAzureTokenCredentials: true,
		})
		if credentialErr != nil {
			return nil, utils.TrackError(credentialErr)
		}
		client, err = azcosmos.NewClient(url, credential, cosmosOptions)
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return client.NewDatabase(dbName)
}

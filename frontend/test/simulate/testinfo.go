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

package simulate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
)

type SimulationTestInfo struct {
	CosmosDatabaseClient     *azcosmos.DatabaseClient
	DBClient                 database.DBClient
	MockClusterServiceClient *mocks.MockClusterServiceClientSpec
	CosmosClient             *azcosmos.Client
	DatabaseName             string
	FrontendURL              string
}

func (s *SimulationTestInfo) CosmosResourcesContainer() *azcosmos.ContainerClient {
	resources, err := s.CosmosClient.NewContainer(s.DatabaseName, "Resources")
	if err != nil {
		panic(err)
	}

	return resources
}

func (s *SimulationTestInfo) Get20240610ClientFactory(subscriptionID string) (*hcpapi20240610.ClientFactory, error) {
	return hcpapi20240610.NewClientFactory(subscriptionID, nil, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: -1, // no retries
			},
			Cloud: cloud.Configuration{
				//ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Audience: "https://management.core.windows.net/",
						Endpoint: s.FrontendURL,
					},
				},
			},
			InsecureAllowCredentialWithHTTP: true,
		},
	})
}

func (s *SimulationTestInfo) CreateNewSubscription(ctx context.Context) (string, *arm.Subscription, error) {
	subscriptionID := uuid.NewString()
	return s.CreateSpecificSubscription(ctx, subscriptionID)
}

func (s *SimulationTestInfo) CreateSpecificSubscription(ctx context.Context, subscriptionID string) (string, *arm.Subscription, error) {
	subscription := &arm.Subscription{
		State: arm.SubscriptionStateRegistered,
	}
	err := s.DBClient.CreateSubscriptionDoc(ctx, subscriptionID, subscription)
	if err != nil {
		return "", nil, err
	}

	ret, err := s.DBClient.GetSubscriptionDoc(ctx, subscriptionID)
	return subscriptionID, ret, err
}

func (s *SimulationTestInfo) Cleanup(ctx context.Context) {
	if err := s.CleanupDatabase(ctx); err != nil {
		fmt.Printf("Failed to cleanup database: %v\n", err)
	}
}

// CleanupDatabase deletes the randomly created database
func (s *SimulationTestInfo) CleanupDatabase(ctx context.Context) error {
	if s.CosmosDatabaseClient == nil || s.DatabaseName == "" {
		return nil // Nothing to cleanup
	}

	_, err := s.CosmosDatabaseClient.Delete(ctx, nil)
	if err != nil {
		// Ignore 404 errors - database already doesn't exist
		var responseErr *azcore.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == 404 {
			return nil
		}
		return fmt.Errorf("failed to delete database %s: %w", s.DatabaseName, err)
	}

	return nil
}

func (s *SimulationTestInfo) CreateInitialCosmosContent(ctx context.Context, createDir fs.FS) error {
	dirContent, err := fs.ReadDir(createDir, ".")
	if err != nil {
		return fmt.Errorf("failed to read dir: %w", err)
	}

	for _, dirEntry := range dirContent {
		if dirEntry.IsDir() {
			return fmt.Errorf("dir %s is not a file", dirEntry.Name())
		}
		fileReader, err := createDir.Open(dirEntry.Name())
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", dirEntry.Name(), err)
		}
		fileContent, err := io.ReadAll(fileReader)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", dirEntry.Name(), err)
		}
		if err := s.createInitialCosmosContent(ctx, fileContent); err != nil {
			return fmt.Errorf("failed to create initial Cosmos content: %w", err)
		}
	}
	return nil
}

func (s *SimulationTestInfo) createInitialCosmosContent(ctx context.Context, content []byte) error {
	contentMap := map[string]any{}
	if err := json.Unmarshal(content, &contentMap); err != nil {
		return fmt.Errorf("failed to unmarshal content: %w", err)
	}

	var err error
	switch {
	case strings.EqualFold(contentMap["resourceType"].(string), api.ClusterResourceType.String()):
		partitionKey := azcosmos.NewPartitionKeyString(contentMap["partitionKey"].(string))
		_, err = s.CosmosResourcesContainer().CreateItem(ctx, partitionKey, content, nil)

	case strings.EqualFold(contentMap["resourceType"].(string), azcorearm.SubscriptionResourceType.String()):
		partitionKey := azcosmos.NewPartitionKeyString(contentMap["partitionKey"].(string))
		_, err = s.CosmosResourcesContainer().CreateItem(ctx, partitionKey, content, nil)

	default:
		return fmt.Errorf("unknown content type: %v", contentMap["resourceType"])
	}

	if err != nil {
		return fmt.Errorf("failed to create item: %w", err)
	}

	return nil
}

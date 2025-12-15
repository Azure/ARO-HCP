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
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/frontend/test/simulate/integrationutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type SimulationTestInfo struct {
	ArtifactsDir string
	mockData     map[string]map[string][]any

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

func (s *SimulationTestInfo) Get20240610ClientFactory(subscriptionID string) *hcpsdk20240610preview.ClientFactory {
	return api.Must(
		hcpsdk20240610preview.NewClientFactory(subscriptionID, nil,
			&azcorearm.ClientOptions{
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
					PerCallPolicies: []policy.Policy{
						emptySystemData{},
					},
				},
			},
		),
	)
}

// emptySystemData provides enough systemdata (normally supplied somewhere in ARM) to enable the server tow ork.
type emptySystemData struct{}

func (emptySystemData) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set(arm.HeaderNameARMResourceSystemData, "{}")
	req.Raw().Header.Set(arm.HeaderNameHomeTenantID, api.TestTenantID)
	return req.Next()
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
	if err := s.saveClusterServiceMockData(ctx); err != nil {
		fmt.Printf("Failed to save mock data: %v\n", err)
	}

	if err := s.CleanupDatabase(ctx); err != nil {
		fmt.Printf("Failed to cleanup database: %v\n", err)
	}
}

// CleanupDatabase reads all records from all containers and saves them to artifacts, then deletes the database
func (s *SimulationTestInfo) CleanupDatabase(ctx context.Context) error {
	if s.CosmosDatabaseClient == nil || s.DatabaseName == "" {
		return nil // Nothing to cleanup
	}

	// Save all database content before deleting
	if err := s.saveAllDatabaseContent(ctx); err != nil {
		fmt.Printf("Failed to save database content: %v\n", err)
		// Continue with deletion even if saving fails
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

// saveAllDatabaseContent reads all records from all containers and saves them to files
func (s *SimulationTestInfo) saveAllDatabaseContent(ctx context.Context) error {
	// Create timestamped subdirectory for this database
	cosmosDir := filepath.Join(s.ArtifactsDir, "cosmos-content")
	if err := os.MkdirAll(cosmosDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifact directory %s: %w", cosmosDir, err)
	}
	fmt.Printf("Saving Cosmos DB content to: %s\n", cosmosDir)

	// List all containers in the database
	containers := []string{"Resources", "Billing", "Locks"}
	for _, containerName := range containers {
		if err := s.saveContainerContent(ctx, containerName, cosmosDir); err != nil {
			fmt.Printf("Failed to save container %s: %v\n", containerName, err)
			// Continue with other containers
		}
	}

	return nil
}

// saveContainerContent saves all documents from a specific container
func (s *SimulationTestInfo) saveContainerContent(ctx context.Context, containerName, outputDir string) error {
	containerClient, err := s.CosmosDatabaseClient.NewContainer(containerName)
	if err != nil {
		return fmt.Errorf("failed to get container client for %s: %w", containerName, err)
	}

	// Create subdirectory for this container
	containerDir := filepath.Join(outputDir, containerName)
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("failed to create container directory %s: %w", containerDir, err)
	}

	// Query all documents in the container
	querySQL := "SELECT * FROM c"
	queryOptions := &azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{},
	}

	queryPager := containerClient.NewQueryItemsPager(querySQL, azcosmos.PartitionKey{}, queryOptions)

	docCount := 0
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to query container %s: %w", containerName, err)
		}

		for _, item := range queryResponse.Items {
			// Parse the document to get its ID for filename
			var docMap map[string]interface{}
			if err := json.Unmarshal(item, &docMap); err != nil {
				fmt.Printf("Failed to parse document in %s: %v\n", containerName, err)
				continue
			}

			filename := ""
			resourceType := docMap["resourceType"]
			var armResourceID *azcorearm.ResourceID
			var properties map[string]any
			obj, hasProperties := docMap["properties"]
			if hasProperties {
				properties = obj.(map[string]any)
				if resourceID, hasResourceID := properties["resourceId"]; hasResourceID && resourceID != nil {
					armResourceID, _ = azcorearm.ParseResourceID(resourceID.(string))
				}
			}
			switch {
			case armResourceID != nil:
				filename = filepath.Join(
					resourceIDToDir(armResourceID),
					armResourceID.Name+".json",
				)

			case strings.EqualFold(resourceType.(string), azcorearm.SubscriptionResourceType.String()):
				filename = filepath.Join(
					"subscriptions",
					fmt.Sprintf("subscription_%s.json", docMap["id"].(string)))

			case strings.EqualFold(resourceType.(string), api.OperationStatusResourceType.String()):
				externalID := properties["externalId"].(string)
				if clusterResourceID, _ := azcorearm.ParseResourceID(externalID); clusterResourceID != nil {
					clusterDir := resourceIDToDir(clusterResourceID)
					filename = filepath.Join(
						clusterDir,
						fmt.Sprintf("hcpoperationstatuses_%v_%v_%v.json", properties["startTime"], properties["request"], docMap["id"]),
					)
				}
			}

			if len(filename) == 0 {
				if id, ok := docMap["id"].(string); ok {
					// Sanitize filename
					basename := strings.ReplaceAll("unknown-type-"+id+".json", "/", "_")
					basename = strings.ReplaceAll(basename, "\\", "_")
					basename = strings.ReplaceAll(basename, ":", "_")
					filename = filepath.Join("unknown", basename)
				} else {
					filename = filepath.Join("unknown", fmt.Sprintf("unknown_%d.json", docCount))
				}
			}
			filename = filepath.Join(containerDir, filename)
			fmt.Printf("Saving document %s\n", filename)

			dirName := filepath.Dir(filename)
			if err := os.MkdirAll(dirName, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dirName, err)
			}
			prettyPrint, err := json.MarshalIndent(docMap, "", "    ")
			if err != nil {
				return fmt.Errorf("failed to marshal document: %w", err)
			}
			// Write document to file
			if err := os.WriteFile(filename, prettyPrint, 0644); err != nil {
				fmt.Printf("Failed to write document to %s: %v\n", filename, err)
				continue
			}

			docCount++
		}
	}

	fmt.Printf("Saved %d documents from container %s\n", docCount, containerName)
	return nil
}

func resourceIDToDir(resourceID *azcorearm.ResourceID) string {
	if resourceID.Parent == nil {
		return ""
	}
	startingDir := resourceIDToDir(resourceID.Parent)

	switch resourceID.ResourceType.String() {
	case "Microsoft.Resources/tenants":
		return ""
	case "Microsoft.Resources/subscriptions":
		return filepath.Join(
			startingDir,
			"subscriptions",
			resourceID.Name,
		)
	case "Microsoft.Resources/resourceGroups":
		return filepath.Join(
			startingDir,
			"resourceGroups",
			resourceID.Name,
		)

	default:
		if resourceID.Parent.ResourceType.String() == "Microsoft.Resources/resourceGroups" {
			return filepath.Join(
				startingDir,
				resourceID.ResourceType.String(),
				resourceID.Name,
			)
		}

		return filepath.Join(
			startingDir,
			resourceID.ResourceType.Types[len(resourceID.ResourceType.Types)-1],
			resourceID.Name,
		)
	}
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
	return integrationutils.CreateInitialCosmosContent(ctx, s.CosmosResourcesContainer(), content)
}

func (s *SimulationTestInfo) saveClusterServiceMockData(ctx context.Context) error {
	for dataName, clusterServiceData := range s.mockData {
		for clusterServiceName, clusterServiceHistory := range clusterServiceData {
			for i, currCluster := range clusterServiceHistory {
				basename := fmt.Sprintf("%d_%s.json", i, strings.ReplaceAll(clusterServiceName, "/", "."))
				filename := filepath.Join(s.ArtifactsDir, "cluster-service-mock-data", dataName, strings.ReplaceAll(clusterServiceName, "/", "."), basename)
				dirname := filepath.Dir(filename)
				if err := os.MkdirAll(dirname, 0755); err != nil {
					return fmt.Errorf("failed to create directory %s: %w", dirname, err)
				}

				clusterServiceBytes, err := marshalClusterServiceAny(currCluster)
				if err != nil {
					return fmt.Errorf("failed to marshal cluster: %w", err)
				}
				obj := map[string]any{}
				if err := json.Unmarshal(clusterServiceBytes, &obj); err != nil {
					return fmt.Errorf("failed to unmarshal cluster: %w", err)
				}
				prettyPrint, err := json.MarshalIndent(obj, "", "    ")
				if err != nil {
					return fmt.Errorf("failed to marshal document: %w", err)
				}
				if err := os.WriteFile(filename, prettyPrint, 0644); err != nil {
					return fmt.Errorf("failed to write document to %s: %w", filename, err)
				}
			}
		}
	}

	return nil
}

// adds mock data for later inclusion in artifacts
func (s *SimulationTestInfo) AddMockData(dataName string, data map[string][]any) error {
	if _, ok := s.mockData[dataName]; ok {
		return fmt.Errorf("mock data for %q already exists", dataName)
	}
	s.mockData[dataName] = data
	return nil
}

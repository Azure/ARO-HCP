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

package integrationutils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type CosmosIntegrationTestInfo struct {
	ArtifactsDir string

	CosmosDatabaseClient *azcosmos.DatabaseClient
	DBClient             database.DBClient
	CosmosClient         *azcosmos.Client
	DatabaseName         string
}

type FrontendIntegrationTestInfo struct {
	*CosmosIntegrationTestInfo

	ArtifactsDir string
	mockData     map[string]map[string][]any

	MockClusterServiceClient *mocks.MockClusterServiceClientSpec
	FrontendURL              string
	Frontend                 *frontend.Frontend
}

func (s *FrontendIntegrationTestInfo) Get20240610ClientFactory(subscriptionID string) *hcpsdk20240610preview.ClientFactory {
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

func (s *FrontendIntegrationTestInfo) Cleanup(ctx context.Context) {
	if err := s.saveClusterServiceMockData(ctx); err != nil {
		fmt.Printf("Failed to save mock data: %v\n", err)
	}

	s.CosmosIntegrationTestInfo.Cleanup(ctx)
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

func (s *FrontendIntegrationTestInfo) saveClusterServiceMockData(ctx context.Context) error {
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

func (s *FrontendIntegrationTestInfo) GetOrCreateMockData(dataName string) map[string][]any {
	if existing, ok := s.mockData[dataName]; ok {
		return existing
	}
	newData := map[string][]any{}
	s.mockData[dataName] = newData
	return newData
}

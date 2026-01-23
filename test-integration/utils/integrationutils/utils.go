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
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"sync"
	"testing"

	// register the APIs.
	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func SkipIfNotSimulationTesting(t *testing.T) {
	if os.Getenv("FRONTEND_SIMULATION_TESTING") != "true" {
		t.Skip("Skipping test")
	}
}

var (
	artifactDir     string
	artifactDirInit sync.Once
)

func getArtifactDir() string {
	artifactDirInit.Do(func() {
		artifactDir = os.Getenv("ARTIFACT_DIR")
		if artifactDir == "" {
			// Default to temp directory if ARTIFACT_DIR not set
			var err error
			artifactDir, err = os.MkdirTemp("", "integration-testing")
			if err != nil {
				panic(err)
			}
		}
	})
	return artifactDir
}

func NewFrontendFromTestingEnv(ctx context.Context, t *testing.T) (*frontend.Frontend, *FrontendIntegrationTestInfo, error) {
	cosmosTestEnv, err := NewCosmosFromTestingEnv(ctx, t)
	if err != nil {
		return nil, nil, err
	}

	logger := utils.DefaultLogger()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	metricsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	noOpAuditClient, err := audit.NewOtelAuditClient(audit.CreateConn(false))
	if err != nil {
		return nil, nil, err
	}

	metricsRegistry := prometheus.NewRegistry()

	clusterServiceMockInfo := NewClusterServiceMock(t, cosmosTestEnv.ArtifactsDir)

	aroHCPFrontend := frontend.NewFrontend(logger, listener, metricsListener, metricsRegistry, cosmosTestEnv.DBClient, clusterServiceMockInfo.MockClusterServiceClient, noOpAuditClient, "fake-location")
	testInfo := &FrontendIntegrationTestInfo{
		CosmosIntegrationTestInfo: cosmosTestEnv,
		ClusterServiceMock:        clusterServiceMockInfo,
		ArtifactsDir:              cosmosTestEnv.ArtifactsDir,
		FrontendURL:               fmt.Sprintf("http://%s", listener.Addr().String()),
	}
	return aroHCPFrontend, testInfo, nil
}

// MockFrontendIntegrationTestInfo holds test information when using a mock database.
type MockFrontendIntegrationTestInfo struct {
	*ClusterServiceMock

	ArtifactsDir   string
	DBClient       database.DBClient
	MockDBClient   *databasetesting.MockDBClient
	ContentLoader  ContentLoader
	DocumentLister DocumentLister
	FrontendURL    string
	Frontend       *frontend.Frontend
}

// Cleanup cleans up the mock test environment.
func (m *MockFrontendIntegrationTestInfo) Cleanup(ctx context.Context) {
	m.ClusterServiceMock.Cleanup(ctx)
}

// Get20240610ClientFactory creates a client factory for the test frontend.
func (m *MockFrontendIntegrationTestInfo) Get20240610ClientFactory(subscriptionID string) *hcpsdk20240610preview.ClientFactory {
	return createClientFactory(subscriptionID, m.FrontendURL)
}

// NewFrontendFromMockDB creates a new Frontend using a mock database client.
// This allows tests to run without requiring a Cosmos DB emulator.
func NewFrontendFromMockDB(ctx context.Context, t *testing.T) (*frontend.Frontend, *MockFrontendIntegrationTestInfo, error) {
	mockDBClient := databasetesting.NewMockDBClient()

	// Set up a mock lock client
	lockClient := databasetesting.NewMockLockClient(10)
	mockDBClient.SetLockClient(lockClient)

	logger := utils.DefaultLogger()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	metricsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	noOpAuditClient, err := audit.NewOtelAuditClient(audit.CreateConn(false))
	if err != nil {
		return nil, nil, err
	}

	metricsRegistry := prometheus.NewRegistry()

	artifactsDir := path.Join(getArtifactDir(), t.Name())

	clusterServiceMockInfo := NewClusterServiceMock(t, artifactsDir)

	aroHCPFrontend := frontend.NewFrontend(logger, listener, metricsListener, metricsRegistry, mockDBClient, clusterServiceMockInfo.MockClusterServiceClient, noOpAuditClient, "fake-location")

	testInfo := &MockFrontendIntegrationTestInfo{
		ClusterServiceMock: clusterServiceMockInfo,
		ArtifactsDir:       artifactsDir,
		DBClient:           mockDBClient,
		MockDBClient:       mockDBClient,
		ContentLoader:      mockDBClient, // MockDBClient implements ContentLoader
		DocumentLister:     mockDBClient, // MockDBClient implements DocumentLister
		FrontendURL:        fmt.Sprintf("http://%s", listener.Addr().String()),
		Frontend:           aroHCPFrontend,
	}
	return aroHCPFrontend, testInfo, nil
}

func MarkOperationsCompleteForName(ctx context.Context, dbClient database.DBClient, subscriptionID, resourceName string) error {
	operationsIterator := dbClient.Operations(subscriptionID).ListActiveOperations(nil)
	for _, operation := range operationsIterator.Items(ctx) {
		if operation.ExternalID.Name != resourceName {
			continue
		}
		err := database.UpdateOperationStatus(ctx, dbClient, operation, arm.ProvisioningStateSucceeded, nil, nil)
		if err != nil {
			return err
		}
	}
	if operationsIterator.GetError() != nil {
		return operationsIterator.GetError()
	}
	return nil
}

// createClientFactory creates a client factory for a given subscription and frontend URL.
func createClientFactory(subscriptionID, frontendURL string) *hcpsdk20240610preview.ClientFactory {
	return api.Must(
		hcpsdk20240610preview.NewClientFactory(subscriptionID, nil,
			&azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{
					Retry: policy.RetryOptions{
						MaxRetries: -1, // no retries
					},
					Cloud: cloud.Configuration{
						Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
							cloud.ResourceManager: {
								Audience: "https://management.core.windows.net/",
								Endpoint: frontendURL,
							},
						},
					},
					InsecureAllowCredentialWithHTTP: true,
					PerCallPolicies: []policy.Policy{
						mockSystemData{},
					},
				},
			},
		),
	)
}

// mockSystemData provides enough systemdata (normally supplied somewhere in ARM) to enable the server to work.
type mockSystemData struct{}

func (mockSystemData) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set(arm.HeaderNameARMResourceSystemData, "{}")
	req.Raw().Header.Set(arm.HeaderNameHomeTenantID, api.TestTenantID)
	return req.Next()
}

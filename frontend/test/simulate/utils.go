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
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/ARO-HCP/frontend/cmd"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/frontend/pkg/util"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
)

type SimulationTestInfo struct {
	CosmosDatabaseClient     *azcosmos.DatabaseClient
	DBClient                 database.DBClient
	MockClusterServiceClient *mocks.MockClusterServiceClientSpec
	CosmosClient             *azcosmos.Client
	DatabaseName             string
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

func NewFrontendFromTestingEnv(ctx context.Context, t *testing.T) (*frontend.Frontend, *SimulationTestInfo, error) {
	logger := util.DefaultLogger()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	metricsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	ctrl := gomock.NewController(t)
	clusterServiceClient := mocks.NewMockClusterServiceClientSpec(ctrl)

	noOpAuditClient, err := audit.NewOtelAuditClient(audit.CreateConn(false))
	if err != nil {
		return nil, nil, err
	}

	cosmosClient, err := createCosmosClientFromEnv()
	if err != nil {
		return nil, nil, err
	}
	cosmosDatabaseName := "frontend-simulation-testing-" + rand.String(5)
	cosmosDatabaseClient, err := initializeCosmosDBForFrontend(ctx, cosmosClient, cosmosDatabaseName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize Cosmos DB: %w", err)
	}
	dbClient, err := database.NewDBClient(ctx, cosmosDatabaseClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create the database client: %w", err)
	}

	metricsRegistry := prometheus.NewRegistry()

	frontend := frontend.NewFrontend(logger, listener, metricsListener, metricsRegistry, dbClient, clusterServiceClient, noOpAuditClient)
	testInfo := &SimulationTestInfo{
		CosmosDatabaseClient:     cosmosDatabaseClient,
		DBClient:                 dbClient,
		MockClusterServiceClient: clusterServiceClient,
		CosmosClient:             cosmosClient,
		DatabaseName:             cosmosDatabaseName,
	}
	return frontend, testInfo, nil
}

func createCosmosClientFromEnv() (*azcosmos.Client, error) {
	// Emulator endpoint and key
	emulatorEndpoint := os.Getenv("FRONTEND_COSMOS_ENDPOINT")
	emulatorKey := os.Getenv("FRONTEND_COSMOS_KEY")

	// Configure HTTP client to skip certificate verification for the emulator
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Create a custom pipeline option for the client
	clientOptions := &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport:       httpClient,
			PerCallPolicies: []policy.Policy{cmd.PolicyFunc(cmd.CorrelationIDPolicy)},
		},
	}

	// Create key credential
	keyCredential, err := azcosmos.NewKeyCredential(emulatorKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create key credential: %w", err)
	}

	// Create Cosmos DB client
	cosmosClient, err := azcosmos.NewClientWithKey(emulatorEndpoint, keyCredential, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cosmos DB client: %w", err)
	}
	return cosmosClient, nil
}

func initializeCosmosDBForFrontend(ctx context.Context, cosmosClient *azcosmos.Client, cosmosDatabaseName string) (*azcosmos.DatabaseClient, error) {
	// Create the database if it doesn't exist
	databaseProperties := azcosmos.DatabaseProperties{ID: cosmosDatabaseName}
	_, err := cosmosClient.CreateDatabase(ctx, databaseProperties, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	// Get the database client
	cosmosDatabaseClient, err := cosmosClient.NewDatabase(cosmosDatabaseName)
	if err != nil {
		return nil, fmt.Errorf("failed to create database client: %w", err)
	}

	// Create required containers
	containers := []struct {
		name         string
		partitionKey string
		defaultTTL   *int32
	}{
		{"Resources", "/partitionKey", nil},
		{"Billing", "/subscriptionId", nil},
		{"Locks", "/id", &[]int32{10}[0]}, // 10 second TTL for locks
	}

	for _, container := range containers {
		containerProperties := azcosmos.ContainerProperties{
			ID: container.name,
			PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{
				Paths: []string{container.partitionKey},
			},
		}
		if container.defaultTTL != nil {
			containerProperties.DefaultTimeToLive = container.defaultTTL
		}

		_, err = cosmosDatabaseClient.CreateContainer(ctx, containerProperties, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create container: %w", err)
		}
	}

	return cosmosDatabaseClient, nil

}

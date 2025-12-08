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
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// register the APIs.
	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/frontend/cmd"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/frontend/pkg/util"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
)

func SkipIfNotSimulationTesting(t *testing.T) {
	if os.Getenv("FRONTEND_SIMULATION_TESTING") != "true" {
		//t.Skip("Skipping test")
	}
}

//go:embed artifacts/*
var artifacts embed.FS

var FastPollOptions = &runtime.PollUntilDoneOptions{Frequency: 5 * time.Millisecond}

func NewFrontendFromTestingEnv(ctx context.Context, t *testing.T) (*frontend.Frontend, *SimulationTestInfo, error) {
	arm.SetAzureLocation("globals-are-evil")

	artifactDir := os.Getenv("ARTIFACT_DIR")
	if artifactDir == "" {
		// Default to temp directory if ARTIFACT_DIR not set
		var err error
		artifactDir, err = os.MkdirTemp("", "simulation-testing")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create temp directory: %w", err)
		}
	}
	artifactDir = filepath.Join(artifactDir, t.Name())
	t.Logf("ARTIFACT_DIR: %s\n", artifactDir)
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create artifact directory %s: %w", artifactDir, err)
	}

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
		ArtifactsDir:             artifactDir,
		mockData:                 make(map[string]map[string][]any),
		CosmosDatabaseClient:     cosmosDatabaseClient,
		DBClient:                 dbClient,
		MockClusterServiceClient: clusterServiceClient,
		CosmosClient:             cosmosClient,
		DatabaseName:             cosmosDatabaseName,
		FrontendURL:              fmt.Sprintf("http://%s", listener.Addr().String()),
	}
	return frontend, testInfo, nil
}

func createCosmosClientFromEnv() (*azcosmos.Client, error) {
	// Emulator endpoint and key
	emulatorEndpoint := os.Getenv("FRONTEND_COSMOS_ENDPOINT")
	emulatorKey := os.Getenv("FRONTEND_COSMOS_KEY")
	if len(emulatorEndpoint) == 0 {
		emulatorEndpoint = "https://localhost:8081" // emulator default
	}
	if len(emulatorKey) == 0 {
		emulatorKey = "C2y6yDjf5/R+ob0N8A7Cgv30VRDJIWEHLM+4QDU5DE2nQ9nDuVTqobD4b8mGGyPMbIZnqyMsEcaGQy67XIw/Jw==" // emulator default
	}

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

func MarkOperationsCompleteForName(ctx context.Context, dbClient database.DBClient, subscriptionID, resourceName string) error {
	operationsIterator := dbClient.ListActiveOperationDocs(azcosmos.NewPartitionKeyString(subscriptionID), nil)
	for _, operation := range operationsIterator.Items(ctx) {
		if operation.ExternalID.Name != resourceName {
			continue
		}
		err := UpdateOperationStatus(ctx, dbClient, operation, arm.ProvisioningStateSucceeded, nil)
		if err != nil {
			return err
		}
	}
	if operationsIterator.GetError() != nil {
		return operationsIterator.GetError()
	}
	return nil
}

// TODO this needs to simplified into something the database client can do on behalf of callers to make it more idiot-proof
func UpdateOperationStatus(ctx context.Context, dbClient database.DBClient, operation *database.OperationDocument, opStatus arm.ProvisioningState, opError *arm.CloudErrorBody) error {
	err := patchOperationDocument(ctx, dbClient, operation, opStatus, opError)
	if err != nil {
		return err
	}

	var patchOperations database.ResourceDocumentPatchOperations

	scalar := strings.ReplaceAll(database.ResourceDocumentJSONPathActiveOperationID, "/", ".")
	condition := fmt.Sprintf("FROM doc WHERE doc%s = '%s'", scalar, operation.OperationID.Name)

	patchOperations.SetCondition(condition)
	patchOperations.SetProvisioningState(opStatus)
	if opStatus.IsTerminal() {
		patchOperations.SetActiveOperationID(nil)
	}

	_, err = dbClient.PatchResourceDoc(ctx, operation.ExternalID, patchOperations)
	return err
}

// TODO this needs to simplified into something the database client can do on behalf of callers to make it more idiot-proof
func patchOperationDocument(ctx context.Context, dbClient database.DBClient, operation *database.OperationDocument, opStatus arm.ProvisioningState, opError *arm.CloudErrorBody) error {
	var patchOperations database.OperationDocumentPatchOperations

	scalar := strings.ReplaceAll(database.OperationDocumentJSONPathStatus, "/", ".")
	condition := fmt.Sprintf("FROM doc WHERE doc%s != '%s'", scalar, opStatus)

	patchOperations.SetCondition(condition)
	patchOperations.SetLastTransitionTime(time.Now())
	patchOperations.SetStatus(opStatus)
	if opError != nil {
		patchOperations.SetError(opError)
	}

	operationPartitionKey := azcosmos.NewPartitionKeyString(operation.OperationID.SubscriptionID)
	_, err := dbClient.PatchOperationDoc(ctx, operationPartitionKey, operation.OperationID.Name, patchOperations)
	return err
}

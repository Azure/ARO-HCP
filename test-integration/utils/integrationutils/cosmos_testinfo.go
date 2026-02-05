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
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/frontend/cmd"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type CosmosIntegrationTestInfo struct {
	ArtifactsDir string

	CosmosDatabaseClient *azcosmos.DatabaseClient
	DBClient             database.DBClient
	cosmosClient         *azcosmos.Client
	DatabaseName         string
}

func NewCosmosFromTestingEnv(ctx context.Context, t *testing.T) (StorageIntegrationTestInfo, error) {
	cosmosClient, err := createCosmosClientFromEnv()
	if err != nil {
		return nil, err
	}
	cosmosDatabaseName := "frontend-simulation-testing-" + rand.String(5)
	cosmosDatabaseClient, err := initializeCosmosDBForFrontend(ctx, cosmosClient, cosmosDatabaseName)
	if err != nil {
		return nil, fmt.Errorf("failed to Initialize Cosmos DB: %w", err)
	}
	dbClient, err := database.NewDBClient(ctx, cosmosDatabaseClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create the database client: %w", err)
	}

	testInfo := &CosmosIntegrationTestInfo{
		ArtifactsDir:         path.Join(getArtifactDir(), t.Name()),
		CosmosDatabaseClient: cosmosDatabaseClient,
		DBClient:             dbClient,
		cosmosClient:         cosmosClient,
		DatabaseName:         cosmosDatabaseName,
	}
	return testInfo, nil
}

func JSONOnly(entry fs.DirEntry) bool {
	return strings.HasSuffix(entry.Name(), ".json")
}

func LoadCosmosContentFromFS(ctx context.Context, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS, filterFn func(fs.DirEntry) bool) error {
	testContent, err := fs.ReadDir(stepDir, ".")
	if err != nil {
		return utils.TrackError(err)
	}
	for _, dirEntry := range testContent {
		if filterFn != nil && !filterFn(dirEntry) {
			continue
		}

		content, err := fs.ReadFile(stepDir, dirEntry.Name())
		if err != nil {
			return utils.TrackError(err)
		}
		if err := LoadCosmosContent(ctx, cosmosContainer, content); err != nil {
			return utils.TrackError(err)
		}
	}

	return nil
}

func (s *CosmosIntegrationTestInfo) ListAllDocuments(ctx context.Context) ([]*database.TypedDocument, error) {
	return NewCosmosContentLoader(s.CosmosResourcesContainer()).ListAllDocuments(ctx)
}

func (s *CosmosIntegrationTestInfo) CosmosClient() database.DBClient {
	return s.DBClient
}

func LoadCosmosContent(ctx context.Context, cosmosContainer *azcosmos.ContainerClient, content []byte) error {
	contentMap := map[string]any{}
	if err := json.Unmarshal(content, &contentMap); err != nil {
		return fmt.Errorf("failed to unmarshal content: %w", err)
	}

	var err error
	switch {
	case strings.EqualFold(contentMap["resourceType"].(string), api.OperationStatusResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.ClusterResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.NodePoolResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.ExternalAuthResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.ClusterControllerResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.NodePoolControllerResourceType.String()),
		strings.EqualFold(contentMap["resourceType"].(string), api.ExternalAuthControllerResourceType.String()):
		partitionKey := azcosmos.NewPartitionKeyString(contentMap["partitionKey"].(string))
		_, err = cosmosContainer.CreateItem(ctx, partitionKey, content, nil)

	case strings.EqualFold(contentMap["resourceType"].(string), azcorearm.SubscriptionResourceType.String()):
		partitionKey := azcosmos.NewPartitionKeyString(contentMap["partitionKey"].(string))
		_, err = cosmosContainer.CreateItem(ctx, partitionKey, content, nil)

	default:
		return fmt.Errorf("unknown content type: %v", contentMap["resourceType"])
	}

	if err != nil {
		return fmt.Errorf("failed to create item: %w", err)
	}

	return nil
}

func (s *CosmosIntegrationTestInfo) CosmosResourcesContainer() *azcosmos.ContainerClient {
	resources, err := s.cosmosClient.NewContainer(s.DatabaseName, "Resources")
	if err != nil {
		panic(err)
	}

	return resources
}

func (s *CosmosIntegrationTestInfo) Cleanup(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	if err := s.cleanupDatabase(ctx); err != nil {
		logger.Error(err, "Failed to cleanup database")
	}
}

// CleanupDatabase reads all records from all containers and saves them to artifacts, then deletes the database
func (s *CosmosIntegrationTestInfo) cleanupDatabase(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)
	if s.CosmosDatabaseClient == nil || s.DatabaseName == "" {
		return nil // Nothing to cleanup
	}

	// Save all database content before deleting
	if err := saveAllDatabaseContent(ctx, s, s.ArtifactsDir); err != nil {
		logger.Error(err, "Failed to save database content")
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
func saveAllDatabaseContent(ctx context.Context, documentLister DocumentLister, artifactDir string) error {
	logger := utils.LoggerFromContext(ctx)

	// Create timestamped subdirectory for this database
	cosmosDir := filepath.Join(artifactDir, "cosmos-content")
	if err := os.MkdirAll(cosmosDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifact directory %s: %w", cosmosDir, err)
	}
	logger.Info("Saving Cosmos DB content", "cosmosDir", cosmosDir)

	// List all containers in the database
	if err := saveContainerContent(ctx, documentLister, cosmosDir); err != nil {
		logger.Error(err, "Failed to save container content")
	}

	return nil
}

// saveContainerContent saves all documents from a specific container
func saveContainerContent(ctx context.Context, documentLister DocumentLister, outputDir string) error {
	logger := utils.LoggerFromContext(ctx)

	// Create subdirectory for this container
	containerDir := filepath.Join(outputDir, "Resources")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("failed to create container directory %s: %w", containerDir, err)
	}

	documents, err := documentLister.ListAllDocuments(ctx)
	if err != nil {
		return fmt.Errorf("failed to list documents: %w", err)
	}

	docCount := 0
	for _, currTypedDocument := range documents {
		item, err := json.MarshalIndent(currTypedDocument, "", "    ")
		if err != nil {
			logger.Error(err, "Failed to serialize")
			continue
		}

		// Parse the document to get its ID for filename
		var docMap map[string]interface{}
		if err := json.Unmarshal(item, &docMap); err != nil {
			logger.Error(err, "Failed to parse document")
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
		logger.Info("Saving document", "filename", filename)

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
			logger.Error(err, "Failed to write document", "filename", filename)
			continue
		}

		docCount++
	}

	logger.Info("Saved documents from container", "numDocs", docCount)
	return nil
}

func LoadAllContent(ctx context.Context, contentLoader ContentLoader, createDir fs.FS) error {
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
		if err := contentLoader.LoadContent(ctx, fileContent); err != nil {
			return fmt.Errorf("failed to create initial Cosmos content: %w", err)
		}
	}
	return nil
}

func (s *CosmosIntegrationTestInfo) LoadContent(ctx context.Context, content []byte) error {
	return LoadCosmosContent(ctx, s.CosmosResourcesContainer(), content)
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
	logger := utils.LoggerFromContext(ctx)

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

	start := time.Now()
	logger.Info("Create all containers")
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

		var lastErr error
		logger.Info("Creating container", "containerName", container.name)
		err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 60*time.Second, true, func(ctx context.Context) (done bool, err error) {
			//&azcosmos.CreateContainerOptions{
			//	ThroughputProperties: ptr.To(azcosmos.NewManualThroughputProperties(100)),
			//}
			_, err = cosmosDatabaseClient.CreateContainer(ctx, containerProperties, nil)
			lastErr = err
			if err != nil {
				return false, nil
			}

			return true, nil
		})
		if lastErr != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create container %s: %w", container.name, lastErr))
		}
		if err != nil {
			return nil, utils.TrackError(err)
		}
		logger.Info("Container created", "containerName", container.name)
	}
	end := time.Now()
	logger.Info("All containers created", "duration", end.Sub(start))

	return cosmosDatabaseClient, nil

}

func (s *CosmosIntegrationTestInfo) GetArtifactDir() string {
	return s.ArtifactsDir
}

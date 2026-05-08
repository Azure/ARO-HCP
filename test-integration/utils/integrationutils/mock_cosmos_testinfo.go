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
	"path"
	"testing"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type MockCosmosIntegrationTestInfo struct {
	ArtifactsDir string

	mockResourcesDBClient *databasetesting.MockResourcesDBClient
	mockBillingDBClient   *databasetesting.MockBillingDBClient
	mockLocksDBClient     *databasetesting.MockLocksDBClient
}

func NewMockCosmosFromTestingEnv(ctx context.Context, t *testing.T) (StorageIntegrationTestInfo, error) {
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockBillingDBClient := databasetesting.NewMockBillingDBClient()
	mockLocksDBClient := databasetesting.NewMockLocksDBClient()

	testInfo := &MockCosmosIntegrationTestInfo{
		ArtifactsDir:          path.Join(getArtifactDir(), t.Name()),
		mockResourcesDBClient: mockResourcesDBClient,
		mockBillingDBClient:   mockBillingDBClient,
		mockLocksDBClient:     mockLocksDBClient,
	}
	return testInfo, nil
}

func (m *MockCosmosIntegrationTestInfo) ResourcesDBClient() database.ResourcesDBClient {
	return m.mockResourcesDBClient
}

func (m *MockCosmosIntegrationTestInfo) BillingDBClient() database.BillingDBClient {
	return m.mockBillingDBClient
}

func (m *MockCosmosIntegrationTestInfo) LocksDBClient() database.LocksDBClient {
	return m.mockLocksDBClient
}

func (m *MockCosmosIntegrationTestInfo) LoadContent(ctx context.Context, content []byte) error {
	return m.mockResourcesDBClient.LoadContent(ctx, content)
}

func (m *MockCosmosIntegrationTestInfo) ListAllDocuments(ctx context.Context) ([]*database.TypedDocument, error) {
	return m.mockResourcesDBClient.ListAllDocuments(ctx)
}

func (m *MockCosmosIntegrationTestInfo) Cleanup(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	// Save all database content before deleting
	if err := saveAllDatabaseContent(ctx, m.mockResourcesDBClient, m.ArtifactsDir); err != nil {
		logger.Error(err, "Failed to save database content")
		// Continue with deletion even if saving fails
	}
}

func (m *MockCosmosIntegrationTestInfo) GetArtifactDir() string {
	return m.ArtifactsDir
}

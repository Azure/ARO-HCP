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

	MockDBClient *databasetesting.MockDBClient
}

func NewMockCosmosFromTestingEnv(ctx context.Context, t *testing.T) (StorageIntegrationTestInfo, error) {
	mockDBClient := databasetesting.NewMockDBClient()

	testInfo := &MockCosmosIntegrationTestInfo{
		ArtifactsDir: path.Join(getArtifactDir(), t.Name()),
		MockDBClient: mockDBClient,
	}
	return testInfo, nil
}

func (m *MockCosmosIntegrationTestInfo) CosmosClient() database.DBClient {
	return m.MockDBClient
}

func (m *MockCosmosIntegrationTestInfo) LoadContent(ctx context.Context, content []byte) error {
	return m.MockDBClient.LoadContent(ctx, content)
}

func (m *MockCosmosIntegrationTestInfo) ListAllDocuments(ctx context.Context) ([]*database.TypedDocument, error) {
	return m.MockDBClient.ListAllDocuments(ctx)
}

func (m *MockCosmosIntegrationTestInfo) Cleanup(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	// Save all database content before deleting
	if err := saveAllDatabaseContent(ctx, m.MockDBClient, m.ArtifactsDir); err != nil {
		logger.Error("Failed to save database content", "error", err)
		// Continue with deletion even if saving fails
	}
}

func (m *MockCosmosIntegrationTestInfo) GetArtifactDir() string {
	return m.ArtifactsDir
}

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

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type MockCosmosIntegrationTestInfo struct {
	ArtifactsDir string

	mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient
	mockBillingDBClient   *billingcosmosstoragetesting.MockBillingDBClient
	mockFleetDBClient     *fleetcosmosstoragetesting.MockFleetDBClient
}

func NewMockCosmosFromTestingEnv(ctx context.Context, t *testing.T) (StorageIntegrationTestInfo, error) {
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()
	mockFleetDBClient := fleetcosmosstoragetesting.NewMockFleetDBClient()

	testInfo := &MockCosmosIntegrationTestInfo{
		ArtifactsDir:          path.Join(getArtifactDir(), t.Name()),
		mockResourcesDBClient: mockResourcesDBClient,
		mockBillingDBClient:   mockBillingDBClient,
		mockFleetDBClient:     mockFleetDBClient,
	}
	return testInfo, nil
}

func (m *MockCosmosIntegrationTestInfo) ResourcesDBClient() corecosmosstorage.ResourcesDBClient {
	return m.mockResourcesDBClient
}

func (m *MockCosmosIntegrationTestInfo) BillingDBClient() billingcosmosstorage.BillingDBClient {
	return m.mockBillingDBClient
}

func (m *MockCosmosIntegrationTestInfo) FleetDBClient() fleetcosmosstorage.FleetDBClient {
	return m.mockFleetDBClient
}

func (m *MockCosmosIntegrationTestInfo) LoadContent(ctx context.Context, content []byte) error {
	return m.mockResourcesDBClient.LoadContent(ctx, content)
}

func (m *MockCosmosIntegrationTestInfo) ListAllDocuments(ctx context.Context) ([]*cosmosstorageutils.TypedDocument, error) {
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

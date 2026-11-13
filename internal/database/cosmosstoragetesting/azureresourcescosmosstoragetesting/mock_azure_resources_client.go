// Copyright 2026 Microsoft Corporation
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

package azureresourcescosmosstoragetesting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

// MockAzureResourcesDBClient is the in-memory test double for
// azureresourcescosmosstorage.AzureResourcesDBClient. It owns its own document
// store, separate from corecosmosstoragetesting.MockResourcesDBClient — production
// has the AzureResources container live separately from the Resources container,
// and the mock mirrors that boundary.
type MockAzureResourcesDBClient struct {
	mu         sync.RWMutex
	documents  map[string]json.RawMessage
	changeFeed corecosmosstoragetesting.MockChangeFeed
}

var _ azureresourcescosmosstorage.AzureResourcesDBClient = &MockAzureResourcesDBClient{}
var _ corecosmosstoragetesting.MockDocumentStore = &MockAzureResourcesDBClient{}

// NewMockAzureResourcesDBClient creates an empty MockAzureResourcesDBClient.
func NewMockAzureResourcesDBClient() *MockAzureResourcesDBClient {
	return &MockAzureResourcesDBClient{
		documents: make(map[string]json.RawMessage),
	}
}

// NewMockAzureResourcesDBClientWithResources creates a MockAzureResourcesDBClient
// and populates it with the given resources. Supported types:
//   - *azureresourcesapi.DeploymentStackDesire
func NewMockAzureResourcesDBClientWithResources(ctx context.Context, resources []any) (*MockAzureResourcesDBClient, error) {
	mock := NewMockAzureResourcesDBClient()
	for i, r := range resources {
		if err := mock.addResource(ctx, r); err != nil {
			return nil, fmt.Errorf("failed to add resource at index %d: %w", i, err)
		}
	}
	return mock, nil
}

func (m *MockAzureResourcesDBClient) addResource(ctx context.Context, resource any) error {
	switch r := resource.(type) {
	case *azureresourcesapi.DeploymentStackDesire:
		id := r.GetResourceID()
		if id == nil || id.Parent == nil {
			return fmt.Errorf("deployment stack desire is missing resource ID or parent cluster ID")
		}
		crud, err := m.DeploymentStackDesiresForCluster(id.SubscriptionID, id.ResourceGroupName, id.Parent.Name)
		if err != nil {
			return err
		}
		_, err = crud.Create(ctx, r, nil)
		return err
	default:
		return fmt.Errorf("unsupported resource type for MockAzureResourcesDBClient: %T", resource)
	}
}

// --- corecosmosstoragetesting.MockDocumentStore implementation ---

func (m *MockAzureResourcesDBClient) GetDocument(cosmosID string) (json.RawMessage, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.documents[strings.ToLower(cosmosID)]
	return data, ok
}

func (m *MockAzureResourcesDBClient) StoreDocument(cosmosID string, data json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[strings.ToLower(cosmosID)] = data
	m.changeFeed.Record(data)
}

func (m *MockAzureResourcesDBClient) MutateDocument(cosmosID string, mutate func(json.RawMessage) (json.RawMessage, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cosmosID = strings.ToLower(cosmosID)
	data, err := mutate(bytes.Clone(m.documents[cosmosID]))
	if err != nil || data == nil {
		return err
	}
	m.documents[cosmosID] = bytes.Clone(data)
	m.changeFeed.Record(data)
	return nil
}

func (m *MockAzureResourcesDBClient) DeleteDocument(cosmosID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.documents, strings.ToLower(cosmosID))
}

func (m *MockAzureResourcesDBClient) ListDocuments(resourceType *azcorearm.ResourceType, prefix string) []json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []json.RawMessage
	for _, data := range m.documents {
		var td cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &td); err != nil {
			continue
		}
		if td.ResourceID == nil {
			continue
		}
		if resourceType != nil && !strings.EqualFold(td.ResourceType, resourceType.String()) {
			continue
		}
		if len(prefix) != 0 &&
			!strings.HasPrefix(strings.ToLower(td.ResourceID.String()), strings.ToLower(prefix)) {
			continue
		}
		results = append(results, data)
	}
	return results
}

func (m *MockAzureResourcesDBClient) GetAllDocuments() map[string]json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(m.documents))
	for k, v := range m.documents {
		out[k] = v
	}
	return out
}

func (m *MockAzureResourcesDBClient) ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	items, nextToken, hasNew := m.changeFeed.Read(options)
	return corecosmosstoragetesting.BuildMockChangeFeedResponse(items, nextToken, hasNew), nil
}

func (m *MockAzureResourcesDBClient) ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return []azcosmos.FeedRange{corecosmosstoragetesting.MockChangeFeedFeedRange}, nil
}

// --- AzureResourcesDBClient implementation ---

func (m *MockAzureResourcesDBClient) DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	parent, err := azureresourcescosmosstorage.ClusterScope(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return m.DeploymentStackDesiresFor(parent)
}

func (m *MockAzureResourcesDBClient) DeploymentStackDesiresFor(parent azureresourcescosmosstorage.DeploymentStackDesireScope) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	if parent.ResourceID() == nil {
		return nil, fmt.Errorf("deployment stack desire scope is not initialized")
	}
	return corecosmosstoragetesting.NewMockResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire, cosmosstorageutils.GenericDocument[azureresourcesapi.DeploymentStackDesire]](
		m, parent.ResourceID(), azureresourcesapi.DeploymentStackDesireResourceTypeForParent(parent.ResourceID()),
	), nil
}

func (m *MockAzureResourcesDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error) {
	return corecosmosstoragetesting.NewMockUntypedCRUD(m, parentResourceID), nil
}

func (m *MockAzureResourcesDBClient) GlobalListers() azureresourcescosmosstorage.AzureResourcesGlobalListers {
	return &mockAzureResourcesGlobalListers{client: m}
}

// --- AzureResourcesGlobalListers ---

type mockAzureResourcesGlobalListers struct {
	client corecosmosstoragetesting.MockDocumentStore
}

var _ azureresourcescosmosstorage.AzureResourcesGlobalListers = &mockAzureResourcesGlobalListers{}

func (g *mockAzureResourcesGlobalListers) DeploymentStackDesires() cosmosstorageutils.GlobalLister[azureresourcesapi.DeploymentStackDesire] {
	return corecosmosstoragetesting.NewMockGlobalLister[azureresourcesapi.DeploymentStackDesire, cosmosstorageutils.GenericDocument[azureresourcesapi.DeploymentStackDesire]](
		g.client,
		[]azcorearm.ResourceType{azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType},
	)
}

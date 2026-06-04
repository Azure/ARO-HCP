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

package databasetesting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// MockFleetDBClient is the in-memory test double for database.FleetDBClient.
// It owns its own document store, separate from MockResourcesDBClient —
// production has the fleet container live in a different Cosmos container
// (and behind different credentials), and the mock mirrors that boundary.
type MockFleetDBClient struct {
	mu        sync.RWMutex
	documents map[string]json.RawMessage
}

var _ database.FleetDBClient = &MockFleetDBClient{}
var _ mockDocumentStore = &MockFleetDBClient{}

// NewMockFleetDBClient creates an empty MockFleetDBClient.
func NewMockFleetDBClient() *MockFleetDBClient {
	return &MockFleetDBClient{
		documents: make(map[string]json.RawMessage),
	}
}

// NewMockFleetDBClientWithResources creates a MockFleetDBClient and populates
// it with the given resources. Supported types:
//   - *fleet.Stamp
//   - *fleet.ManagementCluster
func NewMockFleetDBClientWithResources(ctx context.Context, resources []any) (*MockFleetDBClient, error) {
	mock := NewMockFleetDBClient()
	for i, r := range resources {
		if err := mock.addResource(ctx, r); err != nil {
			return nil, fmt.Errorf("failed to add resource at index %d: %w", i, err)
		}
	}
	return mock, nil
}

func (m *MockFleetDBClient) addResource(ctx context.Context, resource any) error {
	switch r := resource.(type) {
	case *fleet.Stamp:
		return m.addStamp(ctx, r)
	case *fleet.ManagementCluster:
		return m.addManagementCluster(ctx, r)
	default:
		return fmt.Errorf("unsupported resource type for MockFleetDBClient: %T", resource)
	}
}

func (m *MockFleetDBClient) addStamp(ctx context.Context, stamp *fleet.Stamp) error {
	stampIdentifier := stamp.GetStampIdentifier()
	if len(stampIdentifier) == 0 {
		return fmt.Errorf("stamp has empty stamp identifier")
	}
	crud := m.Stamps()
	_, err := crud.Create(ctx, stamp, nil)
	return err
}

func (m *MockFleetDBClient) addManagementCluster(ctx context.Context, mc *fleet.ManagementCluster) error {
	stampIdentifier := mc.GetStampIdentifier()
	if len(stampIdentifier) == 0 {
		return fmt.Errorf("management cluster has empty stamp identifier")
	}
	crud := m.Stamps().ManagementClusters(stampIdentifier)
	_, err := crud.Create(ctx, mc, nil)
	return err
}

// --- mockDocumentStore implementation ---

func (m *MockFleetDBClient) GetDocument(cosmosID string) (json.RawMessage, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.documents[strings.ToLower(cosmosID)]
	return data, ok
}

func (m *MockFleetDBClient) StoreDocument(cosmosID string, data json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[strings.ToLower(cosmosID)] = data
}

func (m *MockFleetDBClient) DeleteDocument(cosmosID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.documents, strings.ToLower(cosmosID))
}

func (m *MockFleetDBClient) ListDocuments(resourceType *azcorearm.ResourceType, prefix string) []json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []json.RawMessage
	for _, data := range m.documents {
		var td database.TypedDocument
		if err := json.Unmarshal(data, &td); err != nil {
			continue
		}
		// Mirror the production query, which requires IS_DEFINED(c.resourceID);
		// documents without a resourceID are never returned by list.
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

func (m *MockFleetDBClient) GetAllDocuments() map[string]json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(m.documents))
	for k, v := range m.documents {
		out[k] = v
	}
	return out
}

// newMockFleetResourceCRUD creates a mockResourceCRUD with path construction
// that mirrors fleetResourceCRUD. Fleet resources live outside the subscription
// hierarchy (e.g. /providers/Microsoft.RedHatOpenShift/stamps/{id}), so the
// standard subscription-scoped mockResourceCRUD path logic does not apply.
func newMockFleetResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	client mockDocumentStore, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType,
) *mockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {
	m := newMockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType](client, parentResourceID, resourceType)
	m.makeResourceIDPath = func(resourceName string) (*azcorearm.ResourceID, error) {
		var base string
		if parentResourceID != nil {
			base = parentResourceID.String() + "/" + resourceType.Types[len(resourceType.Types)-1]
		} else {
			base = "/providers/" + resourceType.String()
		}
		if len(resourceName) > 0 {
			base += "/" + resourceName
		}
		return azcorearm.ParseResourceID(strings.ToLower(base))
	}
	m.getListPrefix = func() (string, error) {
		rid, err := m.makeResourceIDPath("")
		if err != nil {
			return "", err
		}
		return rid.String() + "/", nil
	}
	return m
}

// --- FleetDBClient implementation ---

func (m *MockFleetDBClient) Stamps() database.StampsCRUD {
	inner := newMockFleetResourceCRUD[fleet.Stamp, *fleet.Stamp, database.GenericDocument[fleet.Stamp]](
		m, nil, fleet.StampResourceType,
	)
	return &mockStampsCRUD{
		ValidatingResourceCRUD: database.NewValidatingCRUD(inner,
			validation.ValidateStampCreate,
			validation.ValidateStampUpdate,
		),
		store: m,
	}
}

func (m *MockFleetDBClient) GlobalListers() database.FleetGlobalListers {
	return &mockFleetGlobalListers{client: m}
}

// --- StampsCRUD ---

type mockStampsCRUD struct {
	database.ValidatingResourceCRUD[fleet.Stamp, *fleet.Stamp]
	store *MockFleetDBClient
}

func (s *mockStampsCRUD) ManagementClusters(stampIdentifier string) database.ManagementClustersCRUD {
	parentResourceID, err := fleet.ToStampResourceID(stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", stampIdentifier, err))
	}
	inner := newMockFleetResourceCRUD[fleet.ManagementCluster, *fleet.ManagementCluster, database.GenericDocument[fleet.ManagementCluster]](
		s.store, parentResourceID, fleet.ManagementClusterResourceType,
	)
	return &mockManagementClustersCRUD{
		ValidatingResourceCRUD: database.NewValidatingCRUD(inner,
			validation.ValidateManagementClusterCreate,
			validation.ValidateManagementClusterUpdate,
		),
		store:           s.store,
		stampIdentifier: stampIdentifier,
	}
}

// --- ManagementClustersCRUD ---

type mockManagementClustersCRUD struct {
	database.ValidatingResourceCRUD[fleet.ManagementCluster, *fleet.ManagementCluster]
	store           *MockFleetDBClient
	stampIdentifier string
}

func (m *mockManagementClustersCRUD) Controllers() database.ResourceCRUD[api.Controller, *api.Controller] {
	mcResourceID, err := fleet.ToManagementClusterResourceID(m.stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", m.stampIdentifier, err))
	}
	return newMockFleetResourceCRUD[api.Controller, *api.Controller, database.GenericDocument[api.Controller]](
		m.store, mcResourceID, fleet.ManagementClusterControllerResourceType,
	)
}

// --- FleetGlobalListers ---

type mockFleetGlobalListers struct {
	client mockDocumentStore
}

var _ database.FleetGlobalListers = &mockFleetGlobalListers{}

func (g *mockFleetGlobalListers) Stamps() database.GlobalLister[fleet.Stamp] {
	return &mockGlobalLister[fleet.Stamp, database.GenericDocument[fleet.Stamp]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{fleet.StampResourceType},
	}
}

func (g *mockFleetGlobalListers) ManagementClusters() database.GlobalLister[fleet.ManagementCluster] {
	return &mockGlobalLister[fleet.ManagementCluster, database.GenericDocument[fleet.ManagementCluster]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{fleet.ManagementClusterResourceType},
	}
}

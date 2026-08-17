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

package fleetcosmosstoragetesting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// MockFleetDBClient is the in-memory test double for fleetcosmosstorage.FleetDBClient.
// It owns its own document store, separate from corecosmosstoragetesting.MockResourcesDBClient —
// production has the fleet container live in a different Cosmos container
// (and behind different credentials), and the mock mirrors that boundary.
type MockFleetDBClient struct {
	mu         sync.RWMutex
	documents  map[string]json.RawMessage
	changeFeed corecosmosstoragetesting.MockChangeFeed
}

var _ fleetcosmosstorage.FleetDBClient = &MockFleetDBClient{}
var _ corecosmosstoragetesting.MockDocumentStore = &MockFleetDBClient{}

// NewMockFleetDBClient creates an empty MockFleetDBClient.
func NewMockFleetDBClient() *MockFleetDBClient {
	return &MockFleetDBClient{
		documents: make(map[string]json.RawMessage),
	}
}

// NewMockFleetDBClientWithResources creates a MockFleetDBClient and populates
// it with the given resources. Supported types:
//   - *fleetapi.Stamp
//   - *fleetapi.ManagementCluster
//   - *fleetapi.ManagementClusterScheduling
//   - *fleetapi.HCPResourceRequirements
//   - *fleetapi.ControlPlaneVersionRollout
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
	case *fleetapi.Stamp:
		return m.addStamp(ctx, r)
	case *fleetapi.ManagementCluster:
		return m.addManagementCluster(ctx, r)
	case *fleetapi.ManagementClusterScheduling:
		return m.addManagementClusterScheduling(ctx, r)
	case *fleetapi.HCPResourceRequirements:
		return m.addHCPResourceRequirements(ctx, r)
	case *fleetapi.ControlPlaneVersionRollout:
		return m.addControlPlaneVersionRollout(ctx, r)
	default:
		return fmt.Errorf("unsupported resource type for MockFleetDBClient: %T", resource)
	}
}

func (m *MockFleetDBClient) addStamp(ctx context.Context, stamp *fleetapi.Stamp) error {
	stampIdentifier := stamp.GetStampIdentifier()
	if len(stampIdentifier) == 0 {
		return fmt.Errorf("stamp has empty stamp identifier")
	}
	crud := m.Stamps()
	_, err := crud.Create(ctx, stamp, nil)
	return err
}

func (m *MockFleetDBClient) addManagementCluster(ctx context.Context, mc *fleetapi.ManagementCluster) error {
	stampIdentifier := mc.GetStampIdentifier()
	if len(stampIdentifier) == 0 {
		return fmt.Errorf("management cluster has empty stamp identifier")
	}
	crud := m.Stamps().ManagementClusters(stampIdentifier)
	_, err := crud.Create(ctx, mc, nil)
	return err
}

func (m *MockFleetDBClient) addManagementClusterScheduling(ctx context.Context, scheduling *fleetapi.ManagementClusterScheduling) error {
	stampIdentifier := scheduling.PartitionKey
	if len(stampIdentifier) == 0 {
		return fmt.Errorf("management cluster scheduling has empty partition key")
	}
	crud := m.Stamps().ManagementClusters(stampIdentifier).Scheduling()
	_, err := crud.Create(ctx, scheduling, nil)
	return err
}

func (m *MockFleetDBClient) addHCPResourceRequirements(ctx context.Context, requirements *fleetapi.HCPResourceRequirements) error {
	crud := m.HCPResourceRequirements()
	_, err := crud.Create(ctx, requirements, nil)
	return err
}

func (m *MockFleetDBClient) addControlPlaneVersionRollout(ctx context.Context, rollout *fleetapi.ControlPlaneVersionRollout) error {
	ystreamChannel := rollout.GetStampIdentifier()
	if len(ystreamChannel) == 0 {
		return fmt.Errorf("control plane version rollout has empty y-stream channel identifier")
	}
	crud := m.ControlPlaneVersionRollouts()
	_, err := crud.Create(ctx, rollout, nil)
	return err
}

// --- corecosmosstoragetesting.MockDocumentStore implementation ---

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
	m.changeFeed.Record(data)
}

func (m *MockFleetDBClient) ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	var continuation string
	if options != nil && options.Continuation != nil {
		continuation = *options.Continuation
	}
	items, nextToken, hasNew := m.changeFeed.Read(continuation)
	return corecosmosstoragetesting.BuildMockChangeFeedResponse(items, nextToken, hasNew), nil
}

func (m *MockFleetDBClient) ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return []azcosmos.FeedRange{corecosmosstoragetesting.MockChangeFeedFeedRange}, nil
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
		var td cosmosstorageutils.TypedDocument
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

// newMockFleetResourceCRUD creates a corecosmosstoragetesting.MockResourceCRUD with path construction
// that mirrors fleetResourceCRUD. Fleet resources live outside the subscription
// hierarchy (e.g. /providers/Microsoft.RedHatOpenShift/stamps/{id}), so the
// standard subscription-scoped corecosmosstoragetesting.MockResourceCRUD path logic does not apply.
func newMockFleetResourceCRUD[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	client corecosmosstoragetesting.MockDocumentStore, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType,
) *corecosmosstoragetesting.MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {
	m := corecosmosstoragetesting.NewMockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType](client, parentResourceID, resourceType)
	m.MakeResourceIDPath = func(resourceName string) (*azcorearm.ResourceID, error) {
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
	m.GetListPrefix = func() (string, error) {
		rid, err := m.MakeResourceIDPath("")
		if err != nil {
			return "", err
		}
		return rid.String() + "/", nil
	}
	return m
}

// --- FleetDBClient implementation ---

func (m *MockFleetDBClient) Stamps() fleetcosmosstorage.StampsCRUD {
	inner := newMockFleetResourceCRUD[fleetapi.Stamp, *fleetapi.Stamp, cosmosstorageutils.GenericDocument[fleetapi.Stamp]](
		m, nil, fleetapi.StampResourceType,
	)
	return &mockStampsCRUD{
		ValidatingResourceCRUD: cosmosstorageutils.NewValidatingCRUD(inner,
			validation.ValidateStampCreate,
			validation.ValidateStampUpdate,
		),
		store: m,
	}
}

func (m *MockFleetDBClient) HCPResourceRequirements() cosmosstorageutils.ResourceCRUD[fleetapi.HCPResourceRequirements, *fleetapi.HCPResourceRequirements] {
	return newMockFleetResourceCRUD[fleetapi.HCPResourceRequirements, *fleetapi.HCPResourceRequirements, cosmosstorageutils.GenericDocument[fleetapi.HCPResourceRequirements]](
		m, nil, fleetapi.HCPResourceRequirementsResourceType,
	)
}

func (m *MockFleetDBClient) ControlPlaneVersionRollouts() cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout] {
	inner := newMockFleetResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout, cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]](
		m, nil, fleetapi.ControlPlaneVersionRolloutResourceType,
	)
	return cosmosstorageutils.NewValidatingCRUD(inner,
		validation.ValidateControlPlaneVersionRolloutCreate,
		validation.ValidateControlPlaneVersionRolloutUpdate,
	)
}

func (m *MockFleetDBClient) GlobalListers() fleetcosmosstorage.FleetGlobalListers {
	return &mockFleetGlobalListers{client: m}
}

// --- StampsCRUD ---

type mockStampsCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.Stamp, *fleetapi.Stamp]
	store *MockFleetDBClient
}

func (s *mockStampsCRUD) ManagementClusters(stampIdentifier string) fleetcosmosstorage.ManagementClustersCRUD {
	parentResourceID, err := fleetapi.ToStampResourceID(stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", stampIdentifier, err))
	}
	inner := newMockFleetResourceCRUD[fleetapi.ManagementCluster, *fleetapi.ManagementCluster, cosmosstorageutils.GenericDocument[fleetapi.ManagementCluster]](
		s.store, parentResourceID, fleetapi.ManagementClusterResourceType,
	)
	return &mockManagementClustersCRUD{
		ValidatingResourceCRUD: cosmosstorageutils.NewValidatingCRUD(inner,
			validation.ValidateManagementClusterCreate,
			validation.ValidateManagementClusterUpdate,
		),
		store:           s.store,
		stampIdentifier: stampIdentifier,
	}
}

// --- ManagementClustersCRUD ---

type mockManagementClustersCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ManagementCluster, *fleetapi.ManagementCluster]
	store           *MockFleetDBClient
	stampIdentifier string
}

func (m *mockManagementClustersCRUD) Controllers() cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	managementClusterResourceID, err := fleetapi.ToManagementClusterResourceID(m.stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", m.stampIdentifier, err))
	}
	return newMockFleetResourceCRUD[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](
		m.store, managementClusterResourceID, fleetapi.ManagementClusterControllerResourceType,
	)
}

func (m *mockManagementClustersCRUD) Scheduling() cosmosstorageutils.ResourceCRUD[fleetapi.ManagementClusterScheduling, *fleetapi.ManagementClusterScheduling] {
	managementClusterResourceID, err := fleetapi.ToManagementClusterResourceID(m.stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", m.stampIdentifier, err))
	}
	return newMockFleetResourceCRUD[fleetapi.ManagementClusterScheduling, *fleetapi.ManagementClusterScheduling, cosmosstorageutils.GenericDocument[fleetapi.ManagementClusterScheduling]](
		m.store, managementClusterResourceID, fleetapi.ManagementClusterSchedulingResourceType,
	)
}

// --- FleetGlobalListers ---

type mockFleetGlobalListers struct {
	client corecosmosstoragetesting.MockDocumentStore
}

var _ fleetcosmosstorage.FleetGlobalListers = &mockFleetGlobalListers{}

func (g *mockFleetGlobalListers) Stamps() cosmosstorageutils.GlobalLister[fleetapi.Stamp] {
	return corecosmosstoragetesting.NewMockGlobalLister[fleetapi.Stamp, cosmosstorageutils.GenericDocument[fleetapi.Stamp]](
		g.client,
		[]azcorearm.ResourceType{fleetapi.StampResourceType},
	)
}

func (g *mockFleetGlobalListers) ManagementClusters() cosmosstorageutils.GlobalLister[fleetapi.ManagementCluster] {
	return corecosmosstoragetesting.NewMockGlobalLister[fleetapi.ManagementCluster, cosmosstorageutils.GenericDocument[fleetapi.ManagementCluster]](
		g.client,
		[]azcorearm.ResourceType{fleetapi.ManagementClusterResourceType},
	)
}

func (g *mockFleetGlobalListers) ControlPlaneVersionRollouts() cosmosstorageutils.GlobalLister[fleetapi.ControlPlaneVersionRollout] {
	return corecosmosstoragetesting.NewMockGlobalLister[fleetapi.ControlPlaneVersionRollout, cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]](
		g.client,
		[]azcorearm.ResourceType{fleetapi.ControlPlaneVersionRolloutResourceType},
	)
}

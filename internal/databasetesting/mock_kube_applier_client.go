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
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

// MockKubeApplierDBClient is the in-memory test double for database.KubeApplierDBClient.
// It owns its own document store, separate from MockDBClient — production has the
// kube-applier container live in a different container (and behind different
// credentials) than the resources container, and the mock mirrors that boundary.
type MockKubeApplierDBClient struct {
	mu        sync.RWMutex
	documents map[string]json.RawMessage
}

var _ database.KubeApplierDBClient = &MockKubeApplierDBClient{}

// NewMockKubeApplierDBClient creates an empty MockKubeApplierDBClient.
func NewMockKubeApplierDBClient() *MockKubeApplierDBClient {
	return &MockKubeApplierDBClient{
		documents: make(map[string]json.RawMessage),
	}
}

// NewMockKubeApplierDBClientWithResources creates a MockKubeApplierDBClient and
// populates it with the given *Desire resources. Supported types:
//   - *kubeapplier.ApplyDesire
//   - *kubeapplier.DeleteDesire
//   - *kubeapplier.ReadDesire
func NewMockKubeApplierDBClientWithResources(ctx context.Context, resources []any) (*MockKubeApplierDBClient, error) {
	mock := NewMockKubeApplierDBClient()
	for i, r := range resources {
		if err := mock.addResource(ctx, r); err != nil {
			return nil, fmt.Errorf("failed to add resource at index %d: %w", i, err)
		}
	}
	return mock, nil
}

// --- mockDocumentStore implementation -----------------------------------

func (m *MockKubeApplierDBClient) GetDocument(cosmosID string) (json.RawMessage, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.documents[strings.ToLower(cosmosID)]
	return data, ok
}

func (m *MockKubeApplierDBClient) StoreDocument(cosmosID string, data json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[strings.ToLower(cosmosID)] = data
}

func (m *MockKubeApplierDBClient) DeleteDocument(cosmosID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.documents, strings.ToLower(cosmosID))
}

func (m *MockKubeApplierDBClient) ListDocuments(resourceType *azcorearm.ResourceType, prefix string) []json.RawMessage {
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

func (m *MockKubeApplierDBClient) GetAllDocuments() map[string]json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(m.documents))
	for k, v := range m.documents {
		out[k] = v
	}
	return out
}

// Compile-time assertion: MockKubeApplierDBClient is a mockDocumentStore so that
// the existing mockResourceCRUD[T] machinery can drive its storage.
var _ mockDocumentStore = &MockKubeApplierDBClient{}

// --- KubeApplierDBClient implementation -----------------------------------

func (m *MockKubeApplierDBClient) KubeApplier(managementCluster string) database.KubeApplierCRUD {
	return &mockKubeApplierCRUD{store: m, managementCluster: managementCluster}
}

func (m *MockKubeApplierDBClient) GlobalListers() database.KubeApplierGlobalListers {
	return &mockKubeApplierGlobalListers{store: m}
}

func (m *MockKubeApplierDBClient) PartitionListers(managementCluster string) database.KubeApplierGlobalListers {
	return &mockKubeApplierGlobalListers{
		store:        m,
		partitionKey: strings.ToLower(managementCluster),
	}
}

type mockKubeApplierCRUD struct {
	store             *MockKubeApplierDBClient
	managementCluster string
}

var _ database.KubeApplierCRUD = &mockKubeApplierCRUD{}

func (k *mockKubeApplierCRUD) ApplyDesires(
	parent database.ResourceParent,
) (database.ResourceCRUD[kubeapplier.ApplyDesire], error) {
	parentID, err := desireParentID(parent)
	if err != nil {
		return nil, err
	}
	resourceType := kubeapplier.ClusterScopedApplyDesireResourceType
	if parent.IsNodePoolScoped() {
		resourceType = kubeapplier.NodePoolScopedApplyDesireResourceType
	}
	return newMockResourceCRUD[kubeapplier.ApplyDesire, database.GenericDocument[kubeapplier.ApplyDesire]](
		k.store, parentID, resourceType,
	), nil
}

func (k *mockKubeApplierCRUD) DeleteDesires(
	parent database.ResourceParent,
) (database.ResourceCRUD[kubeapplier.DeleteDesire], error) {
	parentID, err := desireParentID(parent)
	if err != nil {
		return nil, err
	}
	resourceType := kubeapplier.ClusterScopedDeleteDesireResourceType
	if parent.IsNodePoolScoped() {
		resourceType = kubeapplier.NodePoolScopedDeleteDesireResourceType
	}
	return newMockResourceCRUD[kubeapplier.DeleteDesire, database.GenericDocument[kubeapplier.DeleteDesire]](
		k.store, parentID, resourceType,
	), nil
}

func (k *mockKubeApplierCRUD) ReadDesires(
	parent database.ResourceParent,
) (database.ResourceCRUD[kubeapplier.ReadDesire], error) {
	parentID, err := desireParentID(parent)
	if err != nil {
		return nil, err
	}
	resourceType := kubeapplier.ClusterScopedReadDesireResourceType
	if parent.IsNodePoolScoped() {
		resourceType = kubeapplier.NodePoolScopedReadDesireResourceType
	}
	return newMockResourceCRUD[kubeapplier.ReadDesire, database.GenericDocument[kubeapplier.ReadDesire]](
		k.store, parentID, resourceType,
	), nil
}

// desireParentID builds the parent resource ID for *Desire documents using the
// real CRUD's exact format, so the mock and the real client see the same IDs.
func desireParentID(parent database.ResourceParent) (*azcorearm.ResourceID, error) {
	if parent.IsNodePoolScoped() {
		return api.ToNodePoolResourceID(
			parent.SubscriptionID, parent.ResourceGroupName, parent.ClusterName, parent.NodePoolName,
		)
	}
	return api.ToClusterResourceID(parent.SubscriptionID, parent.ResourceGroupName, parent.ClusterName)
}

// --- KubeApplierGlobalListers ------------------------------------------------

type mockKubeApplierGlobalListers struct {
	store        *MockKubeApplierDBClient
	partitionKey string // empty = cross-partition; non-empty = restrict to this partition
}

var _ database.KubeApplierGlobalListers = &mockKubeApplierGlobalListers{}

func (g *mockKubeApplierGlobalListers) ApplyDesires() database.GlobalLister[kubeapplier.ApplyDesire] {
	return &mockKubeApplierDesireGlobalLister[kubeapplier.ApplyDesire, database.GenericDocument[kubeapplier.ApplyDesire]]{
		store:        g.store,
		partitionKey: g.partitionKey,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedApplyDesireResourceType,
			kubeapplier.NodePoolScopedApplyDesireResourceType,
		},
	}
}

func (g *mockKubeApplierGlobalListers) DeleteDesires() database.GlobalLister[kubeapplier.DeleteDesire] {
	return &mockKubeApplierDesireGlobalLister[kubeapplier.DeleteDesire, database.GenericDocument[kubeapplier.DeleteDesire]]{
		store:        g.store,
		partitionKey: g.partitionKey,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedDeleteDesireResourceType,
			kubeapplier.NodePoolScopedDeleteDesireResourceType,
		},
	}
}

func (g *mockKubeApplierGlobalListers) ReadDesires() database.GlobalLister[kubeapplier.ReadDesire] {
	return &mockKubeApplierDesireGlobalLister[kubeapplier.ReadDesire, database.GenericDocument[kubeapplier.ReadDesire]]{
		store:        g.store,
		partitionKey: g.partitionKey,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedReadDesireResourceType,
			kubeapplier.NodePoolScopedReadDesireResourceType,
		},
	}
}

type mockKubeApplierDesireGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	store         *MockKubeApplierDBClient
	resourceTypes []azcorearm.ResourceType
	partitionKey  string
}

func (l *mockKubeApplierDesireGlobalLister[InternalAPIType, CosmosAPIType]) List(
	ctx context.Context, options *database.DBClientListResourceDocsOptions,
) (database.DBClientIterator[InternalAPIType], error) {
	allDocs := l.store.GetAllDocuments()

	var ids []string
	var items []*InternalAPIType

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}
		matches := false
		for _, rt := range l.resourceTypes {
			if strings.EqualFold(typedDoc.ResourceType, rt.String()) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if len(l.partitionKey) > 0 && !strings.EqualFold(typedDoc.PartitionKey, l.partitionKey) {
			continue
		}
		var cosmosObj CosmosAPIType
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}
		internalObj, err := database.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
		if err != nil {
			continue
		}
		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items), nil
}

// --- resource-loading helpers (parallel to mock_init.go) ---------------------

func (m *MockKubeApplierDBClient) addResource(ctx context.Context, resource any) error {
	switch r := resource.(type) {
	case *kubeapplier.ApplyDesire:
		return m.addApplyDesire(ctx, r)
	case *kubeapplier.DeleteDesire:
		return m.addDeleteDesire(ctx, r)
	case *kubeapplier.ReadDesire:
		return m.addReadDesire(ctx, r)
	default:
		return fmt.Errorf("unsupported resource type for MockKubeApplierDBClient: %T", resource)
	}
}

func (m *MockKubeApplierDBClient) addApplyDesire(ctx context.Context, d *kubeapplier.ApplyDesire) error {
	parent, err := parentForKubeApplierDesire(d.GetResourceID())
	if err != nil {
		return err
	}
	crud, err := m.KubeApplier(managementClusterPartitionKey(d.GetManagementCluster())).ApplyDesires(parent)
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

func (m *MockKubeApplierDBClient) addDeleteDesire(ctx context.Context, d *kubeapplier.DeleteDesire) error {
	parent, err := parentForKubeApplierDesire(d.GetResourceID())
	if err != nil {
		return err
	}
	crud, err := m.KubeApplier(managementClusterPartitionKey(d.GetManagementCluster())).DeleteDesires(parent)
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

func (m *MockKubeApplierDBClient) addReadDesire(ctx context.Context, d *kubeapplier.ReadDesire) error {
	parent, err := parentForKubeApplierDesire(d.GetResourceID())
	if err != nil {
		return err
	}
	crud, err := m.KubeApplier(managementClusterPartitionKey(d.GetManagementCluster())).ReadDesires(parent)
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

// managementClusterPartitionKey reduces a *Desire's spec.managementCluster
// resourceID to the lowercased string we use as the Cosmos partition key.
// Returns "" when the resourceID is nil — callers that pass that to
// KubeApplier(...) get the empty partition, which is acceptable in tests.
func managementClusterPartitionKey(rid *azcorearm.ResourceID) string {
	if rid == nil {
		return ""
	}
	return strings.ToLower(rid.String())
}

// parentForKubeApplierDesire derives a database.ResourceParent from a *Desire's
// resource ID, handling both cluster-scoped and node-pool-scoped nestings.
func parentForKubeApplierDesire(resourceID *azcorearm.ResourceID) (database.ResourceParent, error) {
	if resourceID == nil {
		return database.ResourceParent{}, fmt.Errorf("resource ID is nil")
	}
	if resourceID.Parent == nil {
		return database.ResourceParent{}, fmt.Errorf("desire %q has no parent in its resource ID", resourceID.String())
	}
	parentType := resourceID.Parent.ResourceType
	switch {
	case armhelpers.ResourceTypeEqual(parentType, api.ClusterResourceType):
		return database.ResourceParent{
			SubscriptionID:    resourceID.SubscriptionID,
			ResourceGroupName: resourceID.ResourceGroupName,
			ClusterName:       resourceID.Parent.Name,
		}, nil
	case armhelpers.ResourceTypeEqual(parentType, api.NodePoolResourceType):
		if resourceID.Parent.Parent == nil {
			return database.ResourceParent{}, fmt.Errorf(
				"nodepool-scoped desire %q has no grandparent cluster", resourceID.String(),
			)
		}
		return database.ResourceParent{
			SubscriptionID:    resourceID.SubscriptionID,
			ResourceGroupName: resourceID.ResourceGroupName,
			ClusterName:       resourceID.Parent.Parent.Name,
			NodePoolName:      resourceID.Parent.Name,
		}, nil
	}
	return database.ResourceParent{}, fmt.Errorf(
		"unsupported parent resource type for kube-applier desire: %s", parentType,
	)
}

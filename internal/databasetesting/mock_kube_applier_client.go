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
	"path"
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
//
// In the per-management-cluster container model, each MockKubeApplierDBClient
// represents one container. Tests that want multiple containers use
// MockKubeApplierDBClients (plural).
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

func (m *MockKubeApplierDBClient) ApplyDesiresForCluster(
	subscriptionID, resourceGroupName, clusterName string,
) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	parentID, err := api.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return newMockResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire, database.GenericDocument[kubeapplier.ApplyDesire]](
		m, parentID, kubeapplier.ClusterScopedApplyDesireResourceType,
	), nil
}

func (m *MockKubeApplierDBClient) ApplyDesiresForNodePool(
	subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	parentID, err := api.ToNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return newMockResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire, database.GenericDocument[kubeapplier.ApplyDesire]](
		m, parentID, kubeapplier.NodePoolScopedApplyDesireResourceType,
	), nil
}

func (m *MockKubeApplierDBClient) DeleteDesiresForCluster(
	subscriptionID, resourceGroupName, clusterName string,
) (database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error) {
	parentID, err := api.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return newMockResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire, database.GenericDocument[kubeapplier.DeleteDesire]](
		m, parentID, kubeapplier.ClusterScopedDeleteDesireResourceType,
	), nil
}

func (m *MockKubeApplierDBClient) DeleteDesiresForNodePool(
	subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) (database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error) {
	parentID, err := api.ToNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return newMockResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire, database.GenericDocument[kubeapplier.DeleteDesire]](
		m, parentID, kubeapplier.NodePoolScopedDeleteDesireResourceType,
	), nil
}

func (m *MockKubeApplierDBClient) ReadDesiresForCluster(
	subscriptionID, resourceGroupName, clusterName string,
) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	parentID, err := api.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return newMockResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire, database.GenericDocument[kubeapplier.ReadDesire]](
		m, parentID, kubeapplier.ClusterScopedReadDesireResourceType,
	), nil
}

func (m *MockKubeApplierDBClient) ReadDesiresForNodePool(
	subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	parentID, err := api.ToNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return newMockResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire, database.GenericDocument[kubeapplier.ReadDesire]](
		m, parentID, kubeapplier.NodePoolScopedReadDesireResourceType,
	), nil
}

func (m *MockKubeApplierDBClient) Listers() database.KubeApplierListers {
	return &mockKubeApplierListers{store: m}
}

func (m *MockKubeApplierDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (database.UntypedResourceCRUD, error) {
	return &mockKubeApplierUntypedCRUD{store: m, parentResourceID: parentResourceID}, nil
}

// --- KubeApplierListers (in-memory) ----------------------------------------

type mockKubeApplierListers struct {
	store *MockKubeApplierDBClient
}

var _ database.KubeApplierListers = &mockKubeApplierListers{}

func (g *mockKubeApplierListers) ApplyDesires() database.GlobalLister[kubeapplier.ApplyDesire] {
	return &mockKubeApplierDesireLister[kubeapplier.ApplyDesire, database.GenericDocument[kubeapplier.ApplyDesire]]{
		store: g.store,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedApplyDesireResourceType,
			kubeapplier.NodePoolScopedApplyDesireResourceType,
		},
	}
}

func (g *mockKubeApplierListers) DeleteDesires() database.GlobalLister[kubeapplier.DeleteDesire] {
	return &mockKubeApplierDesireLister[kubeapplier.DeleteDesire, database.GenericDocument[kubeapplier.DeleteDesire]]{
		store: g.store,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedDeleteDesireResourceType,
			kubeapplier.NodePoolScopedDeleteDesireResourceType,
		},
	}
}

func (g *mockKubeApplierListers) ReadDesires() database.GlobalLister[kubeapplier.ReadDesire] {
	return &mockKubeApplierDesireLister[kubeapplier.ReadDesire, database.GenericDocument[kubeapplier.ReadDesire]]{
		store: g.store,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedReadDesireResourceType,
			kubeapplier.NodePoolScopedReadDesireResourceType,
		},
	}
}

type mockKubeApplierDesireLister[InternalAPIType, CosmosAPIType any] struct {
	store         *MockKubeApplierDBClient
	resourceTypes []azcorearm.ResourceType
}

func (l *mockKubeApplierDesireLister[InternalAPIType, CosmosAPIType]) List(
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

// --- UntypedCRUD (in-memory) ----------------------------------------------

type mockKubeApplierUntypedCRUD struct {
	store            *MockKubeApplierDBClient
	parentResourceID azcorearm.ResourceID
}

var _ database.UntypedResourceCRUD = &mockKubeApplierUntypedCRUD{}

func (k *mockKubeApplierUntypedCRUD) Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*database.TypedDocument, error) {
	return nil, fmt.Errorf("kube-applier UntypedCRUD.Get is not supported")
}

func (k *mockKubeApplierUntypedCRUD) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[database.TypedDocument], error) {
	return k.listInternal(ctx, true)
}

func (k *mockKubeApplierUntypedCRUD) ListRecursive(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[database.TypedDocument], error) {
	return k.listInternal(ctx, false)
}

func (k *mockKubeApplierUntypedCRUD) listInternal(ctx context.Context, nonRecursive bool) (database.DBClientIterator[database.TypedDocument], error) {
	allDocs := k.store.GetAllDocuments()

	prefix := strings.ToLower(k.parentResourceID.String()) + "/"
	requiredSlashes := strings.Count(k.parentResourceID.String(), "/") + 2
	if strings.EqualFold(k.parentResourceID.ResourceType.Type, "resourceGroups") {
		requiredSlashes = strings.Count(k.parentResourceID.String(), "/") + 4
	}

	var ids []string
	var items []*database.TypedDocument

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		if typedDoc.ResourceID != nil && !strings.HasPrefix(strings.ToLower(typedDoc.ResourceID.String()), prefix) {
			continue
		}

		if nonRecursive && typedDoc.ResourceID != nil {
			if strings.Count(typedDoc.ResourceID.String(), "/") != requiredSlashes {
				continue
			}
		}

		docCopy := typedDoc
		docPointer, err := database.CosmosToInternal[database.TypedDocument, database.TypedDocument](&docCopy)
		if err != nil {
			continue
		}
		ids = append(ids, docPointer.ID)
		items = append(items, docPointer)
	}

	return newMockIterator(ids, items), nil
}

func (k *mockKubeApplierUntypedCRUD) Delete(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	return fmt.Errorf("kube-applier UntypedCRUD.Delete is not supported")
}

func (k *mockKubeApplierUntypedCRUD) DeleteByCosmosID(ctx context.Context, partitionKey, cosmosID string) error {
	k.store.DeleteDocument(cosmosID)
	return nil
}

func (k *mockKubeApplierUntypedCRUD) Child(resourceType azcorearm.ResourceType, resourceName string) (database.UntypedResourceCRUD, error) {
	if len(resourceName) == 0 {
		return nil, fmt.Errorf("resourceName is required")
	}
	parts := []string{k.parentResourceID.String()}
	switch {
	case strings.EqualFold(resourceType.Type, "resourcegroups"):
	case resourceType.Namespace == api.ProviderNamespace && k.parentResourceID.ResourceType.Namespace != api.ProviderNamespace:
		parts = append(parts, "providers", resourceType.Namespace)
	case resourceType.Namespace != api.ProviderNamespace && k.parentResourceID.ResourceType.Namespace == api.ProviderNamespace:
		return nil, fmt.Errorf("cannot switch to a non-RH provider: %q", resourceType.Namespace)
	}
	parts = append(parts, resourceType.Types[len(resourceType.Types)-1])
	parts = append(parts, resourceName)
	newParent, err := azcorearm.ParseResourceID(path.Join(parts...))
	if err != nil {
		return nil, err
	}
	return &mockKubeApplierUntypedCRUD{store: k.store, parentResourceID: *newParent}, nil
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
	subscriptionID, resourceGroupName, clusterName, nodePoolName, err := parentForKubeApplierDesire(d.GetResourceID())
	if err != nil {
		return err
	}
	var crud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]
	if len(nodePoolName) != 0 {
		crud, err = m.ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	} else {
		crud, err = m.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	}
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

func (m *MockKubeApplierDBClient) addDeleteDesire(ctx context.Context, d *kubeapplier.DeleteDesire) error {
	subscriptionID, resourceGroupName, clusterName, nodePoolName, err := parentForKubeApplierDesire(d.GetResourceID())
	if err != nil {
		return err
	}
	var crud database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire]
	if len(nodePoolName) != 0 {
		crud, err = m.DeleteDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	} else {
		crud, err = m.DeleteDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	}
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

func (m *MockKubeApplierDBClient) addReadDesire(ctx context.Context, d *kubeapplier.ReadDesire) error {
	subscriptionID, resourceGroupName, clusterName, nodePoolName, err := parentForKubeApplierDesire(d.GetResourceID())
	if err != nil {
		return err
	}
	var crud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]
	if len(nodePoolName) != 0 {
		crud, err = m.ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	} else {
		crud, err = m.ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	}
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

// parentForKubeApplierDesire splits a *Desire's resource ID into the parent
// field names (subscriptionID, resourceGroupName, clusterName, nodePoolName).
// nodePoolName is empty for cluster-scoped desires.
func parentForKubeApplierDesire(resourceID *azcorearm.ResourceID) (subscriptionID, resourceGroupName, clusterName, nodePoolName string, err error) {
	if resourceID == nil {
		return "", "", "", "", fmt.Errorf("resource ID is nil")
	}
	if resourceID.Parent == nil {
		return "", "", "", "", fmt.Errorf("desire %q has no parent in its resource ID", resourceID.String())
	}
	parentType := resourceID.Parent.ResourceType
	switch {
	case armhelpers.ResourceTypeEqual(parentType, api.ClusterResourceType):
		return resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Parent.Name, "", nil
	case armhelpers.ResourceTypeEqual(parentType, api.NodePoolResourceType):
		if resourceID.Parent.Parent == nil {
			return "", "", "", "", fmt.Errorf(
				"nodepool-scoped desire %q has no grandparent cluster", resourceID.String(),
			)
		}
		return resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Parent.Parent.Name, resourceID.Parent.Name, nil
	}
	return "", "", "", "", fmt.Errorf(
		"unsupported parent resource type for kube-applier desire: %s", parentType,
	)
}

// MockKubeApplierDBClients is the in-memory test double for
// database.KubeApplierDBClients. Construction registers a per-management-cluster
// MockKubeApplierDBClient; For() returns the registered client (or nil for
// unknown resourceIDs). Thread-safe.
type MockKubeApplierDBClients struct {
	mu      sync.Mutex
	clients map[string]*MockKubeApplierDBClient // key = lowercased(rid.String())
}

var _ database.KubeApplierDBClients = &MockKubeApplierDBClients{}

// NewMockKubeApplierDBClients constructs an empty registry; use Register to add
// per-management-cluster clients.
func NewMockKubeApplierDBClients() *MockKubeApplierDBClients {
	return &MockKubeApplierDBClients{clients: map[string]*MockKubeApplierDBClient{}}
}

// Register stores a per-management-cluster client under the given resourceID.
// Replaces any previous registration for the same resourceID.
func (c *MockKubeApplierDBClients) Register(managementClusterResourceID *azcorearm.ResourceID, client *MockKubeApplierDBClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clients[strings.ToLower(managementClusterResourceID.String())] = client
}

func (c *MockKubeApplierDBClients) For(_ context.Context, managementClusterResourceID *azcorearm.ResourceID) database.KubeApplierDBClient {
	if managementClusterResourceID == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	client, ok := c.clients[strings.ToLower(managementClusterResourceID.String())]
	if !ok {
		return nil
	}
	return client
}

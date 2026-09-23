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

package kubeappliercosmosstoragetesting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

// MockKubeApplierDBClient is the in-memory test double for kubeappliercosmosstorage.KubeApplierDBClient.
// It owns its own document store, separate from MockDBClient — production has the
// kube-applier container live in a different container (and behind different
// credentials) than the resources container, and the mock mirrors that boundary.
//
// In the per-management-cluster container model, each MockKubeApplierDBClient
// represents one container. Tests that want multiple containers use
// MockKubeApplierDBClients (plural).
type MockKubeApplierDBClient struct {
	mu         sync.RWMutex
	documents  map[string]json.RawMessage
	changeFeed corecosmosstoragetesting.MockChangeFeed
}

var _ kubeappliercosmosstorage.KubeApplierDBClient = &MockKubeApplierDBClient{}

// NewMockKubeApplierDBClient creates an empty MockKubeApplierDBClient.
func NewMockKubeApplierDBClient() *MockKubeApplierDBClient {
	return &MockKubeApplierDBClient{
		documents: make(map[string]json.RawMessage),
	}
}

// NewMockKubeApplierDBClientWithResources creates a MockKubeApplierDBClient and
// populates it with the given *Desire resources. Supported types:
//   - *kubeapplierapi.ApplyDesire
//   - *kubeapplierapi.ReadDesire
func NewMockKubeApplierDBClientWithResources(ctx context.Context, resources []any) (*MockKubeApplierDBClient, error) {
	mock := NewMockKubeApplierDBClient()
	for i, r := range resources {
		if err := mock.addResource(ctx, r); err != nil {
			return nil, fmt.Errorf("failed to add resource at index %d: %w", i, err)
		}
	}
	return mock, nil
}

// --- corecosmosstoragetesting.MockDocumentStore implementation -----------------------------------

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
	m.changeFeed.Record(data)
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
		if td.DeletionTimestamp != nil {
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

// Compile-time assertion: MockKubeApplierDBClient is a corecosmosstoragetesting.MockDocumentStore so that
// the existing corecosmosstoragetesting.MockResourceCRUD[T] machinery can drive its storage.
var _ corecosmosstoragetesting.MockDocumentStore = &MockKubeApplierDBClient{}

// --- KubeApplierDBClient implementation -----------------------------------

func (m *MockKubeApplierDBClient) ApplyDesiresForCluster(
	subscriptionID, resourceGroupName, clusterName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	parent, err := kubeappliercosmosstorage.ClusterScope(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return m.ApplyDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ApplyDesiresForNodePool(
	subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	parent, err := kubeappliercosmosstorage.NodePoolScope(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return m.ApplyDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ApplyDesiresForSystemAdminCredentialRequest(
	subscriptionID, resourceGroupName, clusterName, credentialRequestName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	parent, err := kubeappliercosmosstorage.CredentialRequestScope(subscriptionID, resourceGroupName, clusterName, credentialRequestName)
	if err != nil {
		return nil, err
	}
	return m.ApplyDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ApplyDesiresForSystemAdminCredentialRevocation(
	subscriptionID, resourceGroupName, clusterName, revocationName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	parent, err := kubeappliercosmosstorage.CredentialRevocationScope(subscriptionID, resourceGroupName, clusterName, revocationName)
	if err != nil {
		return nil, err
	}
	return m.ApplyDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ReadDesiresForCluster(
	subscriptionID, resourceGroupName, clusterName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	parent, err := kubeappliercosmosstorage.ClusterScope(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return m.ReadDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ReadDesiresForNodePool(
	subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	parent, err := kubeappliercosmosstorage.NodePoolScope(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return m.ReadDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ReadDesiresForSystemAdminCredentialRequest(
	subscriptionID, resourceGroupName, clusterName, credentialRequestName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	parent, err := kubeappliercosmosstorage.CredentialRequestScope(subscriptionID, resourceGroupName, clusterName, credentialRequestName)
	if err != nil {
		return nil, err
	}
	return m.ReadDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ReadDesiresForSystemAdminCredentialRevocation(
	subscriptionID, resourceGroupName, clusterName, revocationName string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	parent, err := kubeappliercosmosstorage.CredentialRevocationScope(subscriptionID, resourceGroupName, clusterName, revocationName)
	if err != nil {
		return nil, err
	}
	return m.ReadDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ApplyDesiresForManagementCluster(
	stampIdentifier string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	parent, err := kubeappliercosmosstorage.ManagementClusterScope(stampIdentifier)
	if err != nil {
		return nil, err
	}
	return m.ApplyDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ReadDesiresForManagementCluster(
	stampIdentifier string,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	parent, err := kubeappliercosmosstorage.ManagementClusterScope(stampIdentifier)
	if err != nil {
		return nil, err
	}
	return m.ReadDesiresFor(parent)
}

func (m *MockKubeApplierDBClient) ApplyDesiresFor(
	parent kubeappliercosmosstorage.DesireScope,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	if parent.ResourceID() == nil {
		return nil, errors.New("desire scope is not initialized")
	}
	return newMockDesireCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire](
		m, parent, kubeapplierapi.ApplyDesireResourceTypeForParent(parent.ResourceID()),
	), nil
}

func (m *MockKubeApplierDBClient) ReadDesiresFor(
	parent kubeappliercosmosstorage.DesireScope,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	if parent.ResourceID() == nil {
		return nil, errors.New("desire scope is not initialized")
	}
	return newMockDesireCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire](
		m, parent, kubeapplierapi.ReadDesireResourceTypeForParent(parent.ResourceID()),
	), nil
}

// newMockDesireCRUD creates a MockResourceCRUD whose path builder delegates to
// the scope's ResourceIDBuilder — the same builder production uses. This keeps
// the mock's ID construction in sync with production for every ancestry family
// (cluster-scoped uses ClusterNestedResourceIDBuilder, stamp-scoped uses
// FleetResourceIDBuilder) without the mock having to know about ancestry at all.
func newMockDesireCRUD[T any, PT coreapi.CosmosMetadataAccessorPtr[T]](
	store corecosmosstoragetesting.MockDocumentStore,
	parent kubeappliercosmosstorage.DesireScope,
	resourceType azcorearm.ResourceType,
) *corecosmosstoragetesting.MockResourceCRUD[T, PT, cosmosstorageutils.GenericDocument[T]] {
	crud := corecosmosstoragetesting.NewMockResourceCRUD[T, PT, cosmosstorageutils.GenericDocument[T]](
		store, parent.ResourceID(), resourceType,
	)
	builder := parent.ResourceIDBuilder()
	parentID := parent.ResourceID()
	crud.MakeResourceIDPath = func(resourceName string) (*azcorearm.ResourceID, error) {
		return builder.BuildResourceID(parentID, resourceType, resourceName)
	}
	crud.GetListPrefix = func() (string, error) {
		id, err := builder.BuildResourceID(parentID, resourceType, "")
		if err != nil {
			return "", err
		}
		return id.String() + "/", nil
	}
	return crud
}

func (m *MockKubeApplierDBClient) Listers() kubeappliercosmosstorage.KubeApplierListers {
	return &mockKubeApplierListers{store: m}
}

func (m *MockKubeApplierDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error) {
	return &mockKubeApplierUntypedCRUD{store: m, parentResourceID: parentResourceID}, nil
}

func (m *MockKubeApplierDBClient) ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	var continuation string
	if options != nil && options.Continuation != nil {
		continuation = *options.Continuation
	}
	items, nextToken, hasNew := m.changeFeed.Read(continuation)
	return corecosmosstoragetesting.BuildMockChangeFeedResponse(items, nextToken, hasNew), nil
}

func (m *MockKubeApplierDBClient) ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return []azcosmos.FeedRange{corecosmosstoragetesting.MockChangeFeedFeedRange}, nil
}

// --- KubeApplierListers (in-memory) ----------------------------------------

type mockKubeApplierListers struct {
	store *MockKubeApplierDBClient
}

var _ kubeappliercosmosstorage.KubeApplierListers = &mockKubeApplierListers{}

func (g *mockKubeApplierListers) ApplyDesires() cosmosstorageutils.GlobalLister[kubeapplierapi.ApplyDesire] {
	return corecosmosstoragetesting.NewMockGlobalLister[kubeapplierapi.ApplyDesire, cosmosstorageutils.GenericDocument[kubeapplierapi.ApplyDesire]](
		g.store,
		[]azcorearm.ResourceType{
			kubeapplierapi.ClusterScopedApplyDesireResourceType,
			kubeapplierapi.NodePoolScopedApplyDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRequestScopedApplyDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRevocationScopedApplyDesireResourceType,
			kubeapplierapi.ManagementClusterScopedApplyDesireResourceType,
		},
	)
}

func (g *mockKubeApplierListers) ReadDesires() cosmosstorageutils.GlobalLister[kubeapplierapi.ReadDesire] {
	return corecosmosstoragetesting.NewMockGlobalLister[kubeapplierapi.ReadDesire, cosmosstorageutils.GenericDocument[kubeapplierapi.ReadDesire]](
		g.store,
		[]azcorearm.ResourceType{
			kubeapplierapi.ClusterScopedReadDesireResourceType,
			kubeapplierapi.NodePoolScopedReadDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRequestScopedReadDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRevocationScopedReadDesireResourceType,
			kubeapplierapi.ManagementClusterScopedReadDesireResourceType,
		},
	)
}

// --- UntypedCRUD (in-memory) ----------------------------------------------

type mockKubeApplierUntypedCRUD struct {
	store            *MockKubeApplierDBClient
	parentResourceID azcorearm.ResourceID
}

var _ cosmosstorageutils.UntypedResourceCRUD = &mockKubeApplierUntypedCRUD{}

func (k *mockKubeApplierUntypedCRUD) Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*cosmosstorageutils.TypedDocument, error) {
	return nil, fmt.Errorf("kube-applier UntypedCRUD.Get is not supported")
}

func (k *mockKubeApplierUntypedCRUD) List(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	return k.listInternal(ctx, true)
}

func (k *mockKubeApplierUntypedCRUD) ListRecursive(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	return k.listInternal(ctx, false)
}

func (k *mockKubeApplierUntypedCRUD) listInternal(ctx context.Context, nonRecursive bool) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	allDocs := k.store.GetAllDocuments()

	prefix := strings.ToLower(k.parentResourceID.String()) + "/"
	requiredSlashes := strings.Count(k.parentResourceID.String(), "/") + 2
	if strings.EqualFold(k.parentResourceID.ResourceType.Type, "resourceGroups") {
		requiredSlashes = strings.Count(k.parentResourceID.String(), "/") + 4
	}

	var ids []string
	var items []*cosmosstorageutils.TypedDocument

	for _, data := range allDocs {
		var typedDoc cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		if typedDoc.ResourceID != nil && !strings.HasPrefix(strings.ToLower(typedDoc.ResourceID.String()), prefix) {
			continue
		}

		if typedDoc.DeletionTimestamp != nil {
			continue
		}

		if nonRecursive && typedDoc.ResourceID != nil {
			if strings.Count(typedDoc.ResourceID.String(), "/") != requiredSlashes {
				continue
			}
		}

		docCopy := typedDoc
		docPointer, err := cosmosstorageutils.CosmosToInternal[cosmosstorageutils.TypedDocument, cosmosstorageutils.TypedDocument](&docCopy)
		if err != nil {
			continue
		}
		ids = append(ids, docPointer.ID)
		items = append(items, docPointer)
	}

	return corecosmosstoragetesting.NewMockIterator(ids, items), nil
}

func (k *mockKubeApplierUntypedCRUD) Delete(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	return fmt.Errorf("kube-applier UntypedCRUD.Delete is not supported")
}

func (k *mockKubeApplierUntypedCRUD) DeleteByCosmosID(ctx context.Context, partitionKey, cosmosID string) error {
	k.store.DeleteDocument(cosmosID)
	return nil
}

func (k *mockKubeApplierUntypedCRUD) Child(resourceType azcorearm.ResourceType, resourceName string) (cosmosstorageutils.UntypedResourceCRUD, error) {
	if len(resourceName) == 0 {
		return nil, fmt.Errorf("resourceName is required")
	}
	parts := []string{k.parentResourceID.String()}
	switch {
	case strings.EqualFold(resourceType.Type, "resourcegroups"):
	case resourceType.Namespace == coreapi.ProviderNamespace && k.parentResourceID.ResourceType.Namespace != coreapi.ProviderNamespace:
		parts = append(parts, "providers", resourceType.Namespace)
	case resourceType.Namespace != coreapi.ProviderNamespace && k.parentResourceID.ResourceType.Namespace == coreapi.ProviderNamespace:
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
	case *kubeapplierapi.ApplyDesire:
		return m.addApplyDesire(ctx, r)
	case *kubeapplierapi.ReadDesire:
		return m.addReadDesire(ctx, r)
	default:
		return fmt.Errorf("unsupported resource type for MockKubeApplierDBClient: %T", resource)
	}
}

func (m *MockKubeApplierDBClient) addApplyDesire(ctx context.Context, d *kubeapplierapi.ApplyDesire) error {
	resourceID := d.GetResourceID()
	if resourceID == nil || resourceID.Parent == nil {
		return fmt.Errorf("desire %v has no parent in its resource ID", resourceID)
	}
	parent, err := kubeappliercosmosstorage.ParseDesireScope(resourceID.Parent)
	if err != nil {
		return err
	}
	crud, err := m.ApplyDesiresFor(parent)
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

func (m *MockKubeApplierDBClient) addReadDesire(ctx context.Context, d *kubeapplierapi.ReadDesire) error {
	resourceID := d.GetResourceID()
	if resourceID == nil || resourceID.Parent == nil {
		return fmt.Errorf("desire %v has no parent in its resource ID", resourceID)
	}
	parent, err := kubeappliercosmosstorage.ParseDesireScope(resourceID.Parent)
	if err != nil {
		return err
	}
	crud, err := m.ReadDesiresFor(parent)
	if err != nil {
		return err
	}
	_, err = crud.Create(ctx, d, nil)
	return err
}

// MockKubeApplierDBClients is the in-memory test double for
// kubeappliercosmosstorage.KubeApplierDBClients. Construction registers a per-management-cluster
// MockKubeApplierDBClient; For() returns the registered client (or nil for
// unknown resourceIDs). Thread-safe.
type MockKubeApplierDBClients struct {
	mu      sync.Mutex
	clients map[string]*MockKubeApplierDBClient // key = lowercased(rid.String())
}

var _ kubeappliercosmosstorage.KubeApplierDBClients = &MockKubeApplierDBClients{}

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

func (c *MockKubeApplierDBClients) For(_ context.Context, managementClusterResourceID *azcorearm.ResourceID) kubeappliercosmosstorage.KubeApplierDBClient {
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

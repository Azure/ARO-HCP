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

package databasetesting

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

// MockResourcesDBClient implements the database.ResourcesDBClient interface for unit testing.
// It stores documents in memory and supports loading from cosmos-record context directories.
type MockResourcesDBClient struct {
	mu sync.RWMutex

	// documents stores all documents keyed by their cosmos ID
	documents map[string]json.RawMessage

	// globalListers is an optional custom global listers implementation for testing
	globalListers database.ResourcesGlobalListers
}

// NewMockResourcesDBClient creates a new mock ResourcesDBClient with empty storage.
func NewMockResourcesDBClient() *MockResourcesDBClient {
	return &MockResourcesDBClient{
		documents: make(map[string]json.RawMessage),
	}
}

// SetResourcesGlobalListers sets a custom global listers implementation for testing.
// This allows tests to provide custom ResourcesGlobalListers that return errors or paginate.
func (m *MockResourcesDBClient) SetResourcesGlobalListers(globalListers database.ResourcesGlobalListers) {
	m.globalListers = globalListers
}

// NewTransaction creates a new mock transaction.
func (m *MockResourcesDBClient) NewTransaction(pk string) database.DBTransaction {
	return newMockTransaction(pk, m)
}

// UntypedCRUD provides access to untyped resource operations.
func (m *MockResourcesDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (database.UntypedResourceCRUD, error) {
	return newMockUntypedCRUD(m, parentResourceID), nil
}

// HCPClusters returns a CRUD interface for HCPCluster resources.
func (m *MockResourcesDBClient) HCPClusters(subscriptionID, resourceGroupName string) database.HCPClusterCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	if len(resourceGroupName) > 0 {
		parts = append(parts,
			"resourceGroups",
			resourceGroupName)
	}
	parentResourceID := api.Must(azcorearm.ParseResourceID(strings.ToLower(path.Join(parts...))))

	return newMockHCPClusterCRUD(m, parentResourceID)
}

// Operations returns a CRUD interface for operation resources.
func (m *MockResourcesDBClient) Operations(subscriptionID string) database.OperationCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	parentResourceID := api.Must(azcorearm.ParseResourceID(path.Join(parts...)))

	return newMockOperationCRUD(m, parentResourceID)
}

// Subscriptions returns a CRUD interface for subscription resources.
func (m *MockResourcesDBClient) Subscriptions() database.SubscriptionCRUD {
	return newMockSubscriptionCRUD(m)
}

// ResourcesGlobalListers returns interfaces for listing all resources of a particular
// type across all partitions. If a custom ResourcesGlobalListers was set via SetResourcesGlobalListers,
// that is returned instead.
func (m *MockResourcesDBClient) ResourcesGlobalListers() database.ResourcesGlobalListers {
	if m.globalListers != nil {
		return m.globalListers
	}
	return &mockResourcesGlobalListers{client: m}
}

// ServiceProviderClusters returns a CRUD interface for service provider cluster resources.
func (m *MockResourcesDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) database.ServiceProviderClusterCRUD {
	clusterResourceID := database.NewClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	return newMockServiceProviderClusterCRUD(m, clusterResourceID)
}

// ServiceProviderNodePools returns a CRUD interface for service provider node pool resources.
func (m *MockResourcesDBClient) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ServiceProviderNodePoolCRUD {
	nodePoolResourceID := database.NewNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return newMockServiceProviderNodePoolCRUD(m, nodePoolResourceID)
}

// LoadFromDirectory loads cosmos-record context data from a directory.
// It reads all JSON files that match the pattern for "load" directories.
func (m *MockResourcesDBClient) LoadFromDirectory(dirPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return filepath.Walk(dirPath, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process JSON files
		if !strings.HasSuffix(strings.ToLower(filePath), ".json") {
			return nil
		}

		// Read the file
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", filePath, err)
		}

		// Parse as TypedDocument to get the ID
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			return fmt.Errorf("failed to unmarshal file %s: %w", filePath, err)
		}

		// Store the document
		if len(typedDoc.ID) != 0 {
			m.documents[strings.ToLower(typedDoc.ID)] = data
		}

		return nil
	})
}

// LoadContent loads a single JSON document into the mock database.
// This implements the ContentLoader interface from integrationutils.
func (m *MockResourcesDBClient) LoadContent(ctx context.Context, content []byte) error {
	// Parse as TypedDocument to get the ID
	var typedDoc database.TypedDocument
	if err := json.Unmarshal(content, &typedDoc); err != nil {
		return fmt.Errorf("failed to unmarshal content: %w", err)
	}

	if len(typedDoc.ID) == 0 {
		return fmt.Errorf("document is missing ID field")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[strings.ToLower(typedDoc.ID)] = content
	return nil
}

// ListAllDocuments returns all documents in the mock database.
// This implements the DocumentLister interface from integrationutils.
func (m *MockResourcesDBClient) ListAllDocuments(ctx context.Context) ([]*database.TypedDocument, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*database.TypedDocument
	for _, data := range m.documents {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			return nil, fmt.Errorf("failed to unmarshal document: %w", err)
		}
		results = append(results, &typedDoc)
	}
	return results, nil
}

// StoreDocument stores a raw JSON document in the mock database.
func (m *MockResourcesDBClient) StoreDocument(cosmosID string, data json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[strings.ToLower(cosmosID)] = data
}

// GetDocument retrieves a raw JSON document from the mock database.
func (m *MockResourcesDBClient) GetDocument(cosmosID string) (json.RawMessage, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.documents[strings.ToLower(cosmosID)]
	return data, ok
}

// DeleteDocument removes a document from the mock database.
func (m *MockResourcesDBClient) DeleteDocument(cosmosID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.documents, strings.ToLower(cosmosID))
}

// ListDocuments returns all documents matching the given resource type and prefix.
func (m *MockResourcesDBClient) ListDocuments(resourceType *azcorearm.ResourceType, prefix string) []json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []json.RawMessage
	for _, data := range m.documents {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		// Check resource type match if specified
		if resourceType != nil {
			if !strings.EqualFold(typedDoc.ResourceType, resourceType.String()) {
				continue
			}
		}

		// Check prefix match if specified.  /subscriptions/ doesn't count because in real storage we pass no prefix for subscriptions
		if len(prefix) != 0 && prefix != "/subscriptions/" {
			if !strings.HasPrefix(strings.ToLower(typedDoc.ResourceID.String()), strings.ToLower(prefix)) {
				continue
			}
		}

		results = append(results, data)
	}

	return results
}

// Clear removes all documents from the mock database.
func (m *MockResourcesDBClient) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents = make(map[string]json.RawMessage)
}

// GetAllDocuments returns a copy of all documents (for testing purposes).
func (m *MockResourcesDBClient) GetAllDocuments() map[string]json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]json.RawMessage, len(m.documents))
	for k, v := range m.documents {
		result[k] = v
	}
	return result
}

var _ database.ResourcesDBClient = &MockResourcesDBClient{}

// mockTransaction implements database.DBTransaction for the mock client.
type mockTransaction struct {
	pk        string
	client    *MockResourcesDBClient
	steps     []mockTransactionStep
	onSuccess []database.DBTransactionCallback
}

type mockTransactionStep struct {
	details database.CosmosDBTransactionStepDetails
	execute func() (string, json.RawMessage, error)
}

func newMockTransaction(pk string, client *MockResourcesDBClient) *mockTransaction {
	return &mockTransaction{
		pk:     strings.ToLower(pk),
		client: client,
	}
}

func (t *mockTransaction) GetPartitionKey() string {
	return t.pk
}

func (t *mockTransaction) AddStep(details database.CosmosDBTransactionStepDetails, stepFn database.CosmosDBTransactionStep) {
	// We need to capture what the step does for the mock
	t.steps = append(t.steps, mockTransactionStep{
		details: details,
		execute: func() (string, json.RawMessage, error) {
			// The real implementation uses TransactionalBatch, but we just execute directly
			// We'll handle this in Execute by storing the details
			return details.CosmosID, nil, nil
		},
	})
}

func (t *mockTransaction) OnSuccess(callback database.DBTransactionCallback) {
	if callback != nil {
		t.onSuccess = append(t.onSuccess, callback)
	}
}

func (t *mockTransaction) Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (database.DBTransactionResult, error) {
	result := &mockTransactionResult{
		items: make(map[string]json.RawMessage),
	}

	// Execute all steps
	for _, step := range t.steps {
		cosmosID, data, err := step.execute()
		if err != nil {
			return nil, err
		}
		if data != nil {
			result.items[cosmosID] = data
		}
	}

	// Call success callbacks
	for _, callback := range t.onSuccess {
		callback(result)
	}

	return result, nil
}

var _ database.DBTransaction = &mockTransaction{}

// mockTransactionResult implements database.DBTransactionResult.
type mockTransactionResult struct {
	items map[string]json.RawMessage
}

func (r *mockTransactionResult) GetItem(cosmosUID string) (any, error) {
	data, ok := r.items[cosmosUID]
	if !ok {
		return nil, database.ErrItemNotFound
	}

	var typedDoc database.TypedDocument
	if err := json.Unmarshal(data, &typedDoc); err != nil {
		return nil, err
	}

	switch strings.ToLower(typedDoc.ResourceType) {
	case strings.ToLower(api.ClusterResourceType.String()):
		var cosmosObj database.HCPCluster
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			return nil, err
		}
		return database.CosmosToInternalCluster(&cosmosObj)
	case strings.ToLower(api.NodePoolResourceType.String()):
		var cosmosObj database.NodePool
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			return nil, err
		}
		return database.CosmosToInternalNodePool(&cosmosObj)
	case strings.ToLower(api.ExternalAuthResourceType.String()):
		var cosmosObj database.ExternalAuth
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			return nil, err
		}
		return database.CosmosToInternalExternalAuth(&cosmosObj)
	default:
		return nil, fmt.Errorf("unknown resource type '%s'", typedDoc.ResourceType)
	}
}

var _ database.DBTransactionResult = &mockTransactionResult{}

// mockIterator implements database.DBClientIterator for in-memory iteration.
type mockIterator[T any] struct {
	items             []*T
	ids               []string
	continuationToken string
	err               error
	index             int
}

func newMockIterator[T any](ids []string, items []*T) *mockIterator[T] {
	return &mockIterator[T]{
		items: items,
		ids:   ids,
		index: 0,
	}
}

func (iter *mockIterator[T]) Items(ctx context.Context) database.DBClientIteratorItem[T] {
	return func(yield func(string, *T) bool) {
		for i, item := range iter.items {
			if !yield(iter.ids[i], item) {
				return
			}
		}
	}
}

func (iter *mockIterator[T]) GetContinuationToken() string {
	return iter.continuationToken
}

func (iter *mockIterator[T]) GetError() error {
	return iter.err
}

var _ database.DBClientIterator[api.HCPOpenShiftCluster] = &mockIterator[api.HCPOpenShiftCluster]{}

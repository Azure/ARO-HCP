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
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

// MockDBClient implements the database.DBClient interface for unit testing.
// It stores documents in memory and supports loading from cosmos-record context directories.
type MockDBClient struct {
	mu sync.RWMutex

	// documents stores all documents keyed by their cosmos ID
	documents map[string]json.RawMessage

	// billing stores billing documents keyed by their ID
	billing map[string]*database.BillingDocument

	// lockClient is an optional mock lock client
	lockClient database.LockClientInterface
}

// NewMockDBClient creates a new mock DBClient with empty storage.
func NewMockDBClient() *MockDBClient {
	lockClient := NewMockLockClient(10)

	return &MockDBClient{
		documents:  make(map[string]json.RawMessage),
		billing:    make(map[string]*database.BillingDocument),
		lockClient: lockClient,
	}
}

// SetLockClient sets a mock lock client for testing.
func (m *MockDBClient) SetLockClient(lockClient database.LockClientInterface) {
	m.lockClient = lockClient
}

// GetLockClient returns the mock lock client, or nil if not set.
func (m *MockDBClient) GetLockClient() database.LockClientInterface {
	return m.lockClient
}

// NewTransaction creates a new mock transaction.
func (m *MockDBClient) NewTransaction(pk string) database.DBTransaction {
	return newMockTransaction(pk, m)
}

// CreateBillingDoc creates a new billing document.
func (m *MockDBClient) CreateBillingDoc(ctx context.Context, doc *database.BillingDocument) error {
	if doc.ResourceID == nil {
		return fmt.Errorf("BillingDocument is missing a ResourceID")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.billing[doc.ID]; exists {
		return &azcore.ResponseError{StatusCode: http.StatusConflict}
	}

	m.billing[doc.ID] = doc
	return nil
}

// PatchBillingDoc patches a billing document.
func (m *MockDBClient) PatchBillingDoc(ctx context.Context, resourceID *azcorearm.ResourceID, ops database.BillingDocumentPatchOperations) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the billing document by resourceID
	var foundID string
	for id, doc := range m.billing {
		if strings.EqualFold(doc.ResourceID.String(), resourceID.String()) && doc.DeletionTime == nil {
			foundID = id
			break
		}
	}

	if len(foundID) == 0 {
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}

	// Apply patch operations would be implemented here
	// For now, just acknowledge the operation
	return nil
}

// UntypedCRUD provides access to untyped resource operations.
func (m *MockDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (database.UntypedResourceCRUD, error) {
	return newMockUntypedCRUD(m, parentResourceID), nil
}

// HCPClusters returns a CRUD interface for HCPCluster resources.
func (m *MockDBClient) HCPClusters(subscriptionID, resourceGroupName string) database.HCPClusterCRUD {
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
func (m *MockDBClient) Operations(subscriptionID string) database.OperationCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	parentResourceID := api.Must(azcorearm.ParseResourceID(path.Join(parts...)))

	return newMockOperationCRUD(m, parentResourceID)
}

// Subscriptions returns a CRUD interface for subscription resources.
func (m *MockDBClient) Subscriptions() database.SubscriptionCRUD {
	return newMockSubscriptionCRUD(m)
}

// GlobalListers returns interfaces for listing all resources of a particular
// type across all partitions.
func (m *MockDBClient) GlobalListers() database.GlobalListers {
	return &mockGlobalListers{client: m}
}

// ServiceProviderClusters returns a CRUD interface for service provider cluster resources.
func (m *MockDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) database.ServiceProviderClusterCRUD {
	clusterResourceID := database.NewClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	return newMockServiceProviderClusterCRUD(m, clusterResourceID)
}

// ServiceProviderNodePools returns a CRUD interface for service provider node pool resources.
func (m *MockDBClient) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ServiceProviderNodePoolCRUD {
	nodePoolResourceID := database.NewNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return newMockServiceProviderNodePoolCRUD(m, nodePoolResourceID)
}

// GetResourcesChangeFeed retrieves a single page of the change feed for the
// "Resources" container using the provided options.
func (m *MockDBClient) GetResourcesChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	return azcosmos.ChangeFeedResponse{}, fmt.Errorf("GetResourcesChangeFeed is not implemented")
}

// GetResourcesFeedRanges returns all the feed ranges for the "Resources" container.
func (m *MockDBClient) GetResourcesFeedRanges() []azcosmos.FeedRange {
	return []azcosmos.FeedRange{}
}

// LoadFromDirectory loads cosmos-record context data from a directory.
// It reads all JSON files that match the pattern for "load" directories.
func (m *MockDBClient) LoadFromDirectory(dirPath string) error {
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
func (m *MockDBClient) LoadContent(ctx context.Context, content []byte) error {
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
func (m *MockDBClient) ListAllDocuments(ctx context.Context) ([]*database.TypedDocument, error) {
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
func (m *MockDBClient) StoreDocument(cosmosID string, data json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[strings.ToLower(cosmosID)] = data
}

// GetDocument retrieves a raw JSON document from the mock database.
func (m *MockDBClient) GetDocument(cosmosID string) (json.RawMessage, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.documents[strings.ToLower(cosmosID)]
	return data, ok
}

// DeleteDocument removes a document from the mock database.
func (m *MockDBClient) DeleteDocument(cosmosID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.documents, strings.ToLower(cosmosID))
}

// ListDocuments returns all documents matching the given resource type and prefix.
func (m *MockDBClient) ListDocuments(resourceType *azcorearm.ResourceType, prefix string) []json.RawMessage {
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

		// Check prefix match if specified
		if len(prefix) != 0 {
			if !strings.HasPrefix(strings.ToLower(typedDoc.ResourceID.String()), strings.ToLower(prefix)) {
				continue
			}
		}

		results = append(results, data)
	}

	return results
}

// Clear removes all documents from the mock database.
func (m *MockDBClient) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents = make(map[string]json.RawMessage)
	m.billing = make(map[string]*database.BillingDocument)
}

// GetAllDocuments returns a copy of all documents (for testing purposes).
func (m *MockDBClient) GetAllDocuments() map[string]json.RawMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]json.RawMessage, len(m.documents))
	for k, v := range m.documents {
		result[k] = v
	}
	return result
}

var _ database.DBClient = &MockDBClient{}

// mockTransaction implements database.DBTransaction for the mock client.
type mockTransaction struct {
	pk        string
	client    *MockDBClient
	steps     []mockTransactionStep
	onSuccess []database.DBTransactionCallback
}

type mockTransactionStep struct {
	details database.CosmosDBTransactionStepDetails
	execute func() (string, json.RawMessage, error)
}

func newMockTransaction(pk string, client *MockDBClient) *mockTransaction {
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

// MockLockClient implements database.LockClientInterface for testing.
type MockLockClient struct {
	defaultTTL time.Duration
	locks      map[string]bool
	mu         sync.Mutex
}

// NewMockLockClient creates a new mock lock client.
func NewMockLockClient(defaultTTL time.Duration) *MockLockClient {
	return &MockLockClient{
		defaultTTL: defaultTTL,
		locks:      make(map[string]bool),
	}
}

func (c *MockLockClient) GetDefaultTimeToLive() time.Duration {
	return c.defaultTTL
}

func (c *MockLockClient) SetRetryAfterHeader(header http.Header) {
	header.Set("Retry-After", fmt.Sprintf("%d", int(c.defaultTTL.Seconds())))
}

func (c *MockLockClient) AcquireLock(ctx context.Context, id string, timeout *time.Duration) (*azcosmos.ItemResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.locks[id] {
		return nil, nil
	}
	c.locks[id] = true
	return &azcosmos.ItemResponse{}, nil
}

func (c *MockLockClient) TryAcquireLock(ctx context.Context, id string) (*azcosmos.ItemResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.locks[id] {
		return nil, nil
	}
	c.locks[id] = true
	return &azcosmos.ItemResponse{}, nil
}

func (c *MockLockClient) HoldLock(ctx context.Context, item *azcosmos.ItemResponse) (context.Context, database.StopHoldLock) {
	cancelCtx, cancel := context.WithCancel(ctx)
	return cancelCtx, func() *azcosmos.ItemResponse {
		cancel()
		return item
	}
}

func (c *MockLockClient) RenewLock(ctx context.Context, item *azcosmos.ItemResponse) (*azcosmos.ItemResponse, error) {
	return item, nil
}

func (c *MockLockClient) ReleaseLock(ctx context.Context, item *azcosmos.ItemResponse) error {
	return nil
}

var _ database.LockClientInterface = &MockLockClient{}

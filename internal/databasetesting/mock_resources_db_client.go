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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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

	// changeFeed records each successful StoreDocument call so the
	// production change-feed consumer can be exercised against the
	// in-memory mock the same way it is against real Cosmos.
	changeFeed mockChangeFeed
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
	var parentResourceID *azcorearm.ResourceID
	if len(resourceGroupName) > 0 {
		parentResourceID = api.Must(api.ToResourceGroupResourceID(subscriptionID, resourceGroupName))
	} else {
		parentResourceID = api.Must(arm.ToSubscriptionResourceID(subscriptionID))
	}

	return newMockHCPClusterCRUD(m, parentResourceID)
}

// Operations returns a CRUD interface for operation resources.
func (m *MockResourcesDBClient) Operations(subscriptionID string) database.OperationCRUD {
	parentResourceID := api.Must(arm.ToSubscriptionResourceID(subscriptionID))

	return newMockOperationCRUD(m, parentResourceID)
}

// Subscriptions returns a CRUD interface for subscription resources.
func (m *MockResourcesDBClient) Subscriptions() database.ResourceCRUD[arm.Subscription, *arm.Subscription] {
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
func (m *MockResourcesDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) database.ResourceCRUD[api.ServiceProviderCluster, *api.ServiceProviderCluster] {
	clusterResourceID := api.Must(api.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))
	return newMockServiceProviderClusterCRUD(m, clusterResourceID)
}

// ServiceProviderNodePools returns a CRUD interface for service provider node pool resources.
func (m *MockResourcesDBClient) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ResourceCRUD[api.ServiceProviderNodePool, *api.ServiceProviderNodePool] {
	nodePoolResourceID := api.Must(api.ToNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName))
	return newMockServiceProviderNodePoolCRUD(m, nodePoolResourceID)
}

// GetChangeFeed reads the in-memory change-feed log. Each
// successful StoreDocument call records a snapshot of the document;
// reads return everything past the position encoded in
// options.Continuation. Deletes are not recorded, which mirrors
// "latest version" mode in real Cosmos DB.
func (m *MockResourcesDBClient) GetChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	var continuation string
	if options != nil && options.Continuation != nil {
		continuation = *options.Continuation
	}
	docs, nextToken, hasNew := m.changeFeed.read(continuation)
	return buildMockChangeFeedResponse(docs, nextToken, hasNew), nil
}

// GetFeedRanges returns the single feed range the mock
// advertises. Real Cosmos may report many ranges; one is enough for
// the in-memory mock because there is no partition-level parallelism
// to model.
func (m *MockResourcesDBClient) GetFeedRanges(ctx context.Context) ([]azcosmos.FeedRange, error) {
	return []azcosmos.FeedRange{mockChangeFeedFeedRange}, nil
}

// SystemAdminCredentialRequests returns a CRUD interface for SystemAdminCredentialRequest resources.
func (m *MockResourcesDBClient) SystemAdminCredentialRequests(subscriptionID, resourceGroupName, clusterName string) database.ResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest] {
	clusterResourceID := api.Must(api.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))
	return newMockResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest, database.GenericDocument[api.SystemAdminCredentialRequest]](m, clusterResourceID, api.SystemAdminCredentialRequestResourceType)
}

// SystemAdminCredentialsRevocations returns a CRUD interface for SystemAdminCredentialsRevocation resources.
func (m *MockResourcesDBClient) SystemAdminCredentialsRevocations(subscriptionID, resourceGroupName, clusterName string) database.ResourceCRUD[api.SystemAdminCredentialsRevocation, *api.SystemAdminCredentialsRevocation] {
	clusterResourceID := api.Must(api.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))
	return newMockResourceCRUD[api.SystemAdminCredentialsRevocation, *api.SystemAdminCredentialsRevocation, database.GenericDocument[api.SystemAdminCredentialsRevocation]](m, clusterResourceID, api.SystemAdminCredentialsRevocationResourceType)
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

// StoreDocument stores a raw JSON document in the mock database and
// appends it to the in-memory change-feed log so that consumers of
// GetChangeFeed see the mutation.
func (m *MockResourcesDBClient) StoreDocument(cosmosID string, data json.RawMessage) {
	m.mu.Lock()
	m.documents[strings.ToLower(cosmosID)] = data
	m.mu.Unlock()
	m.changeFeed.record(data)
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

		// Mirror the production query, which requires IS_DEFINED(c.resourceID);
		// documents without a resourceID are never returned by list.
		if typedDoc.ResourceID == nil {
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
	for step, s := range t.steps {
		cosmosID, data, err := s.execute()
		if err != nil {
			var responseErr *azcore.ResponseError
			if errors.As(err, &responseErr) {
				return nil, database.NewTransactionStepError(step+1, len(t.steps), responseErr.StatusCode)
			}
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
		var cosmosObj database.GenericDocument[api.HCPOpenShiftCluster]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			return nil, err
		}
		return database.CosmosGenericToInternal(&cosmosObj)
	case strings.ToLower(api.NodePoolResourceType.String()):
		var cosmosObj database.GenericDocument[api.HCPOpenShiftClusterNodePool]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			return nil, err
		}
		return database.CosmosGenericToInternal(&cosmosObj)
	case strings.ToLower(api.ExternalAuthResourceType.String()):
		var cosmosObj database.GenericDocument[api.HCPOpenShiftClusterExternalAuth]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			return nil, err
		}
		return database.CosmosGenericToInternal(&cosmosObj)
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

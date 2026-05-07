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
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database"
)

// mockBillingStore holds in-memory billing documents for MockBillingDBClient.
type mockBillingStore struct {
	mu   sync.RWMutex
	docs map[string]*database.BillingDocument
}

func newMockBillingStore() *mockBillingStore {
	return &mockBillingStore{
		docs: make(map[string]*database.BillingDocument),
	}
}

func (s *mockBillingStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = make(map[string]*database.BillingDocument)
}

func (s *mockBillingStore) snapshot() map[string]*database.BillingDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*database.BillingDocument, len(s.docs))
	for k, v := range s.docs {
		out[k] = v
	}
	return out
}

// MockBillingDBClient implements database.BillingDBClient with an isolated in-memory store.
type MockBillingDBClient struct {
	store *mockBillingStore
}

var _ database.BillingDBClient = (*MockBillingDBClient)(nil)

// NewMockBillingDBClient returns a BillingDBClient with its own empty billing document store.
func NewMockBillingDBClient() *MockBillingDBClient {
	return &MockBillingDBClient{store: newMockBillingStore()}
}

// GetBillingDocuments returns a copy of all billing documents (for testing).
func (m *MockBillingDBClient) GetBillingDocuments() map[string]*database.BillingDocument {
	return m.store.snapshot()
}

// Clear removes all billing documents (for testing). It does not affect a MockResourcesDBClient.
func (m *MockBillingDBClient) Clear() {
	m.store.clear()
}

func (m *MockBillingDBClient) BillingDocs(subscriptionID string) database.BillingDocCRUD {
	return newMockBillingDocCRUD(m.store, subscriptionID)
}

func (m *MockBillingDBClient) BillingGlobalListers() database.BillingGlobalListers {
	return &mockBillingDBGlobalListers{store: m.store}
}

type mockBillingDBGlobalListers struct {
	store *mockBillingStore
}

var _ database.BillingGlobalListers = (*mockBillingDBGlobalListers)(nil)

func (g *mockBillingDBGlobalListers) BillingDocs() database.GlobalLister[database.BillingDocument] {
	return &mockBillingGlobalLister{store: g.store}
}

// mockBillingDocCRUD implements database.BillingDocCRUD for testing.
type mockBillingDocCRUD struct {
	store          *mockBillingStore
	subscriptionID string
}

func newMockBillingDocCRUD(store *mockBillingStore, subscriptionID string) *mockBillingDocCRUD {
	return &mockBillingDocCRUD{
		store:          store,
		subscriptionID: subscriptionID,
	}
}

func (m *mockBillingDocCRUD) Create(ctx context.Context, doc *database.BillingDocument) error {
	if doc.ResourceID == nil {
		return fmt.Errorf("BillingDocument is missing a ResourceID")
	}

	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	if _, exists := m.store.docs[doc.ID]; exists {
		return &azcore.ResponseError{StatusCode: http.StatusConflict}
	}

	m.store.docs[doc.ID] = doc
	return nil
}

func (m *mockBillingDocCRUD) GetByID(ctx context.Context, billingDocID string) (*database.BillingDocument, error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	doc, exists := m.store.docs[billingDocID]
	if !exists || doc.SubscriptionID != m.subscriptionID {
		return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}

	return doc, nil
}

func (m *mockBillingDocCRUD) List(ctx context.Context) (database.DBClientIterator[database.BillingDocument], error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	var ids []string
	var items []*database.BillingDocument

	for id, doc := range m.store.docs {
		if strings.EqualFold(doc.SubscriptionID, m.subscriptionID) {
			ids = append(ids, id)
			items = append(items, doc)
		}
	}

	return newMockIterator(ids, items), nil
}

func (m *mockBillingDocCRUD) ListActive(ctx context.Context) ([]*database.BillingDocument, error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	var docs []*database.BillingDocument
	for _, doc := range m.store.docs {
		if strings.EqualFold(doc.SubscriptionID, m.subscriptionID) && doc.DeletionTime == nil {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

func (m *mockBillingDocCRUD) ListActiveForCluster(ctx context.Context, resourceID *azcorearm.ResourceID) ([]*database.BillingDocument, error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	var docs []*database.BillingDocument
	for _, doc := range m.store.docs {
		if strings.EqualFold(doc.ResourceID.String(), resourceID.String()) && doc.DeletionTime == nil {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

func (m *mockBillingDocCRUD) PatchByID(ctx context.Context, billingDocID string, ops database.BillingDocumentPatchOperations) error {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	doc, exists := m.store.docs[billingDocID]
	if !exists || doc.SubscriptionID != m.subscriptionID {
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}

	if doc.DeletionTime == nil {
		now := time.Now()
		doc.DeletionTime = &now
	}
	return nil
}

func (m *mockBillingDocCRUD) PatchByClusterID(ctx context.Context, resourceID *azcorearm.ResourceID, ops database.BillingDocumentPatchOperations) error {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	var foundDocs []*database.BillingDocument
	for _, doc := range m.store.docs {
		if strings.EqualFold(doc.ResourceID.String(), resourceID.String()) && doc.DeletionTime == nil {
			foundDocs = append(foundDocs, doc)
		}
	}

	if len(foundDocs) == 0 {
		return &azcore.ResponseError{
			StatusCode: http.StatusNotFound,
		}
	}

	now := time.Now()
	for _, doc := range foundDocs {
		if doc.DeletionTime == nil {
			doc.DeletionTime = &now
		}
	}
	return nil
}

var _ database.BillingDocCRUD = &mockBillingDocCRUD{}

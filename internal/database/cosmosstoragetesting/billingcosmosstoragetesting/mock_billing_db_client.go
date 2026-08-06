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

package billingcosmosstoragetesting

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

// mockBillingStore holds in-memory billing documents for MockBillingDBClient.
type mockBillingStore struct {
	mu   sync.RWMutex
	docs map[string]*billingcosmosstorage.BillingDocument
}

func newMockBillingStore() *mockBillingStore {
	return &mockBillingStore{
		docs: make(map[string]*billingcosmosstorage.BillingDocument),
	}
}

func (s *mockBillingStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = make(map[string]*billingcosmosstorage.BillingDocument)
}

func (s *mockBillingStore) snapshot() map[string]*billingcosmosstorage.BillingDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*billingcosmosstorage.BillingDocument, len(s.docs))
	for k, v := range s.docs {
		out[k] = v
	}
	return out
}

// MockBillingDBClient implements billingcosmosstorage.BillingDBClient with an isolated in-memory store.
type MockBillingDBClient struct {
	store *mockBillingStore
}

var _ billingcosmosstorage.BillingDBClient = (*MockBillingDBClient)(nil)

// NewMockBillingDBClient returns a BillingDBClient with its own empty billing document store.
func NewMockBillingDBClient() *MockBillingDBClient {
	return &MockBillingDBClient{store: newMockBillingStore()}
}

// GetBillingDocuments returns a copy of all billing documents (for testing).
func (m *MockBillingDBClient) GetBillingDocuments() map[string]*billingcosmosstorage.BillingDocument {
	return m.store.snapshot()
}

// Clear removes all billing documents (for testing). It does not affect a corecosmosstoragetesting.MockResourcesDBClient.
func (m *MockBillingDBClient) Clear() {
	m.store.clear()
}

func (m *MockBillingDBClient) BillingDocs(subscriptionID string) billingcosmosstorage.BillingDocCRUD {
	return newMockBillingDocCRUD(m.store, subscriptionID)
}

func (m *MockBillingDBClient) BillingGlobalListers() billingcosmosstorage.BillingGlobalListers {
	return &mockBillingDBGlobalListers{store: m.store}
}

type mockBillingDBGlobalListers struct {
	store *mockBillingStore
}

var _ billingcosmosstorage.BillingGlobalListers = (*mockBillingDBGlobalListers)(nil)

func (g *mockBillingDBGlobalListers) BillingDocs() cosmosstorageutils.GlobalLister[billingcosmosstorage.BillingDocument] {
	return &mockBillingGlobalLister{store: g.store}
}

// mockBillingDocCRUD implements billingcosmosstorage.BillingDocCRUD for testing.
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

func (m *mockBillingDocCRUD) Create(ctx context.Context, doc *billingcosmosstorage.BillingDocument) error {
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

func (m *mockBillingDocCRUD) GetByID(ctx context.Context, billingDocID string) (*billingcosmosstorage.BillingDocument, error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	doc, exists := m.store.docs[billingDocID]
	if !exists || doc.SubscriptionID != m.subscriptionID {
		return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}

	return doc, nil
}

func (m *mockBillingDocCRUD) List(ctx context.Context) (cosmosstorageutils.DBClientIterator[billingcosmosstorage.BillingDocument], error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	var ids []string
	var items []*billingcosmosstorage.BillingDocument

	for id, doc := range m.store.docs {
		if strings.EqualFold(doc.SubscriptionID, m.subscriptionID) {
			ids = append(ids, id)
			items = append(items, doc)
		}
	}

	return corecosmosstoragetesting.NewMockIterator(ids, items), nil
}

func (m *mockBillingDocCRUD) ListActive(ctx context.Context) ([]*billingcosmosstorage.BillingDocument, error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	var docs []*billingcosmosstorage.BillingDocument
	for _, doc := range m.store.docs {
		if strings.EqualFold(doc.SubscriptionID, m.subscriptionID) && doc.DeletionTime == nil {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

func (m *mockBillingDocCRUD) ListActiveForCluster(ctx context.Context, resourceID *azcorearm.ResourceID) ([]*billingcosmosstorage.BillingDocument, error) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()

	var docs []*billingcosmosstorage.BillingDocument
	for _, doc := range m.store.docs {
		if strings.EqualFold(doc.ResourceID.String(), resourceID.String()) && doc.DeletionTime == nil {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

func (m *mockBillingDocCRUD) PatchByID(ctx context.Context, billingDocID string, ops billingcosmosstorage.BillingDocumentPatchOperations) error {
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

func (m *mockBillingDocCRUD) PatchByClusterID(ctx context.Context, resourceID *azcorearm.ResourceID, ops billingcosmosstorage.BillingDocumentPatchOperations) error {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	var foundDocs []*billingcosmosstorage.BillingDocument
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

var _ billingcosmosstorage.BillingDocCRUD = &mockBillingDocCRUD{}

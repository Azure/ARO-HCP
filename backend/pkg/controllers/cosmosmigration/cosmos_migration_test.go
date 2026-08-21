// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cosmosmigration

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

// stubDoc is a trivial document type used to instantiate replaceWithRetry[T].
// Embedding coreapi.CosmosMetadata makes *stubDoc satisfy coreapi.CosmosMetadataAccessor.
type stubDoc struct {
	coreapi.CosmosMetadata
	Name string
}

// stubCRUD implements cosmosstorageutils.ResourceCRUD[stubDoc, *stubDoc] just enough for
// replaceWithRetry tests. Only Get and Replace are exercised; all other
// methods panic.
type stubCRUD struct {
	getFunc     func(ctx context.Context, resourceID string) (*stubDoc, error)
	replaceFunc func(ctx context.Context, newObj *stubDoc, options *azcosmos.ItemOptions) (*stubDoc, error)
}

func (s *stubCRUD) Get(ctx context.Context, resourceID string) (*stubDoc, error) {
	return s.getFunc(ctx, resourceID)
}

func (s *stubCRUD) Replace(ctx context.Context, newObj *stubDoc, options *azcosmos.ItemOptions) (*stubDoc, error) {
	return s.replaceFunc(ctx, newObj, options)
}

func (s *stubCRUD) GetByID(context.Context, string) (*stubDoc, error) {
	panic("not implemented")
}

func (s *stubCRUD) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[stubDoc], error) {
	panic("not implemented")
}

func (s *stubCRUD) Create(context.Context, *stubDoc, *azcosmos.ItemOptions) (*stubDoc, error) {
	panic("not implemented")
}

func (s *stubCRUD) Delete(context.Context, string) error {
	panic("not implemented")
}

func (s *stubCRUD) AddCreateToTransaction(context.Context, cosmosstorageutils.DBTransaction, *stubDoc, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

func (s *stubCRUD) AddReplaceToTransaction(context.Context, cosmosstorageutils.DBTransaction, *stubDoc, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

// newConflictError returns an azcore.ResponseError that IsConflictError recognises.
func newConflictError() error {
	return &azcore.ResponseError{
		ErrorCode:  "409 Conflict",
		StatusCode: http.StatusConflict,
	}
}

// newPreconditionFailedError returns an azcore.ResponseError that IsPreconditionFailedError recognises.
func newPreconditionFailedError() error {
	return &azcore.ResponseError{
		ErrorCode:  "412 Precondition Failed",
		StatusCode: http.StatusPreconditionFailed,
	}
}

func TestReplaceWithRetry(t *testing.T) {
	ctx := context.Background()
	logger := testr.New(t)
	doc := &stubDoc{Name: "test-resource"}

	tests := []struct {
		name        string
		crud        *stubCRUD
		wantErr     bool
		errContains string
	}{
		{
			name: "success on first attempt",
			crud: &stubCRUD{
				getFunc: func(_ context.Context, _ string) (*stubDoc, error) {
					return doc, nil
				},
				replaceFunc: func(_ context.Context, _ *stubDoc, _ *azcosmos.ItemOptions) (*stubDoc, error) {
					return doc, nil
				},
			},
			wantErr: false,
		},
		{
			name: "not found is silently skipped",
			crud: &stubCRUD{
				getFunc: func(_ context.Context, _ string) (*stubDoc, error) {
					return nil, cosmosstorageutils.NewNotFoundError()
				},
				replaceFunc: func(_ context.Context, _ *stubDoc, _ *azcosmos.ItemOptions) (*stubDoc, error) {
					panic("replace should not be called when Get returns not found")
				},
			},
			wantErr: false,
		},
		{
			name: "conflict then success on retry",
			crud: func() *stubCRUD {
				attempt := 0
				return &stubCRUD{
					getFunc: func(_ context.Context, _ string) (*stubDoc, error) {
						return doc, nil
					},
					replaceFunc: func(_ context.Context, _ *stubDoc, _ *azcosmos.ItemOptions) (*stubDoc, error) {
						attempt++
						if attempt == 1 {
							return nil, newConflictError()
						}
						return doc, nil
					},
				}
			}(),
			wantErr: false,
		},
		{
			name: "precondition failed then success on retry",
			crud: func() *stubCRUD {
				attempt := 0
				return &stubCRUD{
					getFunc: func(_ context.Context, _ string) (*stubDoc, error) {
						return doc, nil
					},
					replaceFunc: func(_ context.Context, _ *stubDoc, _ *azcosmos.ItemOptions) (*stubDoc, error) {
						attempt++
						if attempt == 1 {
							return nil, newPreconditionFailedError()
						}
						return doc, nil
					},
				}
			}(),
			wantErr: false,
		},
		{
			name: "conflict exhausts all retries",
			crud: &stubCRUD{
				getFunc: func(_ context.Context, _ string) (*stubDoc, error) {
					return doc, nil
				},
				replaceFunc: func(_ context.Context, _ *stubDoc, _ *azcosmos.ItemOptions) (*stubDoc, error) {
					return nil, newConflictError()
				},
			},
			wantErr:     true,
			errContains: "after 3 attempts due to conflict/precondition failure",
		},
		{
			name: "non-conflict error propagates immediately",
			crud: func() *stubCRUD {
				replaceCount := 0
				return &stubCRUD{
					getFunc: func(_ context.Context, _ string) (*stubDoc, error) {
						return doc, nil
					},
					replaceFunc: func(_ context.Context, _ *stubDoc, _ *azcosmos.ItemOptions) (*stubDoc, error) {
						replaceCount++
						if replaceCount > 1 {
							t.Fatal("replace should not be retried on non-conflict error")
						}
						return nil, fmt.Errorf("internal server error")
					},
				}
			}(),
			wantErr:     true,
			errContains: "failed to replace",
		},
		{
			name: "non-404 get error propagates",
			crud: &stubCRUD{
				getFunc: func(_ context.Context, _ string) (*stubDoc, error) {
					return nil, fmt.Errorf("connection refused")
				},
				replaceFunc: func(_ context.Context, _ *stubDoc, _ *azcosmos.ItemOptions) (*stubDoc, error) {
					panic("replace should not be called when Get fails")
				},
			},
			wantErr:     true,
			errContains: "failed to get",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := replaceWithRetry(ctx, logger, tt.crud, "test-resource", "test doc")
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// opStubCRUD implements cosmosstorageutils.ResourceCRUD[coreapi.Operation, *coreapi.Operation]
// just enough for the TTL-skip test. Only Get and Replace are exercised.
type opStubCRUD struct {
	getFunc     func(ctx context.Context, resourceID string) (*coreapi.Operation, error)
	replaceFunc func(ctx context.Context, newObj *coreapi.Operation, options *azcosmos.ItemOptions) (*coreapi.Operation, error)
}

func (s *opStubCRUD) Get(ctx context.Context, resourceID string) (*coreapi.Operation, error) {
	return s.getFunc(ctx, resourceID)
}

func (s *opStubCRUD) Replace(ctx context.Context, newObj *coreapi.Operation, options *azcosmos.ItemOptions) (*coreapi.Operation, error) {
	return s.replaceFunc(ctx, newObj, options)
}

func (s *opStubCRUD) GetByID(context.Context, string) (*coreapi.Operation, error) {
	panic("not implemented")
}

func (s *opStubCRUD) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Operation], error) {
	panic("not implemented")
}

func (s *opStubCRUD) Create(context.Context, *coreapi.Operation, *azcosmos.ItemOptions) (*coreapi.Operation, error) {
	panic("not implemented")
}

func (s *opStubCRUD) Delete(context.Context, string) error {
	panic("not implemented")
}

func (s *opStubCRUD) AddCreateToTransaction(context.Context, cosmosstorageutils.DBTransaction, *coreapi.Operation, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

func (s *opStubCRUD) AddReplaceToTransaction(context.Context, cosmosstorageutils.DBTransaction, *coreapi.Operation, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

// TestReplaceWithRetrySkipsTTLDocuments verifies that replaceWithRetry does not
// re-write TTL-governed documents (e.g. operations). Rewriting them would bump
// the document's _ts and reset the Cosmos TTL clock, preventing expiry.
func TestReplaceWithRetrySkipsTTLDocuments(t *testing.T) {
	ctx := context.Background()
	logger := testr.New(t)

	getCalled := false
	crud := &opStubCRUD{
		getFunc: func(_ context.Context, _ string) (*coreapi.Operation, error) {
			getCalled = true
			return &coreapi.Operation{}, nil
		},
		replaceFunc: func(_ context.Context, _ *coreapi.Operation, _ *azcosmos.ItemOptions) (*coreapi.Operation, error) {
			t.Fatal("Replace must not be called for TTL-governed documents; doing so resets their TTL clock")
			return nil, nil
		},
	}

	err := replaceWithRetry(ctx, logger, crud, "test-operation", "operation")
	require.NoError(t, err)
	assert.True(t, getCalled, "expected Get to be called before deciding to skip")
}

func TestSyncOnceSkipsAlreadyCompletedSubscription(t *testing.T) {
	ctx := context.Background()

	controller := &cosmosMigrationController{}

	// Mark a subscription as already completed.
	controller.completedSubscriptions.Store("sub-already-done", struct{}{})

	key := controllerutils.SubscriptionKey{SubscriptionID: "sub-already-done"}

	// SyncOnce should return nil immediately without touching the DB clients
	// (which are nil and would panic if accessed).
	err := controller.SyncOnce(ctx, key)
	require.NoError(t, err)
}

func TestSyncOnceMarksSubscriptionComplete(t *testing.T) {
	// Verify that a new subscription is NOT in completedSubscriptions before
	// SyncOnce is called and is NOT marked complete when SyncOnce returns an error.
	controller := &cosmosMigrationController{
		resourcesDBClient: &errorResourcesDBClient{
			MockResourcesDBClient: corecosmosstoragetesting.NewMockResourcesDBClient(),
		},
	}

	key := controllerutils.SubscriptionKey{SubscriptionID: "sub-new"}

	// completedSubscriptions should not contain sub-new before the call.
	_, loaded := controller.completedSubscriptions.Load("sub-new")
	require.False(t, loaded, "subscription should not be marked complete before SyncOnce")

	// SyncOnce should return an error because the stub Subscriptions() CRUD
	// returns a deterministic error, so the subscription should NOT be
	// marked as completed.
	err := controller.SyncOnce(context.Background(), key)
	require.Error(t, err, "SyncOnce should return an error when migration fails")

	_, loaded = controller.completedSubscriptions.Load("sub-new")
	assert.False(t, loaded, "subscription should not be marked complete after a failed SyncOnce")
}

func TestSyncOnceConcurrentSkip(t *testing.T) {
	// Verify that concurrent calls for the same already-completed subscription
	// are all safely skipped.
	controller := &cosmosMigrationController{}
	controller.completedSubscriptions.Store("sub-concurrent", struct{}{})

	key := controllerutils.SubscriptionKey{SubscriptionID: "sub-concurrent"}

	const goroutines = 10
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- controller.SyncOnce(context.Background(), key)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
}

// errorResourcesDBClient wraps MockResourcesDBClient and overrides
// Subscriptions() to return a CRUD that always errors on Get. This
// forces SyncOnce to return an error without relying on nil-pointer panics.
type errorResourcesDBClient struct {
	*corecosmosstoragetesting.MockResourcesDBClient
}

func (e *errorResourcesDBClient) Subscriptions() cosmosstorageutils.ResourceCRUD[coreapi.Subscription, *coreapi.Subscription] {
	return &errorSubscriptionCRUD{}
}

// errorSubscriptionCRUD implements database.SubscriptionCRUD with Get
// always returning a deterministic, non-retryable error.
type errorSubscriptionCRUD struct{}

func (e *errorSubscriptionCRUD) Get(_ context.Context, _ string) (*coreapi.Subscription, error) {
	return nil, fmt.Errorf("simulated subscription fetch failure")
}

func (e *errorSubscriptionCRUD) GetByID(context.Context, string) (*coreapi.Subscription, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Subscription], error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) Create(context.Context, *coreapi.Subscription, *azcosmos.ItemOptions) (*coreapi.Subscription, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) Replace(context.Context, *coreapi.Subscription, *azcosmos.ItemOptions) (*coreapi.Subscription, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) Delete(context.Context, string) error {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) AddCreateToTransaction(context.Context, cosmosstorageutils.DBTransaction, *coreapi.Subscription, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) AddReplaceToTransaction(context.Context, cosmosstorageutils.DBTransaction, *coreapi.Subscription, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

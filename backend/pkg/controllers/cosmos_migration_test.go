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

package controllers

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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// stubDoc is a trivial document type used to instantiate replaceWithRetry[T].
// Embedding arm.CosmosMetadata makes *stubDoc satisfy arm.CosmosMetadataAccessor.
type stubDoc struct {
	arm.CosmosMetadata
	Name string
}

// stubCRUD implements database.ResourceCRUD[stubDoc, *stubDoc] just enough for
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

func (s *stubCRUD) List(context.Context, *database.DBClientListResourceDocsOptions) (database.DBClientIterator[stubDoc], error) {
	panic("not implemented")
}

func (s *stubCRUD) Create(context.Context, *stubDoc, *azcosmos.ItemOptions) (*stubDoc, error) {
	panic("not implemented")
}

func (s *stubCRUD) Delete(context.Context, string) error {
	panic("not implemented")
}

func (s *stubCRUD) AddCreateToTransaction(context.Context, database.DBTransaction, *stubDoc, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

func (s *stubCRUD) AddReplaceToTransaction(context.Context, database.DBTransaction, *stubDoc, *azcosmos.TransactionalBatchItemOptions) (string, error) {
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
					return nil, database.NewNotFoundError()
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

func TestSyncOnceSkipsAlreadyCompletedSubscription(t *testing.T) {
	ctx := context.Background()

	controller := &cosmosMigrationController{}

	// Mark a subscription as already completed.
	controller.completedSubscriptions.Store("sub-already-done", struct{}{})

	key := controllerutils.SubscriptionKey{SubscriptionID: "sub-already-done"}

	// SyncOnce should return nil immediately without touching the DB clients
	// (which are nil and would panic if accessed).
	_, err := controller.SyncOnce(ctx, key)
	require.NoError(t, err)
}

func TestSyncOnceMarksSubscriptionComplete(t *testing.T) {
	// Verify that a new subscription is NOT in completedSubscriptions before
	// SyncOnce is called and is NOT marked complete when SyncOnce returns an error.
	controller := &cosmosMigrationController{
		resourcesDBClient: &errorResourcesDBClient{
			MockResourcesDBClient: databasetesting.NewMockResourcesDBClient(),
		},
	}

	key := controllerutils.SubscriptionKey{SubscriptionID: "sub-new"}

	// completedSubscriptions should not contain sub-new before the call.
	_, loaded := controller.completedSubscriptions.Load("sub-new")
	require.False(t, loaded, "subscription should not be marked complete before SyncOnce")

	// SyncOnce should return an error because the stub Subscriptions() CRUD
	// returns a deterministic error, so the subscription should NOT be
	// marked as completed.
	_, err := controller.SyncOnce(context.Background(), key)
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
	*databasetesting.MockResourcesDBClient
}

func (e *errorResourcesDBClient) Subscriptions() database.ResourceCRUD[arm.Subscription, *arm.Subscription] {
	return &errorSubscriptionCRUD{}
}

// errorSubscriptionCRUD implements database.SubscriptionCRUD with Get
// always returning a deterministic, non-retryable error.
type errorSubscriptionCRUD struct{}

func (e *errorSubscriptionCRUD) Get(_ context.Context, _ string) (*arm.Subscription, error) {
	return nil, fmt.Errorf("simulated subscription fetch failure")
}

func (e *errorSubscriptionCRUD) GetByID(context.Context, string) (*arm.Subscription, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) List(context.Context, *database.DBClientListResourceDocsOptions) (database.DBClientIterator[arm.Subscription], error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) Create(context.Context, *arm.Subscription, *azcosmos.ItemOptions) (*arm.Subscription, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) Replace(context.Context, *arm.Subscription, *azcosmos.ItemOptions) (*arm.Subscription, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) Delete(context.Context, string) error {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) AddCreateToTransaction(context.Context, database.DBTransaction, *arm.Subscription, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

func (e *errorSubscriptionCRUD) AddReplaceToTransaction(context.Context, database.DBTransaction, *arm.Subscription, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	panic("not implemented")
}

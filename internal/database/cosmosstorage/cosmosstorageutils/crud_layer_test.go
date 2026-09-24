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

package cosmosstorageutils

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
)

type crudLayerFunc func(context.Context, func(context.Context) error) error

func (f crudLayerFunc) Do(ctx context.Context, operation func(context.Context) error) error {
	return f(ctx, operation)
}

type layerContextKey struct{}

type recordingCRUD struct {
	ResourceCRUD[coreapi.Subscription, *coreapi.Subscription]
	calls       int
	lastContext context.Context
	lastID      string
	object      *coreapi.Subscription
	options     *azcosmos.ItemOptions
	listOptions *DBClientListResourceDocsOptions
	iterator    DBClientIterator[coreapi.Subscription]
	err         error
}

func (c *recordingCRUD) record(ctx context.Context) (*coreapi.Subscription, error) {
	c.calls++
	c.lastContext = ctx
	return c.object, c.err
}
func (c *recordingCRUD) Get(ctx context.Context, id string) (*coreapi.Subscription, error) {
	c.lastID = id
	return c.record(ctx)
}
func (c *recordingCRUD) GetByID(ctx context.Context, id string) (*coreapi.Subscription, error) {
	c.lastID = id
	return c.record(ctx)
}
func (c *recordingCRUD) Create(ctx context.Context, obj *coreapi.Subscription, opts *azcosmos.ItemOptions) (*coreapi.Subscription, error) {
	c.object = obj
	c.options = opts
	return c.record(ctx)
}
func (c *recordingCRUD) Replace(ctx context.Context, obj *coreapi.Subscription, opts *azcosmos.ItemOptions) (*coreapi.Subscription, error) {
	c.object = obj
	c.options = opts
	return c.record(ctx)
}
func (c *recordingCRUD) Delete(ctx context.Context, id string) error {
	c.lastID = id
	_, err := c.record(ctx)
	return err
}
func (c *recordingCRUD) List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[coreapi.Subscription], error) {
	c.listOptions = opts
	_, err := c.record(ctx)
	return c.iterator, err
}
func (c *recordingCRUD) AddCreateToTransaction(ctx context.Context, _ DBTransaction, obj *coreapi.Subscription, _ *azcosmos.TransactionalBatchItemOptions) (string, error) {
	c.object = obj
	_, err := c.record(ctx)
	return "transaction-item", err
}
func (c *recordingCRUD) AddReplaceToTransaction(ctx context.Context, _ DBTransaction, obj *coreapi.Subscription, _ *azcosmos.TransactionalBatchItemOptions) (string, error) {
	c.object = obj
	_, err := c.record(ctx)
	return "transaction-item", err
}

func TestResourceCRUDLayer(t *testing.T) {
	for _, method := range []string{"get", "getByID", "create", "replace", "delete", "list"} {
		t.Run(method, func(t *testing.T) {
			obj := &coreapi.Subscription{}
			inner := &recordingCRUD{object: obj, iterator: &fakeIterator[coreapi.Subscription]{}}
			before, after := 0, 0
			layer := crudLayerFunc(func(ctx context.Context, operation func(context.Context) error) error {
				before++
				err := operation(context.WithValue(ctx, layerContextKey{}, "layer"))
				after++
				return err
			})
			crud := NewLayeredResourceCRUD(inner, layer)
			options := &azcosmos.ItemOptions{}
			listOptions := &DBClientListResourceDocsOptions{}
			call := func() error {
				var result *coreapi.Subscription
				var err error
				switch method {
				case "get":
					result, err = crud.Get(t.Context(), "id")
				case "getByID":
					result, err = crud.GetByID(t.Context(), "id")
				case "create":
					result, err = crud.Create(t.Context(), obj, options)
				case "replace":
					result, err = crud.Replace(t.Context(), obj, options)
				case "delete":
					return crud.Delete(t.Context(), "id")
				case "list":
					_, err = crud.List(t.Context(), listOptions)
					return err
				}
				require.Same(t, obj, result)
				return err
			}
			require.NoError(t, call())
			require.Equal(t, 1, inner.calls)
			require.Equal(t, "layer", inner.lastContext.Value(layerContextKey{}))
			switch method {
			case "get", "getByID", "delete":
				require.Equal(t, "id", inner.lastID)
			case "create", "replace":
				require.Same(t, options, inner.options)
			case "list":
				require.Same(t, listOptions, inner.listOptions)
			}
			inner.err = errors.New("inner failure")
			require.ErrorIs(t, call(), inner.err)
			require.Equal(t, 2, before)
			require.Equal(t, 2, after)
		})
	}
}

func TestCRUDLayerRejectionAndTransactions(t *testing.T) {
	inner := &recordingCRUD{}
	blocked := errors.New("blocked")
	crud := NewLayeredResourceCRUD(inner, crudLayerFunc(func(context.Context, func(context.Context) error) error { return blocked }))
	_, err := crud.Get(t.Context(), "id")
	require.ErrorIs(t, err, blocked)
	require.Zero(t, inner.calls)
	for _, add := range []func(context.Context, DBTransaction, *coreapi.Subscription, *azcosmos.TransactionalBatchItemOptions) (string, error){crud.AddCreateToTransaction, crud.AddReplaceToTransaction} {
		id, err := add(t.Context(), nil, &coreapi.Subscription{}, nil)
		require.NoError(t, err)
		require.Equal(t, "transaction-item", id)
		require.Same(t, t.Context(), inner.lastContext)
	}
	require.Equal(t, 2, inner.calls)
}

func TestLayeredValidatingCRUDPreservesValidation(t *testing.T) {
	oldObj, newObj := &coreapi.Subscription{}, &coreapi.Subscription{}
	inner := &recordingCRUD{object: newObj, iterator: &fakeIterator[coreapi.Subscription]{}}
	validate := false
	validating := NewValidatingCRUD(inner,
		func(ctx context.Context, obj *coreapi.Subscription) field.ErrorList {
			require.Equal(t, true, ctx.Value(layerContextKey{}))
			require.Same(t, newObj, obj)
			if !validate {
				return field.ErrorList{field.Forbidden(field.NewPath("test"), "rejected")}
			}
			return nil
		},
		func(ctx context.Context, newValue, oldValue *coreapi.Subscription) field.ErrorList {
			require.Equal(t, true, ctx.Value(layerContextKey{}))
			require.Same(t, newObj, newValue)
			require.Same(t, oldObj, oldValue)
			if !validate {
				return field.ErrorList{field.Forbidden(field.NewPath("test"), "rejected")}
			}
			return nil
		})
	calls := 0
	crud := NewLayeredValidatingResourceCRUD(validating, crudLayerFunc(func(ctx context.Context, operation func(context.Context) error) error {
		calls++
		return operation(context.WithValue(ctx, layerContextKey{}, true))
	}))
	_, err := crud.Create(t.Context(), newObj, nil)
	require.ErrorContains(t, err, "validation failed")
	_, err = crud.Replace(t.Context(), newObj, oldObj, nil)
	require.ErrorContains(t, err, "validation failed")
	require.Zero(t, inner.calls)
	validate = true
	_, err = crud.Create(t.Context(), newObj, nil)
	require.NoError(t, err)
	_, err = crud.Replace(t.Context(), newObj, oldObj, nil)
	require.NoError(t, err)
	_, err = crud.Get(t.Context(), "id")
	require.NoError(t, err)
	_, err = crud.GetByID(t.Context(), "id")
	require.NoError(t, err)
	require.NoError(t, crud.Delete(t.Context(), "id"))
	_, err = crud.List(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, 8, calls)
	require.Equal(t, 6, inner.calls)
}

type layerTestTransport func(*http.Request) (*http.Response, error)

func (f layerTestTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestRateLimitedCRUDListPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, err := cosmosratelimit.NewTokenBucket("test", 1, 1)
		require.NoError(t, err)
		pages := 0
		start := time.Now()
		pipeline := runtime.NewPipeline("test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
			PerRetryPolicies: []policy.Policy{cosmosratelimit.NewPolicy()},
			Transport: layerTestTransport(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "iteration", req.Context().Value(layerContextKey{}), "use the Items context")
				require.Equal(t, time.Duration(pages*2)*time.Second, time.Since(start))
				pages++
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: req}
				resp.Header.Set("x-ms-request-charge", "3")
				return resp, nil
			}),
		})
		pager := runtime.NewPager(runtime.PagingHandler[azcosmos.QueryItemsResponse]{
			More: func(azcosmos.QueryItemsResponse) bool { return pages < 2 },
			Fetcher: func(ctx context.Context, _ *azcosmos.QueryItemsResponse) (azcosmos.QueryItemsResponse, error) {
				req, err := runtime.NewRequest(ctx, http.MethodPost, "https://cosmos.test")
				if err != nil {
					return azcosmos.QueryItemsResponse{}, err
				}
				response, err := pipeline.Do(req)
				if response != nil {
					require.NoError(t, response.Body.Close())
				}
				return azcosmos.QueryItemsResponse{}, err
			},
		})
		inner := &recordingCRUD{iterator: NewQueryResourcesIterator[coreapi.Subscription, GenericDocument[coreapi.Subscription]](pager)}
		// Exercise both layer adapters with the same bucket; it must not be charged twice.
		validating := NewValidatingCRUD(NewLayeredResourceCRUD(inner, bucket), nil, nil)
		crud := NewLayeredValidatingResourceCRUD(validating, bucket)
		listCtx, cancel := context.WithCancel(t.Context())
		iterator, err := crud.List(listCtx, nil)
		require.NoError(t, err)
		cancel()
		require.Zero(t, pages, "List must remain lazy")
		for range iterator.Items(context.WithValue(t.Context(), layerContextKey{}, "iteration")) {
		}
		require.NoError(t, iterator.GetError())
		require.Equal(t, 2, pages)
		start = time.Now()
		require.NoError(t, bucket.Wait(t.Context()))
		require.Equal(t, 3*time.Second, time.Since(start), "both pages are charged exactly once")
	})
}

func TestLayeredIteratorErrorsAndEarlyStop(t *testing.T) {
	failure := errors.New("iterator failure")
	inner := &fakeIterator[coreapi.Subscription]{
		entries: []fakeIteratorEntry[coreapi.Subscription]{{id: "one"}, {id: "two"}},
		token:   "next-page", err: failure,
	}
	iterator := &layeredIterator[coreapi.Subscription]{DBClientIterator: inner, layer: crudLayerFunc(func(ctx context.Context, op func(context.Context) error) error { return op(ctx) })}
	yielded := 0
	for range iterator.Items(t.Context()) {
		yielded++
		break
	}
	require.Equal(t, 1, yielded)
	require.Equal(t, "next-page", iterator.GetContinuationToken())
	require.ErrorIs(t, iterator.GetError(), failure)
	blocked := errors.New("blocked")
	iterator.layer = crudLayerFunc(func(context.Context, func(context.Context) error) error { return blocked })
	for range iterator.Items(t.Context()) {
		t.Fatal("layer rejected iteration")
	}
	require.ErrorIs(t, iterator.GetError(), blocked)
}

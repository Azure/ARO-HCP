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

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

func TestHealthzStartup(t *testing.T) {
	cosmosDown := errors.New("403 Forbidden: request blocked by auth")
	for _, tc := range []struct {
		name            string
		listErr         error
		queryErr        error
		hasItem         bool
		waitForDeadline bool
		want            int
	}{
		{name: "empty result", want: http.StatusOK},
		{name: "nonempty result", hasItem: true, want: http.StatusOK},
		{name: "query forbidden", queryErr: cosmosDown, want: http.StatusServiceUnavailable},
		{name: "list fails", listErr: cosmosDown, want: http.StatusServiceUnavailable},
		{name: "query times out", waitForDeadline: true, want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			iter := &startupQueryIterator{queryErr: tc.queryErr, hasItem: tc.hasItem, waitForDeadline: tc.waitForDeadline}
			db := startupDBClient{list: func(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Subscription], error) {
				if options == nil || options.PageSizeHint == nil || *options.PageSizeHint != 1 {
					t.Fatal("startup query must request a single page with page size 1")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("startup query must have a deadline of at most 5 seconds")
				}
				return iter, tc.listErr
			}}
			registry := prometheus.NewRegistry()
			api := NewAdminAPI(testr.New(t), "test", nil, nil,
				db, nil, nil, nil, nil, nil, nil, nil, nil,
				time.Minute, time.Hour, nil, registry, registry, nil)

			for _, endpoint := range []struct {
				name    string
				handler http.Handler
			}{
				{name: "metrics", handler: api.metricsServer.Handler},
				{name: "api", handler: api.server.Handler},
			} {
				// Missing startup routing must fail here rather than silently use a
				// process-only health handler. Both listeners bypass admin middleware.
				iter.queried = false
				recorder := httptest.NewRecorder()
				endpoint.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz/startup", nil))
				if recorder.Code != tc.want {
					t.Fatalf("%s startup: got %d, want %d (body %q)", endpoint.name, recorder.Code, tc.want, recorder.Body.String())
				}
				if tc.listErr == nil && !iter.queried {
					t.Fatal("startup returned without executing the Cosmos query")
				}
				for _, path := range []string{"/healthz/live", "/healthz/ready"} {
					recorder = httptest.NewRecorder()
					endpoint.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
					if recorder.Code != http.StatusOK {
						t.Errorf("%s %s must remain healthy independently of Cosmos: got %d", endpoint.name, path, recorder.Code)
					}
				}
			}

			// A transient failure must not prevent a later successful startup probe.
			tc.listErr = nil
			iter.queryErr = nil
			iter.waitForDeadline = false
			recorder := httptest.NewRecorder()
			api.metricsServer.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz/startup", nil))
			if recorder.Code != http.StatusOK {
				t.Errorf("startup after recovery: got %d, want 200", recorder.Code)
			}
		})
	}
}

// These stubs replace the external Cosmos query. Unused database operations
// panic through the embedded interfaces so accidental extra queries fail loudly.
type startupDBClient struct {
	corecosmosstorage.ResourcesDBClient
	list startupSubscriptionLister
}

func (c startupDBClient) ResourcesGlobalListers() corecosmosstorage.ResourcesGlobalListers {
	return startupGlobalListers{list: c.list}
}

type startupGlobalListers struct {
	corecosmosstorage.ResourcesGlobalListers
	list startupSubscriptionLister
}

func (l startupGlobalListers) Subscriptions() cosmosstorageutils.GlobalLister[coreapi.Subscription] {
	return l.list
}

type startupSubscriptionLister func(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Subscription], error)

func (l startupSubscriptionLister) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Subscription], error) {
	return l(ctx, options)
}

type startupQueryIterator struct {
	queryErr        error
	hasItem         bool
	waitForDeadline bool
	queried         bool
	err             error
}

func (i *startupQueryIterator) Items(ctx context.Context) cosmosstorageutils.DBClientIteratorItem[coreapi.Subscription] {
	return func(yield func(string, *coreapi.Subscription) bool) {
		i.queried = true
		i.err = i.queryErr
		if i.waitForDeadline {
			<-ctx.Done()
			i.err = ctx.Err()
		}
		if i.err == nil && i.hasItem {
			yield("subscription", &coreapi.Subscription{})
		}
	}
}

func (i *startupQueryIterator) GetError() error              { return i.err }
func (i *startupQueryIterator) GetContinuationToken() string { return "" }

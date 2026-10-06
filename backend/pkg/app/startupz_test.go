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

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/client-go/tools/leaderelection"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestBackendHealthzMuxProbes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		listErr      error
		iteratorErr  error
		wantStartupz int
	}{
		{
			name:         "empty Cosmos query succeeds",
			wantStartupz: http.StatusOK,
		},
		{
			name:         "Cosmos list failure is unavailable",
			listErr:      errors.New("Cosmos list failed"),
			wantStartupz: http.StatusServiceUnavailable,
		},
		{
			name:         "Cosmos iterator failure is unavailable",
			iteratorErr:  errors.New("Cosmos iteration failed"),
			wantStartupz: http.StatusServiceUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			globalListers := resourcesDBClient.ResourcesGlobalListers()
			resourcesDBClient.SetResourcesGlobalListers(startupProbeResourcesGlobalListers{
				ResourcesGlobalListers: globalListers,
				subscriptions: startupProbeLister[coreapi.Subscription]{
					t:           t,
					delegate:    globalListers.Subscriptions(),
					listErr:     tc.listErr,
					iteratorErr: tc.iteratorErr,
				},
			})

			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			mux := (&backendHealthzServer{
				metricsRegisterer: prometheus.NewRegistry(),
				electionChecker:   leaderelection.NewLeaderHealthzAdaptor(20 * time.Second),
				resourcesDBClient: resourcesDBClient,
			}).handler(ctx)

			for _, probe := range []struct {
				path string
				want int
			}{
				{path: "/healthz", want: http.StatusOK},
				{path: "/startupz", want: tc.wantStartupz},
			} {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, probe.path, nil).WithContext(ctx)
				mux.ServeHTTP(recorder, request)
				if recorder.Code != probe.want {
					t.Errorf("GET %s: got status %d, want %d (body %q)", probe.path, recorder.Code, probe.want, recorder.Body.String())
				}
			}
		})
	}
}

type startupProbeResourcesGlobalListers struct {
	corecosmosstorage.ResourcesGlobalListers
	subscriptions cosmosstorageutils.GlobalLister[coreapi.Subscription]
}

func (l startupProbeResourcesGlobalListers) Subscriptions() cosmosstorageutils.GlobalLister[coreapi.Subscription] {
	return l.subscriptions
}

type startupProbeLister[T any] struct {
	t           *testing.T
	delegate    cosmosstorageutils.GlobalLister[T]
	listErr     error
	iteratorErr error
}

func (l startupProbeLister[T]) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[T], error) {
	l.t.Helper()
	if options == nil || options.PageSizeHint == nil || *options.PageSizeHint != 1 {
		l.t.Fatalf("startup probe PageSizeHint = %v, want 1", options)
	}
	if l.listErr != nil {
		return nil, l.listErr
	}
	iterator, err := l.delegate.List(ctx, options)
	if err != nil {
		return nil, err
	}
	if l.iteratorErr != nil {
		return startupProbeIterator[T]{DBClientIterator: iterator, err: l.iteratorErr}, nil
	}
	return iterator, nil
}

type startupProbeIterator[T any] struct {
	cosmosstorageutils.DBClientIterator[T]
	err error
}

func (i startupProbeIterator[T]) GetError() error { return i.err }

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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
)

func TestHealthzMuxProbes(t *testing.T) {
	// A Cosmos 403 surfaces when the query pager is pulled, not when the lister
	// is constructed, so the stub reports it through GetError().
	cosmosDown := errors.New("403 Forbidden: request blocked by auth")

	for _, tc := range []struct {
		name        string
		dbClient    kubeappliercosmosstorage.KubeApplierDBClient
		wantHealthz int
		wantReadyz  int
	}{
		{
			name:        "cosmos reachable",
			dbClient:    kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient(),
			wantHealthz: http.StatusOK,
			wantReadyz:  http.StatusOK,
		},
		{
			name:        "cosmos unreachable",
			dbClient:    failingDBClient{err: cosmosDown},
			wantHealthz: http.StatusOK, // process health must not follow Cosmos
			wantReadyz:  http.StatusServiceUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			o := &Options{
				KubeApplierDBClient: tc.dbClient,
				MetricsRegisterer:   registry,
				MetricsGatherer:     registry,
			}
			mux := o.newHealthzMux(testr.New(t), leaderelection.NewLeaderHealthzAdaptor(20*time.Second))

			for path, want := range map[string]int{"/healthz": tc.wantHealthz, "/readyz": tc.wantReadyz} {
				recorder := httptest.NewRecorder()
				mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				if recorder.Code != want {
					t.Errorf("GET %s: got status %d, want %d (body %q)", path, recorder.Code, want, recorder.Body.String())
				}
			}
		})
	}
}

// failingDBClient is a KubeApplierDBClient whose ApplyDesires query fails the
// way a Cosmos data-plane 403 does: the lister is built fine, the error comes
// out of the iterator. Only Listers() is exercised; the embedded nil interface
// panics loudly if anything else is called.
type failingDBClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient
	err error
}

func (c failingDBClient) Listers() kubeappliercosmosstorage.KubeApplierListers {
	return failingListers{err: c.err}
}

type failingListers struct {
	kubeappliercosmosstorage.KubeApplierListers
	err error
}

func (l failingListers) ApplyDesires() cosmosstorageutils.GlobalLister[kubeapplierapi.ApplyDesire] {
	return failingLister{err: l.err}
}

type failingLister struct {
	err error
}

func (l failingLister) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[kubeapplierapi.ApplyDesire], error) {
	return failingIterator(l), nil
}

type failingIterator struct {
	err error
}

func (i failingIterator) Items(context.Context) cosmosstorageutils.DBClientIteratorItem[kubeapplierapi.ApplyDesire] {
	return func(yield func(string, *kubeapplierapi.ApplyDesire) bool) {}
}

func (i failingIterator) GetContinuationToken() string { return "" }

func (i failingIterator) GetError() error { return i.err }

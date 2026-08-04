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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestHealthzMuxProbes(t *testing.T) {
	// A Cosmos 403 surfaces when the query pager is pulled, not when the lister
	// is constructed, so the stub reports it through GetError().
	cosmosDown := errors.New("403 Forbidden: request blocked by auth")

	for _, tc := range []struct {
		name         string
		dbClient     database.KubeApplierDBClient
		wantHealthz  int
		wantReadyz   int
		wantGaugeSet float64
	}{
		{
			name:         "cosmos reachable",
			dbClient:     databasetesting.NewMockKubeApplierDBClient(),
			wantHealthz:  http.StatusOK,
			wantReadyz:   http.StatusOK,
			wantGaugeSet: 1,
		},
		{
			name:         "cosmos unreachable",
			dbClient:     failingDBClient{err: cosmosDown},
			wantHealthz:  http.StatusOK, // process health must not follow Cosmos
			wantReadyz:   http.StatusServiceUnavailable,
			wantGaugeSet: 0,
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

			if got := gaugeValue(t, registry, "kube_applier_cosmos_health"); got != tc.wantGaugeSet {
				t.Errorf("kube_applier_cosmos_health: got %v, want %v", got, tc.wantGaugeSet)
			}
		})
	}
}

func gaugeValue(t *testing.T, gatherer prometheus.Gatherer, name string) float64 {
	t.Helper()
	families, err := gatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("metric %q was not registered", name)
	return 0
}

// failingDBClient is a KubeApplierDBClient whose ApplyDesires query fails the
// way a Cosmos data-plane 403 does: the lister is built fine, the error comes
// out of the iterator. Only Listers() is exercised; the embedded nil interface
// panics loudly if anything else is called.
type failingDBClient struct {
	database.KubeApplierDBClient
	err error
}

func (c failingDBClient) Listers() database.KubeApplierListers {
	return failingListers{err: c.err}
}

type failingListers struct {
	database.KubeApplierListers
	err error
}

func (l failingListers) ApplyDesires() database.GlobalLister[kubeapplier.ApplyDesire] {
	return failingLister{err: l.err}
}

type failingLister struct {
	err error
}

func (l failingLister) List(context.Context, *database.DBClientListResourceDocsOptions) (database.DBClientIterator[kubeapplier.ApplyDesire], error) {
	return failingIterator(l), nil
}

type failingIterator struct {
	err error
}

func (i failingIterator) Items(context.Context) database.DBClientIteratorItem[kubeapplier.ApplyDesire] {
	return func(yield func(string, *kubeapplier.ApplyDesire) bool) {}
}

func (i failingIterator) GetContinuationToken() string { return "" }

func (i failingIterator) GetError() error { return i.err }

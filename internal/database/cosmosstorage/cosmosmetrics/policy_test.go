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

package cosmosmetrics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testPipeline(p policy.Policy, transport testTransport, retries int32) runtime.Pipeline {
	return runtime.NewPipeline("cosmosmetrics-test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
		PerRetryPolicies: []policy.Policy{p},
		Transport:        transport,
		Telemetry:        policy.TelemetryOptions{Disabled: true},
		Retry:            policy.RetryOptions{MaxRetries: retries, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Nanosecond},
	})
}

func testCounter(t *testing.T) (*requestChargePolicy, *prometheus.Registry) {
	t.Helper()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cosmos_request_units_total",
		Help: "Test request units.",
	}, []string{"source_kind", "source", "cosmosdb_container", "operation", "status_code"})
	registry := prometheus.NewRegistry()
	registry.MustRegister(counter)
	return &requestChargePolicy{counter: counter}, registry
}

// Gather the public metric families rather than reaching into counter children.
func gatheredCharges(t *testing.T, gatherer prometheus.Gatherer) map[[5]string]float64 {
	t.Helper()
	families, err := gatherer.Gather()
	require.NoError(t, err)
	charges := map[[5]string]float64{}
	for _, family := range families {
		if family.GetName() != "cosmos_request_units_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			require.Len(t, labels, 5)
			key := [5]string{labels["source_kind"], labels["source"], labels["cosmosdb_container"], labels["operation"], labels["status_code"]}
			charges[key] = metric.GetCounter().GetValue()
		}
	}
	return charges
}

func chargedResponse(status int, charge string) *http.Response {
	response := &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("response body"))}
	if charge != "" {
		response.Header.Set("x-ms-request-charge", charge)
	}
	return response
}

func TestRequestChargeAttribution(t *testing.T) {
	t.Parallel()
	controller := utils.ContextWithControllerName(context.Background(), "ReconcileClusters")
	tests := []struct {
		name   string
		ctx    context.Context
		kind   string
		source string
	}{
		{"no attribution", context.Background(), "unattributed", "unknown"},
		{"controller", controller, "controller", "ReconcileClusters"},
		{"informer", ContextWithInformerName(context.Background(), "ClusterInformer"), "informer", "ClusterInformer"},
		{"informer precedes controller", ContextWithInformerName(controller, "ClusterInformer"), "informer", "ClusterInformer"},
		{"empty informer falls back", ContextWithInformerName(controller, ""), "controller", "ReconcileClusters"},
		{"empty names are unattributed", ContextWithInformerName(utils.ContextWithControllerName(context.Background(), ""), ""), "unattributed", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, registry := testCounter(t)
			pipeline := testPipeline(p, func(req *http.Request) (*http.Response, error) {
				// Attribution stays in context, not in headers sent to Cosmos DB.
				for _, values := range req.Header {
					for _, value := range values {
						require.NotContains(t, value, "ReconcileClusters")
						require.NotContains(t, value, "ClusterInformer")
					}
				}
				return chargedResponse(http.StatusOK, "2.5"), nil
			}, -1)
			req, err := runtime.NewRequest(tt.ctx, http.MethodGet, "https://cosmos.test/dbs/private-db/colls/Resources/docs/private-item")
			require.NoError(t, err)
			_, err = pipeline.Do(req)
			require.NoError(t, err)
			require.Equal(t, map[[5]string]float64{{tt.kind, tt.source, "Resources", "read", "200"}: 2.5}, gatheredCharges(t, registry))
		})
	}
}

func TestRequestChargeConcurrentControllers(t *testing.T) {
	t.Parallel()
	p, registry := testCounter(t)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	pipeline := testPipeline(p, func(*http.Request) (*http.Response, error) {
		arrived <- struct{}{}
		<-release
		return chargedResponse(http.StatusOK, "1.25"), nil
	}, -1)
	errors := make(chan error, 2)
	for _, name := range []string{"ClusterController", "NodePoolController"} {
		req, err := runtime.NewRequest(utils.ContextWithControllerName(context.Background(), name), http.MethodGet, "https://cosmos.test/dbs/db/colls/Resources/docs/id")
		require.NoError(t, err)
		go func() {
			_, err := pipeline.Do(req)
			errors <- err
		}()
	}
	<-arrived
	<-arrived
	close(release)
	require.NoError(t, <-errors)
	require.NoError(t, <-errors)
	require.Equal(t, map[[5]string]float64{
		{"controller", "ClusterController", "Resources", "read", "200"}:  1.25,
		{"controller", "NodePoolController", "Resources", "read", "200"}: 1.25,
	}, gatheredCharges(t, registry))
}

func TestRequestChargeCountsEveryRetryOnce(t *testing.T) {
	t.Parallel()
	p, registry := testCounter(t)
	attempts := 0
	final := chargedResponse(http.StatusOK, "4.5")
	pipeline := testPipeline(p, func(*http.Request) (*http.Response, error) {
		attempts++
		switch attempts {
		case 1:
			return chargedResponse(http.StatusTooManyRequests, "1.25"), nil
		case 2:
			return chargedResponse(http.StatusTooManyRequests, "1.5"), nil
		default:
			return final, nil
		}
	}, 2)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://cosmos.test/dbs/db/colls/Billing/docs/id")
	require.NoError(t, err)
	resp, err := pipeline.Do(req)
	require.NoError(t, err)
	require.Same(t, final, resp)
	require.Equal(t, 3, attempts)
	require.Equal(t, map[[5]string]float64{
		{"unattributed", "unknown", "Billing", "read", "429"}: 2.75,
		{"unattributed", "unknown", "Billing", "read", "200"}: 4.5,
	}, gatheredCharges(t, registry))
}

type failingBody struct{ err error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.err }
func (b failingBody) Close() error             { return nil }

func TestRequestChargeResponseAndErrorPassThrough(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("transport or response body failed")
	tests := []struct {
		name         string
		response     *http.Response
		transportErr error
		bodyErr      bool
		wantCharge   float64 // A negative value means no metric series.
	}{
		{"success", chargedResponse(200, "2.5"), nil, false, 2.5},
		{"zero", chargedResponse(200, "0"), nil, false, 0},
		{"http error", chargedResponse(403, "2.5"), nil, false, 2.5},
		{"not modified", chargedResponse(304, "2.5"), nil, false, 2.5},
		{"body error with charge", chargedResponse(200, "2.5"), nil, true, 2.5},
		{"nil response", nil, sentinel, false, -1},
		{"missing", chargedResponse(200, ""), nil, false, -1},
		{"malformed", chargedResponse(200, "not a number"), nil, false, -1},
		{"nan", chargedResponse(200, "NaN"), nil, false, -1},
		{"positive infinity", chargedResponse(200, "+Inf"), nil, false, -1},
		{"negative infinity", chargedResponse(200, "-Inf"), nil, false, -1},
		{"overflow", chargedResponse(200, "1e999"), nil, false, -1},
		{"negative", chargedResponse(200, "-0.25"), nil, false, -1},
		{"invalid charge and body error", chargedResponse(200, "NaN"), nil, true, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, registry := testCounter(t)
			if tt.bodyErr {
				tt.response.Body = failingBody{err: sentinel}
			}
			var originalHeaders http.Header
			if tt.response != nil {
				originalHeaders = tt.response.Header.Clone()
			}
			pipeline := testPipeline(p, func(*http.Request) (*http.Response, error) {
				return tt.response, tt.transportErr
			}, -1)
			req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://cosmos.test/dbs/db/colls/Fleet/docs/id")
			require.NoError(t, err)
			resp, err := pipeline.Do(req)
			if tt.transportErr != nil || tt.bodyErr {
				require.Same(t, sentinel, err)
			} else {
				require.NoError(t, err)
			}
			if tt.response == nil {
				require.Nil(t, resp)
			} else {
				require.Same(t, tt.response, resp)
				require.Equal(t, originalHeaders, resp.Header)
				if !tt.bodyErr {
					body, err := runtime.Payload(resp)
					require.NoError(t, err)
					require.Equal(t, "response body", string(body))
				}
			}
			want := map[[5]string]float64{}
			if tt.wantCharge >= 0 {
				want[[5]string{"unattributed", "unknown", "Fleet", "read", strconv.Itoa(tt.response.StatusCode)}] = tt.wantCharge
			}
			require.Equal(t, want, gatheredCharges(t, registry))
		})
	}
}

func TestRequestChargeClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		method    string
		path      string
		headers   map[string]string
		container string
		operation string
	}{
		{"read escaped id", "GET", "/dbs/db/colls/Resources/docs/colls%2FBilling%2Fdocs%2Fsecret", nil, "Resources", "read"},
		{"create", "POST", "/dbs/db/colls/Billing/docs", nil, "Billing", "create"},
		{"upsert", "POST", "/dbs/db/colls/Fleet/docs", map[string]string{"x-ms-documentdb-is-upsert": "true"}, "Fleet", "upsert"},
		{"replace", "PUT", "/dbs/db/colls/Locks/docs/id", nil, "Locks", "replace"},
		{"patch", "PATCH", "/dbs/db/colls/Manifests-MC-stamp/docs/id", nil, "Manifests-MC-stamp", "patch"},
		{"delete", "DELETE", "/dbs/db/colls/Resources/docs/id", nil, "Resources", "delete"},
		{"sdk query", "POST", "/dbs/db/colls/Resources/docs", map[string]string{"x-ms-documentdb-query": "True"}, "Resources", "query"},
		{"query plan before query", "POST", "/dbs/db/colls/Resources/docs", map[string]string{"x-ms-cosmos-is-query-plan-request": "True", "x-ms-documentdb-query": "True"}, "Resources", "query_plan"},
		{"batch", "POST", "/dbs/db/colls/Resources/docs", map[string]string{"x-ms-cosmos-is-batch-request": "True"}, "Resources", "batch"},
		{"change feed", "GET", "/dbs/db/colls/Resources/docs", map[string]string{"A-IM": "Incremental feed"}, "Resources", "change_feed"},
		{"ranges before change feed", "GET", "/dbs/db/colls/Resources/pkranges", map[string]string{"A-IM": "Incremental feed"}, "Resources", "feed_ranges"},
		{"container metadata", "GET", "/dbs/db/colls/Resources/", nil, "Resources", "metadata"},
		{"database metadata", "GET", "/dbs/private-db", nil, "unknown", "metadata"},
		{"account metadata", "GET", "/", nil, "unknown", "metadata"},
		{"container listing", "GET", "/dbs/db/colls", nil, "unknown", "metadata"},
		{"offer metadata", "GET", "/offers/private-offer", nil, "unknown", "metadata"},
		{"management cluster container", "GET", "/dbs/db/colls/Manifests-MC-private-cluster/docs/id", nil, "Manifests-MC-private-cluster", "read"},
		{"encoded container", "GET", "/dbs/db/colls/Manifests-MC-stamp%20one/docs/id", nil, "Manifests-MC-stamp one", "read"},
		{"container casing preserved", "GET", "/dbs/db/colls/rEsOuRcEs/docs/id", nil, "rEsOuRcEs", "read"},
		{"custom container", "GET", "/dbs/db/colls/private-cluster/docs/id", nil, "private-cluster", "read"},
		{"id is not container", "GET", "/dbs/db/colls/private/docs/Resources", nil, "private", "read"},
		{"query string is not route", "GET", "/dbs/db/colls/private/docs/id?colls=Resources", nil, "private", "read"},
		{"extra segments", "GET", "/dbs/db/colls/Resources/docs/id/colls/Billing", nil, "unknown", "unknown"},
		{"unexpected root", "GET", "/private/colls/Resources/docs/id", nil, "unknown", "unknown"},
		{"missing container", "GET", "/dbs/db/colls//docs/id", nil, "unknown", "unknown"},
		{"unknown collection route", "GET", "/dbs/db/colls/Resources/private-operation", nil, "Resources", "unknown"},
		{"feed is not point read", "GET", "/dbs/db/colls/Resources/docs", nil, "Resources", "unknown"},
		{"query header false", "POST", "/dbs/db/colls/Resources/docs", map[string]string{"x-ms-documentdb-query": "False"}, "Resources", "create"},
		{"query header cannot override point read", "GET", "/dbs/db/colls/Resources/docs/id", map[string]string{"x-ms-documentdb-query": "True"}, "Resources", "read"},
		{"post item is not create", "POST", "/dbs/db/colls/Resources/docs/id", nil, "Resources", "unknown"},
		{"unknown method", "HEAD", "/dbs/db/colls/Resources/docs/id", nil, "Resources", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, registry := testCounter(t)
			pipeline := testPipeline(p, func(*http.Request) (*http.Response, error) { return chargedResponse(200, "1"), nil }, -1)
			req, err := runtime.NewRequest(context.Background(), tt.method, "https://cosmos.test"+tt.path)
			require.NoError(t, err)
			for name, value := range tt.headers {
				req.Raw().Header.Set(name, value)
			}
			_, err = pipeline.Do(req)
			require.NoError(t, err)
			require.Equal(t, map[[5]string]float64{{"unattributed", "unknown", tt.container, tt.operation, "200"}: 1}, gatheredCharges(t, registry))
		})
	}
}

func TestNewRequestChargePolicySharesRegisteredCounter(t *testing.T) {
	key := [5]string{"controller", t.Name(), "Resources", "read", "200"}
	before := gatheredCharges(t, legacyregistry.DefaultGatherer)[key]
	for range 2 {
		pipeline := testPipeline(NewRequestChargePolicy(), func(*http.Request) (*http.Response, error) { return chargedResponse(200, "1.5"), nil }, -1)
		req, err := runtime.NewRequest(utils.ContextWithControllerName(context.Background(), t.Name()), http.MethodGet, "https://cosmos.test/dbs/db/colls/Resources/docs/id")
		require.NoError(t, err)
		_, err = pipeline.Do(req)
		require.NoError(t, err)
	}
	require.Equal(t, before+3, gatheredCharges(t, legacyregistry.DefaultGatherer)[key])
}

func TestRegisterMetricsExposesSharedRequestCharges(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	require.NoError(t, RegisterMetrics(registry))

	key := [5]string{"controller", t.Name(), "Resources", "read", "200"}
	before := gatheredCharges(t, legacyregistry.DefaultGatherer)[key]
	for range 2 {
		pipeline := testPipeline(NewRequestChargePolicy(), func(*http.Request) (*http.Response, error) {
			return chargedResponse(http.StatusOK, "1.5"), nil
		}, -1)
		req, err := runtime.NewRequest(utils.ContextWithControllerName(context.Background(), t.Name()), http.MethodGet, "https://cosmos.test/dbs/db/colls/Resources/docs/id")
		require.NoError(t, err)
		response, err := pipeline.Do(req)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}

	response := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), fmt.Sprintf(
		"cosmos_request_units_total{cosmosdb_container=\"Resources\",operation=\"read\",source=%q,source_kind=\"controller\",status_code=\"200\"} %g\n",
		t.Name(), before+3,
	))
	require.Equal(t, before+3, gatheredCharges(t, legacyregistry.DefaultGatherer)[key])
}

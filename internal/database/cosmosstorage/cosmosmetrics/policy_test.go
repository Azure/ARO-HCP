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
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
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
	labelNames := []string{"source_kind", "source", "cosmosdb_container", "operation", "status_code"}
	requestUnits := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cosmos_request_units_total",
		Help: "Test request units.",
	}, labelNames)
	requestCount := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cosmos_requests_total",
		Help: "Test request count.",
	}, labelNames)
	registry := prometheus.NewRegistry()
	registry.MustRegister(requestUnits, requestCount)
	return &requestChargePolicy{requestUnits: requestUnits, requestCount: requestCount}, registry
}

type chargeLabels struct {
	SourceKind string
	Source     string
	Container  string
	Operation  string
	StatusCode string
}

type requestCharges map[chargeLabels]float64

func requireRequestCharges(t *testing.T, expected, actual requestCharges) {
	t.Helper()
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Fatalf("request charges mismatch (-expected +actual):\n%s", diff)
	}
}

// requestCounts shares requestCharges' shape: a label set mapped to a metric
// value. gatheredRequestCounts reads cosmos_requests_total the same way
// gatheredCharges reads cosmos_request_units_total.
type requestCounts = requestCharges

// gatherMetricFamily gathers the public metric family named name into a
// label-set-keyed map, rather than reaching into counter children.
func gatherMetricFamily(t *testing.T, gatherer prometheus.Gatherer, name string) requestCharges {
	t.Helper()
	families, err := gatherer.Gather()
	require.NoError(t, err)
	values := requestCharges{}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			require.Len(t, labels, 5)
			key := chargeLabels{
				SourceKind: labels["source_kind"],
				Source:     labels["source"],
				Container:  labels["cosmosdb_container"],
				Operation:  labels["operation"],
				StatusCode: labels["status_code"],
			}
			values[key] = metric.GetCounter().GetValue()
		}
	}
	return values
}

func gatheredCharges(t *testing.T, gatherer prometheus.Gatherer) requestCharges {
	t.Helper()
	return gatherMetricFamily(t, gatherer, "cosmos_request_units_total")
}

func gatheredRequestCounts(t *testing.T, gatherer prometheus.Gatherer) requestCounts {
	t.Helper()
	return gatherMetricFamily(t, gatherer, "cosmos_requests_total")
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
		name               string
		ctx                context.Context
		expectedSourceKind string
		expectedSource     string
	}{
		{
			name:               "no attribution",
			ctx:                context.Background(),
			expectedSourceKind: "unattributed",
			expectedSource:     "unknown",
		},
		{
			name:               "controller",
			ctx:                controller,
			expectedSourceKind: "controller",
			expectedSource:     "ReconcileClusters",
		},
		{
			name:               "informer",
			ctx:                informerutils.ContextWithInformerName(context.Background(), "ClusterInformer"),
			expectedSourceKind: "informer",
			expectedSource:     "ClusterInformer",
		},
		{
			name:               "informer precedes controller",
			ctx:                informerutils.ContextWithInformerName(controller, "ClusterInformer"),
			expectedSourceKind: "informer",
			expectedSource:     "ClusterInformer",
		},
		{
			name:               "empty informer falls back",
			ctx:                informerutils.ContextWithInformerName(controller, ""),
			expectedSourceKind: "controller",
			expectedSource:     "ReconcileClusters",
		},
		{
			name:               "empty names are unattributed",
			ctx:                informerutils.ContextWithInformerName(utils.ContextWithControllerName(context.Background(), ""), ""),
			expectedSourceKind: "unattributed",
			expectedSource:     "unknown",
		},
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
			expected := requestCharges{
				{
					SourceKind: tt.expectedSourceKind,
					Source:     tt.expectedSource,
					Container:  "Resources",
					Operation:  "read",
					StatusCode: "200",
				}: 2.5,
			}
			requireRequestCharges(t, expected, gatheredCharges(t, registry))
		})
	}
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
	_, err = pipeline.Do(req)
	require.NoError(t, err)
	expected := requestCharges{
		{
			SourceKind: "unattributed",
			Source:     "unknown",
			Container:  "Billing",
			Operation:  "read",
			StatusCode: "429",
		}: 2.75,
		{
			SourceKind: "unattributed",
			Source:     "unknown",
			Container:  "Billing",
			Operation:  "read",
			StatusCode: "200",
		}: 4.5,
	}
	requireRequestCharges(t, expected, gatheredCharges(t, registry))
	// The count metric exists precisely because RU cost cannot answer "how many
	// times was this source throttled": a 429 is typically rejected before any
	// work happens and its RU charge is often 0 regardless of how many 429s
	// occurred. Each retry attempt must still increment its own status code once.
	expectedCounts := requestCounts{
		{
			SourceKind: "unattributed",
			Source:     "unknown",
			Container:  "Billing",
			Operation:  "read",
			StatusCode: "429",
		}: 2,
		{
			SourceKind: "unattributed",
			Source:     "unknown",
			Container:  "Billing",
			Operation:  "read",
			StatusCode: "200",
		}: 1,
	}
	requireRequestCharges(t, expectedCounts, gatheredRequestCounts(t, registry))
}

type failingBody struct{ err error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.err }
func (b failingBody) Close() error             { return nil }

func TestRequestChargeResponseAndErrorPassThrough(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("transport or response body failed")
	tests := []struct {
		name           string
		response       *http.Response
		transportErr   error
		bodyErr        bool
		expectedCharge float64 // A negative value means no metric series.
	}{
		{
			name:           "success",
			response:       chargedResponse(200, "2.5"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: 2.5,
		},
		{
			name:           "zero",
			response:       chargedResponse(200, "0"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: 0,
		},
		{
			name:           "http error",
			response:       chargedResponse(403, "2.5"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: 2.5,
		},
		{
			name:           "not modified",
			response:       chargedResponse(304, "2.5"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: 2.5,
		},
		{
			name:           "body error with charge",
			response:       chargedResponse(200, "2.5"),
			transportErr:   nil,
			bodyErr:        true,
			expectedCharge: 2.5,
		},
		{
			name:           "nil response",
			response:       nil,
			transportErr:   sentinel,
			bodyErr:        false,
			expectedCharge: -1,
		},
		{
			name:           "missing",
			response:       chargedResponse(200, ""),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: -1,
		},
		{
			name:           "malformed",
			response:       chargedResponse(200, "not a number"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: -1,
		},
		{
			name:           "nan",
			response:       chargedResponse(200, "NaN"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: -1,
		},
		{
			name:           "positive infinity",
			response:       chargedResponse(200, "+Inf"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: -1,
		},
		{
			name:           "negative",
			response:       chargedResponse(200, "-0.25"),
			transportErr:   nil,
			bodyErr:        false,
			expectedCharge: -1,
		},
		{
			name:           "invalid charge and body error",
			response:       chargedResponse(200, "NaN"),
			transportErr:   nil,
			bodyErr:        true,
			expectedCharge: -1,
		},
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
				require.Equal(t, originalHeaders, resp.Header)
				if !tt.bodyErr {
					body, err := runtime.Payload(resp)
					require.NoError(t, err)
					require.Equal(t, "response body", string(body))
				}
			}
			expected := requestCharges{}
			if tt.expectedCharge >= 0 {
				expected[chargeLabels{
					SourceKind: "unattributed",
					Source:     "unknown",
					Container:  "Fleet",
					Operation:  "read",
					StatusCode: strconv.Itoa(tt.response.StatusCode),
				}] = tt.expectedCharge
			}
			requireRequestCharges(t, expected, gatheredCharges(t, registry))

			// The count metric must not share requestUnits' blind spot: a missing,
			// malformed, or otherwise unparseable charge (the same shape as a real
			// 429's typical zero/absent charge) still counts as one attempt.
			expectedCounts := requestCounts{}
			if tt.response != nil {
				expectedCounts[chargeLabels{
					SourceKind: "unattributed",
					Source:     "unknown",
					Container:  "Fleet",
					Operation:  "read",
					StatusCode: strconv.Itoa(tt.response.StatusCode),
				}] = 1
			}
			requireRequestCharges(t, expectedCounts, gatheredRequestCounts(t, registry))
		})
	}
}

func TestParseCosmosURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		url         string
		expectedURL cosmosURL
	}{
		{
			name: "account without path",
			url:  "https://account.documents.azure.com:443",
			expectedURL: cosmosURL{
				route: cosmosRouteMetadata,
			},
		},
		{
			name: "account root",
			url:  "https://account.documents.azure.com:443/",
			expectedURL: cosmosURL{
				route: cosmosRouteMetadata,
			},
		},
		{
			name: "database metadata",
			url:  "/dbs/private-db",
			expectedURL: cosmosURL{
				route: cosmosRouteMetadata,
			},
		},
		{
			name: "container listing",
			url:  "/dbs/db/colls",
			expectedURL: cosmosURL{
				route: cosmosRouteMetadata,
			},
		},
		{
			name: "offer metadata",
			url:  "/offers/private-offer",
			expectedURL: cosmosURL{
				route: cosmosRouteMetadata,
			},
		},
		{
			name: "container metadata",
			url:  "/dbs/db/colls/Fleet",
			expectedURL: cosmosURL{
				container: "Fleet",
				route:     cosmosRouteMetadata,
			},
		},
		{
			name: "trailing slash",
			url:  "/dbs/db/colls/Fleet/",
			expectedURL: cosmosURL{
				container: "Fleet",
				route:     cosmosRouteMetadata,
			},
		},
		{
			name: "regional document feed",
			url:  "https://account-westus3.documents.azure.com:443/dbs/db/colls/Fleet/docs",
			expectedURL: cosmosURL{
				container: "Fleet",
				route:     cosmosRouteDocumentFeed,
			},
		},
		{
			name: "management cluster document",
			url:  "/dbs/db/colls/Manifests-MC-1/docs/599e38df-f3d1-5b7a-a1f1-72266b3429e8",
			expectedURL: cosmosURL{
				container: "Manifests-MC-1",
				route:     cosmosRouteDocument,
			},
		},
		{
			name: "partition range feed",
			url:  "/dbs/db/colls/Fleet/pkranges",
			expectedURL: cosmosURL{
				container: "Fleet",
				route:     cosmosRoutePartitionRanges,
			},
		},
		{
			name: "escaped database slash",
			url:  "/dbs/db%2Fone/colls/Fleet/docs",
			expectedURL: cosmosURL{
				container: "Fleet",
				route:     cosmosRouteDocumentFeed,
			},
		},
		{
			name: "container decoded once with casing preserved",
			url:  "/dbs/db/colls/rEsOuRcEs%20%252F/docs/id",
			expectedURL: cosmosURL{
				container: "rEsOuRcEs %2F",
				route:     cosmosRouteDocument,
			},
		},
		{
			name: "escaped container slash",
			url:  "/dbs/db/colls/Fleet%2Farchive/docs/id",
			expectedURL: cosmosURL{
				container: "Fleet/archive",
				route:     cosmosRouteDocument,
			},
		},
		{
			name: "escaped ID is not a route",
			url:  "/dbs/db/colls/Resources/docs/colls%2FBilling%2Fdocs%2Fsecret",
			expectedURL: cosmosURL{
				container: "Resources",
				route:     cosmosRouteDocument,
			},
		},
		{
			name: "query and fragment are not the route",
			url:  "/dbs/db/colls/private/docs/id?colls=Resources#/colls/Billing/docs",
			expectedURL: cosmosURL{
				container: "private",
				route:     cosmosRouteDocument,
			},
		},
		{
			name: "unknown child retains container",
			url:  "/dbs/db/colls/Resources/private-operation",
			expectedURL: cosmosURL{
				container: "Resources",
			},
		},
		{
			name:        "unexpected root",
			url:         "/private/colls/Resources/docs/id",
			expectedURL: cosmosURL{},
		},
		{
			name:        "unexpected database child",
			url:         "/dbs/db/users/user",
			expectedURL: cosmosURL{},
		},
		{
			name:        "missing container",
			url:         "/dbs/db/colls//docs/id",
			expectedURL: cosmosURL{},
		},
		{
			name:        "too many segments",
			url:         "/dbs/db/colls/Resources/docs/id/colls/Billing",
			expectedURL: cosmosURL{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tt.url)
			require.NoError(t, err)
			require.Equal(t, tt.expectedURL, parseCosmosURL(u))
		})
	}
}

func TestRequestChargeClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		method            string
		path              string
		headers           map[string]string
		expectedContainer string
		expectedOperation string
	}{
		{
			name:              "read",
			method:            "GET",
			path:              "/dbs/db/colls/Resources/docs/id",
			headers:           nil,
			expectedContainer: "Resources",
			expectedOperation: "read",
		},
		{
			name:              "create",
			method:            "POST",
			path:              "/dbs/db/colls/Billing/docs",
			headers:           nil,
			expectedContainer: "Billing",
			expectedOperation: "create",
		},
		{
			name:   "upsert",
			method: "POST",
			path:   "/dbs/db/colls/Fleet/docs",
			headers: map[string]string{
				"x-ms-documentdb-is-upsert": "true",
			},
			expectedContainer: "Fleet",
			expectedOperation: "upsert",
		},
		{
			name:              "replace",
			method:            "PUT",
			path:              "/dbs/db/colls/Locks/docs/id",
			headers:           nil,
			expectedContainer: "Locks",
			expectedOperation: "replace",
		},
		{
			name:              "patch",
			method:            "PATCH",
			path:              "/dbs/db/colls/Manifests-MC-stamp/docs/id",
			headers:           nil,
			expectedContainer: "Manifests-MC-stamp",
			expectedOperation: "patch",
		},
		{
			name:              "delete",
			method:            "DELETE",
			path:              "/dbs/db/colls/Resources/docs/id",
			headers:           nil,
			expectedContainer: "Resources",
			expectedOperation: "delete",
		},
		{
			name:   "sdk query",
			method: "POST",
			path:   "/dbs/db/colls/Resources/docs",
			headers: map[string]string{
				"x-ms-documentdb-query": "True",
			},
			expectedContainer: "Resources",
			expectedOperation: "query",
		},
		{
			name:   "query plan before query",
			method: "POST",
			path:   "/dbs/db/colls/Resources/docs",
			headers: map[string]string{
				"x-ms-cosmos-is-query-plan-request": "True",
				"x-ms-documentdb-query":             "True",
			},
			expectedContainer: "Resources",
			expectedOperation: "query_plan",
		},
		{
			name:   "batch",
			method: "POST",
			path:   "/dbs/db/colls/Resources/docs",
			headers: map[string]string{
				"x-ms-cosmos-is-batch-request": "True",
			},
			expectedContainer: "Resources",
			expectedOperation: "batch",
		},
		{
			name:   "change feed",
			method: "GET",
			path:   "/dbs/db/colls/Resources/docs",
			headers: map[string]string{
				"A-IM": "Incremental Feed",
			},
			expectedContainer: "Resources",
			expectedOperation: "change_feed",
		},
		{
			name:   "ranges before change feed",
			method: "GET",
			path:   "/dbs/db/colls/Resources/pkranges",
			headers: map[string]string{
				"A-IM": "Incremental Feed",
			},
			expectedContainer: "Resources",
			expectedOperation: "feed_ranges",
		},
		{
			name:              "container metadata",
			method:            "GET",
			path:              "/dbs/db/colls/Resources/",
			headers:           nil,
			expectedContainer: "Resources",
			expectedOperation: "metadata",
		},
		{
			name:              "account metadata",
			method:            "GET",
			path:              "/",
			headers:           nil,
			expectedContainer: "unknown",
			expectedOperation: "metadata",
		},
		{
			name:              "extra segments",
			method:            "GET",
			path:              "/dbs/db/colls/Resources/docs/id/colls/Billing",
			headers:           nil,
			expectedContainer: "unknown",
			expectedOperation: "unknown",
		},
		{
			name:              "unknown collection route",
			method:            "GET",
			path:              "/dbs/db/colls/Resources/private-operation",
			headers:           nil,
			expectedContainer: "Resources",
			expectedOperation: "unknown",
		},
		{
			name:              "feed is not point read",
			method:            "GET",
			path:              "/dbs/db/colls/Resources/docs",
			headers:           nil,
			expectedContainer: "Resources",
			expectedOperation: "unknown",
		},
		{
			name:   "query header false",
			method: "POST",
			path:   "/dbs/db/colls/Resources/docs",
			headers: map[string]string{
				"x-ms-documentdb-query": "False",
			},
			expectedContainer: "Resources",
			expectedOperation: "create",
		},
		{
			name:   "query header cannot override point read",
			method: "GET",
			path:   "/dbs/db/colls/Resources/docs/id",
			headers: map[string]string{
				"x-ms-documentdb-query": "True",
			},
			expectedContainer: "Resources",
			expectedOperation: "read",
		},
		{
			name:              "post item is not create",
			method:            "POST",
			path:              "/dbs/db/colls/Resources/docs/id",
			headers:           nil,
			expectedContainer: "Resources",
			expectedOperation: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest(tt.method, "https://cosmos.test"+tt.path, nil)
			require.NoError(t, err)
			for name, value := range tt.headers {
				req.Header.Set(name, value)
			}
			container, operation := classifyRequest(req)
			require.Equal(t, tt.expectedContainer, container)
			require.Equal(t, tt.expectedOperation, operation)
		})
	}
}

// TestRegisterMetricsExposesSharedRequestCharges proves RegisterMetrics wires
// the same package-level counters NewRequestChargePolicy uses into a caller's
// registry — not private copies — by scraping them over HTTP and cross-checking
// against the legacy registry every policy instance shares.
func TestRegisterMetricsExposesSharedRequestCharges(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	require.NoError(t, RegisterMetrics(registry))

	key := chargeLabels{
		SourceKind: "controller",
		Source:     t.Name(),
		Container:  "Resources",
		Operation:  "read",
		StatusCode: "200",
	}
	chargesBefore := gatheredCharges(t, legacyregistry.DefaultGatherer)[key]
	countsBefore := gatheredRequestCounts(t, legacyregistry.DefaultGatherer)[key]
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
		t.Name(), chargesBefore+3,
	))
	require.Contains(t, response.Body.String(), fmt.Sprintf(
		"cosmos_requests_total{cosmosdb_container=\"Resources\",operation=\"read\",source=%q,source_kind=\"controller\",status_code=\"200\"} %g\n",
		t.Name(), countsBefore+2,
	))
	requireRequestCharges(t, requestCharges{key: chargesBefore + 3}, requestCharges{key: gatheredCharges(t, legacyregistry.DefaultGatherer)[key]})
	requireRequestCharges(t, requestCounts{key: countsBefore + 2}, requestCounts{key: gatheredRequestCounts(t, legacyregistry.DefaultGatherer)[key]})
}

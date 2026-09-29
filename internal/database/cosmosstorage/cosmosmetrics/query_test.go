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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func queryTestContainer(t *testing.T, transport testTransport) *azcosmos.ContainerClient {
	t.Helper()
	key, err := azcosmos.NewKeyCredential("a2V5")
	require.NoError(t, err)
	client, err := azcosmos.NewClientWithKey("https://cosmos.test", key, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			PerRetryPolicies: []policy.Policy{NewRequestChargePolicy()},
			Retry:            policy.RetryOptions{MaxRetries: 1, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Second},
			Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				if err := req.Context().Err(); err != nil {
					return nil, err
				}
				var response *http.Response
				var err error
				if req.URL.Path == "" || req.URL.Path == "/" {
					response = queryTestResponse(http.StatusOK, `{}`, "", "0")
				} else {
					response, err = transport(req)
				}
				if response != nil {
					response.Request = req
				}
				return response, err
			}),
		},
	})
	require.NoError(t, err)
	container, err := client.NewContainer("db", "Resources")
	require.NoError(t, err)
	return container
}

func queryTestResponse(status int, body, continuation, charge string) *http.Response {
	response := &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	response.Header.Set("Content-Type", "application/json")
	response.Header.Set("x-ms-request-charge", charge)
	response.Header.Set("x-ms-activity-id", "test-activity")
	response.Header.Set("x-ms-documentdb-query-metrics", "retrievedDocumentCount=2;outputDocumentCount=1")
	response.Header.Set("x-ms-continuation", continuation)
	return response
}

// Read the public collectors through a registry. Deltas also support -count=N
// without resetting shared collectors or interfering with parallel tests.
func queryTestMetrics(t *testing.T, gatherer prometheus.Gatherer, kind, source, callSite string) map[string]float64 {
	t.Helper()
	families, err := gatherer.Gather()
	require.NoError(t, err)
	values := map[string]float64{}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "cosmos_query_") {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["source"] != source {
				continue
			}
			name := family.GetName()
			if outcome, ok := labels["outcome"]; ok {
				name += ":" + outcome
				delete(labels, "outcome")
			}
			require.Equal(t, map[string]string{
				"source_kind": kind, "source": source, "cosmosdb_container": "Resources",
				"call_site": callSite, "query_shape": "by_type", "query_scope": "cross_partition",
			}, labels)
			if metric.Histogram != nil {
				values[name+":count"] = float64(metric.Histogram.GetSampleCount())
				values[name+":sum"] = metric.Histogram.GetSampleSum()
			} else {
				values[name] = metric.Counter.GetValue()
			}
		}
	}
	return values
}

func TestQueryPagerPagesRetriesAndDiagnostics(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	require.NoError(t, RegisterMetrics(registry))
	before := queryTestMetrics(t, registry, "informer", t.Name(), "list_resources")
	chargesBefore := gatheredCharges(t, registry)
	countsBefore := gatheredRequestCounts(t, registry)
	var logs []map[string]any
	logger := funcr.NewJSON(func(entry string) {
		require.NotContains(t, entry, "private-")
		var fields map[string]any
		require.NoError(t, json.Unmarshal([]byte(entry), &fields))
		logs = append(logs, fields)
	}, funcr.Options{Verbosity: 4})
	live := ContextWithInformerName(utils.ContextWithControllerName(t.Context(), "ignored-controller"), t.Name())
	live = utils.ContextWithLogger(ContextWithCallSite(live, "ignored-live-callsite"), logger)
	live, cancel := context.WithTimeout(live, time.Minute)
	defer cancel()
	deadline, _ := live.Deadline()
	var currentPageContext context.Context
	attempts := 0
	container := queryTestContainer(t, func(req *http.Request) (*http.Response, error) {
		attempts++
		require.NoError(t, req.Context().Err())
		require.Equal(t, currentPageContext.Done(), req.Context().Done())
		actualDeadline, ok := req.Context().Deadline()
		require.True(t, ok)
		require.Equal(t, deadline, actualDeadline)
		kind, source := sourceFromContext(req.Context())
		require.Equal(t, "informer", kind)
		require.Equal(t, t.Name(), source)
		require.Equal(t, "list_resources", CallSiteFromContext(req.Context()))
		shape, scope := queryFromContext(req.Context())
		require.Equal(t, "by_type", shape)
		require.Equal(t, "cross_partition", scope)
		require.Empty(t, req.Header.Get("x-ms-documentdb-populateindexmetrics"))
		require.Equal(t, "true", req.Header.Get("x-ms-documentdb-populatequerymetrics"))
		require.Equal(t, []string{"", "private-first-token", "private-first-token", "private-empty-token"}[attempts-1], req.Header.Get("x-ms-continuation"))
		switch attempts {
		case 1:
			return queryTestResponse(200, `{"Documents":[{"id":"private-one"}]}`, "private-first-token", "2.5"), nil
		case 2:
			response := queryTestResponse(429, `{"message":"private-SQL"}`, "", "1.25")
			response.Header.Set("x-ms-retry-after-ms", "10")
			return response, nil
		case 3:
			return queryTestResponse(200, `{"Documents":[]}`, "private-empty-token", "3.5"), nil
		default:
			return queryTestResponse(200, `{"Documents":[{"id":"private-two"},{"id":"private-three"}]}`, "", "100"), nil
		}
	})
	construction, cancelConstruction := context.WithCancel(ContextWithInformerName(t.Context(), "ignored-construction-source"))
	cancelConstruction()
	pager := NewQueryItemsPager(ContextWithCallSite(construction, "list_resources"), container,
		"SELECT * FROM c WHERE c.type = @type /* private-SQL */", azcosmos.NewPartitionKeyString("private-partition"),
		&azcosmos.QueryOptions{QueryParameters: []azcosmos.QueryParameter{{Name: "@type", Value: "private-parameter"}}}, "by_type", "cross_partition")
	items, itemBytes, pages := 0, 0, 0
	for pager.More() {
		var cancelPage context.CancelFunc
		currentPageContext, cancelPage = context.WithCancel(live)
		page, err := pager.NextPage(currentPageContext)
		cancelPage()
		require.NoError(t, err)
		pages++
		items += len(page.Items)
		for _, item := range page.Items {
			itemBytes += len(item)
		}
	}
	require.Equal(t, 4, attempts)
	require.Equal(t, 3, pages)
	require.Equal(t, 3, items)
	require.Equal(t, len(`{"id":"private-one"}{"id":"private-two"}{"id":"private-three"}`), itemBytes)
	after := queryTestMetrics(t, registry, "informer", t.Name(), "list_resources")
	require.Equal(t, after, queryTestMetrics(t, legacyregistry.DefaultGatherer, "informer", t.Name(), "list_resources"))
	for name, value := range before {
		after[name] -= value
	}
	require.GreaterOrEqual(t, after["cosmos_query_page_duration_seconds:sum"], 0.01)
	delete(after, "cosmos_query_page_duration_seconds:sum")
	require.Equal(t, map[string]float64{
		"cosmos_query_executions_total": 1, "cosmos_query_pages_total:success": 3,
		"cosmos_query_items_total": 3, "cosmos_query_item_bytes_total": float64(itemBytes),
		"cosmos_query_empty_pages_total": 1, "cosmos_query_page_duration_seconds:count": 3,
	}, after)
	for status, expectedCharge := range map[string]float64{"200": 106, "429": 1.25} {
		key := chargeLabels{SourceKind: "informer", Source: t.Name(), Container: "Resources", Operation: "query", StatusCode: status, CallSite: "list_resources", QueryShape: "by_type", QueryScope: "cross_partition"}
		require.Equal(t, expectedCharge, gatheredCharges(t, registry)[key]-chargesBefore[key])
		expectedCount := float64(1)
		if status == "200" {
			expectedCount = 3
		}
		require.Equal(t, expectedCount, gatheredRequestCounts(t, registry)[key]-countsBefore[key])
	}
	metadata := chargeLabels{SourceKind: "informer", Source: t.Name(), Container: "unknown", Operation: "metadata", StatusCode: "200", CallSite: "list_resources", QueryShape: "by_type", QueryScope: "cross_partition"}
	require.Equal(t, float64(1), gatheredRequestCounts(t, registry)[metadata]-countsBefore[metadata])
	require.Len(t, logs, 3)
	for i, fields := range logs {
		require.NotEmpty(t, fields["query_id"])
		require.Equal(t, logs[0]["query_id"], fields["query_id"])
		require.Equal(t, float64(i+1), fields["page_index"])
		require.Equal(t, "informer", fields["source_kind"])
		require.Equal(t, t.Name(), fields["source"])
		require.Equal(t, "list_resources", fields["call_site"])
		require.Equal(t, "Resources", fields["cosmosdb_container"])
		require.Equal(t, "by_type", fields["query_shape"])
		require.Equal(t, "cross_partition", fields["query_scope"])
		require.Equal(t, "test-activity", fields["activity_id"])
		require.Equal(t, "success", fields["outcome"])
		require.Equal(t, "retrievedDocumentCount=2;outputDocumentCount=1", fields["query_metrics"])
		require.Equal(t, i < 2, fields["continuation_present"])
	}
	require.Equal(t, float64(0), logs[1]["items"])
	require.Equal(t, float64(3.5), logs[1]["final_response_request_units"])
	require.Equal(t, float64(4), logs[1]["level"])
	require.Equal(t, float64(100), logs[2]["final_response_request_units"])
	require.Equal(t, float64(0), logs[2]["level"])
}

func TestQueryPagerExecutionSemantics(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"unused", "single", "empty", "early-stop", "resumed", "error", "cancelled", "cancelled-before", "decode-error", "slow", "page-call-site"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			registry := prometheus.NewRegistry()
			require.NoError(t, RegisterMetrics(registry))
			callSite := "unknown"
			if scenario == "page-call-site" {
				callSite = "list_operations"
			}
			before := queryTestMetrics(t, registry, "controller", t.Name(), callSite)
			var logs []map[string]any
			logger := funcr.NewJSON(func(entry string) {
				require.NotContains(t, entry, "private-")
				var fields map[string]any
				require.NoError(t, json.Unmarshal([]byte(entry), &fields))
				logs = append(logs, fields)
			}, funcr.Options{})
			ctx := utils.ContextWithLogger(utils.ContextWithControllerName(t.Context(), t.Name()), logger)
			ctx = ContextWithCallSite(ctx, callSite)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			if scenario == "cancelled-before" {
				cancel()
			}
			attempts := 0
			container := queryTestContainer(t, func(req *http.Request) (*http.Response, error) {
				attempts++
				require.Equal(t, callSite, CallSiteFromContext(req.Context()))
				if scenario == "slow" {
					time.Sleep(time.Second)
				}
				if scenario == "cancelled" {
					cancel()
					require.ErrorIs(t, req.Context().Err(), context.Canceled)
					return nil, req.Context().Err()
				}
				if scenario == "error" {
					return queryTestResponse(400, `{"code":"private-error-code","message":"private-SQL"}`, "", "7.5"), nil
				}
				if scenario == "decode-error" {
					return queryTestResponse(200, `private-invalid-json`, "", "1"), nil
				}
				if scenario == "empty" {
					return queryTestResponse(200, `{"Documents":[]}`, "", "1"), nil
				}
				continuation := ""
				if scenario == "early-stop" || (scenario == "resumed" && attempts == 1) {
					continuation = "private-continuation"
				}
				if scenario == "resumed" && attempts == 2 {
					require.Equal(t, "private-continuation", req.Header.Get("x-ms-continuation"))
				}
				return queryTestResponse(200, `{"Documents":[{"id":"private-item"}]}`, continuation, "1"), nil
			})
			newPager := func(options *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse] {
				return NewQueryItemsPager(t.Context(), container, "SELECT * FROM c /* private-SQL */", azcosmos.NewPartitionKey(), options, "by_type", "cross_partition")
			}
			pager := newPager(nil)
			require.True(t, pager.More())
			expected := map[string]float64{}
			if scenario != "unused" {
				page, err := pager.NextPage(ctx)
				expected["cosmos_query_executions_total"] = 1
				expected["cosmos_query_page_duration_seconds:count"] = 1
				if scenario == "error" || scenario == "cancelled" || scenario == "cancelled-before" || scenario == "decode-error" {
					require.Error(t, err)
					require.False(t, pager.More())
					_, repeatedErr := pager.NextPage(ctx)
					require.True(t, errors.Is(repeatedErr, err))
					expected["cosmos_query_pages_total:error"] = 1
					require.Len(t, logs, 1)
					require.Equal(t, "error", logs[0]["outcome"])
					if scenario == "error" {
						var responseErr *azcore.ResponseError
						require.ErrorAs(t, err, &responseErr)
						require.Equal(t, 400, responseErr.StatusCode)
						require.Equal(t, float64(400), logs[0]["status_code"])
						require.Equal(t, "test-activity", logs[0]["activity_id"])
						require.Equal(t, 7.5, logs[0]["final_response_request_units"])
					}
					if scenario == "cancelled" || scenario == "cancelled-before" {
						require.ErrorIs(t, err, context.Canceled)
					}
					if scenario == "decode-error" || scenario == "cancelled" || scenario == "cancelled-before" {
						require.Nil(t, logs[0]["status_code"], "missing response must not imply a known status")
						require.Nil(t, logs[0]["final_response_request_units"], "missing response must not imply zero RU")
					}
				} else {
					require.NoError(t, err)
					executions := float64(1)
					if scenario == "resumed" {
						require.True(t, pager.More())
						pager = newPager(&azcosmos.QueryOptions{ContinuationToken: page.ContinuationToken})
						_, err = pager.NextPage(ctx)
						require.NoError(t, err)
						executions = 2
					}
					expected["cosmos_query_executions_total"] = executions
					expected["cosmos_query_page_duration_seconds:count"] = executions
					expected["cosmos_query_pages_total:success"] = executions
					expected["cosmos_query_items_total"] = executions
					expected["cosmos_query_item_bytes_total"] = executions * float64(len(`{"id":"private-item"}`))
					if scenario == "empty" {
						expected["cosmos_query_empty_pages_total"] = 1
						expected["cosmos_query_items_total"] = 0
						expected["cosmos_query_item_bytes_total"] = 0
					}
					require.Equal(t, scenario == "early-stop", pager.More())
					if !pager.More() {
						_, err = pager.NextPage(ctx)
						require.Error(t, err)
					}
					if scenario == "slow" {
						require.Len(t, logs, 1)
						require.GreaterOrEqual(t, logs[0]["duration_seconds"].(float64), float64(1))
					} else {
						require.Empty(t, logs, "ordinary successful pages should only log at V(4)")
					}
				}
			}
			after := queryTestMetrics(t, registry, "controller", t.Name(), callSite)
			for name, value := range before {
				after[name] -= value
			}
			delete(after, "cosmos_query_page_duration_seconds:sum")
			require.Equal(t, expected, after)
			if scenario == "cancelled-before" {
				require.Zero(t, attempts)
			} else {
				require.Equal(t, int(expected["cosmos_query_executions_total"]), attempts)
			}
		})
	}
}

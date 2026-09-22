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
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/utils"
)

var queryLabels = []string{"source_kind", "source", "cosmosdb_container", "call_site", "query_shape", "query_scope"}

var queryExecutions = promauto.With(legacyregistry.Registerer()).NewCounterVec(prometheus.CounterOpts{
	Name: "cosmos_query_executions_total",
	Help: "Cosmos DB query pagers whose first page was requested, including failures and resumed queries.",
}, queryLabels)

var queryPages = promauto.With(legacyregistry.Registerer()).NewCounterVec(prometheus.CounterOpts{
	Name: "cosmos_query_pages_total",
	Help: "Cosmos DB query page fetches by outcome, after SDK retries.",
}, append(append([]string{}, queryLabels...), "outcome"))

var queryItems = promauto.With(legacyregistry.Registerer()).NewCounterVec(prometheus.CounterOpts{
	Name: "cosmos_query_items_total",
	Help: "Items returned by successful Cosmos DB query pages.",
}, queryLabels)

var queryItemBytes = promauto.With(legacyregistry.Registerer()).NewCounterVec(prometheus.CounterOpts{
	Name: "cosmos_query_item_bytes_total",
	Help: "JSON bytes of items returned by successful Cosmos DB query pages, excluding response envelopes.",
}, queryLabels)

var queryEmptyPages = promauto.With(legacyregistry.Registerer()).NewCounterVec(prometheus.CounterOpts{
	Name: "cosmos_query_empty_pages_total",
	Help: "Successful Cosmos DB query pages with no items, including pages with continuation tokens.",
}, queryLabels)

var queryPageDuration = promauto.With(legacyregistry.Registerer()).NewHistogramVec(prometheus.HistogramOpts{
	Name:    "cosmos_query_page_duration_seconds",
	Help:    "Cosmos DB query page fetch duration including SDK retries, for both successes and failures.",
	Buckets: prometheus.DefBuckets,
}, queryLabels)

// NewQueryItemsPager wraps the SDK pager with query-level telemetry. Shape and
// scope must be static, finite code-defined classifications, never customer data.
// Only an explicit call site is captured from ctx; each NextPage context supplies
// the fallback call site, current source, logger, cancellation, and deadline.
// An unconsumed pager records nothing; a resumed pager counts as a new execution
// on its first page request.
func NewQueryItemsPager(ctx context.Context, container *azcosmos.ContainerClient, query string, pk azcosmos.PartitionKey, options *azcosmos.QueryOptions, shape string, scope string) *runtime.Pager[azcosmos.QueryItemsResponse] {
	callSite := CallSiteFromContext(ctx)
	pager := container.NewQueryItemsPager(query, pk, options)
	pageIndex := 0
	queryID := ""
	return runtime.NewPager(runtime.PagingHandler[azcosmos.QueryItemsResponse]{
		More: func(azcosmos.QueryItemsResponse) bool { return pager.More() },
		Fetcher: func(ctx context.Context, _ *azcosmos.QueryItemsResponse) (azcosmos.QueryItemsResponse, error) {
			if callSite != "unknown" {
				ctx = ContextWithCallSite(ctx, callSite)
			}
			ctx = contextWithQuery(ctx, shape, scope)
			pageCallSite := CallSiteFromContext(ctx)
			sourceKind, source := sourceFromContext(ctx)
			queryShape, queryScope := queryFromContext(ctx)
			labels := []string{sourceKind, source, container.ID(), pageCallSite, queryShape, queryScope}
			if pageIndex == 0 {
				queryID = uuid.NewString()
				queryExecutions.WithLabelValues(labels...).Inc()
			}
			pageIndex++
			start := time.Now()
			page, err := pager.NextPage(ctx)
			duration := time.Since(start)
			queryPageDuration.WithLabelValues(labels...).Observe(duration.Seconds())

			outcome := "success"
			itemBytes := 0
			activityID := page.ActivityID
			var charge *float64
			var status any
			var headers http.Header
			if page.RawResponse != nil {
				status = page.RawResponse.StatusCode
				headers = page.RawResponse.Header
			}
			if err != nil {
				outcome = "error"
				// ResponseError text and error codes can contain query/customer data.
				var responseErr *azcore.ResponseError
				if errors.As(err, &responseErr) {
					status = responseErr.StatusCode
					if responseErr.RawResponse != nil {
						headers = responseErr.RawResponse.Header
						activityID = headers.Get("x-ms-activity-id")
					}
				}
			} else {
				for _, item := range page.Items {
					itemBytes += len(item)
				}
				queryItems.WithLabelValues(labels...).Add(float64(len(page.Items)))
				queryItemBytes.WithLabelValues(labels...).Add(float64(itemBytes))
				if len(page.Items) == 0 {
					queryEmptyPages.WithLabelValues(labels...).Inc()
				}
			}
			queryPages.WithLabelValues(append(labels, outcome)...).Inc()
			// Decode/transport failures can discard the response; null is not zero RU.
			if ru, parseErr := strconv.ParseFloat(headers.Get("x-ms-request-charge"), 64); parseErr == nil && ru >= 0 && !math.IsInf(ru, 0) {
				charge = &ru
			}

			queryMetrics := ""
			if page.QueryMetrics != nil {
				queryMetrics = *page.QueryMetrics
			}
			logger := utils.LoggerFromContext(ctx)
			if err == nil && (charge == nil || *charge < 100) && duration < time.Second {
				logger = logger.V(4)
			}
			logger.Info("Cosmos DB query page",
				"source_kind", sourceKind, "source", source,
				"cosmosdb_container", container.ID(), "call_site", pageCallSite,
				"query_shape", queryShape, "query_scope", queryScope,
				"query_id", queryID, "page_index", pageIndex,
				"outcome", outcome, "status_code", status, "activity_id", activityID,
				"final_response_request_units", charge, "items", len(page.Items),
				"item_bytes", itemBytes, "duration_seconds", duration.Seconds(),
				"continuation_present", page.ContinuationToken != nil, "query_metrics", queryMetrics)
			return page, err
		},
	})
}

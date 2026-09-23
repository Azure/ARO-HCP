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
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var requestUnits = promauto.With(legacyregistry.Registerer()).NewCounterVec(
	prometheus.CounterOpts{
		Name: "cosmos_request_units_total",
		Help: "Total Cosmos DB request units charged across HTTP attempts.",
	},
	[]string{"source_kind", "source", "cosmosdb_container", "operation", "status_code"},
)

var requestCount = promauto.With(legacyregistry.Registerer()).NewCounterVec(
	prometheus.CounterOpts{
		Name: "cosmos_requests_total",
		Help: "Total Cosmos DB HTTP attempts, by outcome, regardless of request charge.",
	},
	[]string{"source_kind", "source", "cosmosdb_container", "operation", "status_code"},
)

// RegisterMetrics registers the shared request charge and request count
// counters with an additional registerer. Both remain registered with the
// legacy registry.
func RegisterMetrics(registerer prometheus.Registerer) error {
	if err := registerer.Register(requestUnits); err != nil {
		return err
	}
	return registerer.Register(requestCount)
}

// NewRequestChargePolicy records the charge and outcome of each HTTP response.
// Install it once in ClientOptions.PerRetryPolicies so retries are counted
// individually. All instances share the same registered counters.
func NewRequestChargePolicy() policy.Policy {
	return &requestChargePolicy{requestUnits: requestUnits, requestCount: requestCount}
}

type requestChargePolicy struct {
	requestUnits *prometheus.CounterVec
	requestCount *prometheus.CounterVec
}

func (p *requestChargePolicy) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if resp == nil {
		return resp, err
	}

	raw := req.Raw()
	container, operation := classifyRequest(raw)
	sourceKind, source := sourceKindUnattributed, "unknown"
	if name, ok := informerutils.InformerNameFromContext(raw.Context()); ok && name != "" {
		sourceKind, source = sourceKindInformer, name
	} else if name, ok := utils.ControllerNameFromContext(raw.Context()); ok && name != "" {
		sourceKind, source = sourceKindController, name
	}
	statusCode := strconv.Itoa(resp.StatusCode)

	p.requestCount.WithLabelValues(sourceKind, source, container, operation, statusCode).Inc()

	rawCharge := resp.Header.Get("x-ms-request-charge")
	if rawCharge == "" {
		return resp, err
	}
	charge, parseErr := strconv.ParseFloat(rawCharge, 64)
	if parseErr != nil || math.IsNaN(charge) || math.IsInf(charge, 0) || charge < 0 {
		return resp, err
	}

	p.requestUnits.WithLabelValues(sourceKind, source, container, operation, statusCode).Add(charge)
	return resp, err
}

const (
	sourceKindUnattributed = "unattributed"
	sourceKindInformer     = "informer"
	sourceKindController   = "controller"
)

type cosmosRoute string

const (
	cosmosRouteMetadata        cosmosRoute = "metadata"
	cosmosRouteDocumentFeed    cosmosRoute = "document_feed"
	cosmosRouteDocument        cosmosRoute = "document"
	cosmosRoutePartitionRanges cosmosRoute = "partition_ranges"
)

// cosmosURL contains only the URL-derived information needed for classification.
// An empty container means the path does not identify one. Unknown child routes
// retain an identifiable container; malformed paths return the zero value.
type cosmosURL struct {
	container string
	route     cosmosRoute
}

// parseCosmosURL parses Cosmos data-plane paths independently of HTTP methods
// and headers. It preserves container casing and decodes its name exactly once.
// The origin, query string, and fragment do not participate in route parsing.
func parseCosmosURL(u *url.URL) cosmosURL {
	if u.Path == "" || u.Path == "/" {
		return cosmosURL{route: cosmosRouteMetadata}
	}
	// Split before unescaping: an escaped slash inside an ID is not a route separator.
	var segments [6]string
	n := 0
	for segment := range strings.SplitSeq(strings.Trim(u.EscapedPath(), "/"), "/") {
		if segment == "" || n == len(segments) {
			return cosmosURL{}
		}
		segments[n] = segment
		n++
	}

	if segments[0] == "offers" && n <= 2 {
		return cosmosURL{route: cosmosRouteMetadata}
	}
	if segments[0] != "dbs" {
		return cosmosURL{}
	}
	if n <= 2 {
		return cosmosURL{route: cosmosRouteMetadata}
	}
	if segments[2] != "colls" {
		return cosmosURL{}
	}
	if n == 3 {
		return cosmosURL{route: cosmosRouteMetadata}
	}
	// EscapedPath guarantees valid URL escaping.
	container, _ := url.PathUnescape(segments[3])
	parsed := cosmosURL{container: container}
	switch {
	case n == 4:
		parsed.route = cosmosRouteMetadata
	case segments[4] == "pkranges":
		parsed.route = cosmosRoutePartitionRanges
	case segments[4] == "docs" && n == 5:
		parsed.route = cosmosRouteDocumentFeed
	case segments[4] == "docs" && n == 6:
		parsed.route = cosmosRouteDocument
	}
	return parsed
}

// classifyRequest returns the Cosmos container and operation for an HTTP attempt.
// For example, a point read uses:
//
//	GET https://account.documents.azure.com/dbs/db/colls/Resources/docs/item
//
// The examples below omit that same https://account.documents.azure.com origin.
// Several operations share a URL, so classification also needs the method and
// distinguishing headers (header values are matched case-insensitively):
//
//	read:        GET    /dbs/db/colls/Resources/docs/item
//	create:      POST   /dbs/db/colls/Resources/docs (none of the special POST headers below)
//	upsert:      POST   /dbs/db/colls/Resources/docs + x-ms-documentdb-is-upsert: True
//	replace:     PUT    /dbs/db/colls/Resources/docs/item
//	patch:       PATCH  /dbs/db/colls/Resources/docs/item
//	delete:      DELETE /dbs/db/colls/Resources/docs/item
//	query:       POST   /dbs/db/colls/Resources/docs + x-ms-documentdb-query: True
//	query_plan:  POST   /dbs/db/colls/Resources/docs + x-ms-cosmos-is-query-plan-request: True
//	batch:       POST   /dbs/db/colls/Resources/docs + x-ms-cosmos-is-batch-request: True
//	change_feed: GET    /dbs/db/colls/Resources/docs + A-IM: Incremental feed
//	feed_ranges: GET    /dbs/db/colls/Resources/pkranges
//	metadata:    GET    /dbs/db/colls/Resources
//	unknown:     HEAD   /dbs/db/colls/Resources/docs/item
//
// azcosmos v1.5.0 keeps its path helpers and pipelineRequestOptions private, so
// this policy classifies the wire request rather than relying on SDK internals.
func classifyRequest(req *http.Request) (string, string) {
	parsed := parseCosmosURL(req.URL)
	container := parsed.container
	if container == "" {
		container = "unknown"
	}
	switch parsed.route {
	case cosmosRouteMetadata:
		return container, "metadata"
	case cosmosRoutePartitionRanges:
		// The SDK also sets A-IM on partition-range cache refreshes.
		if req.Method == http.MethodGet {
			return container, "feed_ranges"
		}
		return container, "unknown"
	case cosmosRouteDocument:
		switch req.Method {
		case http.MethodGet:
			return container, "read"
		case http.MethodPut:
			return container, "replace"
		case http.MethodPatch:
			return container, "patch"
		case http.MethodDelete:
			return container, "delete"
		}
		return container, "unknown"
	case cosmosRouteDocumentFeed:
		// The method and headers distinguish operations on the same feed URL.
	default:
		return container, "unknown"
	}
	if req.Method == http.MethodGet && strings.EqualFold(req.Header.Get("A-IM"), "Incremental feed") {
		return container, "change_feed"
	}
	if req.Method == http.MethodPost {
		switch {
		case strings.EqualFold(req.Header.Get("x-ms-cosmos-is-query-plan-request"), "true"):
			return container, "query_plan"
		case strings.EqualFold(req.Header.Get("x-ms-cosmos-is-batch-request"), "true"):
			return container, "batch"
		case strings.EqualFold(req.Header.Get("x-ms-documentdb-query"), "true"):
			return container, "query"
		case strings.EqualFold(req.Header.Get("x-ms-documentdb-is-upsert"), "true"):
			return container, "upsert"
		default:
			return container, "create"
		}
	}
	return container, "unknown"
}

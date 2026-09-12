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

	"github.com/Azure/ARO-HCP/internal/utils"
)

var requestUnits = promauto.With(legacyregistry.Registerer()).NewCounterVec(
	prometheus.CounterOpts{
		Name: "cosmos_request_units_total",
		Help: "Total Cosmos DB request units charged across HTTP attempts.",
	},
	[]string{"source_kind", "source", "cosmosdb_container", "operation", "status_code"},
)

// RegisterMetrics registers the shared request charge counter with an additional
// registerer. The counter remains registered with the legacy registry.
func RegisterMetrics(registerer prometheus.Registerer) error {
	return registerer.Register(requestUnits)
}

// NewRequestChargePolicy records the charge on each HTTP response. Install it
// once in ClientOptions.PerRetryPolicies so retries are counted individually.
// All instances share the same registered counter.
func NewRequestChargePolicy() policy.Policy {
	return &requestChargePolicy{counter: requestUnits}
}

type requestChargePolicy struct {
	counter *prometheus.CounterVec
}

func (p *requestChargePolicy) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if resp == nil {
		return resp, err
	}
	rawCharge := resp.Header.Get("x-ms-request-charge")
	if rawCharge == "" {
		return resp, err
	}
	charge, parseErr := strconv.ParseFloat(rawCharge, 64)
	if parseErr != nil || math.IsNaN(charge) || math.IsInf(charge, 0) || charge < 0 {
		return resp, err
	}

	raw := req.Raw()
	sourceKind, source := "unattributed", "unknown"
	if name, ok := InformerNameFromContext(raw.Context()); ok && name != "" {
		sourceKind, source = "informer", name
	} else if name, ok := utils.ControllerNameFromContext(raw.Context()); ok && name != "" {
		sourceKind, source = "controller", name
	}
	container, operation := classifyRequest(raw)
	p.counter.WithLabelValues(sourceKind, source, container, operation, strconv.Itoa(resp.StatusCode)).Add(charge)
	return resp, err
}

func classifyRequest(req *http.Request) (string, string) {
	if req.URL.Path == "" || req.URL.Path == "/" {
		return "unknown", "metadata"
	}
	// Split the escaped path, not URL.Path: an escaped slash inside an ID must
	// not turn that ID into route segments. A fixed array bounds work and avoids
	// allocating a slice for every charged response.
	var segments [6]string
	n := 0
	for segment := range strings.SplitSeq(strings.Trim(req.URL.EscapedPath(), "/"), "/") {
		if segment == "" {
			return "unknown", "unknown"
		}
		if n == len(segments) {
			return "unknown", "unknown"
		}
		segments[n] = segment
		n++
	}

	if segments[0] == "offers" && n <= 2 {
		return "unknown", "metadata"
	}
	if segments[0] != "dbs" {
		return "unknown", "unknown"
	}
	if n <= 2 {
		return "unknown", "metadata"
	}
	if segments[2] != "colls" {
		return "unknown", "unknown"
	}
	if n == 3 {
		return "unknown", "metadata"
	}
	container, err := url.PathUnescape(segments[3])
	if err != nil {
		container = "unknown"
	}
	if n == 4 {
		return container, "metadata"
	}
	if segments[4] == "pkranges" && req.Method == http.MethodGet {
		// The SDK also sets A-IM on partition-range cache refreshes.
		return container, "feed_ranges"
	}
	if segments[4] != "docs" {
		return container, "unknown"
	}
	if n == 6 {
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
			// azcosmos uses x-ms-documentdb-query, not the REST isquery header.
			return container, "query"
		case strings.EqualFold(req.Header.Get("x-ms-documentdb-is-upsert"), "true"):
			return container, "upsert"
		default:
			return container, "create"
		}
	}
	return container, "unknown"
}

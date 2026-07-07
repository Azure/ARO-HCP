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

package database

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Metric verb labels identify which ResourceCRUD method produced a sample.
// They are kept as constants so the instrumented decorator and its tests refer
// to exactly the same strings.
const (
	verbGetByID                 = "get_by_id"
	verbGet                     = "get"
	verbList                    = "list"
	verbCreate                  = "create"
	verbReplace                 = "replace"
	verbDelete                  = "delete"
	verbAddCreateToTransaction  = "add_create_to_transaction"
	verbAddReplaceToTransaction = "add_replace_to_transaction"
)

// databaseRequestBuckets mirrors the latency buckets used by the
// kube-apiserver request-duration histogram, tuned for sub-second Cosmos DB
// calls with a long tail up to ten seconds.
var databaseRequestBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// databaseMetrics bundles the Prometheus collectors shared by the instrumented
// CRUD decorators. The collectors are created with promauto.With(r) so they
// register on the supplied prometheus.Registerer rather than the global
// prometheus.DefaultRegisterer. In production that registerer is the
// k8s.io/component-base legacy registry, which is the registry ARO-HCP exposes
// through its /metrics endpoint (see legacyregistry.DefaultGatherer); in tests
// it is an isolated prometheus.NewRegistry() so assertions don't leak between
// cases.
type databaseMetrics struct {
	// requestTotal counts every ResourceCRUD operation, partitioned by the CRUD
	// verb, the resource type of the wrapped CRUD, and the HTTP status code
	// derived from the returned error.
	requestTotal *prometheus.CounterVec

	// requestDuration records the wall-clock latency of every ResourceCRUD
	// operation with the same label set as requestTotal.
	requestDuration *prometheus.HistogramVec
}

// newDatabaseMetrics constructs the database CRUD collectors and registers them
// on r. Calling it more than once with the same registerer panics on duplicate
// registration, so callers should go through sharedDatabaseMetrics, which
// memoizes the result per registerer.
func newDatabaseMetrics(r prometheus.Registerer) *databaseMetrics {
	return &databaseMetrics{
		requestTotal: promauto.With(r).NewCounterVec(
			prometheus.CounterOpts{
				Name: "database_request_total",
				Help: "Total number of database CRUD requests, partitioned by verb, resource type, and HTTP status code.",
			},
			[]string{"verb", "resource_type", "code"},
		),
		requestDuration: promauto.With(r).NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "database_request_duration_seconds",
				Help:    "Duration of database CRUD requests in seconds, partitioned by verb, resource type, and HTTP status code.",
				Buckets: databaseRequestBuckets,
			},
			[]string{"verb", "resource_type", "code"},
		),
	}
}

var (
	databaseMetricsMu    sync.Mutex
	databaseMetricsCache = map[prometheus.Registerer]*databaseMetrics{}
)

// sharedDatabaseMetrics returns the databaseMetrics registered against r,
// constructing and caching them on first use. The CRUD constructors are invoked
// many times — once per resource scope and again for every nested sub-resource
// accessor — but a given Prometheus collector may only be registered once per
// registry. Memoizing per registerer lets every instrumented CRUD that shares a
// registerer also share a single set of collectors (exactly as the previous
// package-level vars did) instead of panicking on duplicate registration. A nil
// registerer defaults to the legacy registry that ARO-HCP scrapes.
func sharedDatabaseMetrics(r prometheus.Registerer) *databaseMetrics {
	if r == nil {
		r = legacyregistry.Registerer()
	}
	databaseMetricsMu.Lock()
	defer databaseMetricsMu.Unlock()
	if m, ok := databaseMetricsCache[r]; ok {
		return m
	}
	m := newDatabaseMetrics(r)
	databaseMetricsCache[r] = m
	return m
}

// instrumentedCRUD wraps a ResourceCRUD and records Prometheus request-count
// and request-duration metrics for every operation. Like validatingCRUD, the
// wrapped CRUD is only reachable through the decorator, so no operation can
// bypass instrumentation. The design mirrors the kube-apiserver request
// metrics: one counter and one latency histogram, labelled by verb, subject
// (resource_type) and HTTP status code.
type instrumentedCRUD[T any, TP arm.CosmosMetadataAccessorPtr[T]] struct {
	inner             ResourceCRUD[T, TP]
	resourceTypeLabel string
	metrics           *databaseMetrics
}

// NewInstrumentedCRUD returns a ResourceCRUD that delegates to inner while
// recording database_request_total and database_request_duration_seconds for
// every operation, labelling each sample with the resource_type derived from
// resourceType (see sanitizeResourceType). The collectors are registered on
// registerer (see sharedDatabaseMetrics); pass legacyregistry.Registerer() in
// production so the metrics land on the registry exposed by /metrics, or a
// dedicated prometheus.NewRegistry() in tests.
func NewInstrumentedCRUD[T any, TP arm.CosmosMetadataAccessorPtr[T]](inner ResourceCRUD[T, TP], resourceType azcorearm.ResourceType, registerer prometheus.Registerer) ResourceCRUD[T, TP] {
	return &instrumentedCRUD[T, TP]{
		inner:             inner,
		resourceTypeLabel: sanitizeResourceType(resourceType),
		metrics:           sharedDatabaseMetrics(registerer),
	}
}

// resourceTypeLabelSanitizer matches every character that is not valid in the
// stable, readable resource_type label we want. ARM ResourceType.String()
// values look like "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools";
// their "." and "/" separators are the characters this replaces.
var resourceTypeLabelSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// sanitizeResourceType converts an ARM ResourceType into a Prometheus-safe
// resource_type label by replacing every character outside [a-zA-Z0-9_] with an
// underscore. For example the ResourceType whose String() is
// "Microsoft.RedHatOpenShift/hcpOpenShiftClusters" becomes
// "Microsoft_RedHatOpenShift_hcpOpenShiftClusters". Deriving the label from the
// ResourceType constant, rather than a hand-written string, keeps the metric
// label in lock-step with the resource type the CRUD actually serves.
func sanitizeResourceType(rt azcorearm.ResourceType) string {
	return resourceTypeLabelSanitizer.ReplaceAllString(rt.String(), "_")
}

// observe records one counter increment and one histogram observation for a
// completed operation. The status code is derived from err.
func (c *instrumentedCRUD[T, TP]) observe(verb string, start time.Time, err error) {
	code := codeForError(err)
	c.metrics.requestTotal.WithLabelValues(verb, c.resourceTypeLabel, code).Inc()
	c.metrics.requestDuration.WithLabelValues(verb, c.resourceTypeLabel, code).Observe(time.Since(start).Seconds())
}

// codeForError maps an operation result to the HTTP status code used as the
// "code" metric label. A nil error is reported as 200. An azcore.ResponseError
// (possibly wrapped) contributes its HTTP StatusCode. Any other error is
// reported as 500.
func codeForError(err error) string {
	if err == nil {
		return strconv.Itoa(200)
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return strconv.Itoa(respErr.StatusCode)
	}
	return strconv.Itoa(500)
}

func (c *instrumentedCRUD[T, TP]) GetByID(ctx context.Context, cosmosID string) (_ *T, err error) {
	start := time.Now()
	defer func() { c.observe(verbGetByID, start, err) }()
	return c.inner.GetByID(ctx, cosmosID)
}

func (c *instrumentedCRUD[T, TP]) Get(ctx context.Context, resourceID string) (_ *T, err error) {
	start := time.Now()
	defer func() { c.observe(verbGet, start, err) }()
	return c.inner.Get(ctx, resourceID)
}

// List instruments only the construction of the result iterator, not the
// Cosmos queries themselves. The Cosmos SDK pages lazily: the actual query work
// happens while the caller ranges over the returned iterator's Items(), which
// runs outside this decorator. The recorded list duration therefore reflects
// "time to build the pager", not "time to read all pages", and undercounts real
// query latency. This is a known limitation of instrumenting at the CRUD
// boundary.
func (c *instrumentedCRUD[T, TP]) List(ctx context.Context, opts *DBClientListResourceDocsOptions) (_ DBClientIterator[T], err error) {
	start := time.Now()
	defer func() { c.observe(verbList, start, err) }()
	return c.inner.List(ctx, opts)
}

func (c *instrumentedCRUD[T, TP]) Create(ctx context.Context, newObj *T, options *azcosmos.ItemOptions) (_ *T, err error) {
	start := time.Now()
	defer func() { c.observe(verbCreate, start, err) }()
	return c.inner.Create(ctx, newObj, options)
}

func (c *instrumentedCRUD[T, TP]) Replace(ctx context.Context, newObj *T, options *azcosmos.ItemOptions) (_ *T, err error) {
	start := time.Now()
	defer func() { c.observe(verbReplace, start, err) }()
	return c.inner.Replace(ctx, newObj, options)
}

func (c *instrumentedCRUD[T, TP]) Delete(ctx context.Context, resourceID string) (err error) {
	start := time.Now()
	defer func() { c.observe(verbDelete, start, err) }()
	return c.inner.Delete(ctx, resourceID)
}

// AddCreateToTransaction and AddReplaceToTransaction measure the enqueue
// operation: the time spent adding an op to the DBTransaction batch, not the
// Cosmos round-trip. The batch is sent to Cosmos later, as a single request,
// when DBTransaction.Execute() runs — that execution latency is captured
// separately by Execute, not here. Instrumenting these keeps all eight
// ResourceCRUD methods covered by the same counter and duration histogram; the
// "code" label therefore reflects only enqueue errors, not the batch's eventual
// execution result.
func (c *instrumentedCRUD[T, TP]) AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *T, opts *azcosmos.TransactionalBatchItemOptions) (_ string, err error) {
	start := time.Now()
	defer func() { c.observe(verbAddCreateToTransaction, start, err) }()
	return c.inner.AddCreateToTransaction(ctx, transaction, newObj, opts)
}

func (c *instrumentedCRUD[T, TP]) AddReplaceToTransaction(ctx context.Context, transaction DBTransaction, newObj *T, opts *azcosmos.TransactionalBatchItemOptions) (_ string, err error) {
	start := time.Now()
	defer func() { c.observe(verbAddReplaceToTransaction, start, err) }()
	return c.inner.AddReplaceToTransaction(ctx, transaction, newObj, opts)
}

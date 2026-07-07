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
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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

var (
	// databaseRequestTotal counts every ResourceCRUD operation, partitioned by
	// the CRUD verb, the resource type of the wrapped CRUD, and the HTTP status
	// code derived from the returned error.
	databaseRequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "database_request_total",
			Help: "Total number of database CRUD requests, partitioned by verb, resource type, and HTTP status code.",
		},
		[]string{"verb", "resource_type", "code"},
	)

	// databaseRequestDuration records the wall-clock latency of every
	// ResourceCRUD operation with the same label set as databaseRequestTotal.
	databaseRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "database_request_duration_seconds",
			Help:    "Duration of database CRUD requests in seconds, partitioned by verb, resource type, and HTTP status code.",
			Buckets: databaseRequestBuckets,
		},
		[]string{"verb", "resource_type", "code"},
	)
)

// instrumentedCRUD wraps a ResourceCRUD and records Prometheus request-count
// and request-duration metrics for every operation. Like validatingCRUD, the
// wrapped CRUD is only reachable through the decorator, so no operation can
// bypass instrumentation. The design mirrors the kube-apiserver request
// metrics: one counter and one latency histogram, labelled by verb, subject
// (resource_type) and HTTP status code.
type instrumentedCRUD[T any, TP arm.CosmosMetadataAccessorPtr[T]] struct {
	inner        ResourceCRUD[T, TP]
	resourceType string
}

// NewInstrumentedCRUD returns a ResourceCRUD that delegates to inner while
// recording database_request_total and database_request_duration_seconds for
// every operation, labelling each sample with the supplied resourceType.
func NewInstrumentedCRUD[T any, TP arm.CosmosMetadataAccessorPtr[T]](inner ResourceCRUD[T, TP], resourceType string) ResourceCRUD[T, TP] {
	return &instrumentedCRUD[T, TP]{
		inner:        inner,
		resourceType: resourceType,
	}
}

// observe records one counter increment and one histogram observation for a
// completed operation. The status code is derived from err.
func (c *instrumentedCRUD[T, TP]) observe(verb string, start time.Time, err error) {
	code := codeForError(err)
	databaseRequestTotal.WithLabelValues(verb, c.resourceType, code).Inc()
	databaseRequestDuration.WithLabelValues(verb, c.resourceType, code).Observe(time.Since(start).Seconds())
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

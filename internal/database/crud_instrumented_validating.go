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
	"time"

	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// instrumentedValidatingCRUD wraps a ValidatingResourceCRUD and records the same
// Prometheus request-count and request-duration metrics as instrumentedCRUD.
//
// It exists because ValidatingResourceCRUD has a different Replace signature
// (it takes the previous object for update validation) and therefore does not
// satisfy the plain ResourceCRUD interface that instrumentedCRUD decorates.
//
// This decorator is meant to sit on the OUTSIDE of the validating layer
// (instrumented -> validating -> raw) so that a validation failure is recorded
// as a request just like a Cosmos error: the metrics capture everything,
// including validation errors. It reuses the metric collectors, verb labels and
// codeForError helper defined alongside instrumentedCRUD.
type instrumentedValidatingCRUD[T any, TP arm.CosmosMetadataAccessorPtr[T]] struct {
	inner             ValidatingResourceCRUD[T, TP]
	resourceTypeLabel string
	metrics           *databaseMetrics
}

// NewInstrumentedValidatingCRUD returns a ValidatingResourceCRUD that delegates
// to inner while recording database_request_total and
// database_request_duration_seconds for every operation, labelling each sample
// with the resource_type derived from resourceType (see sanitizeResourceType).
// As with NewInstrumentedCRUD, the collectors are registered on registerer (see
// sharedDatabaseMetrics) so both decorators share a single set of collectors per
// registry.
func NewInstrumentedValidatingCRUD[T any, TP arm.CosmosMetadataAccessorPtr[T]](inner ValidatingResourceCRUD[T, TP], resourceType azcorearm.ResourceType, registerer prometheus.Registerer) ValidatingResourceCRUD[T, TP] {
	return &instrumentedValidatingCRUD[T, TP]{
		inner:             inner,
		resourceTypeLabel: sanitizeResourceType(resourceType),
		metrics:           sharedDatabaseMetrics(registerer),
	}
}

// observe records one counter increment and one histogram observation for a
// completed operation. The status code is derived from err by codeForError.
func (c *instrumentedValidatingCRUD[T, TP]) observe(verb string, start time.Time, err error) {
	code := codeForError(err)
	c.metrics.requestTotal.WithLabelValues(verb, c.resourceTypeLabel, code).Inc()
	c.metrics.requestDuration.WithLabelValues(verb, c.resourceTypeLabel, code).Observe(time.Since(start).Seconds())
}

func (c *instrumentedValidatingCRUD[T, TP]) GetByID(ctx context.Context, cosmosID string) (_ *T, err error) {
	start := time.Now()
	defer func() { c.observe(verbGetByID, start, err) }()
	return c.inner.GetByID(ctx, cosmosID)
}

func (c *instrumentedValidatingCRUD[T, TP]) Get(ctx context.Context, resourceID string) (_ *T, err error) {
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
func (c *instrumentedValidatingCRUD[T, TP]) List(ctx context.Context, opts *DBClientListResourceDocsOptions) (_ DBClientIterator[T], err error) {
	start := time.Now()
	defer func() { c.observe(verbList, start, err) }()
	return c.inner.List(ctx, opts)
}

func (c *instrumentedValidatingCRUD[T, TP]) Create(ctx context.Context, newObj *T, options *azcosmos.ItemOptions) (_ *T, err error) {
	start := time.Now()
	defer func() { c.observe(verbCreate, start, err) }()
	return c.inner.Create(ctx, newObj, options)
}

func (c *instrumentedValidatingCRUD[T, TP]) Replace(ctx context.Context, newObj *T, oldObj *T, options *azcosmos.ItemOptions) (_ *T, err error) {
	start := time.Now()
	defer func() { c.observe(verbReplace, start, err) }()
	return c.inner.Replace(ctx, newObj, oldObj, options)
}

func (c *instrumentedValidatingCRUD[T, TP]) Delete(ctx context.Context, resourceID string) (err error) {
	start := time.Now()
	defer func() { c.observe(verbDelete, start, err) }()
	return c.inner.Delete(ctx, resourceID)
}

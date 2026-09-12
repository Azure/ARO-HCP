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

package cosmosstorageutils

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
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
type instrumentedValidatingCRUD[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType]] struct {
	inner             ValidatingResourceCRUD[InternalAPIType, InternalAPITypePointer]
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
func NewInstrumentedValidatingCRUD[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType]](inner ValidatingResourceCRUD[InternalAPIType, InternalAPITypePointer], resourceType azcorearm.ResourceType, registerer prometheus.Registerer) ValidatingResourceCRUD[InternalAPIType, InternalAPITypePointer] {
	return &instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]{
		inner:             inner,
		resourceTypeLabel: sanitizeResourceType(resourceType),
		metrics:           sharedDatabaseMetrics(registerer),
	}
}

// observe records one counter increment and one histogram observation for a
// completed operation. The status code is derived from err by codeForError.
func (c *instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]) observe(verb string, start time.Time, err error) {
	code := codeForError(err)
	c.metrics.requestTotal.WithLabelValues(verb, c.resourceTypeLabel, code).Inc()
	c.metrics.requestDuration.WithLabelValues(verb, c.resourceTypeLabel, code).Observe(time.Since(start).Seconds())
}

func (c *instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]) GetByID(ctx context.Context, cosmosID string) (_ *InternalAPIType, err error) {
	start := time.Now()
	defer func() { c.observe(verbGetByID, start, err) }()
	return c.inner.GetByID(ctx, cosmosID)
}

func (c *instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]) Get(ctx context.Context, resourceID string) (_ *InternalAPIType, err error) {
	start := time.Now()
	defer func() { c.observe(verbGet, start, err) }()
	return c.inner.Get(ctx, resourceID)
}

// NOTE: The recorded duration only reflects the time to construct the pager/iterator,
// not the actual Cosmos DB query execution. Real queries happen lazily when
// DBClientIterator.Items() calls pager.NextPage(). This metric is still useful
// for tracking request totals.
func (c *instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]) List(ctx context.Context, opts *DBClientListResourceDocsOptions) (_ DBClientIterator[InternalAPIType], err error) {
	start := time.Now()
	defer func() { c.observe(verbList, start, err) }()
	return c.inner.List(ctx, opts)
}

func (c *instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]) Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (_ *InternalAPIType, err error) {
	start := time.Now()
	defer func() { c.observe(verbCreate, start, err) }()
	return c.inner.Create(ctx, newObj, options)
}

func (c *instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]) Replace(ctx context.Context, newObj *InternalAPIType, oldObj *InternalAPIType, options *azcosmos.ItemOptions) (_ *InternalAPIType, err error) {
	start := time.Now()
	defer func() { c.observe(verbReplace, start, err) }()
	return c.inner.Replace(ctx, newObj, oldObj, options)
}

func (c *instrumentedValidatingCRUD[InternalAPIType, InternalAPITypePointer]) Delete(ctx context.Context, resourceID string) (err error) {
	start := time.Now()
	defer func() { c.observe(verbDelete, start, err) }()
	return c.inner.Delete(ctx, resourceID)
}

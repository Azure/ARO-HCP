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
	"fmt"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// fakeResource is a minimal internal API type whose pointer satisfies
// arm.CosmosMetadataAccessor (via the embedded arm.CosmosMetadata), so it can
// stand in for a real resource type in the generic ResourceCRUD.
type fakeResource struct {
	arm.CosmosMetadata
}

// mockCRUD is a configurable ResourceCRUD used to drive the instrumented
// decorator. Every method returns the configured err (and a canned value) so
// tests can exercise both the success and error metric paths.
type mockCRUD struct {
	err error
}

var _ ResourceCRUD[fakeResource, *fakeResource] = &mockCRUD{}

func (m *mockCRUD) GetByID(ctx context.Context, cosmosID string) (*fakeResource, error) {
	return nil, m.err
}

func (m *mockCRUD) Get(ctx context.Context, resourceID string) (*fakeResource, error) {
	return nil, m.err
}

func (m *mockCRUD) List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[fakeResource], error) {
	return nil, m.err
}

func (m *mockCRUD) Create(ctx context.Context, newObj *fakeResource, options *azcosmos.ItemOptions) (*fakeResource, error) {
	return newObj, m.err
}

func (m *mockCRUD) Replace(ctx context.Context, newObj *fakeResource, options *azcosmos.ItemOptions) (*fakeResource, error) {
	return newObj, m.err
}

func (m *mockCRUD) Delete(ctx context.Context, resourceID string) error {
	return m.err
}

func (m *mockCRUD) AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *fakeResource, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return "cosmos-uid", m.err
}

func (m *mockCRUD) AddReplaceToTransaction(ctx context.Context, transaction DBTransaction, newObj *fakeResource, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return "cosmos-uid", m.err
}

// counterValue reads the current value of the database_request_total series for
// the given labels from the supplied metrics. GetMetricWithLabelValues creates
// the series (initialised to zero) if it does not yet exist, so this is safe to
// call before an operation to capture a baseline.
func counterValue(t *testing.T, m *databaseMetrics, verb, resourceType, code string) float64 {
	t.Helper()
	c, err := m.requestTotal.GetMetricWithLabelValues(verb, resourceType, code)
	require.NoError(t, err, "failed to get counter series")
	return testutil.ToFloat64(c)
}

// histogramSampleCount returns the number of observations recorded on the
// database_request_duration_seconds series for the given labels of the supplied
// metrics.
func histogramSampleCount(t *testing.T, m *databaseMetrics, verb, resourceType, code string) uint64 {
	t.Helper()
	observer, err := m.requestDuration.GetMetricWithLabelValues(verb, resourceType, code)
	require.NoError(t, err, "failed to get histogram series")
	metric, ok := observer.(prometheus.Metric)
	require.True(t, ok, "histogram observer is not a prometheus.Metric")
	var dtoMetric dto.Metric
	require.NoError(t, metric.Write(&dtoMetric), "failed to write histogram metric")
	return dtoMetric.GetHistogram().GetSampleCount()
}

// TestInstrumentedCRUDRecordsMetrics verifies that every ResourceCRUD method
// increments the request counter and records a duration observation, with
// code="200" on success and the configured resource_type label.
func TestInstrumentedCRUDRecordsMetrics(t *testing.T) {
	ctx := context.Background()
	const resourceType = "test.records/resources"

	cases := []struct {
		verb string
		call func(ResourceCRUD[fakeResource, *fakeResource]) error
	}{
		{verbGetByID, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			_, err := c.GetByID(ctx, "cosmos-id")
			return err
		}},
		{verbGet, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			_, err := c.Get(ctx, "resource-id")
			return err
		}},
		{verbList, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			_, err := c.List(ctx, nil)
			return err
		}},
		{verbCreate, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			_, err := c.Create(ctx, &fakeResource{}, nil)
			return err
		}},
		{verbReplace, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			_, err := c.Replace(ctx, &fakeResource{}, nil)
			return err
		}},
		{verbDelete, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			return c.Delete(ctx, "resource-id")
		}},
		{verbAddCreateToTransaction, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			_, err := c.AddCreateToTransaction(ctx, nil, &fakeResource{}, nil)
			return err
		}},
		{verbAddReplaceToTransaction, func(c ResourceCRUD[fakeResource, *fakeResource]) error {
			_, err := c.AddReplaceToTransaction(ctx, nil, &fakeResource{}, nil)
			return err
		}},
	}

	// A dedicated registry keeps this test's series isolated from every other
	// test (and from the production legacy registry). sharedDatabaseMetrics
	// returns the same collectors the decorator registers for reg, so the
	// assertions below read exactly what the operations record.
	reg := prometheus.NewRegistry()
	metrics := sharedDatabaseMetrics(reg)
	crud := NewInstrumentedCRUD[fakeResource, *fakeResource](&mockCRUD{}, resourceType, reg)

	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			beforeCount := counterValue(t, metrics, tc.verb, resourceType, "200")
			beforeSamples := histogramSampleCount(t, metrics, tc.verb, resourceType, "200")

			require.NoError(t, tc.call(crud), "operation should succeed")

			assert.Equal(t, beforeCount+1, counterValue(t, metrics, tc.verb, resourceType, "200"),
				"counter should increment by one for a successful %s", tc.verb)
			assert.Equal(t, beforeSamples+1, histogramSampleCount(t, metrics, tc.verb, resourceType, "200"),
				"histogram should record one observation for a successful %s", tc.verb)
		})
	}
}

// TestInstrumentedCRUDErrorCodes verifies the mapping from operation result to
// the "code" metric label: nil -> 200, azcore.ResponseError (including wrapped)
// -> its HTTP status, and any other error -> 500.
func TestInstrumentedCRUDErrorCodes(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"success", nil, "200"},
		{"response_error_not_found", &azcore.ResponseError{StatusCode: http.StatusNotFound}, "404"},
		{"response_error_conflict", &azcore.ResponseError{StatusCode: http.StatusConflict}, "409"},
		{
			"wrapped_response_error",
			fmt.Errorf("cosmos call failed: %w", &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}),
			"412",
		},
		{"generic_error", errors.New("connection reset"), "500"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A dedicated registry per case keeps each series isolated so
			// absolute assertions (sample count == 1) are stable.
			reg := prometheus.NewRegistry()
			metrics := sharedDatabaseMetrics(reg)
			resourceType := "test.errorcodes/" + tc.name
			crud := NewInstrumentedCRUD[fakeResource, *fakeResource](&mockCRUD{err: tc.err}, resourceType, reg)

			_, err := crud.Get(ctx, "resource-id")
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			assert.Equal(t, float64(1), counterValue(t, metrics, verbGet, resourceType, tc.wantCode),
				"counter for code %s should be one", tc.wantCode)
			assert.Equal(t, uint64(1), histogramSampleCount(t, metrics, verbGet, resourceType, tc.wantCode),
				"histogram for code %s should record one observation", tc.wantCode)

			// No other code label should have been touched for this resource type.
			for _, otherCode := range []string{"200", "404", "409", "412", "500"} {
				if otherCode == tc.wantCode {
					continue
				}
				assert.Zero(t, counterValue(t, metrics, verbGet, resourceType, otherCode),
					"counter for unexpected code %s should be zero", otherCode)
			}
		})
	}
}

// TestInstrumentedCRUDResourceTypeLabel verifies that the resource_type label
// reflects the value supplied to the constructor and that two decorators with
// different resource types record to independent series.
func TestInstrumentedCRUDResourceTypeLabel(t *testing.T) {
	ctx := context.Background()

	const typeA = "test.rtlabel/typeA"
	const typeB = "test.rtlabel/typeB"

	// Both decorators share a single registry so the two resource types are
	// independent series within the same collectors.
	reg := prometheus.NewRegistry()
	metrics := sharedDatabaseMetrics(reg)
	crudA := NewInstrumentedCRUD[fakeResource, *fakeResource](&mockCRUD{}, typeA, reg)
	crudB := NewInstrumentedCRUD[fakeResource, *fakeResource](&mockCRUD{}, typeB, reg)

	beforeA := counterValue(t, metrics, verbGet, typeA, "200")
	beforeB := counterValue(t, metrics, verbGet, typeB, "200")

	_, err := crudA.Get(ctx, "resource-id")
	require.NoError(t, err)

	// An operation on crudA affects only typeA's series.
	assert.Equal(t, beforeA+1, counterValue(t, metrics, verbGet, typeA, "200"),
		"typeA counter should increment after an operation on crudA")
	assert.Equal(t, beforeB, counterValue(t, metrics, verbGet, typeB, "200"),
		"typeB counter should be unaffected by an operation on crudA")

	_, err = crudB.Get(ctx, "resource-id")
	require.NoError(t, err)

	// An operation on crudB affects only typeB's series.
	assert.Equal(t, beforeB+1, counterValue(t, metrics, verbGet, typeB, "200"),
		"typeB counter should increment after an operation on crudB")
	assert.Equal(t, beforeA+1, counterValue(t, metrics, verbGet, typeA, "200"),
		"typeA counter should be unchanged by an operation on crudB")
}

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

package maestro

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// mockMaestroClient implements Client for testing
type mockMaestroClient struct {
	createErr error
	getErr    error
	deleteErr error
	patchErr  error
	listErr   error
}

func (m *mockMaestroClient) Create(ctx context.Context, manifestWork *workv1.ManifestWork, opts metav1.CreateOptions) (*workv1.ManifestWork, error) {
	return manifestWork, m.createErr
}

func (m *mockMaestroClient) Get(ctx context.Context, name string, opts metav1.GetOptions) (*workv1.ManifestWork, error) {
	return &workv1.ManifestWork{}, m.getErr
}

func (m *mockMaestroClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return m.deleteErr
}

func (m *mockMaestroClient) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*workv1.ManifestWork, error) {
	return &workv1.ManifestWork{}, m.patchErr
}

func (m *mockMaestroClient) List(ctx context.Context, opts metav1.ListOptions) (*workv1.ManifestWorkList, error) {
	return &workv1.ManifestWorkList{}, m.listErr
}

func TestIsExpectedError(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		err       error
		expected  bool
	}{
		{
			name:      "NotFound on Get is expected",
			operation: "get",
			err:       k8serrors.NewNotFound(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test"),
			expected:  true,
		},
		{
			name:      "AlreadyExists on Create is expected",
			operation: "create",
			err:       k8serrors.NewAlreadyExists(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test"),
			expected:  true,
		},
		{
			name:      "NotFound on Create is unexpected",
			operation: "create",
			err:       k8serrors.NewNotFound(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test"),
			expected:  false,
		},
		{
			name:      "AlreadyExists on Get is unexpected",
			operation: "get",
			err:       k8serrors.NewAlreadyExists(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test"),
			expected:  false,
		},
		{
			name:      "Forbidden on Get is unexpected",
			operation: "get",
			err:       k8serrors.NewForbidden(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test", errors.New("forbidden")),
			expected:  false,
		},
		{
			name:      "Generic error on Create is unexpected",
			operation: "create",
			err:       errors.New("some generic error"),
			expected:  false,
		},
		{
			name:      "NotFound on Delete is unexpected",
			operation: "delete",
			err:       k8serrors.NewNotFound(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test"),
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExpectedError(tt.operation, tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMaestroMetrics_ExpectedErrorsNotCounted(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMaestroMetrics(registry)

	notFoundErr := k8serrors.NewNotFound(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test")
	alreadyExistsErr := k8serrors.NewAlreadyExists(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test")

	mockClient := &mockMaestroClient{
		getErr:    notFoundErr,
		createErr: alreadyExistsErr,
	}
	client := NewInstrumentedMaestroClient(mockClient, metrics)

	ctx := context.Background()

	// Perform Get (NotFound error - expected)
	_, _ = client.Get(ctx, "test", metav1.GetOptions{})

	// Perform Create (AlreadyExists error - expected)
	_, _ = client.Create(ctx, &workv1.ManifestWork{}, metav1.CreateOptions{})

	// Verify that errors_total counter is still 0 for both operations
	expectedMetrics := `
# HELP maestro_grpc_errors_total Total number of unexpected Maestro GRPC operation failures (excludes expected errors like NotFound/AlreadyExists)
# TYPE maestro_grpc_errors_total counter
maestro_grpc_errors_total{operation="create"} 0
maestro_grpc_errors_total{operation="delete"} 0
maestro_grpc_errors_total{operation="get"} 0
maestro_grpc_errors_total{operation="list"} 0
maestro_grpc_errors_total{operation="patch"} 0
`
	if err := testutil.CollectAndCompare(metrics.errorsTotal, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric value:\n%s", err)
	}
}

func TestMaestroMetrics_UnexpectedErrorsCounted(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMaestroMetrics(registry)

	forbiddenErr := k8serrors.NewForbidden(schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}, "test", errors.New("forbidden"))
	genericErr := errors.New("connection refused")

	mockClient := &mockMaestroClient{
		getErr:    forbiddenErr,
		createErr: genericErr,
	}
	client := NewInstrumentedMaestroClient(mockClient, metrics)

	ctx := context.Background()

	// Perform Get (Forbidden error - unexpected)
	_, _ = client.Get(ctx, "test", metav1.GetOptions{})

	// Perform Create (generic error - unexpected)
	_, _ = client.Create(ctx, &workv1.ManifestWork{}, metav1.CreateOptions{})

	// Verify that errors_total counter incremented for both operations
	expectedMetrics := `
# HELP maestro_grpc_errors_total Total number of unexpected Maestro GRPC operation failures (excludes expected errors like NotFound/AlreadyExists)
# TYPE maestro_grpc_errors_total counter
maestro_grpc_errors_total{operation="create"} 1
maestro_grpc_errors_total{operation="delete"} 0
maestro_grpc_errors_total{operation="get"} 1
maestro_grpc_errors_total{operation="list"} 0
maestro_grpc_errors_total{operation="patch"} 0
`
	if err := testutil.CollectAndCompare(metrics.errorsTotal, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric value:\n%s", err)
	}
}

func TestMaestroMetrics_OperationsTotalIncremented(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMaestroMetrics(registry)

	mockClient := &mockMaestroClient{}
	client := NewInstrumentedMaestroClient(mockClient, metrics)

	ctx := context.Background()

	// Perform operations
	_, _ = client.Get(ctx, "test", metav1.GetOptions{})
	_, _ = client.Create(ctx, &workv1.ManifestWork{}, metav1.CreateOptions{})
	_, _ = client.List(ctx, metav1.ListOptions{})

	// Verify operations_total counter
	expectedMetrics := `
# HELP maestro_grpc_operations_total Total number of Maestro GRPC operations by operation type
# TYPE maestro_grpc_operations_total counter
maestro_grpc_operations_total{operation="create"} 1
maestro_grpc_operations_total{operation="delete"} 0
maestro_grpc_operations_total{operation="get"} 1
maestro_grpc_operations_total{operation="list"} 1
maestro_grpc_operations_total{operation="patch"} 0
`
	if err := testutil.CollectAndCompare(metrics.operationsTotal, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric value:\n%s", err)
	}
}

func TestMaestroMetrics_DurationRecorded(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMaestroMetrics(registry)

	mockClient := &mockMaestroClient{}
	client := NewInstrumentedMaestroClient(mockClient, metrics)

	ctx := context.Background()

	// Perform an operation
	_, _ = client.Get(ctx, "test", metav1.GetOptions{})

	// Verify that duration histogram has recorded a sample
	// We can't verify exact duration, but we can check that count > 0
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var found bool
	for _, mf := range metricFamilies {
		if mf.GetName() == "maestro_grpc_operation_duration_seconds" {
			found = true
			for _, m := range mf.GetMetric() {
				for _, label := range m.GetLabel() {
					if label.GetName() == "operation" && label.GetValue() == "get" {
						if m.GetHistogram().GetSampleCount() != 1 {
							t.Errorf("expected 1 sample for get operation, got %d", m.GetHistogram().GetSampleCount())
						}
					}
				}
			}
		}
	}

	if !found {
		t.Error("maestro_grpc_operation_duration_seconds metric not found")
	}
}

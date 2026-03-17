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

package controllerutils

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestPhaseLabel(t *testing.T) {
	tests := []struct {
		input    arm.ProvisioningState
		expected string
	}{
		{arm.ProvisioningStateAccepted, "accepted"},
		{arm.ProvisioningStateProvisioning, "provisioning"},
		{arm.ProvisioningStateUpdating, "updating"},
		{arm.ProvisioningStateDeleting, "deleting"},
		{arm.ProvisioningStateSucceeded, "succeeded"},
		{arm.ProvisioningStateFailed, "failed"},
		{arm.ProvisioningStateCanceled, "canceled"},
		{arm.ProvisioningStateAwaitingSecret, "awaitingsecret"},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			assert.Equal(t, tt.expected, PhaseLabel(tt.input))
		})
	}
}

func TestResourceTypeFromExternalID(t *testing.T) {
	tests := []struct {
		name       string
		externalID *azcorearm.ResourceID
		expected   string
	}{
		{
			name:       "nil returns unknown",
			externalID: nil,
			expected:   "unknown",
		},
		{
			name:       "cluster resource type",
			externalID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
			expected:   "cluster",
		},
		{
			name:       "nodepool resource type",
			externalID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1")),
			expected:   "nodepool",
		},
		{
			name:       "externalauth resource type",
			externalID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/externalAuths/ea-1")),
			expected:   "externalauth",
		},
		{
			name:       "unknown resource type",
			externalID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.SomeOther/someResource/foo")),
			expected:   "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ResourceTypeFromExternalID(tt.externalID))
		})
	}
}

func TestOperationTypeLabel(t *testing.T) {
	tests := []struct {
		input    api.OperationRequest
		expected string
	}{
		{api.OperationRequestCreate, "create"},
		{api.OperationRequestUpdate, "update"},
		{api.OperationRequestDelete, "delete"},
		{api.OperationRequestRequestCredential, "requestcredential"},
		{api.OperationRequestRevokeCredentials, "revokecredentials"},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			assert.Equal(t, tt.expected, OperationTypeLabel(tt.input))
		})
	}
}

func newTestOperation(t *testing.T, opName string, request api.OperationRequest, status arm.ProvisioningState, externalID string, startTime, lastTransition time.Time) *api.Operation {
	t.Helper()
	operationID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName))
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + opName))
	op := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID:         resourceID,
		OperationID:        operationID,
		Request:            request,
		Status:             status,
		StartTime:          startTime,
		LastTransitionTime: lastTransition,
	}
	if externalID != "" {
		op.ExternalID = api.Must(azcorearm.ParseResourceID(externalID))
	}
	return op
}

// newTestController creates a controller with fresh GaugeVecs for test isolation.
func newTestController(t *testing.T) *OperationPhaseMetricsController {
	t.Helper()
	reg := prometheus.NewRegistry()

	pi := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_resource_operation_phase_info",
		Help: "Current phase of each operation (value is always 1).",
	}, labelNames)
	st := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_resource_operation_start_time_seconds",
		Help: "Unix timestamp when the operation started.",
	}, labelNames)
	ltt := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backend_resource_operation_last_transition_time_seconds",
		Help: "Unix timestamp when the operation last changed phase.",
	}, labelNames)
	reg.MustRegister(pi, st, ltt)

	return &OperationPhaseMetricsController{
		name:               "OperationPhaseMetrics",
		phaseInfo:          pi,
		startTime:          st,
		lastTransitionTime: ltt,
	}
}

func TestSetMetrics_SetsAllThreeMetrics(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)

	c := newTestController(t)
	c.setMetrics(context.Background(), op)

	assert.Equal(t, 1, testutil.CollectAndCount(c.phaseInfo))
	assert.Equal(t, 1, testutil.CollectAndCount(c.startTime))
	assert.Equal(t, 1, testutil.CollectAndCount(c.lastTransitionTime))
}

func TestSyncOperation_SetsMetricsFromIndexer(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(op))

	c := newTestController(t)
	c.indexer = indexer

	key, err := cache.MetaNamespaceKeyFunc(op)
	require.NoError(t, err)

	err = c.syncOperation(context.Background(), key)
	assert.NoError(t, err)
	assert.Equal(t, 1, testutil.CollectAndCount(c.phaseInfo), "expected 1 phase_info metric")
	assert.Equal(t, 1, testutil.CollectAndCount(c.startTime), "expected 1 start_time metric")
	assert.Equal(t, 1, testutil.CollectAndCount(c.lastTransitionTime), "expected 1 last_transition_time metric")
}

func TestSyncOperation_DeletesMetricsWhenOperationRemoved(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(op))

	c := newTestController(t)
	c.indexer = indexer

	key, err := cache.MetaNamespaceKeyFunc(op)
	require.NoError(t, err)

	// First sync: metrics are set.
	err = c.syncOperation(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, 1, testutil.CollectAndCount(c.phaseInfo))

	// Remove from indexer, simulating informer Delete event.
	require.NoError(t, indexer.Delete(op))

	// Second sync with same key: metrics are cleaned up.
	err = c.syncOperation(context.Background(), key)
	assert.NoError(t, err)
	assert.Equal(t, 0, testutil.CollectAndCount(c.phaseInfo), "expected 0 metrics after operation removed")
	assert.Equal(t, 0, testutil.CollectAndCount(c.startTime))
	assert.Equal(t, 0, testutil.CollectAndCount(c.lastTransitionTime))
}

func TestSyncOperation_SkipsNilOperationID(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op-nil-id"))
	op := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID:         resourceID,
		OperationID:        nil,
		ExternalID:         api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
		Request:            api.OperationRequestCreate,
		Status:             arm.ProvisioningStateAccepted,
		StartTime:          now,
		LastTransitionTime: now,
	}

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(op))

	c := newTestController(t)
	c.indexer = indexer

	key, err := cache.MetaNamespaceKeyFunc(op)
	require.NoError(t, err)

	err = c.syncOperation(context.Background(), key)
	assert.NoError(t, err)
	assert.Equal(t, 0, testutil.CollectAndCount(c.phaseInfo), "expected 0 metrics for operation with nil OperationID")
}

func TestSetMetrics_SkipsZeroTimestamps(t *testing.T) {
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		time.Time{}, time.Time{})

	c := newTestController(t)
	c.setMetrics(context.Background(), op)

	assert.Equal(t, 1, testutil.CollectAndCount(c.phaseInfo), "expected only phase_info metric when timestamps are zero")
	assert.Equal(t, 0, testutil.CollectAndCount(c.startTime))
	assert.Equal(t, 0, testutil.CollectAndCount(c.lastTransitionTime))
}

func TestSetMetrics_MultipleOperations(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op1 := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)
	op2 := newTestOperation(t, "op-2",
		api.OperationRequestDelete,
		arm.ProvisioningStateDeleting,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1",
		now, now)

	c := newTestController(t)
	c.setMetrics(context.Background(), op1)
	c.setMetrics(context.Background(), op2)

	assert.Equal(t, 2, testutil.CollectAndCount(c.phaseInfo), "expected 2 phase_info metrics")
	assert.Equal(t, 2, testutil.CollectAndCount(c.startTime), "expected 2 start_time metrics")
	assert.Equal(t, 2, testutil.CollectAndCount(c.lastTransitionTime), "expected 2 last_transition_time metrics")
}

func TestSetMetrics_VerifiesLabelValues(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateProvisioning,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)

	c := newTestController(t)
	c.setMetrics(context.Background(), op)

	hash := ResourceIDHash(op.GetResourceID().String())
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="provisioning",resource_id_hash="%s",resource_type="cluster"} 1
`, hash)

	err := testutil.CollectAndCompare(c.phaseInfo, strings.NewReader(expected), "backend_resource_operation_phase_info")
	require.NoError(t, err)
}

func TestSetMetrics_NilExternalIDUsesUnknownResourceType(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-no-external",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"",
		now, now)

	c := newTestController(t)
	c.setMetrics(context.Background(), op)

	hash := ResourceIDHash(op.GetResourceID().String())
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="accepted",resource_id_hash="%s",resource_type="unknown"} 1
`, hash)

	err := testutil.CollectAndCompare(c.phaseInfo, strings.NewReader(expected), "backend_resource_operation_phase_info")
	require.NoError(t, err)
}

func TestSetMetrics_PhaseTransitionDeletesOldSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)

	c := newTestController(t)

	// Initial set with "accepted" phase.
	c.setMetrics(context.Background(), op)
	assert.Equal(t, 1, testutil.CollectAndCount(c.phaseInfo))

	// Phase transition to "provisioning".
	op.Status = arm.ProvisioningStateProvisioning
	c.setMetrics(context.Background(), op)

	// Should still be exactly 1 metric (old "accepted" deleted via DeletePartialMatch, new "provisioning" set).
	assert.Equal(t, 1, testutil.CollectAndCount(c.phaseInfo))

	hash := ResourceIDHash(op.GetResourceID().String())
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="provisioning",resource_id_hash="%s",resource_type="cluster"} 1
`, hash)
	err := testutil.CollectAndCompare(c.phaseInfo, strings.NewReader(expected))
	require.NoError(t, err)
}

func TestDeleteMetricsByKey_CleansUpAllGauges(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)

	c := newTestController(t)
	c.setMetrics(context.Background(), op)
	assert.Equal(t, 1, testutil.CollectAndCount(c.phaseInfo))

	// The store key is the lowercased ResourceID (CosmosMetadata), same value used by setMetrics.
	c.deleteMetricsByKey(strings.ToLower(op.GetResourceID().String()))
	assert.Equal(t, 0, testutil.CollectAndCount(c.phaseInfo))
	assert.Equal(t, 0, testutil.CollectAndCount(c.startTime))
	assert.Equal(t, 0, testutil.CollectAndCount(c.lastTransitionTime))
}

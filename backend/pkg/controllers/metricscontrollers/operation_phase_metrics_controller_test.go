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

package metricscontrollers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestPhaseMetricLabel(t *testing.T) {
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
			require.Equal(t, tt.expected, phaseMetricLabel(tt.input))
		})
	}
}

func TestResourceIDToTypeMetricLabel(t *testing.T) {
	tests := []struct {
		name       string
		resourceID *azcorearm.ResourceID
		expected   string
	}{
		{
			name:       "nil returns unknown",
			resourceID: nil,
			expected:   "unknown",
		},
		{
			name:       "cluster resource type",
			resourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
			expected:   "microsoft.redhatopenshift/hcpopenshiftclusters",
		},
		{
			name:       "nodepool resource type",
			resourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1")),
			expected:   "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools",
		},
		{
			name:       "externalauth resource type",
			resourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/externalAuths/ea-1")),
			expected:   "microsoft.redhatopenshift/hcpopenshiftclusters/externalauths",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, resourceIDToTypeMetricLabel(tt.resourceID))
		})
	}
}

func TestSubscriptionIDMetricLabel(t *testing.T) {
	tests := []struct {
		name       string
		resourceID *azcorearm.ResourceID
		expected   string
	}{
		{
			name:       "nil returns empty",
			resourceID: nil,
			expected:   "",
		},
		{
			name:       "subscription is lowercased",
			resourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/SUB-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
			expected:   "sub-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, subscriptionIDMetricLabel(tt.resourceID))
		})
	}
}

func TestOperationTypeMetricLabel(t *testing.T) {
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
			require.Equal(t, tt.expected, operationTypeMetricLabel(tt.input))
		})
	}
}

func newTestOperation(t *testing.T, opName string, request api.OperationRequest, status arm.ProvisioningState, externalID string, startTime, lastTransition time.Time) *api.Operation {
	t.Helper()

	operationID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName))
	resourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + opName))
	op := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
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

func newTestOperationHandler(t *testing.T) (*operationPhaseMetricsHandler, *prometheus.Registry) {
	t.Helper()

	reg := prometheus.NewRegistry()
	handler, ok := NewOperationPhaseMetricsHandler(reg).(*operationPhaseMetricsHandler)
	require.True(t, ok)
	return handler, reg
}

func TestOperationPhaseMetricsHandler_SetsAllThreeMetrics(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 1, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 1, testutil.CollectAndCount(handler.lastTransitionTime))
}

func TestOperationPhaseMetricsHandler_PhaseTransitionDeletesOldSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	op.Status = arm.ProvisioningStateProvisioning
	op.LastTransitionTime = now.Add(5 * time.Minute)
	handler.Sync(context.Background(), op)

	resourceID := resourceIDMetricLabel(op.GetResourceID())
	subscriptionID := subscriptionIDMetricLabel(op.GetResourceID())
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="provisioning",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
	require.Equal(t, 1, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 1, testutil.CollectAndCount(handler.lastTransitionTime))
}

func TestOperationControllerSyncResource_SetsMetricsFromIndexer(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(op))

	handler, reg := newTestOperationHandler(t)
	controller := &Controller[*api.Operation]{
		name:    "OperationPhaseMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(context.Background(), key))

	resourceID := resourceIDMetricLabel(op.GetResourceID())
	subscriptionID := subscriptionIDMetricLabel(op.GetResourceID())
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="accepted",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

func TestOperationControllerSyncResource_DeletesMetricsWhenOperationRemoved(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(op))

	handler, reg := newTestOperationHandler(t)
	controller := &Controller[*api.Operation]{
		name:    "OperationPhaseMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(context.Background(), key))
	require.NoError(t, indexer.Delete(op))
	require.NoError(t, controller.syncResource(context.Background(), key))

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(""), "backend_resource_operation_phase_info", "backend_resource_operation_start_time_seconds", "backend_resource_operation_last_transition_time_seconds"))
}

func TestResourceIDStoreKeyForObject_MatchesMetaNamespaceKeyFuncForOperation(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	got, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)

	expected, err := cache.MetaNamespaceKeyFunc(op)
	require.NoError(t, err)

	require.Equal(t, expected, got)
}

func TestOperationPhaseMetricsHandler_SkipsNilOperationID(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	resourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op-nil-id"))
	op := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ExternalID:         api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
		Request:            api.OperationRequestCreate,
		Status:             arm.ProvisioningStateAccepted,
		StartTime:          now,
		LastTransitionTime: now,
	}

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 0, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 0, testutil.CollectAndCount(handler.lastTransitionTime))
}

func TestOperationPhaseMetricsHandler_SkipsZeroTimestamps(t *testing.T) {
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		time.Time{},
		time.Time{},
	)

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 0, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 0, testutil.CollectAndCount(handler.lastTransitionTime))
}

func TestOperationPhaseMetricsHandler_MultipleOperations(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op1 := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)
	op2 := newTestOperation(
		t,
		"op-2",
		api.OperationRequestDelete,
		arm.ProvisioningStateDeleting,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1",
		now,
		now,
	)

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)

	require.Equal(t, 2, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 2, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 2, testutil.CollectAndCount(handler.lastTransitionTime))
}

func TestOperationPhaseMetricsHandler_VerifiesLabelValues(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateProvisioning,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	resourceID := resourceIDMetricLabel(op.GetResourceID())
	subscriptionID := subscriptionIDMetricLabel(op.GetResourceID())
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="provisioning",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

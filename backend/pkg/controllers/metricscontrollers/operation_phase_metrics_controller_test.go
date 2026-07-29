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

	"github.com/go-logr/logr"
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
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
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

	resourceID := resourceIDMetricLabel(op.ExternalID)
	subscriptionID := subscriptionIDMetricLabel(op.ExternalID)
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

	resourceID := resourceIDMetricLabel(op.ExternalID)
	subscriptionID := subscriptionIDMetricLabel(op.ExternalID)
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="accepted",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

// TestOperationControllerSyncResource_DeleteIsNoOp documents that
// the controller framework calls handler.Delete when an operation
// is removed from the indexer, but the operation handler's Delete
// is intentionally a no-op (see Delete doc-comment). The previously-
// emitted series persists until process restart.
func TestOperationControllerSyncResource_DeleteIsNoOp(t *testing.T) {
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

	handler, _ := newTestOperationHandler(t)
	controller := &Controller[*api.Operation]{
		name:    "OperationPhaseMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(context.Background(), key))
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 1, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 1, testutil.CollectAndCount(handler.lastTransitionTime))

	require.NoError(t, indexer.Delete(op))
	require.NoError(t, controller.syncResource(context.Background(), key))

	// All three metric vectors persist after Delete (no-op behavior).
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 1, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 1, testutil.CollectAndCount(handler.lastTransitionTime))
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
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
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

	resourceID := resourceIDMetricLabel(op.ExternalID)
	subscriptionID := subscriptionIDMetricLabel(op.ExternalID)
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="provisioning",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="%s"} 1
`, resourceID, subscriptionID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

// TestOperationPhaseMetricsHandler_ResourceIDIsExternalIDNotCosmosID asserts
// that the resource_id label is the ARM resource id from op.ExternalID,
// not the cosmos doc id from op.GetResourceID(). This is the headline
// behavior change introduced by ARO-26795.
func TestOperationPhaseMetricsHandler_ResourceIDIsExternalIDNotCosmosID(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armResourceID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateProvisioning,
		armResourceID,
		now,
		now,
	)

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	// op.GetResourceID() returns the cosmos doc id, NOT the ARM id.
	// The new metric label must NOT match this.
	cosmosID := resourceIDMetricLabel(op.GetResourceID())
	require.Contains(t, cosmosID, "hcpoperationstatuses",
		"sanity: cosmos id should be the operationstatuses-prefixed string")

	armID := resourceIDMetricLabel(op.ExternalID)
	require.Equal(t, strings.ToLower(armResourceID), armID,
		"ExternalID should be the lowercased ARM id")
	require.NotEqual(t, cosmosID, armID,
		"sanity: cosmos id and ARM id must differ for this test to be meaningful")

	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="provisioning",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`, armID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

// TestOperationPhaseMetricsHandler_SkipsWhenExternalIDNil verifies that an
// operation with no ExternalID (which would happen if the always-set
// invariant ever broke) does not emit a metric series. The skip is
// silent at the metric layer; the controller logs an info entry to surface
// the unexpected state to operators.
func TestOperationPhaseMetricsHandler_SkipsWhenExternalIDNil(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	resourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op-no-ext-id"))
	op := &api.Operation{
		CosmosMetadata:     api.CosmosMetadata{ResourceID: resourceID},
		OperationID:        api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/op-no-ext-id")),
		Request:            api.OperationRequestCreate,
		Status:             arm.ProvisioningStateAccepted,
		StartTime:          now,
		LastTransitionTime: now,
		// ExternalID intentionally nil
	}

	handler, _ := newTestOperationHandler(t)
	ctx := logr.NewContext(context.Background(), logr.Discard())
	handler.Sync(ctx, op)

	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 0, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 0, testutil.CollectAndCount(handler.lastTransitionTime))
}

// TestOperationPhaseMetricsHandler_LowercasesResourceID verifies that ARM
// ids with mixed case (e.g. Microsoft.RedHatOpenShift) are emitted
// lowercased to match the convention used by the sibling resource state
// metrics.
func TestOperationPhaseMetricsHandler_LowercasesResourceID(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mixedCase := "/Subscriptions/SUB-1/ResourceGroups/RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/Cluster-MixedCase"
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		mixedCase,
		now,
		now,
	)

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="accepted",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`, strings.ToLower(mixedCase))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

// TestOperationPhaseMetricsHandler_SubscriptionIDFromExternalID verifies
// that subscription_id co-switches to op.ExternalID.SubscriptionID
// alongside resource_id. This is benign in production (cosmos doc and
// ARM id share a subscription) but the test pins the invariant so a
// future code path that breaks it surfaces here.
func TestOperationPhaseMetricsHandler_SubscriptionIDFromExternalID(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-target/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	// Both resource_id and subscription_id come from op.ExternalID,
	// not from op.GetResourceID() (whose subscription would be sub-1
	// from the cosmos doc id, which can differ from ExternalID's
	// subscription in malformed fixtures).
	expected := `# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="accepted",resource_id="/subscriptions/sub-target/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-target"} 1
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

// TestOperationPhaseMetricsHandler_DeleteIsNoOp verifies that
// handler.Delete does NOT remove series for the operation. See the
// Delete doc-comment for the rationale: Delete is intentionally a
// no-op because deleting by resource_id can blank a sibling
// operation's currently-emitted series.
func TestOperationPhaseMetricsHandler_DeleteIsNoOp(t *testing.T) {
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

	cosmosKey, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)

	handler.Delete(cosmosKey)

	// Series persists after Delete (no-op behavior).
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
}

// TestOperationPhaseMetricsHandler_MultipleOpsSameExternalIDCoexistByOperationType
// documents the design decision that multiple operations of DIFFERENT types
// sharing one ARM resource id coexist as independent Prometheus series. The
// handler emits one series per (resource_id, operation_type) combination;
// only operations of the SAME type collapse (last-emitted-labels-win).
//
// This ensures that e.g. a completed "create" operation does not clobber
// an in-flight "delete" operation's metrics on informer relists, which
// previously caused false alerts when the iteration order was unfavorable.
func TestOperationPhaseMetricsHandler_MultipleOpsSameExternalIDCoexistByOperationType(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	op1 := newTestOperation(t, "op-1", api.OperationRequestCreate, arm.ProvisioningStateSucceeded, armID, now, now)
	op2 := newTestOperation(t, "op-2", api.OperationRequestUpdate, arm.ProvisioningStateProvisioning, armID, now.Add(time.Hour), now.Add(time.Hour))

	handler, reg := newTestOperationHandler(t)

	// Two ops of different types on the same ARM id. After both Syncs,
	// both series are present because deletion is scoped to
	// resource_id + operation_type.
	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)

	expected := `# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="succeeded",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
backend_resource_operation_phase_info{operation_type="update",phase="provisioning",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
	require.Equal(t, 2, testutil.CollectAndCount(handler.phaseInfo))

	// The bug this test guards against is specifically about start_time and
	// last_transition_time being clobbered across operation types. Verify
	// these metric vecs also retain both series.
	require.Equal(t, 2, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 2, testutil.CollectAndCount(handler.lastTransitionTime))
}

// TestOperationPhaseMetricsHandler_SameOperationTypeCollapsesToOneSeries
// verifies that multiple operations of the SAME type on one ARM resource
// still collapse: the last-processed operation's labels win.
func TestOperationPhaseMetricsHandler_SameOperationTypeCollapsesToOneSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	op1 := newTestOperation(t, "op-1", api.OperationRequestUpdate, arm.ProvisioningStateSucceeded, armID, now, now)
	op2 := newTestOperation(t, "op-2", api.OperationRequestUpdate, arm.ProvisioningStateProvisioning, armID, now.Add(time.Hour), now.Add(time.Hour))

	handler, reg := newTestOperationHandler(t)

	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)

	// Only op2's labels remain (same operation_type, last writer wins).
	expected := `# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="update",phase="provisioning",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
}

// TestOperationPhaseMetricsHandler_DeleteOnSiblingDoesNotBlankActiveSeries
// is the direct regression guard for the bug a previous iteration of
// this PR introduced: when two operations share an ExternalID,
// Delete on the older terminal operation must NOT blank the newer
// operation's currently-emitted series.
// The fix is that Delete is a no-op; this test pins it.
func TestOperationPhaseMetricsHandler_DeleteOnSiblingDoesNotBlankActiveSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	// op1: completed Create still in cosmos TTL window.
	op1 := newTestOperation(t, "op-1", api.OperationRequestCreate, arm.ProvisioningStateSucceeded, armID, now, now)
	// op2: fresh Update on the same cluster, currently in flight.
	op2 := newTestOperation(t, "op-2", api.OperationRequestUpdate, arm.ProvisioningStateProvisioning, armID, now.Add(time.Hour), now.Add(time.Hour))

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)

	// Both ops coexist (different operation_type).
	require.Equal(t, 2, testutil.CollectAndCount(handler.phaseInfo))

	// op1 ages out of cosmos TTL: the controller framework calls
	// handler.Delete with op1's cosmos doc id. This must NOT blank
	// op2's series.
	op1CosmosKey, err := resourceIDStoreKeyForObject(op1)
	require.NoError(t, err)
	handler.Delete(op1CosmosKey)

	// Both series are still emitted (Delete is a no-op).
	require.Equal(t, 2, testutil.CollectAndCount(handler.phaseInfo))
	expected := `# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="succeeded",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
backend_resource_operation_phase_info{operation_type="update",phase="provisioning",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

// TestOperationPhaseMetricsHandler_NilOperationIDDoesNotBlankSibling
// guards against future regressions of the nil-OperationID branch
// in Sync. The branch must NOT call deleteByResourceIDAndOperationType,
// because a sibling operation may already own the emitted series for
// the shared ExternalID and operation type. Implicit child-resource
// cleanups (parent Delete cascades) produce nil-OperationID ops on
// child ARM ids in production cosmos shape.
func TestOperationPhaseMetricsHandler_NilOperationIDDoesNotBlankSibling(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	// op-A: explicit operation, owns the emitted series.
	opA := newTestOperation(t, "op-a", api.OperationRequestUpdate, arm.ProvisioningStateProvisioning, armID, now, now)

	// op-B: implicit operation (nil OperationID) on the same ARM resource.
	opB := newTestOperation(t, "op-b", api.OperationRequestDelete, arm.ProvisioningStateDeleting, armID, now.Add(time.Minute), now.Add(time.Minute))
	opB.OperationID = nil

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), opA)
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))

	// Sync op-B (nil OperationID, same ExternalID) must NOT blank
	// op-A's series.
	handler.Sync(context.Background(), opB)

	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
	expected := `# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="update",phase="provisioning",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

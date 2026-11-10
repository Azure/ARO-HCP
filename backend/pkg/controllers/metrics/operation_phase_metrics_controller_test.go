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

package metrics

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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestPhaseMetricLabel(t *testing.T) {
	tests := []struct {
		input    coreapi.ProvisioningState
		expected string
	}{
		{coreapi.ProvisioningStateAccepted, "accepted"},
		{coreapi.ProvisioningStateProvisioning, "provisioning"},
		{coreapi.ProvisioningStateUpdating, "updating"},
		{coreapi.ProvisioningStateDeleting, "deleting"},
		{coreapi.ProvisioningStateSucceeded, "succeeded"},
		{coreapi.ProvisioningStateFailed, "failed"},
		{coreapi.ProvisioningStateCanceled, "canceled"},
		{coreapi.ProvisioningStateAwaitingSecret, "awaitingsecret"},
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
			resourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
			expected:   "microsoft.redhatopenshift/hcpopenshiftclusters",
		},
		{
			name:       "nodepool resource type",
			resourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1")),
			expected:   "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools",
		},
		{
			name:       "externalauth resource type",
			resourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/externalAuths/ea-1")),
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
			resourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/SUB-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
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
		input    coreapi.OperationRequest
		expected string
	}{
		{coreapi.OperationRequestCreate, "create"},
		{coreapi.OperationRequestUpdate, "update"},
		{coreapi.OperationRequestDelete, "delete"},
		{coreapi.OperationRequestSystemAdminCredentialRequest, "requestcredential"},
		{coreapi.OperationRequestSystemAdminCredentialRevocation, "revokecredentials"},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			require.Equal(t, tt.expected, operationTypeMetricLabel(tt.input))
		})
	}
}

func newTestOperation(t *testing.T, opName string, request coreapi.OperationRequest, status coreapi.ProvisioningState, externalID string, startTime, lastTransition time.Time) *coreapi.Operation {
	t.Helper()

	operationID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName))
	resourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + opName))
	op := &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
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
		op.ExternalID = metadataapi.Must(azcorearm.ParseResourceID(externalID))
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	op.Status = coreapi.ProvisioningStateProvisioning
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(op))

	handler, reg := newTestOperationHandler(t)
	controller := &Controller[*coreapi.Operation]{
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

// TestOperationControllerSyncResource_DeleteClearsGauges verifies
// that the controller framework's syncResource path clears gauges
// when the operation is removed from the indexer (simulating TTL expiry).
func TestOperationControllerSyncResource_DeleteClearsGauges(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	indexer := cache.NewIndexer(resourceIDStoreKeyForObject, cache.Indexers{})
	require.NoError(t, indexer.Add(op))

	handler, _ := newTestOperationHandler(t)
	controller := &Controller[*coreapi.Operation]{
		name:    "OperationPhaseMetrics",
		indexer: indexer,
		handler: handler,
	}

	key, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)
	require.NoError(t, controller.syncResource(context.Background(), key))
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"phaseInfo should have 1 series after Sync")
	require.Equal(t, 1, testutil.CollectAndCount(handler.startTime),
		"startTime should have 1 series after Sync")
	require.Equal(t, 1, testutil.CollectAndCount(handler.lastTransitionTime),
		"lastTransitionTime should have 1 series after Sync")

	require.NoError(t, indexer.Delete(op))
	require.NoError(t, controller.syncResource(context.Background(), key))

	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"phaseInfo should be cleared after indexer removal")
	require.Equal(t, 0, testutil.CollectAndCount(handler.startTime),
		"startTime should be cleared after indexer removal")
	require.Equal(t, 0, testutil.CollectAndCount(handler.lastTransitionTime),
		"lastTransitionTime should be cleared after indexer removal")
}

func TestResourceIDStoreKeyForObject_MatchesMetaNamespaceKeyFuncForOperation(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
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
	resourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op-nil-id"))
	op := &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID:         metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")),
		Request:            coreapi.OperationRequestCreate,
		Status:             coreapi.ProvisioningStateAccepted,
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)
	op2 := newTestOperation(
		t,
		"op-2",
		coreapi.OperationRequestDelete,
		coreapi.ProvisioningStateDeleting,
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateProvisioning,
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateProvisioning,
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
	resourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op-no-ext-id"))
	op := &coreapi.Operation{
		CosmosMetadata:     coreapi.CosmosMetadata{ResourceID: resourceID},
		OperationID:        metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/op-no-ext-id")),
		Request:            coreapi.OperationRequestCreate,
		Status:             coreapi.ProvisioningStateAccepted,
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
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
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
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

// TestOperationPhaseMetricsHandler_DeleteClearsSoleOperation verifies
// that deleting the only operation for a (resourceID, operationType)
// pair clears all gauge series. This is the core ARO-28723 fix.
func TestOperationPhaseMetricsHandler_DeleteClearsSoleOperation(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"phaseInfo should have 1 series after Sync")

	cosmosKey, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)

	handler.Delete(cosmosKey)

	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"phaseInfo should be cleared after deleting the sole operation")
	require.Equal(t, 0, testutil.CollectAndCount(handler.startTime),
		"startTime should be cleared after deleting the sole operation")
	require.Equal(t, 0, testutil.CollectAndCount(handler.lastTransitionTime),
		"lastTransitionTime should be cleared after deleting the sole operation")
}

// TestOperationPhaseMetricsHandler_MultipleOpsSameExternalIDCoexistByOperationType
// documents the design decision that multiple operations of DIFFERENT types
// sharing one ARM resource id coexist as independent Prometheus series. The
// handler emits one series per (resource_id, operation_type) combination;
// only operations of the SAME type collapse to the newest retained attempt.
//
// This ensures that e.g. a completed "create" operation does not clobber
// an in-flight "delete" operation's metrics on informer relists, which
// previously caused false alerts when the iteration order was unfavorable.
func TestOperationPhaseMetricsHandler_MultipleOpsSameExternalIDCoexistByOperationType(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	op1 := newTestOperation(t, "op-1", coreapi.OperationRequestCreate, coreapi.ProvisioningStateSucceeded, armID, now, now)
	op2 := newTestOperation(t, "op-2", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateProvisioning, armID, now.Add(time.Hour), now.Add(time.Hour))

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
// still collapse to the newest retained attempt.
func TestOperationPhaseMetricsHandler_SameOperationTypeCollapsesToOneSeries(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	op1 := newTestOperation(t, "op-1", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateSucceeded, armID, now, now)
	op2 := newTestOperation(t, "op-2", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateProvisioning, armID, now.Add(time.Hour), now.Add(time.Hour))

	handler, reg := newTestOperationHandler(t)

	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)

	// Only op2's labels remain (same operation_type, newest attempt wins).
	expected := `# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="update",phase="provisioning",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
}

func requireOperationMetrics(t *testing.T, reg *prometheus.Registry, op *coreapi.Operation) {
	t.Helper()

	labels := fmt.Sprintf(`operation_type="%s",phase="%s",resource_id="%s",resource_type="%s",subscription_id="%s"`,
		operationTypeMetricLabel(op.Request), phaseMetricLabel(op.Status), resourceIDMetricLabel(op.ExternalID),
		resourceIDToTypeMetricLabel(op.ExternalID), subscriptionIDMetricLabel(op.ExternalID))
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{%s} 1
# HELP backend_resource_operation_last_transition_time_seconds Unix timestamp when the operation last changed phase.
# TYPE backend_resource_operation_last_transition_time_seconds gauge
backend_resource_operation_last_transition_time_seconds{%s} %d
`, labels, labels, op.LastTransitionTime.Unix())
	if !op.StartTime.IsZero() {
		expected += fmt.Sprintf(`# HELP backend_resource_operation_start_time_seconds Unix timestamp when the operation started.
# TYPE backend_resource_operation_start_time_seconds gauge
backend_resource_operation_start_time_seconds{%s} %d
`, labels, op.StartTime.Unix())
	}
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"backend_resource_operation_phase_info", "backend_resource_operation_start_time_seconds",
		"backend_resource_operation_last_transition_time_seconds"))
}

func TestOperationPhaseMetricsHandler_NewestOperationWinsRegardlessOfSyncOrder(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"
	for _, request := range []coreapi.OperationRequest{coreapi.OperationRequestCreate, coreapi.OperationRequestUpdate, coreapi.OperationRequestDelete} {
		t.Run(string(request), func(t *testing.T) {
			for _, order := range [][]int{{0, 1}, {1, 0}, {1, 0, 1, 0}} {
				t.Run(fmt.Sprint(order), func(t *testing.T) {
					// The older attempt transitions later, but must not replace the newer attempt.
					older := newTestOperation(t, "op-z", request, coreapi.ProvisioningStateSucceeded, armID, now, now.Add(2*time.Hour))
					newer := newTestOperation(t, "op-a", request, coreapi.ProvisioningStateFailed, armID, now.Add(time.Hour), now.Add(65*time.Minute))
					ops := []*coreapi.Operation{older, newer}
					handler, reg := newTestOperationHandler(t)
					for _, index := range order {
						handler.Sync(context.Background(), ops[index])
					}
					requireOperationMetrics(t, reg, newer)
				})
			}
		})
	}
}

func TestOperationPhaseMetricsHandler_EqualStartTimesUseStableKey(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"
	for _, start := range []time.Time{now, {}} {
		t.Run(start.String(), func(t *testing.T) {
			a := newTestOperation(t, "op-a", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateSucceeded, armID, start, now.Add(time.Hour))
			b := newTestOperation(t, "op-b", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateFailed, armID, start, now)
			for _, order := range [][]*coreapi.Operation{{a, b}, {b, a}} {
				handler, reg := newTestOperationHandler(t)
				for _, op := range order {
					handler.Sync(context.Background(), op)
				}
				requireOperationMetrics(t, reg, b)
			}
		})
	}
}

func TestOperationPhaseMetricsHandler_NewerRetryAndPhaseTransitions(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"
	failed := newTestOperation(t, "op-1", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateFailed, armID, now, now.Add(time.Minute))
	retry := newTestOperation(t, "op-2", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateProvisioning, armID, now.Add(time.Hour), now.Add(time.Hour))
	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), failed)
	handler.Sync(context.Background(), retry)
	handler.Sync(context.Background(), failed)
	requireOperationMetrics(t, reg, retry)

	retry.Status = coreapi.ProvisioningStateSucceeded
	retry.LastTransitionTime = now.Add(65 * time.Minute)
	handler.Sync(context.Background(), retry)
	handler.Sync(context.Background(), failed)
	requireOperationMetrics(t, reg, retry)
}

func TestOperationPhaseMetricsHandler_KnownStartTimeWinsOverMissing(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"
	unknownStart := newTestOperation(t, "op-z", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateSucceeded, armID, time.Time{}, now.Add(time.Hour))
	knownStart := newTestOperation(t, "op-a", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateFailed, armID, now, now)
	for _, order := range [][]*coreapi.Operation{{unknownStart, knownStart}, {knownStart, unknownStart}} {
		handler, reg := newTestOperationHandler(t)
		for _, op := range order {
			handler.Sync(context.Background(), op)
		}
		requireOperationMetrics(t, reg, knownStart)
	}
}

func TestOperationPhaseMetricsHandler_DeleteNewestSelectsNewestSurvivor(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"
	older := newTestOperation(t, "op-1", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateSucceeded, armID, now, now)
	middle := newTestOperation(t, "op-2", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateFailed, armID, now.Add(time.Hour), now.Add(time.Hour))
	newer := newTestOperation(t, "op-3", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateProvisioning, armID, now.Add(2*time.Hour), now.Add(2*time.Hour))
	handler, reg := newTestOperationHandler(t)
	for _, op := range []*coreapi.Operation{middle, newer, older} {
		handler.Sync(context.Background(), op)
	}
	requireOperationMetrics(t, reg, newer)

	handler.Delete(resourceIDMetricLabel(newer.GetResourceID()))
	requireOperationMetrics(t, reg, middle)
	handler.Sync(context.Background(), older)
	requireOperationMetrics(t, reg, middle)
	handler.Delete(resourceIDMetricLabel(older.GetResourceID()))
	requireOperationMetrics(t, reg, middle)
	handler.Delete(resourceIDMetricLabel(middle.GetResourceID()))
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo))
	require.Equal(t, 0, testutil.CollectAndCount(handler.startTime))
	require.Equal(t, 0, testutil.CollectAndCount(handler.lastTransitionTime))
}

// TestOperationPhaseMetricsHandler_DeleteDifferentTypeClearsOnlyExpired
// verifies that when two operations of DIFFERENT types share an ARM
// resource ID, deleting one clears only that type's gauge and leaves
// the other type's series intact.
func TestOperationPhaseMetricsHandler_DeleteDifferentTypeClearsOnlyExpired(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	// op1: completed Create still in cosmos TTL window.
	op1 := newTestOperation(t, "op-1", coreapi.OperationRequestCreate, coreapi.ProvisioningStateSucceeded, armID, now, now)
	// op2: fresh Update on the same cluster, currently in flight.
	op2 := newTestOperation(t, "op-2", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateProvisioning, armID, now.Add(time.Hour), now.Add(time.Hour))

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)

	// Both ops coexist (different operation_type).
	require.Equal(t, 2, testutil.CollectAndCount(handler.phaseInfo),
		"both operation types should coexist after Sync")

	// op1 ages out of cosmos TTL: Delete clears the "create" gauge
	// but must NOT touch op2's "update" gauge.
	op1CosmosKey, err := resourceIDStoreKeyForObject(op1)
	require.NoError(t, err)
	handler.Delete(op1CosmosKey)

	// Only the "update" series survives.
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"only the update series should survive after deleting the create operation")
	expected := `# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="update",phase="provisioning",resource_id="/subscriptions/sub-1/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-1",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

// TestOperationPhaseMetricsHandler_DeleteSameTypeSiblingPreservesGauge
// verifies that when two Cosmos docs share the same (resourceID, operationType),
// deleting an older sibling does not clear the newest operation's gauges.
func TestOperationPhaseMetricsHandler_DeleteSameTypeSiblingPreservesGauge(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	op1 := newTestOperation(t, "op-1", coreapi.OperationRequestCreate, coreapi.ProvisioningStateSucceeded, armID, now, now)
	op2 := newTestOperation(t, "op-2", coreapi.OperationRequestCreate, coreapi.ProvisioningStateFailed, armID, now.Add(time.Hour), now.Add(time.Hour))

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)

	// Same type collapses to the newest of the two retained operations.
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"same-type ops collapse to 1 series")

	op1Key, err := resourceIDStoreKeyForObject(op1)
	require.NoError(t, err)
	handler.Delete(op1Key)

	// The older sibling is removed; the newest operation still owns the gauges.
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should survive when a same-type sibling still exists")

	// Now delete the second; no operations remain.
	op2Key, err := resourceIDStoreKeyForObject(op2)
	require.NoError(t, err)
	handler.Delete(op2Key)

	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be cleared after the last same-type sibling is deleted")
}

// TestOperationPhaseMetricsHandler_RelistReEmitsSurvivorAfterSiblingExpiry
// simulates an informer relist cycle with two same-type operations where
// the newest operation expires first. Delete immediately selects the surviving
// sibling, and a later resync preserves that selection.
func TestOperationPhaseMetricsHandler_RelistReEmitsSurvivorAfterSiblingExpiry(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	op1 := newTestOperation(t, "op-1", coreapi.OperationRequestCreate, coreapi.ProvisioningStateSucceeded, armID, now, now)
	op2 := newTestOperation(t, "op-2", coreapi.OperationRequestCreate, coreapi.ProvisioningStateFailed, armID, now.Add(time.Hour), now.Add(time.Hour))

	handler, reg := newTestOperationHandler(t)

	// Initial sync: both operations tracked, op2 is the newest.
	handler.Sync(context.Background(), op1)
	handler.Sync(context.Background(), op2)
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"same-type ops collapse to 1 series")

	// The newest operation expires; op1 is now the newest retained operation.
	op2Key, err := resourceIDStoreKeyForObject(op2)
	require.NoError(t, err)
	handler.Delete(op2Key)
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should survive — op1 still tracked")

	// Simulate the next relist: the informer re-syncs op1 (the survivor).
	// This re-emits the gauge with op1's labels (succeeded, not failed).
	handler.Sync(context.Background(), op1)
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be re-emitted with survivor's labels after relist")

	resourceID := resourceIDMetricLabel(op1.ExternalID)
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_type="create",phase="succeeded",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters",subscription_id="sub-1"} 1
`, resourceID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"),
		"survivor's labels (succeeded) should replace the expired op's labels (failed)")

	// Final cleanup: op1 TTL-expires, gauge fully cleared.
	op1Key, err := resourceIDStoreKeyForObject(op1)
	require.NoError(t, err)
	handler.Delete(op1Key)
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be cleared after the last operation expires")
}

// TestOperationPhaseMetricsHandler_DoubleDeleteIsNoOp verifies that
// calling Delete twice for the same key does not panic or corrupt
// state. The second Delete finds the key already removed and returns.
func TestOperationPhaseMetricsHandler_DoubleDeleteIsNoOp(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateFailed,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	cosmosKey, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)

	handler.Delete(cosmosKey)
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be cleared after first Delete")

	// Second Delete on the same key — silent no-op.
	handler.Delete(cosmosKey)
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should remain cleared after double Delete")
}

// TestOperationPhaseMetricsHandler_DeleteUnknownKeyIsNoOp verifies
// that Delete with a key not present in the bookkeeper is a silent no-op.
func TestOperationPhaseMetricsHandler_DeleteUnknownKeyIsNoOp(t *testing.T) {
	handler, _ := newTestOperationHandler(t)

	// Delete on an empty handler — no panic, no side effects.
	handler.Delete("/subscriptions/sub-1/providers/microsoft.redhatopenshift/hcpoperationstatuses/nonexistent")

	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"no series should exist after deleting an unknown key")
}

// TestOperationPhaseMetricsHandler_ResyncIdempotency verifies that
// Sync called multiple times for the same operation retains only one
// snapshot, so a single Delete clears the gauges.
func TestOperationPhaseMetricsHandler_ResyncIdempotency(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, _ := newTestOperationHandler(t)

	// Simulate 3 relists — Sync called 3 times for the same operation.
	handler.Sync(context.Background(), op)
	handler.Sync(context.Background(), op)
	handler.Sync(context.Background(), op)

	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"3 syncs of the same op should produce 1 series")

	cosmosKey, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)
	handler.Delete(cosmosKey)

	// A single Delete should clear the gauges despite repeated Syncs.
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be cleared after a single Delete despite multiple Syncs")
}

// TestOperationPhaseMetricsHandler_SyncIdentityChangeUpdatesTracking
// exercises the defensive branch where the same cosmosKey is synced
// with a different (resourceID, operationType) identity. The snapshot
// moves to the new identity, clearing the old identity's gauges when
// it has no remaining operations.
func TestOperationPhaseMetricsHandler_SyncIdentityChangeUpdatesTracking(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	armID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	op := newTestOperation(t, "op-1", coreapi.OperationRequestCreate, coreapi.ProvisioningStateAccepted, armID, now, now)

	handler, _ := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"phaseInfo should have 1 series after initial Sync")

	// Mutate the operation's Request to simulate an identity change
	// on the same cosmosKey. This should not happen in production,
	// but the defensive branch must keep the maps consistent.
	op.Request = coreapi.OperationRequestUpdate
	handler.Sync(context.Background(), op)

	// The old "create" gauge should be cleared (sole contributor),
	// and a new "update" gauge should be emitted.
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"phaseInfo should still have 1 series after identity change")

	cosmosKey, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)
	handler.Delete(cosmosKey)

	// The "update" gauges should be cleared (sole operation for the new identity).
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"phaseInfo should be cleared after Delete")
	require.Equal(t, 0, len(handler.operationsBookkeeper),
		"operationsBookkeeper should be empty after final Delete")
	require.Equal(t, 0, len(handler.operationsByIdentity),
		"operationsByIdentity should be empty after final Delete")
}

// TestOperationPhaseMetricsHandler_SyncReestablishesAfterDelete
// verifies that after an operation is deleted and its gauge cleared,
// a new Sync for the same cosmosKey re-establishes tracking and
// re-emits the gauge. Simulates an operation being recreated.
func TestOperationPhaseMetricsHandler_SyncReestablishesAfterDelete(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(
		t,
		"op-1",
		coreapi.OperationRequestCreate,
		coreapi.ProvisioningStateFailed,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now,
		now,
	)

	handler, _ := newTestOperationHandler(t)

	// First lifecycle: Sync → Delete → gauge cleared.
	handler.Sync(context.Background(), op)
	cosmosKey, err := resourceIDStoreKeyForObject(op)
	require.NoError(t, err)
	handler.Delete(cosmosKey)
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be cleared after Delete")

	// Second lifecycle: Sync re-establishes tracking.
	handler.Sync(context.Background(), op)
	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be re-emitted after re-Sync")

	// And Delete works again.
	handler.Delete(cosmosKey)
	require.Equal(t, 0, testutil.CollectAndCount(handler.phaseInfo),
		"gauge should be cleared after second Delete")
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
	opA := newTestOperation(t, "op-a", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateProvisioning, armID, now, now)

	// op-B: implicit operation (nil OperationID) on the same ARM resource.
	opB := newTestOperation(t, "op-b", coreapi.OperationRequestDelete, coreapi.ProvisioningStateDeleting, armID, now.Add(time.Minute), now.Add(time.Minute))
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

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

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const nodePoolExternalID = "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/pool-1"

func newNodePoolOperation(t *testing.T, opName string, status coreapi.ProvisioningState, operationError *coreapi.CloudErrorBody) *coreapi.Operation {
	t.Helper()
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, opName, coreapi.OperationRequestUpdate, status, nodePoolExternalID, now, now)
	op.Error = operationError
	return op
}

func TestOperationPhaseMetricsHandler_FaultLabel(t *testing.T) {
	tests := []struct {
		name      string
		status    coreapi.ProvisioningState
		opError   *coreapi.CloudErrorBody
		wantFault string
	}{
		{"failed with quota exceeded is the customer's", coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{Code: coreapi.CloudErrorCodeQuotaExceeded}, faultCustomer},
		{"failed with deadline exceeded is the system's", coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{Code: coreapi.CloudErrorCodeDeadlineExceeded}, faultSystem},
		{"failed with internal server error is the system's", coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{Code: coreapi.CloudErrorCodeInternalServerError}, faultSystem},
		{"failed with an unknown code is the system's", coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{Code: "SomethingNew"}, faultSystem},
		{"failed with an empty code is the system's", coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{}, faultSystem},
		{"failed without an error body is the system's", coreapi.ProvisioningStateFailed, nil, faultSystem},

		// Only failed operations have a fault. The label is empty, so absent in Prometheus.
		{"updating has no fault", coreapi.ProvisioningStateUpdating, nil, ""},
		{"succeeded has no fault", coreapi.ProvisioningStateSucceeded, nil, ""},
		{"updating ignores a stray error body", coreapi.ProvisioningStateUpdating, &coreapi.CloudErrorBody{Code: coreapi.CloudErrorCodeQuotaExceeded}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := newNodePoolOperation(t, "op-1", tt.status, tt.opError)

			handler, reg := newTestOperationHandler(t)
			handler.Sync(context.Background(), op)

			expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{fault="%s",operation_type="update",phase="%s",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters/nodepools",subscription_id="%s"} 1
`, tt.wantFault, phaseMetricLabel(tt.status), resourceIDMetricLabel(op.ExternalID), subscriptionIDMetricLabel(op.ExternalID))
			require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
		})
	}
}

// Queries such as the stuck operation alerts combine the three gauges without
// on() or ignoring(), so every series for an operation must carry the same labels.
func TestOperationPhaseMetricsHandler_FaultLabelIsIdenticalAcrossAllThreeGauges(t *testing.T) {
	op := newNodePoolOperation(t, "op-1", coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{Code: coreapi.CloudErrorCodeQuotaExceeded})

	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), op)

	families, err := reg.Gather()
	require.NoError(t, err)

	labelSets := map[string]string{}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "backend_resource_operation_") {
			continue
		}
		require.Len(t, family.GetMetric(), 1, family.GetName())
		var pairs []string
		for _, l := range family.GetMetric()[0].GetLabel() {
			pairs = append(pairs, l.GetName()+"="+l.GetValue())
		}
		labelSets[family.GetName()] = strings.Join(pairs, ",")
	}
	require.Len(t, labelSets, 3)
	require.Contains(t, labelSets["backend_resource_operation_phase_info"], "fault=customer")
	require.Equal(t, labelSets["backend_resource_operation_phase_info"], labelSets["backend_resource_operation_start_time_seconds"])
	require.Equal(t, labelSets["backend_resource_operation_phase_info"], labelSets["backend_resource_operation_last_transition_time_seconds"])
}

func TestOperationPhaseMetricsHandler_FaultClearsWhenOperationReplaced(t *testing.T) {
	handler, reg := newTestOperationHandler(t)
	handler.Sync(context.Background(), newNodePoolOperation(t, "op-1", coreapi.ProvisioningStateFailed, &coreapi.CloudErrorBody{Code: coreapi.CloudErrorCodeQuotaExceeded}))

	// A later update to the same node pool succeeds. Sync clears the previous
	// series for the same resource and operation type before emitting.
	now := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	succeeded := newTestOperation(t, "op-1", coreapi.OperationRequestUpdate, coreapi.ProvisioningStateSucceeded, nodePoolExternalID, now, now)
	handler.Sync(context.Background(), succeeded)

	require.Equal(t, 1, testutil.CollectAndCount(handler.phaseInfo))
	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{fault="",operation_type="update",phase="succeeded",resource_id="%s",resource_type="microsoft.redhatopenshift/hcpopenshiftclusters/nodepools",subscription_id="sub-1"} 1
`, resourceIDMetricLabel(succeeded.ExternalID))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_resource_operation_phase_info"))
}

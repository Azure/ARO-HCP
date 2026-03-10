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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type mockOperationLister struct {
	ops []*api.Operation
	err error
}

func (m *mockOperationLister) List(_ context.Context) ([]*api.Operation, error) {
	return m.ops, m.err
}

func testParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return rid
}

func TestOperationIDHash(t *testing.T) {
	t.Run("returns 16 hex characters", func(t *testing.T) {
		h := OperationIDHash("test-operation")
		assert.Len(t, h, 16)
		for _, c := range h {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"expected hex character, got %c", c)
		}
	})

	t.Run("is deterministic", func(t *testing.T) {
		h1 := OperationIDHash("same-input")
		h2 := OperationIDHash("same-input")
		assert.Equal(t, h1, h2)
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		h1 := OperationIDHash("input-a")
		h2 := OperationIDHash("input-b")
		assert.NotEqual(t, h1, h2)
	})

	t.Run("handles empty string", func(t *testing.T) {
		h := OperationIDHash("")
		assert.Len(t, h, 16)
	})
}

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
			externalID: testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"),
			expected:   "cluster",
		},
		{
			name:       "nodepool resource type",
			externalID: testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1"),
			expected:   "nodepool",
		},
		{
			name:       "externalauth resource type",
			externalID: testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/externalAuths/ea-1"),
			expected:   "externalauth",
		},
		{
			name:       "unknown resource type",
			externalID: testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.SomeOther/someResource/foo"),
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
	operationID := testParseResourceID(t,
		"/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/"+opName)
	resourceID := testParseResourceID(t,
		"/subscriptions/sub-1/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/"+opName)
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
		op.ExternalID = testParseResourceID(t, externalID)
	}
	return op
}

func TestCollect_EmitsAllThreeMetrics(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	lister := &mockOperationLister{
		ops: []*api.Operation{
			newTestOperation(t, "op-1",
				api.OperationRequestCreate,
				arm.ProvisioningStateAccepted,
				"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
				now, now),
		},
	}

	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())
	count := testutil.CollectAndCount(c)
	assert.Equal(t, 3, count, "expected 3 metrics (phase_info + start_time + last_transition_time)")
}

func TestCollect_SkipsNilOperationID(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	lister := &mockOperationLister{
		ops: []*api.Operation{
			{
				OperationID:        nil,
				ExternalID:         testParseResourceID(t, "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"),
				Request:            api.OperationRequestCreate,
				Status:             arm.ProvisioningStateAccepted,
				StartTime:          now,
				LastTransitionTime: now,
			},
		},
	}

	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())
	count := testutil.CollectAndCount(c)
	assert.Equal(t, 0, count, "expected 0 metrics for operation with nil OperationID")
}

func TestCollect_SkipsZeroTimestamps(t *testing.T) {
	lister := &mockOperationLister{
		ops: []*api.Operation{
			newTestOperation(t, "op-1",
				api.OperationRequestCreate,
				arm.ProvisioningStateAccepted,
				"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
				time.Time{}, time.Time{}),
		},
	}

	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())
	count := testutil.CollectAndCount(c)
	assert.Equal(t, 1, count, "expected only phase_info metric when timestamps are zero")
}

func TestCollect_HandlesListError(t *testing.T) {
	lister := &mockOperationLister{
		err: errors.New("database unavailable"),
	}

	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())
	count := testutil.CollectAndCount(c)
	assert.Equal(t, 0, count, "expected 0 metrics when List returns error")
}

func TestCollect_MultipleOperations(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	lister := &mockOperationLister{
		ops: []*api.Operation{
			newTestOperation(t, "op-1",
				api.OperationRequestCreate,
				arm.ProvisioningStateAccepted,
				"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
				now, now),
			newTestOperation(t, "op-2",
				api.OperationRequestDelete,
				arm.ProvisioningStateDeleting,
				"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1/nodePools/np-1",
				now, now),
		},
	}

	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())
	count := testutil.CollectAndCount(c)
	assert.Equal(t, 6, count, "expected 6 metrics for 2 operations with timestamps")
}

func TestCollect_VerifiesLabelValues(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	hash := OperationIDHash("op-1")
	lister := &mockOperationLister{
		ops: []*api.Operation{
			newTestOperation(t, "op-1",
				api.OperationRequestCreate,
				arm.ProvisioningStateProvisioning,
				"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
				now, now),
		},
	}

	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())

	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_id_hash="%s",operation_type="create",phase="provisioning",resource_type="cluster"} 1
`, hash)

	err := testutil.CollectAndCompare(c, strings.NewReader(expected), "backend_resource_operation_phase_info")
	require.NoError(t, err)
}

func TestCollect_NilExternalIDUsesUnknownResourceType(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	hash := OperationIDHash("op-no-external")
	lister := &mockOperationLister{
		ops: []*api.Operation{
			newTestOperation(t, "op-no-external",
				api.OperationRequestCreate,
				arm.ProvisioningStateAccepted,
				"",
				now, now),
		},
	}

	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())

	expected := fmt.Sprintf(`# HELP backend_resource_operation_phase_info Current phase of each operation (value is always 1).
# TYPE backend_resource_operation_phase_info gauge
backend_resource_operation_phase_info{operation_id_hash="%s",operation_type="create",phase="accepted",resource_type="unknown"} 1
`, hash)

	err := testutil.CollectAndCompare(c, strings.NewReader(expected), "backend_resource_operation_phase_info")
	require.NoError(t, err)
}

func TestCollect_CleansUpSeenHashes(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := newTestOperation(t, "op-1",
		api.OperationRequestCreate,
		arm.ProvisioningStateAccepted,
		"/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		now, now)

	lister := &mockOperationLister{ops: []*api.Operation{op}}
	reg := prometheus.NewRegistry()
	c := NewOperationPhaseCollector(reg, lister, logr.Discard())

	// First collect: hash gets added to seen.
	testutil.CollectAndCount(c)
	c.mu.Lock()
	assert.Len(t, c.seen, 1, "expected 1 seen hash after first collect")
	c.mu.Unlock()

	// Remove the operation from the lister.
	lister.ops = nil

	// Second collect: hash gets cleaned up.
	testutil.CollectAndCount(c)
	c.mu.Lock()
	assert.Len(t, c.seen, 0, "expected 0 seen hashes after operation is removed")
	c.mu.Unlock()
}

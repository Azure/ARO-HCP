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

package operationcontrollers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/tj/assert"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClearConditionsIfNewOperation(t *testing.T) {
	createStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	updateStart := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	createOp := &api.Operation{
		Request:   api.OperationRequestCreate,
		StartTime: createStart,
	}
	updateOp := &api.Operation{
		Request:   api.OperationRequestUpdate,
		StartTime: updateStart,
	}
	deleteOp := &api.Operation{
		Request:   api.OperationRequestDelete,
		StartTime: updateStart,
	}

	tests := []struct {
		name       string
		conditions []api.Condition
		op         *api.Operation
		wantNil    bool
	}{
		{
			name:       "no existing conditions, create op",
			conditions: nil,
			op:         createOp,
			wantNil:    true,
		},
		{
			name: "same operation, matching Accepted timestamp",
			conditions: []api.Condition{
				{Type: "Accepted", Status: api.ConditionFalse, LastTransitionTime: createStart},
				{Type: "Provisioning", Status: api.ConditionTrue, LastTransitionTime: createStart.Add(time.Minute)},
			},
			op:      createOp,
			wantNil: false,
		},
		{
			name: "new operation, stale Accepted timestamp from prior create",
			conditions: []api.Condition{
				{Type: "Accepted", Status: api.ConditionFalse, LastTransitionTime: createStart},
				{Type: "Succeeded", Status: api.ConditionTrue, LastTransitionTime: createStart.Add(5 * time.Minute)},
			},
			op:      updateOp,
			wantNil: true,
		},
		{
			name: "delete operation, no existing Deleting condition",
			conditions: []api.Condition{
				{Type: "Accepted", Status: api.ConditionFalse, LastTransitionTime: createStart},
				{Type: "Succeeded", Status: api.ConditionTrue, LastTransitionTime: createStart.Add(5 * time.Minute)},
			},
			op:      deleteOp,
			wantNil: true,
		},
		{
			name: "same delete operation, matching Deleting timestamp",
			conditions: []api.Condition{
				{Type: "Deleting", Status: api.ConditionTrue, LastTransitionTime: updateStart},
			},
			op:      deleteOp,
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := tt.conditions
			clearConditionsIfNewOperation(&conditions, tt.op)
			if tt.wantNil {
				assert.Nil(t, conditions, "expected conditions to be cleared")
			} else {
				assert.NotNil(t, conditions, "expected conditions to be preserved")
				assert.Equal(t, len(tt.conditions), len(conditions), "expected conditions length to be unchanged")
			}
		})
	}
}

func TestConvertClusterStatus(t *testing.T) {
	// FIXME These tests are all tentative until the new "/api/aro_hcp/v1" OCM
	//       API is available. What's here now is a best guess at converting
	//       ClusterStatus from the "/api/aro_hcp/v1alpha1" API.
	//
	//       Also note, the particular error codes and messages to expect from
	//       Cluster Service is complete guesswork at the moment so we're only
	//       testing whether or not a cloud error is returned and not checking
	//       its content.

	tests := []struct {
		name                     string
		clusterState             arohcpv1alpha1.ClusterState
		currentProvisioningState arm.ProvisioningState
		updatedProvisioningState arm.ProvisioningState
		expectCloudError         bool
		expectConversionError    bool
		internalId               ocm.InternalID
	}{
		{
			name:                     "Convert ClusterStateError",
			clusterState:             arohcpv1alpha1.ClusterStateError,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateFailed,
			expectCloudError:         true,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateHibernating",
			clusterState:             arohcpv1alpha1.ClusterStateHibernating,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateInstalling",
			clusterState:             arohcpv1alpha1.ClusterStateInstalling,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateProvisioning,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending (while not accepted)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			currentProvisioningState: arm.ProvisioningStateFailed,
			updatedProvisioningState: arm.ProvisioningStateFailed,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStatePoweringDown",
			clusterState:             arohcpv1alpha1.ClusterStatePoweringDown,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateReady",
			clusterState:             arohcpv1alpha1.ClusterStateReady,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateSucceeded,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateUpdating",
			clusterState:             arohcpv1alpha1.ClusterStateUpdating,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateUpdating,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateResuming",
			clusterState:             arohcpv1alpha1.ClusterStateResuming,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateUninstalling",
			clusterState:             arohcpv1alpha1.ClusterStateUninstalling,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateDeleting,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateUnknown",
			clusterState:             arohcpv1alpha1.ClusterStateUnknown,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateValidating (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateValidating (while not accepted)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			currentProvisioningState: arm.ProvisioningStateFailed,
			updatedProvisioningState: arm.ProvisioningStateFailed,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateWaiting",
			clusterState:             arohcpv1alpha1.ClusterStateWaiting,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert unexpected cluster state",
			clusterState:             arohcpv1alpha1.ClusterState("unexpected cluster state"),
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
				State(tt.clusterState).
				Build()
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			op := &api.Operation{
				InternalID: tt.internalId,
				Status:     tt.currentProvisioningState,
			}

			opState, opError, err := convertClusterStatus(ctx, nil, op, clusterStatus)

			assert.Equal(t, tt.updatedProvisioningState, opState)

			if tt.expectCloudError {
				assert.NotNil(t, opError)
			} else {
				assert.Nil(t, opError)
			}

			if tt.expectConversionError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

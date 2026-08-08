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

package operationutils

import (
	"context"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/tj/assert"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

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
		operationRequest         coreapi.OperationRequest
		currentProvisioningState coreapi.ProvisioningState
		updatedProvisioningState coreapi.ProvisioningState
		expectCloudError         bool
		expectConversionError    bool
		internalId               ocm.InternalID
	}{
		{
			name:                     "Convert ClusterStateError",
			clusterState:             arohcpv1alpha1.ClusterStateError,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateFailed,
			expectCloudError:         true,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateHibernating",
			clusterState:             arohcpv1alpha1.ClusterStateHibernating,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateInstalling",
			clusterState:             arohcpv1alpha1.ClusterStateInstalling,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateProvisioning,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending create (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestCreate,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending create (while provisioning)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestCreate,
			currentProvisioningState: coreapi.ProvisioningStateProvisioning,
			updatedProvisioningState: coreapi.ProvisioningStateProvisioning,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending create (while failed)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestCreate,
			currentProvisioningState: coreapi.ProvisioningStateFailed,
			updatedProvisioningState: coreapi.ProvisioningStateFailed,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStatePending update (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestUpdate,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending update (while updating)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestUpdate,
			currentProvisioningState: coreapi.ProvisioningStateUpdating,
			updatedProvisioningState: coreapi.ProvisioningStateUpdating,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending update (while provisioning)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestUpdate,
			currentProvisioningState: coreapi.ProvisioningStateProvisioning,
			updatedProvisioningState: coreapi.ProvisioningStateProvisioning,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStatePending delete (while deleting)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestDelete,
			currentProvisioningState: coreapi.ProvisioningStateDeleting,
			updatedProvisioningState: coreapi.ProvisioningStateDeleting,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending delete (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequestDelete,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStatePending unrecognized request",
			clusterState:             arohcpv1alpha1.ClusterStatePending,
			operationRequest:         coreapi.OperationRequest("unexpected"),
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStatePoweringDown",
			clusterState:             arohcpv1alpha1.ClusterStatePoweringDown,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateReady",
			clusterState:             arohcpv1alpha1.ClusterStateReady,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateSucceeded,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateUpdating",
			clusterState:             arohcpv1alpha1.ClusterStateUpdating,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateUpdating,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateResuming",
			clusterState:             arohcpv1alpha1.ClusterStateResuming,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateUninstalling",
			clusterState:             arohcpv1alpha1.ClusterStateUninstalling,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateDeleting,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateUnknown",
			clusterState:             arohcpv1alpha1.ClusterStateUnknown,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateValidating create (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestCreate,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateValidating create (while provisioning)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestCreate,
			currentProvisioningState: coreapi.ProvisioningStateProvisioning,
			updatedProvisioningState: coreapi.ProvisioningStateProvisioning,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateValidating create (while failed)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestCreate,
			currentProvisioningState: coreapi.ProvisioningStateFailed,
			updatedProvisioningState: coreapi.ProvisioningStateFailed,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateValidating update (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestUpdate,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateValidating update (while updating)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestUpdate,
			currentProvisioningState: coreapi.ProvisioningStateUpdating,
			updatedProvisioningState: coreapi.ProvisioningStateUpdating,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateValidating update (while provisioning)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestUpdate,
			currentProvisioningState: coreapi.ProvisioningStateProvisioning,
			updatedProvisioningState: coreapi.ProvisioningStateProvisioning,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateValidating delete (while deleting)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestDelete,
			currentProvisioningState: coreapi.ProvisioningStateDeleting,
			updatedProvisioningState: coreapi.ProvisioningStateDeleting,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateValidating delete (while accepted)",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequestDelete,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateValidating unrecognized request",
			clusterState:             arohcpv1alpha1.ClusterStateValidating,
			operationRequest:         coreapi.OperationRequest("unexpected"),
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateWaiting",
			clusterState:             arohcpv1alpha1.ClusterStateWaiting,
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert unexpected cluster state",
			clusterState:             arohcpv1alpha1.ClusterState("unexpected cluster state"),
			currentProvisioningState: coreapi.ProvisioningStateAccepted,
			updatedProvisioningState: coreapi.ProvisioningStateAccepted,
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

			op := &coreapi.Operation{
				InternalID: tt.internalId,
				Request:    tt.operationRequest,
				Status:     tt.currentProvisioningState,
			}

			opState, opError, err := ConvertClusterStatus(ctx, nil, op, clusterStatus, tt.internalId)

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

	t.Run("Convert ClusterStateError with OCM4001 during create stays Provisioning", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
		mockCSClient.EXPECT().
			GetClusterInflightChecks(gomock.Any(), gomock.Any()).
			Return(&arohcpv1alpha1.InflightCheckList{}, nil)

		clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
			State(arohcpv1alpha1.ClusterStateError).
			ProvisionErrorCode(InflightChecksFailedProvisionErrorCode).
			ProvisionErrorMessage("inflight checks failed").
			Build()
		if err != nil {
			t.Fatal(err)
		}

		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		op := &api.Operation{
			Request: api.OperationRequestCreate,
			Status:  arm.ProvisioningStateAccepted,
		}

		opState, opError, err := ConvertClusterStatus(ctx, mockCSClient, op, clusterStatus, ocm.InternalID{})

		assert.Equal(t, arm.ProvisioningStateProvisioning, opState)
		assert.NotNil(t, opError)
		assert.NoError(t, err)
	})

	t.Run("Convert ClusterStateError with OCM4001 during update stays Failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
		mockCSClient.EXPECT().
			GetClusterInflightChecks(gomock.Any(), gomock.Any()).
			Return(&arohcpv1alpha1.InflightCheckList{}, nil)

		clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
			State(arohcpv1alpha1.ClusterStateError).
			ProvisionErrorCode(InflightChecksFailedProvisionErrorCode).
			ProvisionErrorMessage("inflight checks failed").
			Build()
		if err != nil {
			t.Fatal(err)
		}

		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		op := &api.Operation{
			Request: api.OperationRequestUpdate,
			Status:  arm.ProvisioningStateUpdating,
		}

		opState, opError, err := ConvertClusterStatus(ctx, mockCSClient, op, clusterStatus, ocm.InternalID{})

		assert.Equal(t, arm.ProvisioningStateFailed, opState)
		assert.NotNil(t, opError)
		assert.NoError(t, err)
	})

	t.Run("Convert ClusterStateError with non-OCM4001 during create stays Failed", func(t *testing.T) {
		clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
			State(arohcpv1alpha1.ClusterStateError).
			ProvisionErrorCode("ERR001").
			ProvisionErrorMessage("some other error").
			Build()
		if err != nil {
			t.Fatal(err)
		}

		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		op := &api.Operation{
			Request: api.OperationRequestCreate,
			Status:  arm.ProvisioningStateAccepted,
		}

		opState, opError, err := ConvertClusterStatus(ctx, nil, op, clusterStatus, ocm.InternalID{})

		assert.Equal(t, arm.ProvisioningStateFailed, opState)
		assert.NotNil(t, opError)
		assert.NoError(t, err)
	})
}

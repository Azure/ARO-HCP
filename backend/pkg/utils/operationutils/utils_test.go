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

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/tj/assert"

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
}

func TestConvertInflightCheck(t *testing.T) {
	tests := []struct {
		name         string
		details      map[string]interface{}
		expectedCode string
	}{
		{
			name: "quota error message gets QuotaExceeded code",
			details: map[string]interface{}{
				"error": "insufficient public IP address quota: required 2, available 0",
			},
			expectedCode: coreapi.CloudErrorCodeQuotaExceeded,
		},
		{
			name: "QuotaExceeded ARM error code in message",
			details: map[string]interface{}{
				"error": "QuotaExceeded: Operation could not be completed as it results in exceeding approved standardDSv3Family Cores quota",
			},
			expectedCode: coreapi.CloudErrorCodeQuotaExceeded,
		},
		{
			name: "PublicIPCountLimitReached in message",
			details: map[string]interface{}{
				"error": "PublicIPCountLimitReached: Cannot create more than 300 public IP addresses",
			},
			expectedCode: coreapi.CloudErrorCodeQuotaExceeded,
		},
		{
			name: "OverconstrainedZonalAllocationRequest in message",
			details: map[string]interface{}{
				"error": "OverconstrainedZonalAllocationRequest: The required resources are not available in zone 1",
			},
			expectedCode: coreapi.CloudErrorCodeQuotaExceeded,
		},
		{
			name: "non-quota error message gets InternalServerError code",
			details: map[string]interface{}{
				"error": "failed to create hosted cluster: internal service error",
			},
			expectedCode: coreapi.CloudErrorCodeInternalServerError,
		},
		{
			name:         "missing error details gets InternalServerError code",
			details:      map[string]interface{}{},
			expectedCode: coreapi.CloudErrorCodeInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := arohcpv1alpha1.NewInflightCheck().
				Name("test-check").
				State(arohcpv1alpha1.InflightCheckStateFailed)
			if tt.details != nil {
				builder = builder.Details(tt.details)
			}
			inflightCheck, err := builder.Build()
			assert.NoError(t, err)

			result := convertInflightCheck(inflightCheck, logr.Discard())
			assert.Equal(t, tt.expectedCode, result.Code)
		})
	}
}

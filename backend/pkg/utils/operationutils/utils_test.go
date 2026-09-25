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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/tj/assert"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/ocm"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
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

func TestExternalAuthStateTransitionsTotal(t *testing.T) {
	fixture := operationtesting.NewExternalAuthTestFixture()
	testTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(testTime)

	tests := []struct {
		name string
		// existingExternalAuth is the resource already in Cosmos before
		// UpdateOperationStatus runs.
		existingExternalAuth *coreapi.HCPOpenShiftClusterExternalAuth
		// existingOperation is the operation document already in Cosmos.
		existingOperation *coreapi.Operation
		// newOperationStatus is the target provisioning state passed to
		// UpdateOperationStatus.
		newOperationStatus coreapi.ProvisioningState
		// expectCounter is the expected total count of all counter series
		// after the call. 0 means no series were created.
		expectCounter int
		// expectFromState / expectToState are the expected label values
		// when expectCounter > 0.
		expectFromState string
		expectToState   string
	}{
		{
			name: "resource transitions from Provisioning to Succeeded",
			existingExternalAuth: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				ea := fixture.NewExternalAuth()
				ea.Properties.ProvisioningState = coreapi.ProvisioningStateProvisioning
				return ea
			}(),
			existingOperation: func() *coreapi.Operation {
				op := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
				op.Status = coreapi.ProvisioningStateProvisioning
				return op
			}(),
			newOperationStatus: coreapi.ProvisioningStateSucceeded,
			expectCounter:      1,
			expectFromState:    "provisioning",
			expectToState:      "succeeded",
		},
		{
			name: "delete operation: resource transitions from Succeeded to Deleting",
			existingExternalAuth: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				ea := fixture.NewExternalAuth()
				ea.Properties.ProvisioningState = coreapi.ProvisioningStateSucceeded
				return ea
			}(),
			existingOperation: func() *coreapi.Operation {
				op := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)
				op.Status = coreapi.ProvisioningStateAccepted
				return op
			}(),
			newOperationStatus: coreapi.ProvisioningStateDeleting,
			expectCounter:      1,
			expectFromState:    "succeeded",
			expectToState:      "deleting",
		},
		{
			name: "resource already at same non-terminal state — no update, no counter",
			existingExternalAuth: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				ea := fixture.NewExternalAuth()
				ea.Properties.ProvisioningState = coreapi.ProvisioningStateProvisioning
				return ea
			}(),
			existingOperation: func() *coreapi.Operation {
				op := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
				op.Status = coreapi.ProvisioningStateAccepted
				return op
			}(),
			newOperationStatus: coreapi.ProvisioningStateProvisioning,
			expectCounter:      0,
		},
		{
			name: "different active operation owns resource — no update, no counter",
			existingExternalAuth: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				ea := fixture.NewExternalAuth()
				ea.Properties.ProvisioningState = coreapi.ProvisioningStateProvisioning
				ea.ServiceProviderProperties.ActiveOperationID = "other-operation"
				return ea
			}(),
			existingOperation: func() *coreapi.Operation {
				op := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
				op.Status = coreapi.ProvisioningStateProvisioning
				return op
			}(),
			newOperationStatus: coreapi.ProvisioningStateSucceeded,
			expectCounter:      0,
		},
		{
			name: "nil ExternalID — no counter",
			existingExternalAuth: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				ea := fixture.NewExternalAuth()
				ea.Properties.ProvisioningState = coreapi.ProvisioningStateProvisioning
				return ea
			}(),
			existingOperation: func() *coreapi.Operation {
				op := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
				op.Status = coreapi.ProvisioningStateProvisioning
				op.ExternalID = nil
				return op
			}(),
			newOperationStatus: coreapi.ProvisioningStateSucceeded,
			expectCounter:      0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ExternalAuthStateTransitionsTotal.Reset()

			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{fixture.NewCluster()}
			if tc.existingExternalAuth != nil {
				resources = append(resources, tc.existingExternalAuth)
			}
			resources = append(resources, tc.existingOperation)

			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			// Re-read the operation from the mock DB so it has the
			// CosmosETag assigned by Create — UpdateOperationStatus
			// uses a transactional replace that requires a non-empty etag.
			storedOp, err := mockDB.Operations(tc.existingOperation.OperationID.SubscriptionID).Get(ctx, tc.existingOperation.OperationID.Name)
			require.NoError(t, err)

			err = UpdateOperationStatus(
				ctx,
				utilsclock.PassiveClock(fakeClock),
				mockDB,
				storedOp,
				tc.newOperationStatus,
				nil,
				nil,
			)
			require.NoError(t, err)

			seriesCount := testutil.CollectAndCount(ExternalAuthStateTransitionsTotal)
			if tc.expectCounter == 0 {
				assert.Equal(t, 0, seriesCount, "expected no counter series")
			} else {
				count := testutil.ToFloat64(ExternalAuthStateTransitionsTotal.WithLabelValues(
					tc.expectFromState,
					tc.expectToState,
					"microsoft.redhatopenshift/hcpopenshiftclusters/externalauths",
				))
				assert.Equal(t, float64(tc.expectCounter), count,
					"expected counter=%d for from_state=%s, to_state=%s",
					tc.expectCounter, tc.expectFromState, tc.expectToState)
			}
		})
	}
}

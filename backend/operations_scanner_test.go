// Copyright 2025 Microsoft Corporation
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

package main

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestSetDeleteOperationAsCompleted(t *testing.T) {
	tests := []struct {
		name                    string
		operationStatus         arm.ProvisioningState
		resourceDocPresent      bool
		expectAsyncNotification bool
		expectError             bool
	}{
		{
			name:                    "Database updated properly",
			operationStatus:         arm.ProvisioningStateDeleting,
			resourceDocPresent:      true,
			expectAsyncNotification: true,
			expectError:             false,
		},
		{
			name:                    "Resource already deleted",
			operationStatus:         arm.ProvisioningStateDeleting,
			resourceDocPresent:      false,
			expectAsyncNotification: true,
			expectError:             false,
		},
		{
			name:                    "Operation already succeeded",
			operationStatus:         arm.ProvisioningStateSucceeded,
			resourceDocPresent:      true,
			expectAsyncNotification: true,
			expectError:             false,
		},
	}

	// Placeholder InternalID for NewOperation
	internalID, err := api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/placeholder")
	require.NoError(t, err)

	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)

	operationID, err := azcorearm.ParseResourceID(api.TestSubscriptionResourceID + "/providers/" + api.ProviderNamespace + "/locations/oz/" + api.OperationStatusResourceTypeName + "/operationID")
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request

			ctx := context.Background()
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockOperationCRUD := mocks.NewMockOperationCRUD(ctrl)
			mockUntypedResourceCRUD := mocks.NewMockUntypedResourceCRUD(ctrl)

			noTypedDocs := maps.All(map[string]*database.TypedDocument{})
			mockIter := mocks.NewMockDBClientIterator[database.TypedDocument](ctrl)
			mockIter.EXPECT().
				Items(gomock.Any()).
				Return(database.DBClientIteratorItem[database.TypedDocument](noTypedDocs))
			mockIter.EXPECT().
				GetError().
				Return(nil)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					request = r
				}
			}))
			defer server.Close()

			scanner := &OperationsScanner{
				dbClient:           mockDBClient,
				notificationClient: server.Client(),
				newTimestamp:       func() time.Time { return time.Now().UTC() },
			}

			operationDoc := database.NewOperation(
				database.OperationRequestDelete,
				resourceID,
				internalID,
				"azure-location",
				"",
				"",
				"",
				nil)
			operationDoc.OperationID = operationID
			operationDoc.NotificationURI = server.URL
			operationDoc.Status = tt.operationStatus

			op := operation{
				id:     operationID.Name,
				doc:    operationDoc,
				logger: slog.Default(),
			}

			var resourceDocDeleted bool

			mockDBClient.EXPECT().
				UntypedCRUD(*resourceID).
				Return(mockUntypedResourceCRUD, nil)
			mockUntypedResourceCRUD.EXPECT().
				Delete(gomock.Any(), resourceID).
				Do(func(ctx context.Context, resourceID *azcorearm.ResourceID) error {
					resourceDocDeleted = tt.resourceDocPresent
					return nil
				})
			mockUntypedResourceCRUD.EXPECT().
				List(gomock.Any(), nil).
				Return(mockIter, nil)

			mockDBClient.EXPECT().
				Operations(op.doc.OperationID.SubscriptionID).
				DoAndReturn(func(s string) database.OperationCRUD {
					return mockOperationCRUD
				})
			mockOperationCRUD.EXPECT().
				Replace(gomock.Any(), gomock.Any(), nil).
				DoAndReturn(func(ctx context.Context, a *api.Operation, options *azcosmos.ItemOptions) (*api.Operation, error) {
					if operationDoc.Status != arm.ProvisioningStateSucceeded {
						operationDoc.Status = arm.ProvisioningStateSucceeded
						return operationDoc, nil
					} else {
						return operationDoc, nil
					}
				})
			if tt.expectAsyncNotification {
				mockDBClient.EXPECT().
					Operations(op.doc.OperationID.SubscriptionID).
					DoAndReturn(func(s string) database.OperationCRUD {
						return mockOperationCRUD
					})
				mockOperationCRUD.EXPECT().
					Replace(gomock.Any(), gomock.Any(), nil).
					DoAndReturn(func(ctx context.Context, a *api.Operation, options *azcosmos.ItemOptions) (*api.Operation, error) {
						operationDoc.NotificationURI = ""
						return operationDoc, nil
					})
			}

			err = scanner.setDeleteOperationAsCompleted(ctx, op)

			if tt.expectError {
				assert.Error(t, err)

			} else if assert.NoError(t, err) {
				if tt.resourceDocPresent {
					assert.True(t, resourceDocDeleted)
				}

				if tt.expectAsyncNotification {
					assert.Equal(t, arm.ProvisioningStateSucceeded, operationDoc.Status)
					assert.NotNil(t, request, "Did not POST to async notification URI")
					assert.Empty(t, operationDoc.NotificationURI)
				} else {
					assert.Nil(t, request, "Unexpected POST to async notification URI")
				}
			}
		})
	}
}

func TestUpdateOperationStatus(t *testing.T) {
	tests := []struct {
		name                             string
		currentOperationStatus           arm.ProvisioningState
		updatedOperationStatus           arm.ProvisioningState
		resourceDocPresent               bool
		resourceMatchOperationID         bool
		resourceProvisioningState        arm.ProvisioningState
		expectAsyncNotification          bool
		expectResourceOperationIDCleared bool
		expectResourceProvisioningState  arm.ProvisioningState
		expectError                      bool
	}{
		{
			name:                             "Resource updated to terminal state",
			currentOperationStatus:           arm.ProvisioningStateProvisioning,
			updatedOperationStatus:           arm.ProvisioningStateSucceeded,
			resourceDocPresent:               true,
			resourceMatchOperationID:         true,
			resourceProvisioningState:        arm.ProvisioningStateProvisioning,
			expectAsyncNotification:          true,
			expectResourceOperationIDCleared: true,
			expectResourceProvisioningState:  arm.ProvisioningStateSucceeded,
			expectError:                      false,
		},
		{
			name:                             "Resource updated to non-terminal state",
			currentOperationStatus:           arm.ProvisioningStateSucceeded,
			updatedOperationStatus:           arm.ProvisioningStateDeleting,
			resourceDocPresent:               true,
			resourceMatchOperationID:         true,
			resourceProvisioningState:        arm.ProvisioningStateSucceeded,
			expectAsyncNotification:          false,
			expectResourceOperationIDCleared: false,
			expectResourceProvisioningState:  arm.ProvisioningStateDeleting,
			expectError:                      false,
		},
		{
			name:                             "Operation already at target provisioning state",
			currentOperationStatus:           arm.ProvisioningStateSucceeded,
			updatedOperationStatus:           arm.ProvisioningStateSucceeded,
			resourceDocPresent:               true,
			resourceMatchOperationID:         true,
			resourceProvisioningState:        arm.ProvisioningStateSucceeded,
			expectAsyncNotification:          true,
			expectResourceOperationIDCleared: true,
			expectResourceProvisioningState:  arm.ProvisioningStateSucceeded,
			expectError:                      false,
		},
		{
			name:                    "Resource not found",
			currentOperationStatus:  arm.ProvisioningStateProvisioning,
			updatedOperationStatus:  arm.ProvisioningStateSucceeded,
			resourceDocPresent:      false,
			expectAsyncNotification: true,
			expectError:             true,
		},
		{
			name:                             "Resource has a different active operation",
			currentOperationStatus:           arm.ProvisioningStateProvisioning,
			updatedOperationStatus:           arm.ProvisioningStateSucceeded,
			resourceDocPresent:               true,
			resourceMatchOperationID:         false,
			resourceProvisioningState:        arm.ProvisioningStateDeleting,
			expectAsyncNotification:          true,
			expectResourceOperationIDCleared: false,
			expectResourceProvisioningState:  arm.ProvisioningStateDeleting,
			expectError:                      true,
		},
	}

	// Placeholder InternalID for NewOperation
	internalID, err := api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/placeholder")
	require.NoError(t, err)

	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)

	operationID, err := azcorearm.ParseResourceID(api.TestSubscriptionResourceID + "/providers/" + api.ProviderNamespace + "/locations/oz/" + api.OperationStatusResourceTypeName + "/operationID")
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request

			ctx := context.Background()
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockOperationCRUD := mocks.NewMockOperationCRUD(ctrl)
			mockHCPCluster := mocks.NewMockHCPClusterCRUD(ctrl)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					request = r
				}
			}))
			defer server.Close()

			scanner := &OperationsScanner{
				notificationClient: server.Client(),
				newTimestamp:       func() time.Time { return time.Now().UTC() },
			}

			operationDoc := database.NewOperation(
				database.OperationRequestCreate,
				resourceID,
				internalID,
				"azure-location",
				"",
				"",
				"",
				nil)
			operationDoc.OperationID = operationID
			operationDoc.NotificationURI = server.URL
			operationDoc.Status = tt.currentOperationStatus

			var resourceDoc *api.HCPOpenShiftCluster
			var clusterCopy api.HCPOpenShiftCluster
			if tt.resourceDocPresent {
				resourceDoc = &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{
							ID: resourceID,
						},
					},
				}
				if tt.resourceMatchOperationID {
					resourceDoc.ServiceProviderProperties.ActiveOperationID = operationDoc.OperationID.Name
				} else {
					resourceDoc.ServiceProviderProperties.ActiveOperationID = "another operation"
				}
				resourceDoc.ServiceProviderProperties.ProvisioningState = tt.resourceProvisioningState
				clusterCopy = *resourceDoc
			}

			mockDBClient.EXPECT().
				Operations(operationDoc.OperationID.SubscriptionID).
				DoAndReturn(func(s string) database.OperationCRUD {
					return mockOperationCRUD
				})
			mockOperationCRUD.EXPECT().
				Replace(gomock.Any(), gomock.Any(), nil).
				DoAndReturn(func(ctx context.Context, a *api.Operation, options *azcosmos.ItemOptions) (*api.Operation, error) {
					if operationDoc.Status != tt.updatedOperationStatus {
						operationDoc.Status = tt.updatedOperationStatus
						return operationDoc, nil
					} else {
						return operationDoc, nil
					}
				})
			mockDBClient.EXPECT().
				HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
				Return(mockHCPCluster)
			mockHCPCluster.EXPECT().
				Get(gomock.Any(), resourceID.Name).
				DoAndReturn(func(ctx context.Context, s string) (*api.HCPOpenShiftCluster, error) {
					if resourceDoc != nil {
						return &clusterCopy, nil
					}
					return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
				})
			if tt.resourceDocPresent && resourceDoc.ServiceProviderProperties.ActiveOperationID == operationDoc.OperationID.Name {
				mockHCPCluster.EXPECT().
					Replace(gomock.Any(), gomock.Any(), nil).
					DoAndReturn(func(ctx context.Context, cluster *api.HCPOpenShiftCluster, options *azcosmos.ItemOptions) (*api.HCPOpenShiftCluster, error) {
						if resourceDoc.ServiceProviderProperties.ActiveOperationID == operationDoc.OperationID.Name {
							resourceDoc.ServiceProviderProperties.ProvisioningState = operationDoc.Status
							if operationDoc.Status.IsTerminal() {
								resourceDoc.ServiceProviderProperties.ActiveOperationID = ""
							}
							return resourceDoc, nil
						} else {
							return nil, &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}
						}

					})
			}
			if tt.expectAsyncNotification {
				mockDBClient.EXPECT().
					Operations(operationDoc.OperationID.SubscriptionID).
					DoAndReturn(func(s string) database.OperationCRUD {
						return mockOperationCRUD
					})
				mockOperationCRUD.EXPECT().
					Replace(gomock.Any(), gomock.Any(), nil).
					DoAndReturn(func(ctx context.Context, a *api.Operation, options *azcosmos.ItemOptions) (*api.Operation, error) {
						operationDoc.NotificationURI = ""
						return operationDoc, nil
					})
			}

			err = database.UpdateOperationStatus(ctx, mockDBClient, operationDoc, tt.updatedOperationStatus, nil, scanner.postAsyncNotification)

			if tt.expectError {
				assert.Error(t, err)

			} else if assert.NoError(t, err) {
				if tt.resourceDocPresent {
					if tt.expectResourceOperationIDCleared {
						assert.Empty(t, resourceDoc.ServiceProviderProperties.ActiveOperationID, "Resource's active operation ID was not cleared")
					} else {
						assert.NotEmpty(t, resourceDoc.ServiceProviderProperties.ActiveOperationID, "Resource's active operation ID is unexpectedly empty")
					}

					assert.Equal(t, tt.expectResourceProvisioningState, resourceDoc.ServiceProviderProperties.ProvisioningState)
				}

				if tt.expectAsyncNotification {
					assert.Equal(t, tt.updatedOperationStatus, operationDoc.Status)
					assert.NotNil(t, request, "Did not POST to async notification URI")
					assert.Empty(t, operationDoc.NotificationURI)
				} else {
					assert.Nil(t, request, "Unexpected POST to async notification URI")
				}
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
		var operationsScanner *OperationsScanner
		t.Run(tt.name, func(t *testing.T) {
			clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
				State(tt.clusterState).
				Build()
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()

			op := operation{
				doc: &api.Operation{
					InternalID: tt.internalId,
					Status:     tt.currentProvisioningState,
				},
				logger: slog.Default(),
			}

			opState, opError, err := operationsScanner.convertClusterStatus(ctx, op, clusterStatus)

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

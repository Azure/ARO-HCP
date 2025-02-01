package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"go.uber.org/mock/gomock"

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
			expectAsyncNotification: false,
			expectError:             false,
		},
	}

	// Placeholder InternalID for NewOperationDocument
	internalID, err := ocm.NewInternalID("/api/clusters_mgmt/v1/clusters/placeholder")
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request

			ctx := context.Background()
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)

			resourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/testGroup/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/testCluster")
			if err != nil {
				t.Fatal(err)
			}

			operationID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.RedHatOpenShift/locations/oz/hcpOperationsStatus/operationID")
			if err != nil {
				t.Fatal(err)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					request = r
				}
			}))
			defer server.Close()

			scanner := &OperationsScanner{
				dbClient:           mockDBClient,
				notificationClient: server.Client(),
			}

			operationDoc := database.NewOperationDocument(database.OperationRequestDelete, resourceID, internalID)
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
				DeleteResourceDoc(gomock.Any(), resourceID).
				Do(func(ctx context.Context, resourceID *azcorearm.ResourceID) error {
					resourceDocDeleted = tt.resourceDocPresent
					return nil
				})
			mockDBClient.EXPECT().
				UpdateOperationDoc(gomock.Any(), op.pk, op.id, gomock.Any()).
				DoAndReturn(func(ctx context.Context, pk azcosmos.PartitionKey, operationID string, callback func(*database.OperationDocument) bool) (bool, error) {
					return callback(operationDoc), nil
				})
			if tt.expectAsyncNotification {
				mockDBClient.EXPECT().
					GetOperationDoc(gomock.Any(), op.pk, op.id).
					Return(operationDoc, nil)
			}

			err = scanner.setDeleteOperationAsCompleted(ctx, op)

			if request == nil && tt.expectAsyncNotification {
				t.Error("Did not POST to async notification URI")
			} else if request != nil && !tt.expectAsyncNotification {
				t.Error("Unexpected POST to async notification URI")
			}

			if err == nil && tt.expectError {
				t.Error("Expected error but got none")
			} else if err != nil && !tt.expectError {
				t.Errorf("Got unexpected error: %v", err)
			}

			if err == nil && tt.resourceDocPresent && !resourceDocDeleted {
				t.Error("Expected resource document to be deleted")
			}

			if err == nil && tt.expectAsyncNotification {
				if operationDoc.Status != arm.ProvisioningStateSucceeded {
					t.Errorf("Expected updated operation status to be %s but got %s",
						arm.ProvisioningStateSucceeded,
						operationDoc.Status)
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
			expectAsyncNotification:          true,
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
			expectAsyncNotification:          false,
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
			expectError:                      false,
		},
	}

	// Placeholder InternalID for NewOperationDocument
	internalID, err := ocm.NewInternalID("/api/clusters_mgmt/v1/clusters/placeholder")
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request

			ctx := context.Background()
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)

			resourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/testGroup/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/testCluster")
			if err != nil {
				t.Fatal(err)
			}

			operationID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.RedHatOpenShift/locations/oz/hcpOperationsStatus/operationID")
			if err != nil {
				t.Fatal(err)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					request = r
				}
			}))
			defer server.Close()

			scanner := &OperationsScanner{
				dbClient:           mockDBClient,
				notificationClient: server.Client(),
			}

			operationDoc := database.NewOperationDocument(database.OperationRequestCreate, resourceID, internalID)
			operationDoc.OperationID = operationID
			operationDoc.NotificationURI = server.URL
			operationDoc.Status = tt.currentOperationStatus

			op := operation{
				id:     operationID.Name,
				doc:    operationDoc,
				logger: slog.Default(),
			}

			var resourceDoc *database.ResourceDocument

			if tt.resourceDocPresent {
				resourceDoc = database.NewResourceDocument(resourceID)
				if tt.resourceMatchOperationID {
					resourceDoc.ActiveOperationID = op.id
				} else {
					resourceDoc.ActiveOperationID = "another operation"
				}
				resourceDoc.ProvisioningState = tt.resourceProvisioningState
			}

			mockDBClient.EXPECT().
				UpdateOperationDoc(gomock.Any(), op.pk, op.id, gomock.Any()).
				DoAndReturn(func(ctx context.Context, pk azcosmos.PartitionKey, operationID string, callback func(*database.OperationDocument) bool) (bool, error) {
					return callback(operationDoc), nil
				})
			mockDBClient.EXPECT().
				UpdateResourceDoc(gomock.Any(), resourceID, gomock.Any()).
				DoAndReturn(func(ctx context.Context, resourceID *azcorearm.ResourceID, callback func(*database.ResourceDocument) bool) (bool, error) {
					if resourceDoc != nil {
						return callback(resourceDoc), nil
					} else {
						return false, database.ErrNotFound
					}
				})
			if tt.expectAsyncNotification {
				mockDBClient.EXPECT().
					GetOperationDoc(gomock.Any(), op.pk, op.id).
					Return(operationDoc, nil)
			}

			err = scanner.updateOperationStatus(ctx, op, tt.updatedOperationStatus, nil)

			if request == nil && tt.expectAsyncNotification {
				t.Error("Did not POST to async notification URI")
			} else if request != nil && !tt.expectAsyncNotification {
				t.Error("Unexpected POST to async notification URI")
			}

			if err == nil && tt.expectError {
				t.Error("Expected error but got none")
			} else if err != nil && !tt.expectError {
				t.Errorf("Got unexpected error: %v", err)
			}

			if err == nil && tt.expectAsyncNotification {
				if operationDoc.Status != tt.updatedOperationStatus {
					t.Errorf("Expected updated operation status to be %s but got %s",
						tt.updatedOperationStatus,
						operationDoc.Status)
				}
			}

			if err == nil && tt.resourceDocPresent {
				if resourceDoc.ActiveOperationID == "" && !tt.expectResourceOperationIDCleared {
					t.Error("Resource's active operation ID is unexpectedly empty")
				} else if resourceDoc.ActiveOperationID != "" && tt.expectResourceOperationIDCleared {
					t.Errorf("Resource's active operation ID was not cleared; has '%s'", resourceDoc.ActiveOperationID)
				}
				if resourceDoc.ProvisioningState != tt.expectResourceProvisioningState {
					t.Errorf("Expected updated provisioning state to be %s but got %s",
						tt.expectResourceProvisioningState,
						resourceDoc.ProvisioningState)
				}
			}
		})
	}
}

func TestConvertClusterStatus(t *testing.T) {
	// FIXME These tests are all tentative until the new "/api/aro_hcp/v1" OCM
	//       API is available. What's here now is a best guess at converting
	//       ClusterStatus from the "/api/clusters_mgmt/v1" API.
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

			opState, opError, err := convertClusterStatus(clusterStatus, tt.currentProvisioningState)
			if opState != tt.updatedProvisioningState {
				t.Errorf("Expected provisioning state '%s' but got '%s'", tt.updatedProvisioningState, opState)
			}
			if opError == nil && tt.expectCloudError {
				t.Error("Expected a cloud error but got none")
			} else if opError != nil && !tt.expectCloudError {
				t.Errorf("Got unexpected cloud error: %v", opError)
			}
			if err == nil && tt.expectConversionError {
				t.Error("Expected a conversion error but got none")
			} else if err != nil && !tt.expectConversionError {
				t.Errorf("Got unexpected conversion error: %v", err)
			}
		})
	}
}

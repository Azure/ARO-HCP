package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestDeleteOperationCompleted(t *testing.T) {
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

			resourceID, err := arm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/testGroup/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/testCluster")
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
				dbClient:           database.NewCache(),
				notificationClient: server.Client(),
			}

			operationDoc := database.NewOperationDocument(database.OperationRequestDelete, resourceID, internalID)
			operationDoc.NotificationURI = server.URL
			operationDoc.Status = tt.operationStatus

			_ = scanner.dbClient.CreateOperationDoc(ctx, operationDoc)

			if tt.resourceDocPresent {
				resourceDoc := database.NewResourceDocument(resourceID)
				_ = scanner.dbClient.CreateResourceDoc(ctx, resourceDoc)
			}

			err = scanner.deleteOperationCompleted(ctx, slog.Default(), operationDoc)

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

			if err == nil && tt.resourceDocPresent {
				_, getErr := scanner.dbClient.GetResourceDoc(ctx, resourceID)
				if !errors.Is(getErr, database.ErrNotFound) {
					t.Error("Expected resource document to be deleted")
				}
			}

			if err == nil && tt.expectAsyncNotification {
				operationDoc, getErr := scanner.dbClient.GetOperationDoc(ctx, operationDoc.ID)
				if getErr != nil {
					t.Fatal(getErr)
				}
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

			resourceID, err := arm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/testGroup/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/testCluster")
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
				dbClient:           database.NewCache(),
				notificationClient: server.Client(),
			}

			operationDoc := database.NewOperationDocument(database.OperationRequestCreate, resourceID, internalID)
			operationDoc.NotificationURI = server.URL
			operationDoc.Status = tt.currentOperationStatus

			_ = scanner.dbClient.CreateOperationDoc(ctx, operationDoc)

			if tt.resourceDocPresent {
				resourceDoc := database.NewResourceDocument(resourceID)
				if tt.resourceMatchOperationID {
					resourceDoc.ActiveOperationID = operationDoc.ID
				} else {
					resourceDoc.ActiveOperationID = "another operation"
				}
				resourceDoc.ProvisioningState = tt.resourceProvisioningState
				_ = scanner.dbClient.CreateResourceDoc(ctx, resourceDoc)
			}

			err = scanner.updateOperationStatus(ctx, slog.Default(), operationDoc, tt.updatedOperationStatus, nil)

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
				operationDoc, getErr := scanner.dbClient.GetOperationDoc(ctx, operationDoc.ID)
				if getErr != nil {
					t.Fatal(getErr)
				}
				if operationDoc.Status != tt.updatedOperationStatus {
					t.Errorf("Expected updated operation status to be %s but got %s",
						tt.updatedOperationStatus,
						operationDoc.Status)
				}
			}

			if err == nil && tt.resourceDocPresent {
				resourceDoc, getErr := scanner.dbClient.GetResourceDoc(ctx, resourceID)
				if getErr != nil {
					t.Fatal(getErr)
				}
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
		clusterState             cmv1.ClusterState
		currentProvisioningState arm.ProvisioningState
		updatedProvisioningState arm.ProvisioningState
		expectCloudError         bool
		expectConversionError    bool
	}{
		{
			name:                     "Convert ClusterStateError",
			clusterState:             cmv1.ClusterStateError,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateFailed,
			expectCloudError:         true,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateHibernating",
			clusterState:             cmv1.ClusterStateHibernating,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateInstalling",
			clusterState:             cmv1.ClusterStateInstalling,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateProvisioning,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending (while accepted)",
			clusterState:             cmv1.ClusterStatePending,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStatePending (while not accepted)",
			clusterState:             cmv1.ClusterStatePending,
			currentProvisioningState: arm.ProvisioningStateFailed,
			updatedProvisioningState: arm.ProvisioningStateFailed,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStatePoweringDown",
			clusterState:             cmv1.ClusterStatePoweringDown,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateReady",
			clusterState:             cmv1.ClusterStateReady,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateSucceeded,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateResuming",
			clusterState:             cmv1.ClusterStateResuming,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateUninstalling",
			clusterState:             cmv1.ClusterStateUninstalling,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateDeleting,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateUnknown",
			clusterState:             cmv1.ClusterStateUnknown,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateValidating (while accepted)",
			clusterState:             cmv1.ClusterStateValidating,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    false,
		},
		{
			name:                     "Convert ClusterStateValidating (while not accepted)",
			clusterState:             cmv1.ClusterStateValidating,
			currentProvisioningState: arm.ProvisioningStateFailed,
			updatedProvisioningState: arm.ProvisioningStateFailed,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert ClusterStateWaiting",
			clusterState:             cmv1.ClusterStateWaiting,
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
		{
			name:                     "Convert unexpected cluster state",
			clusterState:             cmv1.ClusterState("unexpected cluster state"),
			currentProvisioningState: arm.ProvisioningStateAccepted,
			updatedProvisioningState: arm.ProvisioningStateAccepted,
			expectCloudError:         false,
			expectConversionError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterStatus, err := cmv1.NewClusterStatus().
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

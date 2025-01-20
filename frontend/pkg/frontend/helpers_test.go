package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
)

func TestCheckForProvisioningStateConflict(t *testing.T) {
	const clusterResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/testGroup/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/testCluster"
	const nodePoolResourceID = clusterResourceID + "/nodePools/testNodePool"

	tests := []struct {
		name             string
		resourceID       string
		operationRequest database.OperationRequest
		directConflicts  map[arm.ProvisioningState]bool
		parentConflicts  map[arm.ProvisioningState]bool
	}{
		{
			name:             "Create cluster",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestCreate,
			directConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     false,
				arm.ProvisioningStateDeleting:     false,
				arm.ProvisioningStateProvisioning: false,
				arm.ProvisioningStateUpdating:     false,
			},
		},
		{
			name:             "Delete cluster",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestDelete,
			directConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     false,
				arm.ProvisioningStateDeleting:     true,
				arm.ProvisioningStateProvisioning: false,
				arm.ProvisioningStateUpdating:     false,
			},
		},
		{
			name:             "Update cluster",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestUpdate,
			directConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     true,
				arm.ProvisioningStateDeleting:     true,
				arm.ProvisioningStateProvisioning: true,
				arm.ProvisioningStateUpdating:     true,
			},
		},
		{
			name:             "Create node pool",
			resourceID:       nodePoolResourceID,
			operationRequest: database.OperationRequestCreate,
			directConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     false,
				arm.ProvisioningStateDeleting:     false,
				arm.ProvisioningStateProvisioning: false,
				arm.ProvisioningStateUpdating:     false,
			},
			parentConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     false,
				arm.ProvisioningStateDeleting:     true,
				arm.ProvisioningStateProvisioning: false,
				arm.ProvisioningStateUpdating:     false,
			},
		},
		{
			name:             "Delete node pool",
			resourceID:       nodePoolResourceID,
			operationRequest: database.OperationRequestDelete,
			directConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     false,
				arm.ProvisioningStateDeleting:     true,
				arm.ProvisioningStateProvisioning: false,
				arm.ProvisioningStateUpdating:     false,
			},
			parentConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     false,
				arm.ProvisioningStateDeleting:     true,
				arm.ProvisioningStateProvisioning: false,
				arm.ProvisioningStateUpdating:     false,
			},
		},
		{
			name:             "Update node pool",
			resourceID:       nodePoolResourceID,
			operationRequest: database.OperationRequestUpdate,
			directConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     true,
				arm.ProvisioningStateDeleting:     true,
				arm.ProvisioningStateProvisioning: true,
				arm.ProvisioningStateUpdating:     true,
			},
			parentConflicts: map[arm.ProvisioningState]bool{
				arm.ProvisioningStateSucceeded:    false,
				arm.ProvisioningStateFailed:       false,
				arm.ProvisioningStateCanceled:     false,
				arm.ProvisioningStateAccepted:     false,
				arm.ProvisioningStateDeleting:     true,
				arm.ProvisioningStateProvisioning: false,
				arm.ProvisioningStateUpdating:     false,
			},
		},
	}

	for _, tt := range tests {
		var name string

		resourceID, err := arm.ParseResourceID(tt.resourceID)
		if err != nil {
			t.Fatal(err)
		}

		for directState, directConflict := range tt.directConflicts {
			name = fmt.Sprintf("%s (directState=%s)", tt.name, directState)
			t.Run(name, func(t *testing.T) {
				ctx := ContextWithLogger(context.Background(), testLogger)
				ctrl := gomock.NewController(t)
				mockDBClient := mocks.NewMockDBClient(ctrl)

				frontend := &Frontend{
					dbClient: mockDBClient,
				}

				doc := database.NewResourceDocument(resourceID)
				doc.ProvisioningState = directState

				parentResourceID := resourceID.GetParent()
				parentDoc := database.NewResourceDocument(parentResourceID)
				// Hold the provisioning state to something benign.
				parentDoc.ProvisioningState = arm.ProvisioningStateSucceeded

				mockDBClient.EXPECT().
					GetResourceDoc(gomock.Any(), equalResourceID(parentResourceID)). // defined in frontend_test.go
					Return(parentDoc, nil).
					MaxTimes(1)

				cloudError := frontend.CheckForProvisioningStateConflict(ctx, tt.operationRequest, doc)

				if cloudError == nil {
					if directConflict {
						t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
					}
				} else {
					if !directConflict || cloudError.StatusCode != http.StatusConflict {
						t.Errorf("Got unexpected error: %d %s", cloudError.StatusCode, http.StatusText(cloudError.StatusCode))
					}
				}
			})
		}

		for parentState, parentConflict := range tt.parentConflicts {
			name = fmt.Sprintf("%s (parentState=%s)", tt.name, parentState)
			t.Run(name, func(t *testing.T) {
				ctx := ContextWithLogger(context.Background(), testLogger)
				ctrl := gomock.NewController(t)
				mockDBClient := mocks.NewMockDBClient(ctrl)

				frontend := &Frontend{
					dbClient: mockDBClient,
				}

				doc := database.NewResourceDocument(resourceID)
				// Hold the provisioning state to something benign.
				doc.ProvisioningState = arm.ProvisioningStateSucceeded

				parentResourceID := resourceID.GetParent()
				if parentResourceID.ResourceType.Namespace == resourceID.ResourceType.Namespace {
					parentDoc := database.NewResourceDocument(parentResourceID)
					parentDoc.ProvisioningState = parentState

					mockDBClient.EXPECT().
						GetResourceDoc(gomock.Any(), equalResourceID(parentResourceID)). // defined in frontend_test.go
						Return(parentDoc, nil)
				} else {
					t.Fatalf("Parent resource type namespace (%s) differs from child namespace (%s)",
						parentResourceID.ResourceType.Namespace,
						resourceID.ResourceType.Namespace)
				}

				cloudError := frontend.CheckForProvisioningStateConflict(ctx, tt.operationRequest, doc)

				if cloudError == nil {
					if parentConflict {
						t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
					}
				} else {
					if !parentConflict || cloudError.StatusCode != http.StatusConflict {
						t.Errorf("Got unexpected error: %d %s", cloudError.StatusCode, http.StatusText(cloudError.StatusCode))
					}
				}
			})
		}
	}
}

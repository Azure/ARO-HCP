package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
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
		directConflict   func(arm.ProvisioningState) bool
		parentConflict   func(arm.ProvisioningState) bool
	}{
		{
			name:             "Create cluster",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestCreate,
			directConflict:   func(s arm.ProvisioningState) bool { return false },
		},
		{
			name:             "Delete cluster",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestDelete,
			directConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
		{
			name:             "Update cluster",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestUpdate,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Request cluster credential",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestRequestCredential,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Revoke cluster credentials",
			resourceID:       clusterResourceID,
			operationRequest: database.OperationRequestRevokeCredentials,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Create node pool",
			resourceID:       nodePoolResourceID,
			operationRequest: database.OperationRequestCreate,
			directConflict:   func(s arm.ProvisioningState) bool { return false },
			parentConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
		{
			name:             "Delete node pool",
			resourceID:       nodePoolResourceID,
			operationRequest: database.OperationRequestDelete,
			directConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
			parentConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
		{
			name:             "Update node pool",
			resourceID:       nodePoolResourceID,
			operationRequest: database.OperationRequestUpdate,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
			parentConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
	}

	for _, tt := range tests {
		var name string

		resourceID, err := azcorearm.ParseResourceID(tt.resourceID)
		if err != nil {
			t.Fatal(err)
		}

		for provisioningState := range arm.ListProvisioningStates() {
			name = fmt.Sprintf("%s (provisioningState=%s)", tt.name, provisioningState)
			t.Run(name, func(t *testing.T) {
				ctx := ContextWithLogger(context.Background(), testLogger)
				ctrl := gomock.NewController(t)
				mockDBClient := mocks.NewMockDBClient(ctrl)

				frontend := &Frontend{
					dbClient: mockDBClient,
				}

				doc := database.NewResourceDocument(resourceID)
				doc.ProvisioningState = provisioningState

				parentResourceID := resourceID.Parent
				parentDoc := database.NewResourceDocument(parentResourceID)
				// Hold the provisioning state to something benign.
				parentDoc.ProvisioningState = arm.ProvisioningStateSucceeded

				mockDBClient.EXPECT().
					GetResourceDoc(gomock.Any(), equalResourceID(parentResourceID)). // defined in frontend_test.go
					Return(parentDoc, nil).
					MaxTimes(1)

				cloudError := frontend.CheckForProvisioningStateConflict(ctx, tt.operationRequest, doc)

				if cloudError == nil {
					if tt.directConflict(provisioningState) {
						t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
					}
				} else {
					if !tt.directConflict(provisioningState) || cloudError.StatusCode != http.StatusConflict {
						t.Errorf("Got unexpected error: %d %s", cloudError.StatusCode, http.StatusText(cloudError.StatusCode))
					}
				}
			})
		}

		if tt.parentConflict != nil {
			for provisioningState := range arm.ListProvisioningStates() {
				name = fmt.Sprintf("%s (parent provisioningState=%s)", tt.name, provisioningState)
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

					parentResourceID := resourceID.Parent
					if parentResourceID.ResourceType.Namespace == resourceID.ResourceType.Namespace {
						parentDoc := database.NewResourceDocument(parentResourceID)
						parentDoc.ProvisioningState = provisioningState

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
						if tt.parentConflict(provisioningState) {
							t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
						}
					} else {
						if !tt.parentConflict(provisioningState) || cloudError.StatusCode != http.StatusConflict {
							t.Errorf("Got unexpected error: %d %s", cloudError.StatusCode, http.StatusText(cloudError.StatusCode))
						}
					}
				})
			}
		}
	}
}

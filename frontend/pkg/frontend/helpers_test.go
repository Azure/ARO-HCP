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

package frontend

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestCheckForProvisioningStateConflict(t *testing.T) {

	parentConflictFunc := func(s arm.ProvisioningState) bool {
		return s == arm.ProvisioningStateProvisioning || s == arm.ProvisioningStateDeleting
	}

	tests := []struct {
		name             string
		resourceID       string
		operationRequest database.OperationRequest
		directConflict   func(arm.ProvisioningState) bool
		parentConflict   func(arm.ProvisioningState) bool
	}{
		{
			name:             "Create cluster",
			resourceID:       api.TestClusterResourceID,
			operationRequest: database.OperationRequestCreate,
			directConflict:   func(s arm.ProvisioningState) bool { return false },
		},
		{
			name:             "Delete cluster",
			resourceID:       api.TestClusterResourceID,
			operationRequest: database.OperationRequestDelete,
			directConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
		{
			name:             "Update cluster",
			resourceID:       api.TestClusterResourceID,
			operationRequest: database.OperationRequestUpdate,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Request cluster credential",
			resourceID:       api.TestClusterResourceID,
			operationRequest: database.OperationRequestRequestCredential,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Revoke cluster credentials",
			resourceID:       api.TestClusterResourceID,
			operationRequest: database.OperationRequestRevokeCredentials,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Create node pool",
			resourceID:       api.TestNodePoolResourceID,
			operationRequest: database.OperationRequestCreate,
			directConflict:   func(s arm.ProvisioningState) bool { return false },
			parentConflict:   parentConflictFunc,
		},
		{
			name:             "Delete node pool",
			resourceID:       api.TestNodePoolResourceID,
			operationRequest: database.OperationRequestDelete,
			directConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
			parentConflict:   parentConflictFunc,
		},
		{
			name:             "Update node pool",
			resourceID:       api.TestNodePoolResourceID,
			operationRequest: database.OperationRequestUpdate,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
			parentConflict:   parentConflictFunc,
		},
	}

	for _, tt := range tests {
		var name string

		resourceID, err := azcorearm.ParseResourceID(tt.resourceID)
		require.NoError(t, err)

		for provisioningState := range arm.ListProvisioningStates() {
			name = fmt.Sprintf("%s (provisioningState=%s)", tt.name, provisioningState)
			t.Run(name, func(t *testing.T) {
				ctx := utils.ContextWithLogger(context.Background(), api.NewTestLogger())
				ctrl := gomock.NewController(t)
				mockDBClient := mocks.NewMockDBClient(ctrl)
				mockClusterCRUD := mocks.NewMockHCPClusterCRUD(ctrl)

				frontend := &Frontend{
					dbClient: mockDBClient,
				}

				doc := database.NewResourceDocument(resourceID)
				doc.ProvisioningState = provisioningState

				parentResourceID := resourceID.Parent
				mockDBClient.EXPECT().
					HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).
					Return(mockClusterCRUD).
					MaxTimes(1)
				mockClusterCRUD.EXPECT().
					Get(gomock.Any(), parentResourceID.Name).
					Return(
						&api.HCPOpenShiftCluster{
							TrackedResource: arm.TrackedResource{
								Resource: arm.Resource{
									ID: parentResourceID,
								},
							},
							ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
								ProvisioningState: arm.ProvisioningStateSucceeded,
							},
						},
						nil).
					MaxTimes(1)

				cloudError := checkForProvisioningStateConflict(ctx, frontend.dbClient, tt.operationRequest, doc.ResourceID, doc.ProvisioningState)

				if cloudError == nil {
					if tt.directConflict(provisioningState) {
						t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
					}
				} else {
					if !tt.directConflict(provisioningState) || cloudError.(*arm.CloudError).StatusCode != http.StatusConflict {
						t.Errorf("Got unexpected error: %d %s", cloudError.(*arm.CloudError).StatusCode, http.StatusText(cloudError.(*arm.CloudError).StatusCode))
					}
				}
			})
		}

		if tt.parentConflict != nil {
			for provisioningState := range arm.ListProvisioningStates() {
				name = fmt.Sprintf("%s (parent provisioningState=%s)", tt.name, provisioningState)
				t.Run(name, func(t *testing.T) {
					ctx := utils.ContextWithLogger(context.Background(), api.NewTestLogger())
					ctrl := gomock.NewController(t)
					mockDBClient := mocks.NewMockDBClient(ctrl)
					mockClusterCRUD := mocks.NewMockHCPClusterCRUD(ctrl)

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
							HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).
							Return(mockClusterCRUD)
						mockClusterCRUD.EXPECT().
							Get(gomock.Any(), parentResourceID.Name).
							Return(
								&api.HCPOpenShiftCluster{
									TrackedResource: arm.TrackedResource{
										Resource: arm.Resource{
											ID: parentResourceID,
										},
									},
									ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
										ProvisioningState: provisioningState,
									},
								},
								nil)

					} else {
						t.Fatalf("Parent resource type namespace (%s) differs from child namespace (%s)",
							parentResourceID.ResourceType.Namespace,
							resourceID.ResourceType.Namespace)
					}

					cloudError := checkForProvisioningStateConflict(ctx, frontend.dbClient, tt.operationRequest, doc.ResourceID, doc.ProvisioningState)

					if cloudError == nil {
						if tt.parentConflict(provisioningState) {
							t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
						}
					} else {
						if !tt.parentConflict(provisioningState) || cloudError.(*arm.CloudError).StatusCode != http.StatusConflict {
							t.Errorf("Got unexpected error: %d %s", cloudError.(*arm.CloudError).StatusCode, http.StatusText(cloudError.(*arm.CloudError).StatusCode))
						}
					}
				})
			}
		}
	}
}

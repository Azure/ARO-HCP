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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
)

func TestCheckForProvisioningStateConflict(t *testing.T) {
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
			parentConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
		{
			name:             "Delete node pool",
			resourceID:       api.TestNodePoolResourceID,
			operationRequest: database.OperationRequestDelete,
			directConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
			parentConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
		{
			name:             "Update node pool",
			resourceID:       api.TestNodePoolResourceID,
			operationRequest: database.OperationRequestUpdate,
			directConflict:   func(s arm.ProvisioningState) bool { return !s.IsTerminal() },
			parentConflict:   func(s arm.ProvisioningState) bool { return s == arm.ProvisioningStateDeleting },
		},
	}

	for _, tt := range tests {
		var name string

		resourceID, err := azcorearm.ParseResourceID(tt.resourceID)
		require.NoError(t, err)

		for provisioningState := range arm.ListProvisioningStates() {
			name = fmt.Sprintf("%s (provisioningState=%s)", tt.name, provisioningState)
			t.Run(name, func(t *testing.T) {
				ctx := ContextWithLogger(context.Background(), api.NewTestLogger())
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
					Return("parentItemID", parentDoc, nil).
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
					ctx := ContextWithLogger(context.Background(), api.NewTestLogger())
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
							Return("parentItemID", parentDoc, nil)
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

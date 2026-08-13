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
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestCheckForProvisioningStateConflict(t *testing.T) {

	parentConflictFunc := func(s coreapi.ProvisioningState) bool {
		return s == coreapi.ProvisioningStateProvisioning || s == coreapi.ProvisioningStateDeleting
	}

	tests := []struct {
		name             string
		resourceID       string
		operationRequest cosmosstorageutils.OperationRequest
		directConflict   func(coreapi.ProvisioningState) bool
		parentConflict   func(coreapi.ProvisioningState) bool
	}{
		{
			name:             "Create cluster",
			resourceID:       coreapitesting.TestClusterResourceID,
			operationRequest: cosmosstorageutils.OperationRequestCreate,
			directConflict:   func(s coreapi.ProvisioningState) bool { return false },
		},
		{
			name:             "Delete cluster",
			resourceID:       coreapitesting.TestClusterResourceID,
			operationRequest: cosmosstorageutils.OperationRequestDelete,
			directConflict:   func(s coreapi.ProvisioningState) bool { return s == coreapi.ProvisioningStateDeleting },
		},
		{
			name:             "Update cluster",
			resourceID:       coreapitesting.TestClusterResourceID,
			operationRequest: cosmosstorageutils.OperationRequestUpdate,
			directConflict:   func(s coreapi.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Request cluster credential",
			resourceID:       coreapitesting.TestClusterResourceID,
			operationRequest: cosmosstorageutils.OperationRequestSystemAdminCredentialRequest,
			directConflict:   func(s coreapi.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Revoke cluster credentials",
			resourceID:       coreapitesting.TestClusterResourceID,
			operationRequest: cosmosstorageutils.OperationRequestSystemAdminCredentialRevocation,
			directConflict:   func(s coreapi.ProvisioningState) bool { return !s.IsTerminal() },
		},
		{
			name:             "Create node pool",
			resourceID:       coreapitesting.TestNodePoolResourceID,
			operationRequest: cosmosstorageutils.OperationRequestCreate,
			directConflict:   func(s coreapi.ProvisioningState) bool { return false },
			parentConflict:   parentConflictFunc,
		},
		{
			name:             "Delete node pool",
			resourceID:       coreapitesting.TestNodePoolResourceID,
			operationRequest: cosmosstorageutils.OperationRequestDelete,
			directConflict:   func(s coreapi.ProvisioningState) bool { return s == coreapi.ProvisioningStateDeleting },
			parentConflict:   parentConflictFunc,
		},
		{
			name:             "Update node pool",
			resourceID:       coreapitesting.TestNodePoolResourceID,
			operationRequest: cosmosstorageutils.OperationRequestUpdate,
			directConflict:   func(s coreapi.ProvisioningState) bool { return !s.IsTerminal() },
			parentConflict:   parentConflictFunc,
		},
	}

	for _, tt := range tests {
		var name string

		resourceID, err := azcorearm.ParseResourceID(tt.resourceID)
		require.NoError(t, err)

		for provisioningState := range coreapi.ListProvisioningStates() {
			name = fmt.Sprintf("%s (provisioningState=%s)", tt.name, provisioningState)
			t.Run(name, func(t *testing.T) {
				ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
				mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

				frontend := &Frontend{
					resourcesDBClient: mockResourcesDBClient,
				}

				// Pre-populate the parent cluster in the database for nested resources (node pool, external auth)
				if tt.parentConflict != nil {
					parentResourceID := resourceID.Parent
					clusterInternalID := metadataapi.Must(metadataapi.NewInternalID(ocm.GenerateOCMCommercialClusterHREF("testCluster")))
					parentCluster := &coreapi.HCPOpenShiftCluster{
						CosmosMetadata: coreapi.CosmosMetadata{
							ResourceID:   parentResourceID,
							PartitionKey: strings.ToLower(parentResourceID.SubscriptionID),
						},
						TrackedResource: coreapi.TrackedResource{
							Resource: coreapi.Resource{
								ID: parentResourceID,
							},
						},
						ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
							ProvisioningState: coreapi.ProvisioningStateSucceeded,
							ClusterServiceID:  &clusterInternalID,
						},
					}
					_, _ = mockResourcesDBClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).Create(ctx, parentCluster, nil)
				}

				cloudError := checkForProvisioningStateConflict(ctx, frontend.resourcesDBClient, tt.operationRequest, resourceID, provisioningState)

				if cloudError == nil {
					if tt.directConflict(provisioningState) {
						t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
					}
				} else {
					if !tt.directConflict(provisioningState) || cloudError.(*coreapi.CloudError).StatusCode != http.StatusConflict {
						t.Errorf("Got unexpected error: %d %s", cloudError.(*coreapi.CloudError).StatusCode, http.StatusText(cloudError.(*coreapi.CloudError).StatusCode))
					}
				}
			})
		}

		if tt.parentConflict != nil {
			for provisioningState := range coreapi.ListProvisioningStates() {
				name = fmt.Sprintf("%s (parent provisioningState=%s)", tt.name, provisioningState)
				t.Run(name, func(t *testing.T) {
					ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
					mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

					frontend := &Frontend{
						resourcesDBClient: mockResourcesDBClient,
					}

					parentResourceID := resourceID.Parent
					if parentResourceID.ResourceType.Namespace == resourceID.ResourceType.Namespace {
						// Pre-populate the parent cluster with the test provisioning state
						clusterInternalID := metadataapi.Must(metadataapi.NewInternalID(ocm.GenerateOCMCommercialClusterHREF("testCluster")))
						parentCluster := &coreapi.HCPOpenShiftCluster{
							CosmosMetadata: coreapi.CosmosMetadata{
								ResourceID:   parentResourceID,
								PartitionKey: strings.ToLower(parentResourceID.SubscriptionID),
							},
							TrackedResource: coreapi.TrackedResource{
								Resource: coreapi.Resource{
									ID: parentResourceID,
								},
							},
							ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
								ProvisioningState: provisioningState,
								ClusterServiceID:  &clusterInternalID,
							},
						}
						_, _ = mockResourcesDBClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).Create(ctx, parentCluster, nil)
					} else {
						t.Fatalf("Parent resource type namespace (%s) differs from child namespace (%s)",
							parentResourceID.ResourceType.Namespace,
							resourceID.ResourceType.Namespace)
					}

					cloudError := checkForProvisioningStateConflict(ctx, frontend.resourcesDBClient, tt.operationRequest, resourceID, coreapi.ProvisioningStateSucceeded)

					if cloudError == nil {
						if tt.parentConflict(provisioningState) {
							t.Errorf("Expected %d %s but got no error", http.StatusConflict, http.StatusText(http.StatusConflict))
						}
					} else {
						if !tt.parentConflict(provisioningState) || cloudError.(*coreapi.CloudError).StatusCode != http.StatusConflict {
							t.Errorf("Got unexpected error: %d %s", cloudError.(*coreapi.CloudError).StatusCode, http.StatusText(cloudError.(*coreapi.CloudError).StatusCode))
						}
					}
				})
			}
		}
	}
}

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

package deletion

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// TestExtraDeleteGateShouldDeleteServiceProviderClusterManagedResourceGroup verifies
// that the ServiceProviderCluster delete gate blocks deletion while the managed
// resource group is still reflected (AzureResource or PendingAzureResource set) and
// allows it once both references are cleared.
func TestExtraDeleteGateShouldDeleteServiceProviderClusterManagedResourceGroup(t *testing.T) {
	const (
		subscriptionID    = "00000000-0000-0000-0000-000000000000"
		resourceGroupName = "test-rg"
		clusterName       = "test-cluster"
		managedRGName     = "test-managed-rg"
	)

	managedResourceGroupID := metadataapi.Must(coreapi.ToResourceGroupResourceID(subscriptionID, managedRGName))
	serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/" + coreapi.ServiceProviderClusterResourceTypeName +
			"/" + coreapi.ServiceProviderClusterResourceName,
	))

	testCases := []struct {
		name               string
		reference          coreapi.AzureReference
		expectShouldDelete bool
	}{
		{
			name:               "azure resource set blocks deletion",
			reference:          coreapi.AzureReference{AzureResource: managedResourceGroupID},
			expectShouldDelete: false,
		},
		{
			name:               "pending azure resource set blocks deletion",
			reference:          coreapi.AzureReference{PendingAzureResource: managedResourceGroupID},
			expectShouldDelete: false,
		},
		{
			name:               "both references nil allows deletion",
			reference:          coreapi.AzureReference{},
			expectShouldDelete: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			serviceProviderCluster := &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   serviceProviderClusterResourceID,
					PartitionKey: strings.ToLower(serviceProviderClusterResourceID.SubscriptionID),
				},
			}
			serviceProviderCluster.Status.AzureResources.ManagedResourceGroup = tc.reference

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{serviceProviderCluster})
			require.NoError(t, err)

			controller := &clusterChildResourcesCleanupController{
				resourcesDBClient: mockResourcesDB,
			}

			shouldDelete, err := controller.extraDeleteGateShouldDeleteServiceProviderCluster(ctx, serviceProviderClusterResourceID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectShouldDelete, shouldDelete)
		})
	}
}

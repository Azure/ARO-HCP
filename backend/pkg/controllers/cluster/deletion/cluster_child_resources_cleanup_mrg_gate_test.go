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

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/azureresourcescosmosstoragetesting"
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

	managedResourceGroupID := metadataapi.Must(coreapihelpers.ToResourceGroupResourceID(subscriptionID, managedRGName))
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

// TestEnsureDeploymentStackDesiresDeletedMockFPAGate verifies that the cluster child
// cleanup keeps the mock FPA role assignments DeploymentStackDesire while the managed
// resource group is still reflected (the mock FPA needs that Owner assignment to delete
// the MRG), while other DeploymentStackDesires are swept unconditionally, and that the
// mock FPA desire is finally swept once the MRG references are cleared.
func TestEnsureDeploymentStackDesiresDeletedMockFPAGate(t *testing.T) {
	const (
		subscriptionID    = "00000000-0000-0000-0000-000000000000"
		resourceGroupName = "test-rg"
		clusterName       = "test-cluster"
		managedRGName     = "test-managed-rg"
		otherDesireName   = "some-other-stack-desire"
	)

	managedResourceGroupID := metadataapi.Must(coreapihelpers.ToResourceGroupResourceID(subscriptionID, managedRGName))
	clusterResourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))

	newDesire := func(name string) *azureresourcesapi.DeploymentStackDesire {
		resourceID := metadataapi.Must(azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(subscriptionID, resourceGroupName, clusterName, name))
		return &azureresourcesapi.DeploymentStackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(subscriptionID),
			},
		}
	}

	// A soft-deleted desire is reported as not-found by Get, so Get (not List, which
	// still returns soft-deleted documents in the mock) tells us what actually survived.
	remainingDesireNames := func(ctx context.Context, t *testing.T, db *azureresourcescosmosstoragetesting.MockAzureResourcesDBClient) []string {
		t.Helper()
		crud, err := db.DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
		require.NoError(t, err)
		names := make([]string, 0)
		for _, name := range []string{azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName, otherDesireName} {
			_, err := crud.Get(ctx, name)
			if cosmosstorageutils.IsNotFoundError(err) {
				continue
			}
			require.NoError(t, err)
			names = append(names, name)
		}
		return names
	}

	testCases := []struct {
		name          string
		reference     coreapi.AzureReference
		wantRemaining []string
	}{
		{
			name:          "mock FPA desire kept while MRG confirmed, other swept",
			reference:     coreapi.AzureReference{AzureResource: managedResourceGroupID},
			wantRemaining: []string{azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName},
		},
		{
			name:          "mock FPA desire kept while MRG pending, other swept",
			reference:     coreapi.AzureReference{PendingAzureResource: managedResourceGroupID},
			wantRemaining: []string{azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName},
		},
		{
			name:          "mock FPA desire swept once MRG references cleared",
			reference:     coreapi.AzureReference{},
			wantRemaining: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			serviceProviderCluster := &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
						clusterResourceID.String() +
							"/" + coreapi.ServiceProviderClusterResourceTypeName +
							"/" + coreapi.ServiceProviderClusterResourceName,
					)),
					PartitionKey: strings.ToLower(subscriptionID),
				},
			}
			serviceProviderCluster.Status.AzureResources.ManagedResourceGroup = tc.reference

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{serviceProviderCluster})
			require.NoError(t, err)

			mockAzureResourcesDB, err := azureresourcescosmosstoragetesting.NewMockAzureResourcesDBClientWithResources(ctx, []any{
				newDesire(azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName),
				newDesire(otherDesireName),
			})
			require.NoError(t, err)

			controller := &clusterChildResourcesCleanupController{
				resourcesDBClient:      mockResourcesDB,
				azureResourcesDBClient: mockAzureResourcesDB,
			}

			err = controller.ensureDeploymentStackDesiresDeleted(ctx, clusterResourceID)
			require.NoError(t, err)

			assert.ElementsMatch(t, tc.wantRemaining, remainingDesireNames(ctx, t, mockAzureResourcesDB))
		})
	}
}

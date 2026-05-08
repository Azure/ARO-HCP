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

package nodepoolpropertiescontroller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testLocation     = "eastus"
	testVersionID    = "4.15"
	testChannelGroup = "stable"
	testSubnetID     = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
)

func TestNodePoolCustomerPropertiesMigrationController_SyncOnce(t *testing.T) {
	const (
		testNodePoolVMSize = "Standard_D8s_v3"
	)

	testCases := []struct {
		name                   string
		cachedNodePool         *api.HCPOpenShiftClusterNodePool // nodePool in cache, nil means use same as existingNodePool
		existingCosmosNodePool *api.HCPOpenShiftClusterNodePool // nodePool in cosmos
		csNodePool             *arohcpv1alpha1.NodePool
		csError                error
		expectCSCall           bool
		expectError            bool
		expectedVMSize         string
	}{
		{
			name: "cache indicates no work needed - early return without cosmos lookup",
			cachedNodePool: newTestNodePoolForMigration(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Platform.VMSize = testNodePoolVMSize
			}),
			existingCosmosNodePool: newTestNodePoolForMigration(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Platform.VMSize = testNodePoolVMSize
			}),
			expectCSCall:   false,
			expectError:    false,
			expectedVMSize: testNodePoolVMSize,
		},
		{
			name:           "cache says work needed but live data says no work needed",
			cachedNodePool: newTestNodePoolForMigration(t, func(np *api.HCPOpenShiftClusterNodePool) {}), // cache has no vmSize info
			existingCosmosNodePool: newTestNodePoolForMigration(t, func(np *api.HCPOpenShiftClusterNodePool) {
				// cosmos has the version info (cache is stale)
				np.Properties.Platform.VMSize = testNodePoolVMSize
			}),
			expectCSCall:   false,
			expectError:    false,
			expectedVMSize: testNodePoolVMSize,
		},
		{
			name:                   "error reading from cluster-service",
			existingCosmosNodePool: newTestNodePoolForMigration(t, func(np *api.HCPOpenShiftClusterNodePool) {}),
			csError:                fmt.Errorf("connection refused"),
			expectCSCall:           true,
			expectError:            true,
		},
		{
			name:                   "success - migrate vmSize when missing",
			existingCosmosNodePool: newTestNodePoolForMigration(t, func(np *api.HCPOpenShiftClusterNodePool) {}),
			csNodePool:             newTestFullCSNodePool(testNodePoolVMSize),
			expectCSCall:           true,
			expectError:            false,
			expectedVMSize:         testNodePoolVMSize,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			// Setup mock DB
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

			// Create the nodePool in the mock DB (cosmos)
			nodePoolCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName)
			_, err := nodePoolCRUD.Create(ctx, tc.existingCosmosNodePool, nil)
			require.NoError(t, err)

			// Setup slice nodePool lister (cache)
			// If cachedCluster is nil, use the same as existingCluster
			cachedNodePool := tc.cachedNodePool
			if cachedNodePool == nil {
				cachedNodePool = tc.existingCosmosNodePool
			}
			sliceNodePoolLister := &listertesting.SliceNodePoolLister{
				NodePools: []*api.HCPOpenShiftClusterNodePool{cachedNodePool},
			}

			// Setup mock CS client
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tc.expectCSCall {
				mockCSClient.EXPECT().
					GetNodePool(gomock.Any(), api.Must(api.NewInternalID(testNodePoolCSIDStr))).
					Return(tc.csNodePool, tc.csError)
			}

			// Create syncer
			syncer := &nodePoolCustomerPropertiesMigrationController{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				nodePoolLister:       sliceNodePoolLister,
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
			}

			// Execute
			key := controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			}
			err = syncer.SyncOnce(ctx, key)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Verify the cluster state in Cosmos
			updatedNodePool, err := nodePoolCRUD.Get(ctx, testNodePoolName)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedVMSize, updatedNodePool.Properties.Platform.VMSize)
		})
	}
}

// newTestNodePoolForMigration builds a node pool for customer-properties migration tests: it initializes a new
// test nodepool without customer properties, and it then applies opts on top of it.
func newTestNodePoolForMigration(t *testing.T, opts func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	nodePool := newTestNodePool(t, nil)
	nodePool.Properties = api.HCPOpenShiftClusterNodePoolProperties{}
	if opts != nil {
		opts(nodePool)
	}
	return nodePool
}

// newTestFullCSNodePool creates a mock Clusters Service NodePool with all fields
// used by ocm.ConvertCStoNodePool, using a fixed number of replicas.
func newTestFullCSNodePool(vmSize string) *arohcpv1alpha1.NodePool {
	nodePool, err := arohcpv1alpha1.NewNodePool().
		ID(testNodePoolName).
		Version(arohcpv1alpha1.NewVersion().RawID("test-version-id").ChannelGroup("test-channel-group")).
		Subnet(testSubnetID).
		AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
			ResourceName(testNodePoolName).
			VMSize(vmSize).
			EncryptionAtHost(arohcpv1alpha1.NewAzureNodePoolEncryptionAtHost().State("disabled")).
			OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().SizeGibibytes(64).StorageAccountType(string(api.DiskStorageAccountTypePremium_LRS)).Persistence("persistent"))).
		AvailabilityZone("1").
		AutoRepair(true).
		Labels(map[string]string{"key": "value"}).
		Taints(arohcpv1alpha1.NewTaint().Key("key").Value("value").Effect("NoExecute")).
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().Unit("seconds").Value(10)).
		Replicas(3).
		Build()
	if err != nil {
		panic(err)
	}
	return nodePool

}

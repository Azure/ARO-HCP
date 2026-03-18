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

package clusterpropertiescontroller

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

func TestClusterServiceMigrationSyncer_SyncOnce(t *testing.T) {
	testCases := []struct {
		name                 string
		cachedCluster        *api.HCPOpenShiftCluster // cluster in cache, nil means use same as existingCluster
		existingCluster      *api.HCPOpenShiftCluster // cluster in cosmos
		csCluster            *arohcpv1alpha1.Cluster
		csError              error
		expectCosmosGet      bool
		expectCSCall         bool
		expectCosmosUpdate   bool
		expectError          bool
		expectedVersionID    string
		expectedChannelGroup string
	}{
		{
			name: "cache indicates no work needed - early return without cosmos lookup",
			cachedCluster: newTestClusterForMigration(func(c *api.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = testVersionID
				c.CustomerProperties.Version.ChannelGroup = testChannelGroup
			}),
			existingCluster: newTestClusterForMigration(func(c *api.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = testVersionID
				c.CustomerProperties.Version.ChannelGroup = testChannelGroup
			}),
			expectCosmosGet:      false,
			expectCSCall:         false,
			expectCosmosUpdate:   false,
			expectError:          false,
			expectedVersionID:    testVersionID,
			expectedChannelGroup: testChannelGroup,
		},
		{
			name:          "cache says work needed but live data says no work needed",
			cachedCluster: newTestClusterForMigration(), // cache has no version info
			existingCluster: newTestClusterForMigration(func(c *api.HCPOpenShiftCluster) {
				// cosmos has the version info (cache is stale)
				c.CustomerProperties.Version.ID = testVersionID
				c.CustomerProperties.Version.ChannelGroup = testChannelGroup
			}),
			expectCosmosGet:      true,
			expectCSCall:         false,
			expectCosmosUpdate:   false,
			expectError:          false,
			expectedVersionID:    testVersionID,
			expectedChannelGroup: testChannelGroup,
		},
		{
			name: "no work to do - both versionID and channelGroup already set",
			existingCluster: newTestClusterForMigration(func(c *api.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = testVersionID
				c.CustomerProperties.Version.ChannelGroup = testChannelGroup
			}),
			expectCosmosGet:      false,
			expectCSCall:         false,
			expectCosmosUpdate:   false,
			expectError:          false,
			expectedVersionID:    testVersionID,
			expectedChannelGroup: testChannelGroup,
		},
		{
			name:                 "error reading from cluster-service",
			existingCluster:      newTestClusterForMigration(),
			csError:              fmt.Errorf("connection refused"),
			expectCosmosGet:      true,
			expectCSCall:         true,
			expectCosmosUpdate:   false,
			expectError:          true,
			expectedVersionID:    "",
			expectedChannelGroup: "",
		},
		{
			name:                 "success - migrate version when both fields missing",
			existingCluster:      newTestClusterForMigration(),
			csCluster:            buildFullCSCluster(testVersionID, testChannelGroup),
			expectCosmosGet:      true,
			expectCSCall:         true,
			expectCosmosUpdate:   true,
			expectError:          false,
			expectedVersionID:    testVersionID,
			expectedChannelGroup: testChannelGroup,
		},
		{
			name: "success - migrate version when only ID missing",
			existingCluster: newTestClusterForMigration(func(c *api.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ChannelGroup = testChannelGroup
			}),
			csCluster:            buildFullCSCluster(testVersionID, testChannelGroup),
			expectCosmosGet:      true,
			expectCSCall:         true,
			expectCosmosUpdate:   true,
			expectError:          false,
			expectedVersionID:    testVersionID,
			expectedChannelGroup: testChannelGroup,
		},
		{
			name: "success - migrate version when only ChannelGroup missing",
			existingCluster: newTestClusterForMigration(func(c *api.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = testVersionID
			}),
			csCluster:            buildFullCSCluster(testVersionID, testChannelGroup),
			expectCosmosGet:      true,
			expectCSCall:         true,
			expectCosmosUpdate:   true,
			expectError:          false,
			expectedVersionID:    testVersionID,
			expectedChannelGroup: testChannelGroup,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Setup mock DB
			mockDB := databasetesting.NewMockDBClient()

			// Create the cluster in the mock DB (cosmos)
			clusterCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName)
			_, err := clusterCRUD.Create(ctx, tc.existingCluster, nil)
			require.NoError(t, err)

			// Setup slice cluster lister (cache)
			// If cachedCluster is nil, use the same as existingCluster
			cachedCluster := tc.cachedCluster
			if cachedCluster == nil {
				cachedCluster = tc.existingCluster
			}
			sliceClusterLister := &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{cachedCluster},
			}

			// Setup mock CS client
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tc.expectCSCall {
				mockCSClient.EXPECT().
					GetCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(tc.csCluster, tc.csError)
			}

			// Create syncer
			syncer := &clusterCustomerPropertiesMigrationController{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				clusterLister:        sliceClusterLister,
				cosmosClient:         mockDB,
				clusterServiceClient: mockCSClient,
			}

			// Execute
			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}
			err = syncer.SyncOnce(ctx, key)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Verify the cluster state in Cosmos
			updatedCluster, err := clusterCRUD.Get(ctx, testClusterName)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedVersionID, updatedCluster.CustomerProperties.Version.ID)
			assert.Equal(t, tc.expectedChannelGroup, updatedCluster.CustomerProperties.Version.ChannelGroup)
		})
	}
}

// newTestClusterForMigration creates a test HCPOpenShiftCluster with default values
// including Location for use with LegacyCreateInternalClusterFromClusterService.
func newTestClusterForMigration(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	cluster := newTestCluster(opts...)
	cluster.Location = testLocation
	return cluster
}

// buildFullCSCluster creates a mock Cluster Service cluster with all required fields
// for LegacyCreateInternalClusterFromClusterService.
func buildFullCSCluster(versionID, channelGroup string) *arohcpv1alpha1.Cluster {
	cluster, err := arohcpv1alpha1.NewCluster().
		API(arohcpv1alpha1.NewClusterAPI().
			Listening(arohcpv1alpha1.ListeningMethodExternal)).
		Azure(arohcpv1alpha1.NewAzure().
			EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
				DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
					KeyManagementMode("platform_managed"))).
			ManagedResourceGroupName("managed-rg").
			NetworkSecurityGroupResourceID("").
			NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
				OutboundType("load_balancer")).
			OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().
				ManagedIdentities(arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
					ControlPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)).
					DataPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)).
					ManagedIdentitiesDataPlaneIdentityUrl(""))).
			SubnetResourceID(testSubnetID)).
		Console(arohcpv1alpha1.NewClusterConsole().URL(testConsoleURL)).
		DNS(arohcpv1alpha1.NewDNS().BaseDomain(testBaseDomain)).
		DomainPrefix(testBaseDomainPrefix).
		Network(arohcpv1alpha1.NewNetwork().
			HostPrefix(23).
			MachineCIDR("10.0.0.0/16").
			PodCIDR("10.128.0.0/14").
			ServiceCIDR("172.30.0.0/16").
			Type("OVNKubernetes")).
		Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
			PodPriorityThreshold(-10).
			MaxNodeProvisionTime("15m").
			MaxPodGracePeriod(600)).
		Version(arohcpv1alpha1.NewVersion().
			ID("openshift-v" + versionID + ".0").
			ChannelGroup(channelGroup)).
		ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
			State("enabled")).
		Build()
	if err != nil {
		panic(err)
	}
	return cluster
}

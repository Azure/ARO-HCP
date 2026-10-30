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

package properties

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestClusterPropertiesSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		existingCluster    *coreapi.Cluster
		readDesire         *kubeapplierapi.ReadDesire
		wantErr            bool
		expectedConsoleURL string
		expectedBaseDomain string
		expectedAPIURL     string
		expectedIssuerURL  string
		expectedError      string
	}{
		{
			name: "sync cluster properties from HostedCluster ReadDesire when they differ from cache",
			existingCluster: newTestCluster(testClusterName, func(c *coreapi.Cluster) {
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),
			readDesire:         newTestHostedClusterReadDesire(t),
			expectedConsoleURL: testConsoleURL,
			expectedBaseDomain: "",
			expectedAPIURL:     testAPIURL,
			expectedIssuerURL:  testIssuerURL,
		},
		{
			name: "short-circuit when cluster properties match HostedCluster ReadDesire",
			existingCluster: newTestCluster(testClusterName, func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.ServiceProviderProperties.API.URL = testAPIURL
				c.ServiceProviderProperties.Platform.IssuerURL = testIssuerURL
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),
			readDesire:         newTestHostedClusterReadDesire(t),
			expectedConsoleURL: testConsoleURL,
			expectedBaseDomain: testBaseDomain,
			expectedAPIURL:     testAPIURL,
			expectedIssuerURL:  testIssuerURL,
		},
		{
			name: "no-op when HostedCluster ReadDesire not found",
			existingCluster: newTestCluster(testOtherClusterName, func(c *coreapi.Cluster) {
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),
			readDesire: nil,
		},
		{
			name:            "no-op when base domain prefix is unset",
			existingCluster: newTestCluster(testClusterName),
			readDesire:      nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.existingCluster})
			require.NoError(t, err)

			readDesireLister, err := newSeededReadDesireLister(ctx, tc.readDesire)
			require.NoError(t, err)

			syncer := &clusterPropertiesSyncer{
				clusterLister:     &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
				resourcesDBClient: mockResourcesDB,
				readDesireLister:  readDesireLister,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    tc.existingCluster.Name,
			}
			err = syncer.SyncOnce(ctx, key)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)

				unchangedCluster, getErr := mockResourcesDB.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, tc.existingCluster.Name)
				require.NoError(t, getErr)
				assert.Empty(t, unchangedCluster.ServiceProviderProperties.Console.URL)
				assert.Equal(t, tc.existingCluster.ServiceProviderProperties.DNS.BaseDomain, unchangedCluster.ServiceProviderProperties.DNS.BaseDomain)
				assert.Empty(t, unchangedCluster.ServiceProviderProperties.API.URL)
				assert.Empty(t, unchangedCluster.ServiceProviderProperties.Platform.IssuerURL)
				return
			}
			require.NoError(t, err)

			updatedCluster, err := mockResourcesDB.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, tc.existingCluster.Name)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedConsoleURL, updatedCluster.ServiceProviderProperties.Console.URL)
			assert.Equal(t, tc.expectedBaseDomain, updatedCluster.ServiceProviderProperties.DNS.BaseDomain)
			assert.Equal(t, tc.expectedAPIURL, updatedCluster.ServiceProviderProperties.API.URL)
			assert.Equal(t, tc.expectedIssuerURL, updatedCluster.ServiceProviderProperties.Platform.IssuerURL)
		})
	}
}

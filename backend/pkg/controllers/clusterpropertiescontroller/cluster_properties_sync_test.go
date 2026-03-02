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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"

	testConsoleURL       = "https://console.example.com"
	testBaseDomain       = "example.openshiftapps.com"
	testBaseDomainPrefix = "my-cluster"
)

func TestClusterPropertiesSyncer_SyncOnce(t *testing.T) {
	testCases := []struct {
		name                     string
		existingCluster          *api.HCPOpenShiftCluster
		csCluster                *arohcpv1alpha1.Cluster
		expectCSCall             bool
		expectCosmosUpdate       bool
		expectedConsoleURL       string
		expectedBaseDomain       string
		expectedBaseDomainPrefix string
	}{
		{
			name: "short-circuit when all properties already set",
			existingCluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),
			expectCSCall:             false,
			expectCosmosUpdate:       false,
			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
		},
		{
			name:            "sync all properties from CS when all are missing",
			existingCluster: newTestCluster(),
			csCluster: buildCSCluster(
				testConsoleURL,
				testBaseDomain,
				testBaseDomainPrefix,
			),
			expectCSCall:             true,
			expectCosmosUpdate:       true,
			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
		},
		{
			name: "sync only missing Console.URL",
			existingCluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),
			csCluster: buildCSCluster(
				testConsoleURL,
				testBaseDomain,
				testBaseDomainPrefix,
			),
			expectCSCall:             true,
			expectCosmosUpdate:       true,
			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
		},
		{
			name: "sync only missing DNS.BaseDomain",
			existingCluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),
			csCluster: buildCSCluster(
				testConsoleURL,
				testBaseDomain,
				testBaseDomainPrefix,
			),
			expectCSCall:             true,
			expectCosmosUpdate:       true,
			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
		},
		{
			name: "sync only missing DNS.BaseDomainPrefix",
			existingCluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
			}),
			csCluster: buildCSCluster(
				testConsoleURL,
				testBaseDomain,
				testBaseDomainPrefix,
			),
			expectCSCall:             true,
			expectCosmosUpdate:       true,
			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
		},
		{
			name:                     "no update when CS returns empty values",
			existingCluster:          newTestCluster(),
			csCluster:                buildCSCluster("", "", ""),
			expectCSCall:             true,
			expectCosmosUpdate:       false,
			expectedConsoleURL:       "",
			expectedBaseDomain:       "",
			expectedBaseDomainPrefix: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Setup mock DB
			mockDB := databasetesting.NewMockDBClient()

			// Create the cluster in the mock DB
			clusterCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName)
			_, err := clusterCRUD.Create(ctx, tc.existingCluster, nil)
			require.NoError(t, err)

			// Setup mock CS client
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tc.expectCSCall {
				mockCSClient.EXPECT().
					GetCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(tc.csCluster, nil)
			}

			// Create syncer
			syncer := &clusterPropertiesSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
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
			require.NoError(t, err)

			// Verify the cluster state in Cosmos
			updatedCluster, err := clusterCRUD.Get(ctx, testClusterName)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedConsoleURL, updatedCluster.ServiceProviderProperties.Console.URL)
			assert.Equal(t, tc.expectedBaseDomain, updatedCluster.ServiceProviderProperties.DNS.BaseDomain)
			assert.Equal(t, tc.expectedBaseDomainPrefix, updatedCluster.CustomerProperties.DNS.BaseDomainPrefix)
		})
	}
}

// newTestCluster creates a test HCPOpenShiftCluster with default values.
// Options can be provided to customize the cluster.
func newTestCluster(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID(testClusterServiceIDStr)),
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}

// buildCSCluster creates a mock Cluster Service cluster with the given values.
func buildCSCluster(consoleURL, baseDomain, domainPrefix string) *arohcpv1alpha1.Cluster {
	cluster, err := arohcpv1alpha1.NewCluster().
		Console(arohcpv1alpha1.NewClusterConsole().URL(consoleURL)).
		DNS(arohcpv1alpha1.NewDNS().BaseDomain(baseDomain)).
		DomainPrefix(domainPrefix).
		Build()
	if err != nil {
		panic(err)
	}
	return cluster
}

// alwaysSyncCooldownChecker always allows syncing
type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

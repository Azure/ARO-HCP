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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testOtherClusterName    = "other-cluster"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"

	testConsoleURL                     = "https://console-openshift-console.apps.aro.cluster1.example.com"
	testBaseDomain                     = "example.com"
	testHostedClusterIngressBaseDomain = "aro.cluster1.example.com"
	testBaseDomainPrefix               = "cluster1"
	testAPIHost                        = "api.cluster1.example.com"
	testAPIPort                        = int32(6443)
	testAPIURL                         = "https://api.cluster1.example.com:6443"
	testIssuerURL                      = "https://issuer.example.com/cluster1"
)

func TestClusterPropertiesSyncer_SyncOnce(t *testing.T) {
	hostedClusterReadDesire := newHostedClusterReadDesire(t, &hsv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testBaseDomainPrefix},
		Spec: hsv1beta1.HostedClusterSpec{
			DNS:                  hsv1beta1.DNSSpec{BaseDomain: testHostedClusterIngressBaseDomain},
			KubeAPIServerDNSName: testAPIHost,
			IssuerURL:            testIssuerURL,
		},
		Status: hsv1beta1.HostedClusterStatus{
			ControlPlaneEndpoint: hsv1beta1.APIEndpoint{Port: testAPIPort},
		},
	})

	testCases := []struct {
		name                     string
		existingCluster          *api.HCPOpenShiftCluster
		csDomainPrefix           string
		expectCSGetCluster       bool
		expectedConsoleURL       string
		expectedBaseDomain       string
		expectedBaseDomainPrefix string
		expectedAPIURL           string
		expectedIssuerURL        string
	}{
		{
			name: "short-circuit when all properties already set",
			existingCluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.ServiceProviderProperties.API.URL = testAPIURL
				c.ServiceProviderProperties.Platform.IssuerURL = testIssuerURL
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),

			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           testAPIURL,
			expectedIssuerURL:        testIssuerURL,
		},
		{
			name:                     "sync all properties from Cluster Service and HostedCluster when all are missing",
			existingCluster:          newTestCluster(testClusterName),
			csDomainPrefix:           testBaseDomainPrefix,
			expectCSGetCluster:       true,
			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           testAPIURL,
			expectedIssuerURL:        testIssuerURL,
		},
		{
			name: "sync only missing Console.URL",
			existingCluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.ServiceProviderProperties.API.URL = testAPIURL
				c.ServiceProviderProperties.Platform.IssuerURL = testIssuerURL
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),

			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           testAPIURL,
			expectedIssuerURL:        testIssuerURL,
		},
		{
			name: "sync only missing DNS.BaseDomain",
			existingCluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.API.URL = testAPIURL
				c.ServiceProviderProperties.Platform.IssuerURL = testIssuerURL
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),

			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           testAPIURL,
			expectedIssuerURL:        testIssuerURL,
		},
		{
			name: "sync only missing API.URL",
			existingCluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.ServiceProviderProperties.Platform.IssuerURL = testIssuerURL
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),

			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           testAPIURL,
			expectedIssuerURL:        testIssuerURL,
		},
		{
			name: "sync only missing DNS.BaseDomainPrefix from Cluster Service",
			existingCluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.ServiceProviderProperties.API.URL = testAPIURL
				c.ServiceProviderProperties.Platform.IssuerURL = testIssuerURL
			}),
			csDomainPrefix:           testBaseDomainPrefix,
			expectCSGetCluster:       true,
			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           testAPIURL,
			expectedIssuerURL:        testIssuerURL,
		},
		{
			name: "sync only missing Platform.IssuerURL",
			existingCluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.Console.URL = testConsoleURL
				c.ServiceProviderProperties.DNS.BaseDomain = testBaseDomain
				c.ServiceProviderProperties.API.URL = testAPIURL
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),

			expectedConsoleURL:       testConsoleURL,
			expectedBaseDomain:       testBaseDomain,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           testAPIURL,
			expectedIssuerURL:        testIssuerURL,
		},
		{
			name:                     "sync domain prefix from Cluster Service when HostedCluster ReadDesire not found",
			existingCluster:          newTestCluster(testOtherClusterName),
			csDomainPrefix:           testBaseDomainPrefix,
			expectCSGetCluster:       true,
			expectedConsoleURL:       "",
			expectedBaseDomain:       "",
			expectedBaseDomainPrefix: testBaseDomainPrefix,
			expectedAPIURL:           "",
			expectedIssuerURL:        "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Setup mock DB with the existing cluster
			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.existingCluster})
			require.NoError(t, err)

			readDesireLister, err := newSeededReadDesireLister(ctx, hostedClusterReadDesire)
			require.NoError(t, err)

			// Setup mock CS client
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.expectCSGetCluster {
				csCluster, err := arohcpv1alpha1.NewCluster().DomainPrefix(tc.csDomainPrefix).Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetCluster(gomock.Any(), gomock.Any()).
					Return(csCluster, nil)
			}

			// Create syncer
			syncer := &clusterPropertiesSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				resourcesDBClient:    mockResourcesDB,
				clusterServiceClient: mockCSClient,
				readDesireLister:     readDesireLister,
			}

			// Execute
			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    tc.existingCluster.Name,
			}
			err = syncer.SyncOnce(ctx, key)
			require.NoError(t, err)

			// Verify the cluster state in Cosmos
			updatedCluster, err := mockResourcesDB.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, tc.existingCluster.Name)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedConsoleURL, updatedCluster.ServiceProviderProperties.Console.URL)
			assert.Equal(t, tc.expectedBaseDomain, updatedCluster.ServiceProviderProperties.DNS.BaseDomain)
			assert.Equal(t, tc.expectedAPIURL, updatedCluster.ServiceProviderProperties.API.URL)
			assert.Equal(t, tc.expectedBaseDomainPrefix, updatedCluster.CustomerProperties.DNS.BaseDomainPrefix)
			assert.Equal(t, tc.expectedIssuerURL, updatedCluster.ServiceProviderProperties.Platform.IssuerURL)
		})
	}
}

func newSeededReadDesireLister(ctx context.Context, readDesire *kubeapplier.ReadDesire) (dblisters.ReadDesireLister, error) {
	mockKubeApplierDB, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{readDesire})
	if err != nil {
		return nil, err
	}

	kubeApplierClients := databasetesting.NewMockKubeApplierDBClients()
	managementClusterID := api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	kubeApplierClients.Register(managementClusterID, mockKubeApplierDB)

	return &internallistertesting.DBReadDesireLister{
		Clients: kubeApplierClients,
		Lister: &internallistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleet.ManagementCluster{
				{
					CosmosMetadata: api.CosmosMetadata{ResourceID: managementClusterID},
					ResourceID:     managementClusterID,
				},
			},
		},
	}, nil
}

func newTestCluster(hcpClusterName string, opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + hcpClusterName,
	))

	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: hcpClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID(testClusterServiceIDStr))),
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}

func newHostedClusterReadDesire(t *testing.T, hostedCluster *hsv1beta1.HostedCluster) *kubeapplier.ReadDesire {
	t.Helper()

	resourceIDString := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		testSubscriptionID,
		testResourceGroupName,
		testClusterName,
		maestrohelpers.ReadDesireNameReadonlyHostedCluster,
	)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDString))

	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: api.Must(azcorearm.ParseResourceID(
				"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a")),
		},
		Status: kubeapplier.ReadDesireStatus{
			KubeContent: &runtime.RawExtension{Raw: raw},
		},
	}
}

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

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
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestRequestIDPropagator(t *testing.T) {
	const testRequestID = "00000000-0000-0000-0000-000000000000"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get(clusterServiceRequestIDHeader)))
	}))
	defer ts.Close()

	do := func(c *http.Client) string {
		t.Helper()

		ctx := context.Background()
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
		require.NoError(t, err)
		correlationData := arm.NewCorrelationData(r)
		correlationData.RequestID = uuid.MustParse(testRequestID)
		r = r.WithContext(ContextWithCorrelationData(ctx, correlationData))

		rs, err := c.Do(r)
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, rs.StatusCode)

		b, err := io.ReadAll(rs.Body)
		require.NoError(t, err)

		return string(b)
	}

	// Without the transport wrapper, the request ID isn't echoed.
	c := ts.Client()
	assert.Empty(t, do(c))

	// With the transport wrapper, the request ID is echoed.
	c.Transport = RequestIDPropagator(c.Transport)
	assert.Equal(t, testRequestID, do(c))
}

func TestConvertCStoHCPOpenShiftCluster(t *testing.T) {
	resourceID := testResourceID(t)
	testCases := []struct {
		name    string
		cluster *arohcpv1alpha1.ClusterBuilder
		want    *api.HCPOpenShiftCluster
	}{
		{
			name:    "zero",
			cluster: arohcpv1alpha1.NewCluster(),
			want:    clusterResource(),
		},
		{
			name: "converts nodeDrainGracePeriod to nodeDrainTimeoutMinutes",
			cluster: arohcpv1alpha1.NewCluster().
				NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
					Unit(azureNodePoolNodeDrainGracePeriodUnit).
					Value(42),
				),
			want: clusterResource(
				func(hsc *api.HCPOpenShiftCluster) {
					hsc.Properties.NodeDrainTimeoutMinutes = 42
				},
			),
		},
		// TODO: Uncomment when CS supports it.
		// {
		// 	name: "converts EtcdEncryption for only default PlatformManaged",
		// 	cluster: arohcpv1alpha1.NewCluster().
		// 		Azure(arohcpv1alpha1.NewAzure().
		// 			EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
		// 				DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
		// 					KeyManagementMode("PlatformManaged")),
		// 			),
		// 		),
		// 	want: clusterResource(
		// 		func(hsc *api.HCPOpenShiftCluster) {
		// 			hsc.Properties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged
		// 			hsc.Properties.Etcd.DataEncryption.CustomerManaged = api.CustomerManagedEncryptionProfile{}
		// 		},
		// 	),
		// },
		{
			name: "converts CS ClusterImageRegistry to ClusterImageRegistryProfile",
			cluster: arohcpv1alpha1.NewCluster().
				ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
					State(string(csImageRegistryStateDisabled)),
				),
			want: clusterResource(
				func(hsc *api.HCPOpenShiftCluster) {
					hsc.Properties.ClusterImageRegistry = api.ClusterImageRegistryProfile{
						State: api.ClusterImageRegistryProfileStateDisabled,
					}
				},
			),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster, err := tc.cluster.Build()
			require.NoError(t, err)
			assert.Equalf(t, tc.want, ConvertCStoHCPOpenShiftCluster(resourceID, cluster), "ConvertCStoHCPOpenShiftCluster(%v, %v)", resourceID, cluster)
		})
	}
}

func TestWithImmutableAttributes(t *testing.T) {
	testCases := []struct {
		name       string
		hcpCluster *api.HCPOpenShiftCluster
		want       *arohcpv1alpha1.Cluster
	}{
		{
			name: "simple default",
			hcpCluster: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						ManagedResourceGroup: "test",
					},
				},
			},
			want: ocmCluster(t, withOCMClusterDefaults()),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, arohcpv1alpha1.MarshalCluster(tc.want, &buf))
			want := buf.String()
			result, err := withImmutableAttributes(arohcpv1alpha1.NewCluster(), tc.hcpCluster, "test", "test", "test", "test", "").Build()
			require.NoError(t, err)
			buf.Reset()
			require.NoError(t, arohcpv1alpha1.MarshalCluster(result, &buf))
			got := buf.String()
			assert.JSONEqf(t, want, got, "withImmutableAttributes(%v, %v, %v, %v, %v, %v, %v)", "NewCluster()", tc.hcpCluster, "test", "test", "test", "test", "test")
		})
	}
}

func testResourceID(t *testing.T) *azcorearm.ResourceID {
	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)
	return resourceID
}

func clusterResource(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	c := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   api.TestClusterResourceID,
				Name: api.TestClusterName,
				Type: api.ClusterResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterProperties{},
	}
	for _, opt := range opts {
		opt(c)
	}
	// Temporarily add a default that CS doesn't supply.
	c.Properties.Etcd = api.EtcdProfile{
		DataEncryption: api.EtcdDataEncryptionProfile{
			KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
		},
	}
	return c
}

func ocmCluster(t *testing.T, opts ...func(*arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.Cluster {
	b := arohcpv1alpha1.NewCluster()
	for _, opt := range opts {
		b = opt(b)
	}
	c, err := b.Build()
	assert.NoError(t, err)
	return c
}

func withOCMClusterDefaults() func(*arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
	return func(b *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
		// This reflects how the immutable attributes get set when passed an empty[*] RP
		// cluster. (well, not exactly empty, need to set Platform.ManagedResourceGroupName
		// so that we don't get a corresdponding random value in the output.)
		return b.
			API(arohcpv1alpha1.NewClusterAPI().Listening("")).
			Azure(arohcpv1alpha1.NewAzure().
				ManagedResourceGroupName("test").
				NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
					OutboundType("")).
				ResourceGroupName("test").
				ResourceName("").
				SubnetResourceID("").
				SubscriptionID("test").
				TenantID("test"),
			).
			CCS(arohcpv1alpha1.NewCCS().Enabled(true)).
			CloudProvider(cmv1.NewCloudProvider().
				ID("azure")).
			Flavour(cmv1.NewFlavour().
				ID("osd-4")).
			Hypershift(arohcpv1alpha1.NewHypershift().
				Enabled(true)).
			Name("").
			Network(arohcpv1alpha1.NewNetwork().
				HostPrefix(0).
				MachineCIDR("").
				PodCIDR("").
				ServiceCIDR("").
				Type("")).
			Product(cmv1.NewProduct().
				ID("aro")).
			Region(cmv1.NewCloudRegion().
				ID("test")).
			Version(arohcpv1alpha1.NewVersion().
				ID("").
				ChannelGroup("")).
			ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
				State(""))
	}
}

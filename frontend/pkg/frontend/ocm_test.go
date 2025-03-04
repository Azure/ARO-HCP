package frontend

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	clustersmgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

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
			name: "disabled capability",
			cluster: arohcpv1alpha1.NewCluster().
				Azure(arohcpv1alpha1.NewAzure().
					ClusterCapabilities(
						arohcpv1alpha1.NewAzureClusterCapabilities().
							Disabled("red"),
					),
				),
			want: clusterResource(withDisabledCapabilities("red")),
		},
		{
			name: "disabled capabilities",
			cluster: arohcpv1alpha1.NewCluster().
				Azure(arohcpv1alpha1.NewAzure().
					ClusterCapabilities(
						arohcpv1alpha1.NewAzureClusterCapabilities().
							Disabled("red", "green", "blue"),
					),
				),
			want: clusterResource(withDisabledCapabilities("red", "green", "blue")),
		},
		{
			name: "disabled capabilities empty",
			cluster: arohcpv1alpha1.NewCluster().
				Azure(arohcpv1alpha1.NewAzure().
					ClusterCapabilities(arohcpv1alpha1.NewAzureClusterCapabilities()),
				),
			want: clusterResource(),
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
					Spec: api.ClusterSpec{
						Platform: api.PlatformProfile{
							ManagedResourceGroup: "test",
						},
					},
				},
			},
			want: ocmCluster(t, withOCMClusterDefaults()),
		},
		{
			name: "disabled capability",
			hcpCluster: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Spec: api.ClusterSpec{
						Platform: api.PlatformProfile{
							ManagedResourceGroup: "test",
						},
						Capabilities: api.ClusterCapabilitiesProfile{
							Disabled: []*api.OptionalClusterCapability{
								api.Ptr(api.OptionalClusterCapability("TEST_ONE")),
							},
						},
					},
				},
			},
			want: ocmCluster(t, withOCMClusterDefaults(), withOCMDisabledCapabilities("TEST_ONE")),
		},
		{
			name: "disabled capabilities",
			hcpCluster: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Spec: api.ClusterSpec{
						Platform: api.PlatformProfile{
							ManagedResourceGroup: "test",
						},
						Capabilities: api.ClusterCapabilitiesProfile{
							Disabled: []*api.OptionalClusterCapability{
								api.Ptr(api.OptionalClusterCapability("TEST_ONE")),
								api.Ptr(api.OptionalClusterCapability("TEST_TWO")),
							},
						},
					},
				},
			},
			want: ocmCluster(t, withOCMClusterDefaults(), withOCMDisabledCapabilities("TEST_ONE", "TEST_TWO")),
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
	resourceID, err := azcorearm.ParseResourceID("/subscriptions/test/resourceGroups/test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test")
	require.NoError(t, err)
	return resourceID
}

func clusterResource(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	c := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   "/subscriptions/test/resourceGroups/test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test",
				Name: "test",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		Properties: api.HCPOpenShiftClusterProperties{
			Spec: api.ClusterSpec{
				ExternalAuth: api.ExternalAuthConfigProfile{
					ExternalAuths: []*configv1.OIDCProvider{},
				},
			},
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func withDisabledCapabilities(caps ...string) func(*api.HCPOpenShiftCluster) {
	return func(c *api.HCPOpenShiftCluster) {
		for _, capability := range caps {
			c.Properties.Spec.Capabilities.Disabled = append(c.Properties.Spec.Capabilities.Disabled, api.Ptr(api.OptionalClusterCapability(capability)))
		}
	}
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
func withOCMDisabledCapabilities(values ...string) func(*arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
	var occs []arohcpv1alpha1.AzureOptionalClusterCapability
	for _, value := range values {
		occs = append(occs, arohcpv1alpha1.AzureOptionalClusterCapability(value))
	}
	return func(b *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
		// we don't have a reference to a possibly already existing AzureBuilder, so we
		// reset have to reset the defaults for the AzureBuilder here.
		return b.
			Azure(arohcpv1alpha1.NewAzure().
				ClusterCapabilities(arohcpv1alpha1.NewAzureClusterCapabilities().
					Disabled(occs...)).
				ManagedResourceGroupName("test").
				NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
					OutboundType("")).
				ResourceGroupName("test").
				ResourceName("").
				SubnetResourceID("").
				SubscriptionID("test").
				TenantID("test"))
	}
}

func withOCMClusterDefaults() func(*arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
	return func(b *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
		// This reflects how the immutable attributes get set when passed an empty[*] RP
		// cluster. (well, not exactly empty, need to set Platform.ManagedResourceGroupName
		// so that we don't get a corresdponding random value in the output.)
		return b.
			API(arohcpv1alpha1.NewClusterAPI().Listening("")).
			Azure(arohcpv1alpha1.NewAzure().
				ClusterCapabilities(arohcpv1alpha1.NewAzureClusterCapabilities().
					Disabled()).
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
			FIPS(false).
			CloudProvider(clustersmgmtv1.NewCloudProvider().
				ID("azure")).
			EtcdEncryption(false).
			Flavour(clustersmgmtv1.NewFlavour().
				ID("osd-4")).
			Hypershift(arohcpv1alpha1.NewHypershift().
				Enabled(true)).
			MultiAZ(true).
			Name("").
			Network(arohcpv1alpha1.NewNetwork().
				HostPrefix(0).
				MachineCIDR("").
				PodCIDR("").
				ServiceCIDR("").
				Type("")).
			Product(clustersmgmtv1.NewProduct().
				ID("aro")).
			Region(clustersmgmtv1.NewCloudRegion().
				ID("test")).
			Version(clustersmgmtv1.NewVersion().
				ID("").
				ChannelGroup(""))
	}
}

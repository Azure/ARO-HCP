package ocm

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	v1 "github.com/openshift/api/config/v1"
	"log"
)

func ConvertCStoFrontend(cluster cmv1.Cluster) (*api.HCPOpenShiftCluster, error) {
	hcpcluster := api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Location: "",
			Tags:     nil,
			Resource: arm.Resource{
				ID:   cluster.ID(),
				Name: cluster.Name(),
				Type: cluster.Flavour().ID(),
				SystemData: &arm.SystemData{
					CreatedBy:          "",
					CreatedByType:      "",
					CreatedAt:          nil,
					LastModifiedBy:     "",
					LastModifiedByType: "",
					LastModifiedAt:     nil,
				},
			},
		},
		Properties: api.HCPOpenShiftClusterProperties{
			Spec: api.ClusterSpec{
				Version: api.VersionProfile{
					ID:                cluster.Version().ID(),
					ChannelGroup:      cluster.Version().ChannelGroup(),
					AvailableUpgrades: cluster.Version().AvailableUpgrades(),
				},
				DNS: api.DNSProfile{
					BaseDomain:       cluster.DNS().BaseDomain(),
					BaseDomainPrefix: cluster.DomainPrefix(),
				},
				Network: api.NetworkProfile{
					NetworkType: api.NetworkType(cluster.Network().Type()),
					PodCIDR:     cluster.Network().PodCIDR(),
					ServiceCIDR: cluster.Network().ServiceCIDR(),
					MachineCIDR: cluster.Network().MachineCIDR(),
					HostPrefix:  int32(cluster.Network().HostPrefix()),
				},
				Console: api.ConsoleProfile{
					URL: cluster.Console().URL(),
				},
				API: api.APIProfile{
					URL:        cluster.API().URL(),
					IP:         "",
					Visibility: api.Visibility(cluster.API().Listening()),
				},
				FIPS:                          cluster.FIPS(),
				EtcdEncryption:                cluster.EtcdEncryption(),
				DisableUserWorkloadMonitoring: cluster.DisableUserWorkloadMonitoring(),
				Proxy: api.ProxyProfile{
					HTTPProxy:  cluster.Proxy().HTTPProxy(),
					HTTPSProxy: cluster.Proxy().HTTPSProxy(),
					NoProxy:    cluster.Proxy().NoProxy(),
					TrustedCA:  cluster.AdditionalTrustBundle(),
				},
				Platform: api.PlatformProfile{
					ManagedResourceGroup:   cluster.Azure().ManagedResourceGroupName(),
					SubnetID:               cluster.Azure().SubnetResourceID(),
					OutboundType:           "",
					NetworkSecurityGroupID: cluster.Azure().NetworkSecurityGroupResourceID(),
					EtcdEncryptionSetID:    "",
				},
				IssuerURL: "",
				ExternalAuth: api.ExternalAuthConfigProfile{
					Enabled:       false,
					ExternalAuths: []*v1.OIDCProvider{},
				},
				Ingress: []*api.IngressProfile{},
			},
		},
	}

	return &hcpcluster, nil
}

func BuildCSCluster(hcpCluster *api.HCPOpenShiftCluster) (*cmv1.Cluster, error) {

	azureSpec := cmv1.NewAzure().
		ManagedResourceGroupName(hcpCluster.Properties.Spec.Platform.ManagedResourceGroup).
		ResourceGroupName("xyz").
		SubnetResourceID(hcpCluster.Properties.Spec.Platform.SubnetID).
		NetworkSecurityGroupResourceID(hcpCluster.Properties.Spec.Platform.NetworkSecurityGroupID).
		ResourceName(hcpCluster.Name).
		SubscriptionID("00000000-0000-0000-0000-000000000000").
		TenantID("anatale-test-tenant")

	clusterBuilder := cmv1.NewCluster().
		Name(hcpCluster.Name).
		Flavour(cmv1.NewFlavour().
			ID(hcpCluster.Type)).
		Version(cmv1.NewVersion().
			ID(hcpCluster.Properties.Spec.Version.ID).
			ChannelGroup(hcpCluster.Properties.Spec.Version.ChannelGroup)).
		Network(cmv1.NewNetwork().
			Type(string(hcpCluster.Properties.Spec.Network.NetworkType)).
			PodCIDR(hcpCluster.Properties.Spec.Network.PodCIDR).
			ServiceCIDR(hcpCluster.Properties.Spec.Network.ServiceCIDR).
			MachineCIDR(hcpCluster.Properties.Spec.Network.MachineCIDR).
			HostPrefix(int(hcpCluster.Properties.Spec.Network.HostPrefix))).
		Console(cmv1.NewClusterConsole().
			URL(hcpCluster.Properties.Spec.Console.URL)).
		API(cmv1.NewClusterAPI().
			URL(hcpCluster.Properties.Spec.Console.URL).
			Listening(cmv1.ListeningMethod(hcpCluster.Properties.Spec.API.Visibility))).
		FIPS(hcpCluster.Properties.Spec.FIPS).
		EtcdEncryption(hcpCluster.Properties.Spec.EtcdEncryption).
		DisableUserWorkloadMonitoring(hcpCluster.Properties.Spec.DisableUserWorkloadMonitoring).
		Proxy(cmv1.NewProxy().
			HTTPProxy(hcpCluster.Properties.Spec.Proxy.HTTPProxy).
			HTTPSProxy(hcpCluster.Properties.Spec.Proxy.HTTPSProxy).
			NoProxy(hcpCluster.Properties.Spec.Proxy.NoProxy)).
		AdditionalTrustBundle(hcpCluster.Properties.Spec.Proxy.TrustedCA).
		Azure(azureSpec).
		Region(cmv1.NewCloudRegion().
			ID("eastus")).
		CloudProvider(cmv1.NewCloudProvider().
			ID("azure")).
		Product(cmv1.NewProduct().
			ID("aro")).
		Hypershift(cmv1.NewHypershift().
			Enabled(true)).
		MultiAZ(true)

	log.Print("BUILD_CS_CLUSTER_CALL")
	log.Printf("PROVIDER: %+v", azureSpec)
	log.Printf("CLUSTER BUILDER: %+v", clusterBuilder)

	cluster, err := clusterBuilder.Build()
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

package ocm

import (
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	v1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	csCloudProvider    string = "azure"
	csProductId        string = "aro"
	csHypershifEnabled bool   = true
	csMultiAzEnabled   bool   = true
	csCCSEnabled       bool   = true
)

// ConvertCStoFrontend converts a CS Cluster object into HCPOpenShiftCluster object
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

// BuildCSCluster creates a CS Cluster object from an HCPOpenShiftCluster object
func BuildCSCluster(rg string, subID string, hcpCluster *api.HCPOpenShiftCluster) (*cmv1.Cluster, error) {

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
		AdditionalTrustBundle(hcpCluster.Properties.Spec.Proxy.TrustedCA).
		Azure(cmv1.NewAzure().
			ManagedResourceGroupName(hcpCluster.Properties.Spec.Platform.ManagedResourceGroup).
			ResourceGroupName(rg).
			SubnetResourceID(hcpCluster.Properties.Spec.Platform.SubnetID).
			NetworkSecurityGroupResourceID(hcpCluster.Properties.Spec.Platform.NetworkSecurityGroupID).
			ResourceName(hcpCluster.Name).
			SubscriptionID(subID).
			TenantID("dev-test-tenant")). // tenant ID is not available at this time -- this is a dummy placeholder)
		Region(cmv1.NewCloudRegion().
			ID("eastus")). // Region is not available at this time -- this is a dummy placeholder)
		CloudProvider(cmv1.NewCloudProvider().
			ID(csCloudProvider)).
		Product(cmv1.NewProduct().
			ID(csProductId)).
		Hypershift(cmv1.NewHypershift().
			Enabled(csHypershifEnabled)).
		MultiAZ(csMultiAzEnabled).
		CCS(cmv1.NewCCS().Enabled(csCCSEnabled)).
		Properties(map[string]string{ // per CS, required for testing locally)
			"provision_shard_id":           "1",
			"provisioner_noop_provision":   "true",
			"provisioner_noop_deprovision": "true",
		}) // temporary values for testing purposes

	cluster, err := clusterBuilder.Build()
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

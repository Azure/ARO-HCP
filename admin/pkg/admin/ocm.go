package admin

import (
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func ConvertCStoHCPOpenShiftCluster(resourceID *arm.ResourceID, cluster *cmv1.Cluster) *api.HCPOpenShiftCluster {
	hcpcluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Location: cluster.Region().ID(),
			Resource: arm.Resource{
				ID:   resourceID.String(),
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterProperties{
			// ProvisioningState: cluster.State(), // TODO: align with OCM on ProvisioningState
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
					Visibility: convertListeningToVisibility(cluster.API().Listening()),
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
					OutboundType:           api.OutboundTypeLoadBalancer,
					NetworkSecurityGroupID: cluster.Azure().NetworkSecurityGroupResourceID(),
					EtcdEncryptionSetID:    "",
				},
				IssuerURL: "",
				ExternalAuth: api.ExternalAuthConfigProfile{
					Enabled:       false,
					ExternalAuths: []*configv1.OIDCProvider{},
				},
			},
		},
	}

	return hcpcluster
}

func convertListeningToVisibility(listening cmv1.ListeningMethod) (visibility api.Visibility) {
	switch listening {
	case cmv1.ListeningMethodExternal:
		visibility = api.VisibilityPublic
	case cmv1.ListeningMethodInternal:
		visibility = api.VisibilityPrivate
	}
	return
}

package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

func (v version) NewHCPOpenShiftCluster(from *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	out := &HCPOpenShiftCluster{
		Properties: HCPOpenShiftClusterProperties{
			ProvisioningState: from.Properties.ProvisioningState,
			ClusterProfile: ClusterProfile{
				ControlPlaneVersion:  from.Properties.ClusterProfile.ControlPlaneVersion,
				SubnetID:             from.Properties.ClusterProfile.SubnetID,
				ManagedResourceGroup: from.Properties.ClusterProfile.ManagedResourceGroup,
				OIDCIssuerURL:        from.Properties.ClusterProfile.OIDCIssuerURL,
			},
			ProxyProfile: ProxyProfile{
				HTTPProxy:  from.Properties.ProxyProfile.HTTPProxy,
				HTTPSProxy: from.Properties.ProxyProfile.HTTPSProxy,
				NoProxy:    from.Properties.ProxyProfile.NoProxy,
				TrustedCA:  from.Properties.ProxyProfile.TrustedCA,
			},
			APIProfile: APIProfile{
				URL:        from.Properties.APIProfile.URL,
				IP:         from.Properties.APIProfile.IP,
				Visibility: Visibility(from.Properties.APIProfile.Visibility),
			},
			ConsoleProfile: ConsoleProfile{
				URL:  from.Properties.ConsoleProfile.URL,
				FIPS: from.Properties.ConsoleProfile.FIPS,
			},
			IngressProfile: IngressProfile{
				IP:         from.Properties.IngressProfile.IP,
				URL:        from.Properties.IngressProfile.URL,
				Visibility: Visibility(from.Properties.IngressProfile.Visibility),
			},
			NetworkProfile: NetworkProfile{
				PodCIDR:           from.Properties.NetworkProfile.PodCIDR,
				ServiceCIDR:       from.Properties.NetworkProfile.ServiceCIDR,
				MachineCIDR:       from.Properties.NetworkProfile.MachineCIDR,
				HostPrefix:        from.Properties.NetworkProfile.HostPrefix,
				OutboundType:      OutboundType(from.Properties.NetworkProfile.OutboundType),
				PreconfiguredNSGs: from.Properties.NetworkProfile.PreconfiguredNSGs,
			},
			NodePoolProfiles: make([]api.VersionedNodePoolProfile, 0, len(from.Properties.NodePoolProfiles)),
			EtcdEncryption: EtcdEncryptionProfile{
				DiscEncryptionSetID: from.Properties.EtcdEncryption.DiscEncryptionSetID,
			},
		},
	}

	out.TrackedResource.Copy(&from.TrackedResource)

	for _, item := range from.Properties.NodePoolProfiles {
		out.Properties.NodePoolProfiles = append(
			out.Properties.NodePoolProfiles,
			v.NewNodePoolProfile(item))
	}

	return out
}

func (c *HCPOpenShiftCluster) Normalize(out *api.HCPOpenShiftCluster) {
	c.TrackedResource.Copy(&out.TrackedResource)
	out.Properties.ProvisioningState = c.Properties.ProvisioningState
	out.Properties.ClusterProfile.ControlPlaneVersion = c.Properties.ClusterProfile.ControlPlaneVersion
	out.Properties.ClusterProfile.SubnetID = c.Properties.ClusterProfile.SubnetID
	out.Properties.ClusterProfile.ManagedResourceGroup = c.Properties.ClusterProfile.ManagedResourceGroup
	out.Properties.ClusterProfile.OIDCIssuerURL = c.Properties.ClusterProfile.OIDCIssuerURL
	out.Properties.ProxyProfile.HTTPProxy = c.Properties.ProxyProfile.HTTPProxy
	out.Properties.ProxyProfile.HTTPSProxy = c.Properties.ProxyProfile.HTTPSProxy
	out.Properties.ProxyProfile.NoProxy = c.Properties.ProxyProfile.NoProxy
	out.Properties.ProxyProfile.TrustedCA = c.Properties.ProxyProfile.TrustedCA
	out.Properties.APIProfile.URL = c.Properties.APIProfile.URL
	out.Properties.APIProfile.IP = c.Properties.APIProfile.IP
	out.Properties.APIProfile.Visibility = api.Visibility(c.Properties.APIProfile.Visibility)
	out.Properties.ConsoleProfile.URL = c.Properties.ConsoleProfile.URL
	out.Properties.ConsoleProfile.FIPS = c.Properties.ConsoleProfile.FIPS
	out.Properties.IngressProfile.IP = c.Properties.IngressProfile.IP
	out.Properties.IngressProfile.URL = c.Properties.IngressProfile.URL
	out.Properties.IngressProfile.Visibility = api.Visibility(c.Properties.IngressProfile.Visibility)
	out.Properties.NetworkProfile.PodCIDR = c.Properties.NetworkProfile.PodCIDR
	out.Properties.NetworkProfile.ServiceCIDR = c.Properties.NetworkProfile.ServiceCIDR
	out.Properties.NetworkProfile.MachineCIDR = c.Properties.NetworkProfile.MachineCIDR
	out.Properties.NetworkProfile.HostPrefix = c.Properties.NetworkProfile.HostPrefix
	out.Properties.NetworkProfile.OutboundType = api.OutboundType(c.Properties.NetworkProfile.OutboundType)
	out.Properties.NetworkProfile.PreconfiguredNSGs = c.Properties.NetworkProfile.PreconfiguredNSGs
	out.Properties.NodePoolProfiles = make([]*api.NodePoolProfile, 0, len(c.Properties.NodePoolProfiles))
	for _, item := range c.Properties.NodePoolProfiles {
		npp := &api.NodePoolProfile{}
		item.Normalize(npp)
		out.Properties.NodePoolProfiles = append(
			out.Properties.NodePoolProfiles, npp)
	}
	out.Properties.EtcdEncryption = api.EtcdEncryptionProfile{
		DiscEncryptionSetID: c.Properties.EtcdEncryption.DiscEncryptionSetID,
	}
}

func (c *HCPOpenShiftCluster) ValidateStatic() error {
	return nil
}

package frontend

import (
	"context"
	"fmt"

	cmv2alpha1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v2alpha1"
	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	csFlavourId        string = "osd-4" // managed cluster
	csCloudProvider    string = "azure"
	csProductId        string = "aro"
	csHypershifEnabled bool   = true
	csMultiAzEnabled   bool   = true
	csCCSEnabled       bool   = true
)

func convertListeningToVisibility(listening cmv2alpha1.ListeningMethod) (visibility api.Visibility) {
	switch listening {
	case cmv2alpha1.ListeningMethodExternal:
		visibility = api.VisibilityPublic
	case cmv2alpha1.ListeningMethodInternal:
		visibility = api.VisibilityPrivate
	}
	return
}

func convertVisibilityToListening(visibility api.Visibility) (listening cmv2alpha1.ListeningMethod) {
	switch visibility {
	case api.VisibilityPublic:
		listening = cmv2alpha1.ListeningMethodExternal
	case api.VisibilityPrivate:
		listening = cmv2alpha1.ListeningMethodInternal
	}
	return
}

// ConvertCStoHCPOpenShiftCluster converts a CS Cluster object into HCPOpenShiftCluster object
func (f *Frontend) ConvertCStoHCPOpenShiftCluster(systemData *arm.SystemData, cluster *cmv2alpha1.Cluster) (*api.HCPOpenShiftCluster, error) {
	resourceGroupName := cluster.Azure().ResourceGroupName()
	resourceName := cluster.Azure().ResourceName()
	subID := cluster.Azure().SubscriptionID()
	resourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s", subID, resourceGroupName, api.ResourceType, resourceName)

	hcpcluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Location: cluster.Region().ID(),
			Tags:     nil, // TODO: OCM should support cluster.Azure().Tags(),
			Resource: arm.Resource{
				ID:         resourceID,
				Name:       resourceName,
				Type:       api.ResourceType,
				SystemData: systemData,
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

	return hcpcluster, nil
}

// BuildCSCluster creates a CS Cluster object from an HCPOpenShiftCluster object
func (f *Frontend) BuildCSCluster(ctx context.Context, hcpCluster *api.HCPOpenShiftCluster, updating bool) (*cmv2alpha1.Cluster, error) {
	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get parsed resource ID: %w", err)
	}
	tenantID, err := TenantIDFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get tenant ID: %w", err)
	}

	// additionalProperties should be empty in production, it is configurable for development to pin to specific
	// provision shards or instruct CS to skip the full provisioning/deprovisioning flow.
	additionalProperties := map[string]string{
		// Enable the ARO HCP provisioner during development. For now, if not set a cluster will not progress past the
		// installing state in CS.
		"provisioner_hostedcluster_step_enabled": "true",
		// Enable the provisioning of ACM's ManagedCluster CR associated to the ARO-HCP
		// cluster during ARO-HCP Cluster provisioning. For now, if not set a cluster will not progress past the
		// installing state in CS.
		"provisioner_managedcluster_step_enabled": "true",

		// Enable the provisioning and deprovisioning of ARO-HCP Node Pools. For now, if not set the provisioning
		// and deprovisioning of day 2 ARO-HCP Node Pools will not be performed on the Management Cluster.
		"np_provisioner_provision_enabled":   "true",
		"np_provisioner_deprovision_enabled": "true",
	}
	if f.clusterServiceConfig.ProvisionShardID != nil {
		additionalProperties["provision_shard_id"] = *f.clusterServiceConfig.ProvisionShardID
	}
	if f.clusterServiceConfig.ProvisionerNoOpProvision {
		additionalProperties["provisioner_noop_provision"] = "true"
	}
	if f.clusterServiceConfig.ProvisionerNoOpDeprovision {
		additionalProperties["provisioner_noop_deprovision"] = "true"
	}

	clusterBuilder := cmv2alpha1.NewCluster()

	// FIXME HcpOpenShiftCluster attributes not being passed:
	//       PlatformProfile.OutboundType        (no CS equivalent?)
	//       PlatformProfile.EtcdEncryptionSetID (no CS equivalent?)
	//       ExternalAuth                        (TODO, complicated)

	// These attributes cannot be updated after cluster creation.
	if !updating {
		clusterBuilder = clusterBuilder.
			Name(hcpCluster.Name).
			Flavour(cmv2alpha1.NewFlavour().
				ID(csFlavourId)).
			Region(cmv2alpha1.NewCloudRegion().
				ID(f.location)).
			CloudProvider(cmv2alpha1.NewCloudProvider().
				ID(csCloudProvider)).
			Azure(cmv2alpha1.NewAzure().
				TenantID(tenantID).
				SubscriptionID(resourceID.SubscriptionID).
				ResourceGroupName(resourceID.ResourceGroupName).
				ResourceName(hcpCluster.Name).
				ManagedResourceGroupName(hcpCluster.Properties.Spec.Platform.ManagedResourceGroup).
				SubnetResourceID(hcpCluster.Properties.Spec.Platform.SubnetID).
				NetworkSecurityGroupResourceID(hcpCluster.Properties.Spec.Platform.NetworkSecurityGroupID)).
			Product(cmv2alpha1.NewProduct().
				ID(csProductId)).
			Hypershift(cmv2alpha1.NewHypershift().
				Enabled(csHypershifEnabled)).
			MultiAZ(csMultiAzEnabled).
			CCS(cmv2alpha1.NewCCS().Enabled(csCCSEnabled)).
			Version(cmv2alpha1.NewVersion().
				ID(hcpCluster.Properties.Spec.Version.ID).
				ChannelGroup(hcpCluster.Properties.Spec.Version.ChannelGroup)).
			Network(cmv2alpha1.NewNetwork().
				Type(string(hcpCluster.Properties.Spec.Network.NetworkType)).
				PodCIDR(hcpCluster.Properties.Spec.Network.PodCIDR).
				ServiceCIDR(hcpCluster.Properties.Spec.Network.ServiceCIDR).
				MachineCIDR(hcpCluster.Properties.Spec.Network.MachineCIDR).
				HostPrefix(int(hcpCluster.Properties.Spec.Network.HostPrefix))).
			API(cmv2alpha1.NewClusterAPI().
				Listening(convertVisibilityToListening(hcpCluster.Properties.Spec.API.Visibility))).
			FIPS(hcpCluster.Properties.Spec.FIPS).
			EtcdEncryption(hcpCluster.Properties.Spec.EtcdEncryption)

		// Cluster Service rejects an empty DomainPrefix string.
		if hcpCluster.Properties.Spec.DNS.BaseDomainPrefix != "" {
			clusterBuilder = clusterBuilder.
				DomainPrefix(hcpCluster.Properties.Spec.DNS.BaseDomainPrefix)
		}
	}

	proxyBuilder := cmv2alpha1.NewProxy()
	// Cluster Service allows an empty HTTPProxy on PATCH but not PUT.
	if updating || hcpCluster.Properties.Spec.Proxy.HTTPProxy != "" {
		proxyBuilder = proxyBuilder.
			HTTPProxy(hcpCluster.Properties.Spec.Proxy.HTTPProxy)
	}
	// Cluster Service allows an empty HTTPSProxy on PATCH but not PUT.
	if updating || hcpCluster.Properties.Spec.Proxy.HTTPSProxy != "" {
		proxyBuilder = proxyBuilder.
			HTTPSProxy(hcpCluster.Properties.Spec.Proxy.HTTPSProxy)
	}
	// Cluster Service allows an empty HTTPSProxy on PATCH but not PUT.
	if updating || hcpCluster.Properties.Spec.Proxy.NoProxy != "" {
		proxyBuilder = proxyBuilder.
			NoProxy(hcpCluster.Properties.Spec.Proxy.NoProxy)
	}

	clusterBuilder = clusterBuilder.
		DisableUserWorkloadMonitoring(hcpCluster.Properties.Spec.DisableUserWorkloadMonitoring).
		Proxy(proxyBuilder).
		AdditionalTrustBundle(hcpCluster.Properties.Spec.Proxy.TrustedCA).
		Properties(additionalProperties)

	cluster, err := clusterBuilder.Build()
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

// ConvertCStoNodepool converts a CS Node Pool object into HCPOpenShiftClusterNodePool object
func (f *Frontend) ConvertCStoNodepool(ctx context.Context, systemData *arm.SystemData, np *cmv2alpha1.NodePool) (*api.HCPOpenShiftClusterNodePool, error) {
	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get parsed resource ID: %w", err)
	}

	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         resourceID.String(),
				Name:       resourceID.Name,
				Type:       resourceID.ResourceType.String(),
				SystemData: systemData,
			},
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			// ProvisioningState: np.Status(), // TODO: Align with OCM on aligning with ProvisioningState
			Spec: api.NodePoolSpec{
				Version: api.VersionProfile{
					ID:                np.Version().ID(),
					ChannelGroup:      np.Version().ChannelGroup(),
					AvailableUpgrades: np.Version().AvailableUpgrades(),
				},
				Platform: api.NodePoolPlatformProfile{
					SubnetID:               np.Subnet(),
					VMSize:                 np.AzureNodePool().VMSize(),
					DiskStorageAccountType: np.AzureNodePool().OSDiskStorageAccountType(),
					AvailabilityZone:       np.AvailabilityZone(),
					EncryptionAtHost:       false, // TODO: Not implemented in OCM
					DiskSizeGiB:            int32(np.AzureNodePool().OSDiskSizeGibibytes()),
					DiskEncryptionSetID:    "", // TODO: Not implemented in OCM
					EphemeralOSDisk:        np.AzureNodePool().EphemeralOSDiskEnabled(),
				},
				Replicas:   int32(np.Replicas()),
				AutoRepair: np.AutoRepair(),
				Autoscaling: api.NodePoolAutoscaling{
					Min: int32(np.Autoscaling().MinReplica()),
					Max: int32(np.Autoscaling().MaxReplica()),
				},
				Labels:        np.Labels(),
				TuningConfigs: np.TuningConfigs(),
			},
		},
	}

	taints := make([]*api.Taint, len(np.Taints()))
	for i, t := range np.Taints() {
		taints[i] = &api.Taint{
			Effect: api.Effect(t.Effect()),
			Key:    t.Key(),
			Value:  t.Value(),
		}
	}
	nodePool.Properties.Spec.Taints = taints

	return nodePool, nil
}

// BuildCSNodepool creates a CS Node Pool object from an HCPOpenShiftClusterNodePool object
func (f *Frontend) BuildCSNodepool(ctx context.Context, nodepool *api.HCPOpenShiftClusterNodePool) (*cmv2alpha1.NodePool, error) {
	azureNodepool := cmv2alpha1.NewAzureNodePool().
		VMSize(nodepool.Properties.Spec.Platform.VMSize).
		ResourceName(nodepool.Name).
		EphemeralOSDiskEnabled(nodepool.Properties.Spec.Platform.EphemeralOSDisk).
		OSDiskSizeGibibytes(int(nodepool.Properties.Spec.Platform.DiskSizeGiB)).
		OSDiskStorageAccountType(nodepool.Properties.Spec.Platform.DiskStorageAccountType)

	npBuilder := cmv2alpha1.NewNodePool().
		AutoRepair(nodepool.Properties.Spec.AutoRepair).
		Labels(nodepool.Properties.Spec.Labels)

	// from CS API: "Only one of 'replicas' and 'autoscaling' can be provided.
	if nodepool.Properties.Spec.Replicas != 0 {
		npBuilder.Replicas(int(nodepool.Properties.Spec.Replicas))
	} else {
		npBuilder.Autoscaling(cmv2alpha1.NewNodePoolAutoscaling().
			MinReplica(int(nodepool.Properties.Spec.Autoscaling.Min)).
			MaxReplica(int(nodepool.Properties.Spec.Autoscaling.Max)))
	}

	npBuilder.
		Subnet(nodepool.Properties.Spec.Platform.SubnetID).
		TuningConfigs(nodepool.Properties.Spec.TuningConfigs...).
		Version(cmv2alpha1.NewVersion().
			ID(nodepool.Properties.Spec.Version.ID).
			ChannelGroup(nodepool.Properties.Spec.Version.ChannelGroup).
			AvailableUpgrades(nodepool.Properties.Spec.Version.AvailableUpgrades...)).
		AzureNodePool(azureNodepool).
		ID(nodepool.Name)

	for _, t := range nodepool.Properties.Spec.Taints {
		npBuilder = npBuilder.Taints(cmv2alpha1.NewTaint().
			Effect(string(t.Effect)).
			Key(t.Key).
			Value(t.Value))
	}

	return npBuilder.Build()
}

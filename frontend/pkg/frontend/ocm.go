package frontend

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	v1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	csCloudProvider    string = "azure"
	csProductId        string = "aro"
	resourceType       string = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
	csHypershifEnabled bool   = true
	csMultiAzEnabled   bool   = true
	csCCSEnabled       bool   = true
)

// ConvertCStoHCPOpenShiftCluster converts a CS Cluster object into HCPOpenShiftCluster object
func (f *Frontend) ConvertCStoHCPOpenShiftCluster(systemData *arm.SystemData, cluster *cmv1.Cluster) (*api.HCPOpenShiftCluster, error) {

	resourceGroupName := cluster.Azure().ResourceGroupName()
	resourceName := cluster.Azure().ResourceName()
	subID := cluster.Azure().SubscriptionID()
	resourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s", subID, resourceGroupName, resourceType, resourceName)

	hcpcluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Location: cluster.Region().ID(),
			Tags:     nil, // TODO: OCM should support cluster.Azure().Tags(),
			Resource: arm.Resource{
				ID:         resourceID,
				Name:       resourceName,
				Type:       resourceType,
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
					OutboundType:           api.OutboundTypeLoadBalancer,
					NetworkSecurityGroupID: cluster.Azure().NetworkSecurityGroupResourceID(),
					EtcdEncryptionSetID:    "",
				},
				IssuerURL: "",
				ExternalAuth: api.ExternalAuthConfigProfile{
					Enabled:       false,
					ExternalAuths: []*v1.OIDCProvider{},
				},
				Ingress: []*api.IngressProfile{
					{
						IP:         "", // TODO: Unsure if OCM will support this field
						URL:        "",
						Visibility: "",
					},
				},
			},
		},
	}

	return hcpcluster, nil
}

// BuildCSCluster creates a CS Cluster object from an HCPOpenShiftCluster object
func (f *Frontend) BuildCSCluster(ctx context.Context, hcpCluster *api.HCPOpenShiftCluster) (*cmv1.Cluster, error) {
	originalPath, err := OriginalPathFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get original path: %w", err)
	}
	resourceID, err := azcorearm.ParseResourceID(originalPath)
	if err != nil {
		return nil, fmt.Errorf("could not parse resource ID: %w", err)
	}
	tenantID, err := TenantIDFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get tenant ID: %w", err)
	}

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
			ResourceGroupName(resourceID.ResourceGroupName).
			SubnetResourceID(hcpCluster.Properties.Spec.Platform.SubnetID).
			NetworkSecurityGroupResourceID(hcpCluster.Properties.Spec.Platform.NetworkSecurityGroupID).
			ResourceName(hcpCluster.Name).
			SubscriptionID(resourceID.SubscriptionID).
			TenantID(tenantID)).
		Region(cmv1.NewCloudRegion().
			ID(f.region)).
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

// ConvertCStoNodepool converts a CS Node Pool object into HCPOpenShiftClusterNodePool object
func (f *Frontend) ConvertCStoNodepool(ctx context.Context, systemData *arm.SystemData, np *cmv1.NodePool) (*api.HCPOpenShiftClusterNodePool, error) {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{}, // TODO: Implement
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
func (f *Frontend) BuildCSNodepool(ctx context.Context, nodepool *api.HCPOpenShiftClusterNodePool) (*cmv1.NodePool, error) {
	azureNodepool := cmv1.NewAzureNodePool().
		VMSize(nodepool.Properties.Spec.Platform.VMSize).
		ResourceName(nodepool.Name).
		EphemeralOSDiskEnabled(nodepool.Properties.Spec.Platform.EphemeralOSDisk).
		OSDiskSizeGibibytes(int(nodepool.Properties.Spec.Platform.DiskSizeGiB)).
		OSDiskStorageAccountType(nodepool.Properties.Spec.Platform.DiskStorageAccountType)

	npBuilder := cmv1.NewNodePool().
		AutoRepair(nodepool.Properties.Spec.AutoRepair).
		Autoscaling(cmv1.NewNodePoolAutoscaling().
			MinReplica(int(nodepool.Properties.Spec.Autoscaling.Min)).
			MaxReplica(int(nodepool.Properties.Spec.Autoscaling.Max))).
		Labels(nodepool.Properties.Spec.Labels).
		Replicas(int(nodepool.Properties.Spec.Replicas)).
		Subnet(nodepool.Properties.Spec.Platform.SubnetID).
		TuningConfigs(nodepool.Properties.Spec.TuningConfigs...).
		Version(cmv1.NewVersion().
			ID(nodepool.Properties.Spec.Version.ID).
			ChannelGroup(nodepool.Properties.Spec.Version.ChannelGroup).
			AvailableUpgrades(nodepool.Properties.Spec.Version.AvailableUpgrades...)).
		AzureNodePool(azureNodepool)

	for _, t := range nodepool.Properties.Spec.Taints {
		npBuilder = npBuilder.Taints(cmv1.NewTaint().
			Effect(string(t.Effect)).
			Key(t.Key).
			Value(t.Value))
	}

	return npBuilder.Build()
}

// GetCSCluster creates and sends a GET request to fetch a cluster from Clusters Service
func (f *Frontend) GetCSCluster(clusterID string) (*cmv1.ClusterGetResponse, error) {
	cluster, err := f.conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).Get().Send()
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

// PostCSCluster creates and sends a POST request to create a cluster in Clusters Service
func (f *Frontend) PostCSCluster(cluster *cmv1.Cluster) (*cmv1.ClustersAddResponse, error) {
	resp, err := f.conn.ClustersMgmt().V1().Clusters().Add().Body(cluster).Send()
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// DeleteCSCluster creates and sends a DELETE request to delete a cluster from Clusters Service
func (f *Frontend) DeleteCSCluster(clusterID string) (*cmv1.ClusterDeleteResponse, error) {
	resp, err := f.conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).Delete().Send()
	if err != nil {
		return nil, err
	}
	return resp, nil
}

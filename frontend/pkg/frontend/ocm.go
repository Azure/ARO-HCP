package frontend

import (
	"context"
	"fmt"
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
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

func convertListeningToVisibility(listening arohcpv1alpha1.ListeningMethod) (visibility api.Visibility) {
	switch listening {
	case arohcpv1alpha1.ListeningMethodExternal:
		visibility = api.VisibilityPublic
	case arohcpv1alpha1.ListeningMethodInternal:
		visibility = api.VisibilityPrivate
	}
	return
}

func convertVisibilityToListening(visibility api.Visibility) (listening arohcpv1alpha1.ListeningMethod) {
	switch visibility {
	case api.VisibilityPublic:
		listening = arohcpv1alpha1.ListeningMethodExternal
	case api.VisibilityPrivate:
		listening = arohcpv1alpha1.ListeningMethodInternal
	}
	return
}

func convertOutboundTypeCSToRP(outboundTypeCS string) (outboundTypeRP api.OutboundType) {
	switch outboundTypeCS {
	case "load_balancer":
		outboundTypeRP = api.OutboundTypeLoadBalancer
	}
	return
}

func convertOutboundTypeRPToCS(outboundTypeRP api.OutboundType) (outboundTypeCS string) {
	switch outboundTypeRP {
	case api.OutboundTypeLoadBalancer:
		outboundTypeCS = "load_balancer"
	}
	return
}

// ConvertCStoHCPOpenShiftCluster converts a CS Cluster object into HCPOpenShiftCluster object
func ConvertCStoHCPOpenShiftCluster(resourceID *azcorearm.ResourceID, cluster *arohcpv1alpha1.Cluster) *api.HCPOpenShiftCluster {
	// A word about ProvisioningState:
	// ProvisioningState is stored in Cosmos and is applied to the
	// HCPOpenShiftCluster struct along with the ARM metadata that
	// is also stored in Cosmos. We could convert the ClusterState
	// from Cluster Service to a ProvisioningState, but instead we
	// defer that to the backend pod so that the ProvisioningState
	// stays consistent with the Status of any active non-terminal
	// operation on the cluster.
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
					OutboundType:           convertOutboundTypeCSToRP(cluster.Azure().NodesOutboundConnectivity().OutboundType()),
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

	// Each managed identity retrieved from Cluster Service needs to be added
	// to the HCPOpenShiftCluster in two places:
	// - The top-level Identity.UserAssignedIdentities map will need both the
	//   resourceID (as keys) and principal+client IDs (as values).
	// - The operator-specific maps under OperatorsAuthentication mimics the
	//   Cluster Service maps but just has operator-to-resourceID pairings.
	if cluster.Azure().OperatorsAuthentication() != nil {
		if mi, ok := cluster.Azure().OperatorsAuthentication().GetManagedIdentities(); ok {
			hcpcluster.Identity.UserAssignedIdentities = make(map[string]*arm.UserAssignedIdentity)
			hcpcluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = make(map[string]string)
			hcpcluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = make(map[string]string)
			for operatorName, operatorIdentity := range mi.ControlPlaneOperatorsManagedIdentities() {
				clientID, _ := operatorIdentity.GetClientID()
				principalID, _ := operatorIdentity.GetPrincipalID()
				hcpcluster.Identity.UserAssignedIdentities[operatorIdentity.ResourceID()] = &arm.UserAssignedIdentity{ClientID: &clientID,
					PrincipalID: &principalID}
				hcpcluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[operatorName] = operatorIdentity.ResourceID()
			}
			for operatorName, operatorIdentity := range mi.DataPlaneOperatorsManagedIdentities() {
				// Skip adding to hcpcluster.Identity.UserAssignedIdentities map as it is not needed for the dataplane operator MIs.
				hcpcluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[operatorName] = operatorIdentity.ResourceID()
			}
			clientID, _ := mi.ServiceManagedIdentity().GetClientID()
			principalID, _ := mi.ServiceManagedIdentity().GetPrincipalID()
			hcpcluster.Identity.UserAssignedIdentities[mi.ServiceManagedIdentity().ResourceID()] = &arm.UserAssignedIdentity{ClientID: &clientID,
				PrincipalID: &principalID}
			hcpcluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = mi.ServiceManagedIdentity().ResourceID()
		}
	}

	return hcpcluster
}

// ensureManagedResourceGroupName makes sure the ManagedResourceGroupName field is set.
// If the field is empty a default is generated.
func ensureManagedResourceGroupName(hcpCluster *api.HCPOpenShiftCluster) string {
	if hcpCluster.Properties.Spec.Platform.ManagedResourceGroup != "" {
		return hcpCluster.Properties.Spec.Platform.ManagedResourceGroup
	}
	var clusterName string
	if len(hcpCluster.Name) >= 45 {
		clusterName = (hcpCluster.Name)[:45]
	} else {
		clusterName = hcpCluster.Name
	}

	return "arohcp-" + clusterName + "-" + uuid.New().String()
}

// BuildCSCluster creates a CS Cluster object from an HCPOpenShiftCluster object
func (f *Frontend) BuildCSCluster(resourceID *azcorearm.ResourceID, requestHeader http.Header, hcpCluster *api.HCPOpenShiftCluster, updating bool) (*arohcpv1alpha1.Cluster, error) {

	// Ensure required headers are present.
	if requestHeader.Get(arm.HeaderNameHomeTenantID) == "" {
		return nil, fmt.Errorf("Missing " + arm.HeaderNameHomeTenantID + " header")
	}

	clusterBuilder := arohcpv1alpha1.NewCluster()

	// FIXME HcpOpenShiftCluster attributes not being passed:
	//       PlatformProfile.EtcdEncryptionSetID (no CS equivalent?)
	//       ExternalAuth                        (TODO, complicated)

	// These attributes cannot be updated after cluster creation.
	if !updating {
		clusterBuilder = clusterBuilder.
			Name(hcpCluster.Name).
			Flavour(cmv1.NewFlavour().
				ID(csFlavourId)).
			Region(cmv1.NewCloudRegion().
				ID(f.location)).
			CloudProvider(cmv1.NewCloudProvider().
				ID(csCloudProvider)).
			Product(cmv1.NewProduct().
				ID(csProductId)).
			Hypershift(arohcpv1alpha1.NewHypershift().
				Enabled(csHypershifEnabled)).
			MultiAZ(csMultiAzEnabled).
			CCS(arohcpv1alpha1.NewCCS().Enabled(csCCSEnabled)).
			Version(cmv1.NewVersion().
				ID(hcpCluster.Properties.Spec.Version.ID).
				ChannelGroup(hcpCluster.Properties.Spec.Version.ChannelGroup)).
			Network(arohcpv1alpha1.NewNetwork().
				Type(string(hcpCluster.Properties.Spec.Network.NetworkType)).
				PodCIDR(hcpCluster.Properties.Spec.Network.PodCIDR).
				ServiceCIDR(hcpCluster.Properties.Spec.Network.ServiceCIDR).
				MachineCIDR(hcpCluster.Properties.Spec.Network.MachineCIDR).
				HostPrefix(int(hcpCluster.Properties.Spec.Network.HostPrefix))).
			API(arohcpv1alpha1.NewClusterAPI().
				Listening(convertVisibilityToListening(hcpCluster.Properties.Spec.API.Visibility))).
			FIPS(hcpCluster.Properties.Spec.FIPS).
			EtcdEncryption(hcpCluster.Properties.Spec.EtcdEncryption)

		azureBuilder := arohcpv1alpha1.NewAzure().
			TenantID(requestHeader.Get(arm.HeaderNameHomeTenantID)).
			SubscriptionID(resourceID.SubscriptionID).
			ResourceGroupName(resourceID.ResourceGroupName).
			ResourceName(hcpCluster.Name).
			ManagedResourceGroupName(ensureManagedResourceGroupName(hcpCluster)).
			SubnetResourceID(hcpCluster.Properties.Spec.Platform.SubnetID).
			NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
				OutboundType(convertOutboundTypeRPToCS(hcpCluster.Properties.Spec.Platform.OutboundType)))

		// Cluster Service rejects an empty NetworkSecurityGroupResourceID string.
		if hcpCluster.Properties.Spec.Platform.NetworkSecurityGroupID != "" {
			azureBuilder = azureBuilder.
				NetworkSecurityGroupResourceID(hcpCluster.Properties.Spec.Platform.NetworkSecurityGroupID)
		}

		// Only pass managed identity information if the x-ms-identity-url header is present.
		if requestHeader.Get(arm.HeaderNameIdentityURL) != "" {
			controlPlaneOperators := make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)
			for operatorName, identityResourceID := range hcpCluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
				controlPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureControlPlaneManagedIdentity().ResourceID(identityResourceID)
			}

			dataPlaneOperators := make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)
			for operatorName, identityResourceID := range hcpCluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
				dataPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureDataPlaneManagedIdentity().ResourceID(identityResourceID)
			}

			managedIdentitiesBuilder := arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
				ManagedIdentitiesDataPlaneIdentityUrl(requestHeader.Get(arm.HeaderNameIdentityURL)).
				ControlPlaneOperatorsManagedIdentities(controlPlaneOperators).
				DataPlaneOperatorsManagedIdentities(dataPlaneOperators)

			if hcpCluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity != "" {
				managedIdentitiesBuilder = managedIdentitiesBuilder.ServiceManagedIdentity(arohcpv1alpha1.NewAzureServiceManagedIdentity().
					ResourceID(hcpCluster.Properties.Spec.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity))
			}

			azureBuilder = azureBuilder.OperatorsAuthentication(
				arohcpv1alpha1.NewAzureOperatorsAuthentication().ManagedIdentities(managedIdentitiesBuilder))
		}

		clusterBuilder = clusterBuilder.Azure(azureBuilder)

		// Cluster Service rejects an empty DomainPrefix string.
		if hcpCluster.Properties.Spec.DNS.BaseDomainPrefix != "" {
			clusterBuilder = clusterBuilder.
				DomainPrefix(hcpCluster.Properties.Spec.DNS.BaseDomainPrefix)
		}
	}

	proxyBuilder := arohcpv1alpha1.NewProxy()
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
		AdditionalTrustBundle(hcpCluster.Properties.Spec.Proxy.TrustedCA)

	clusterBuilder = f.clusterServiceClient.AddProperties(clusterBuilder)

	return clusterBuilder.Build()
}

// ConvertCStoNodePool converts a CS Node Pool object into HCPOpenShiftClusterNodePool object
func ConvertCStoNodePool(resourceID *azcorearm.ResourceID, np *cmv1.NodePool) *api.HCPOpenShiftClusterNodePool {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID.String(),
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
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
				AutoRepair:    np.AutoRepair(),
				Labels:        np.Labels(),
				TuningConfigs: np.TuningConfigs(),
			},
		},
	}

	if replicas, ok := np.GetReplicas(); ok {
		nodePool.Properties.Spec.Replicas = int32(replicas)
	}

	if autoscaling, ok := np.GetAutoscaling(); ok {
		nodePool.Properties.Spec.AutoScaling = &api.NodePoolAutoScaling{
			Min: int32(autoscaling.MinReplica()),
			Max: int32(autoscaling.MaxReplica()),
		}
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

	return nodePool
}

// BuildCSNodePool creates a CS Node Pool object from an HCPOpenShiftClusterNodePool object
func (f *Frontend) BuildCSNodePool(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool, updating bool) (*cmv1.NodePool, error) {
	npBuilder := cmv1.NewNodePool()

	// FIXME HCPOpenShiftClusterNodePool attributes not being passed:
	//       PlatformProfile.EncryptionAtHost    (no CS equivalent?)
	//       PlatformProfile.DiskEncryptionSetID (no CS equivalent?)

	// These attributes cannot be updated after node pool creation.
	if !updating {
		npBuilder = npBuilder.
			ID(nodePool.Name).
			Version(cmv1.NewVersion().
				ID(nodePool.Properties.Spec.Version.ID).
				ChannelGroup(nodePool.Properties.Spec.Version.ChannelGroup).
				AvailableUpgrades(nodePool.Properties.Spec.Version.AvailableUpgrades...)).
			Subnet(nodePool.Properties.Spec.Platform.SubnetID).
			AzureNodePool(cmv1.NewAzureNodePool().
				ResourceName(nodePool.Name).
				VMSize(nodePool.Properties.Spec.Platform.VMSize).
				OSDiskSizeGibibytes(int(nodePool.Properties.Spec.Platform.DiskSizeGiB)).
				OSDiskStorageAccountType(nodePool.Properties.Spec.Platform.DiskStorageAccountType).
				EphemeralOSDiskEnabled(nodePool.Properties.Spec.Platform.EphemeralOSDisk)).
			AvailabilityZone(nodePool.Properties.Spec.Platform.AvailabilityZone).
			AutoRepair(nodePool.Properties.Spec.AutoRepair)
	}

	npBuilder = npBuilder.
		Labels(nodePool.Properties.Spec.Labels).
		TuningConfigs(nodePool.Properties.Spec.TuningConfigs...)

	if nodePool.Properties.Spec.AutoScaling != nil {
		npBuilder.Autoscaling(cmv1.NewNodePoolAutoscaling().
			MinReplica(int(nodePool.Properties.Spec.AutoScaling.Min)).
			MaxReplica(int(nodePool.Properties.Spec.AutoScaling.Max)))
	} else {
		npBuilder.Replicas(int(nodePool.Properties.Spec.Replicas))
	}

	for _, t := range nodePool.Properties.Spec.Taints {
		npBuilder = npBuilder.Taints(cmv1.NewTaint().
			Effect(string(t.Effect)).
			Key(t.Key).
			Value(t.Value))
	}

	return npBuilder.Build()
}

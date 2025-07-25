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
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	csFlavourId        string = "osd-4" // managed cluster
	csCloudProvider    string = "azure"
	csProductId        string = "aro"
	csHypershifEnabled bool   = true
	csCCSEnabled       bool   = true

	// The OCM SDK does not provide these constants.

	azureNodePoolEncryptionAtHostDisabled string = "disabled"
	azureNodePoolEncryptionAtHostEnabled  string = "enabled"
	azureNodePoolNodeDrainGracePeriodUnit string = "minutes"
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

func convertEnableEncryptionAtHostToCSBuilder(in api.NodePoolPlatformProfile) *arohcpv1alpha1.AzureNodePoolEncryptionAtHostBuilder {
	var state string

	if in.EnableEncryptionAtHost {
		state = azureNodePoolEncryptionAtHostEnabled
	} else {
		state = azureNodePoolEncryptionAtHostDisabled
	}

	return arohcpv1alpha1.NewAzureNodePoolEncryptionAtHost().State(state)
}

func convertNodeDrainTimeoutCSToRP(in *arohcpv1alpha1.Cluster) int32 {
	if nodeDrainGracePeriod, ok := in.GetNodeDrainGracePeriod(); ok {
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == azureNodePoolNodeDrainGracePeriodUnit {
			return int32(nodeDrainGracePeriod.Value())
		}
	}
	return 0
}

// TODO: Uncomment when CS supports it.
// func convertCustomerManagedEncryptionCSToRP(in *arohcpv1alpha1.Cluster) api.CustomerManagedEncryptionProfile {
// 	if customerManaged, ok := in.Azure().EtcdEncryption().DataEncryption().GetCustomerManaged(); ok {
// 		return api.CustomerManagedEncryptionProfile{
// 			EncryptionType: api.CustomerManagedEncryptionType(customerManaged.EncryptionType()),
// 			Kms:            convertKmsEncryptionCSToRP(in),
// 		}
// 	}
// 	return api.CustomerManagedEncryptionProfile{}
// }
//
// func convertKmsEncryptionCSToRP(in *arohcpv1alpha1.Cluster) *api.KmsEncryptionProfile {
//
// 	if kms, ok := in.Azure().EtcdEncryption().DataEncryption().CustomerManaged().GetKms(); ok {
// 		if activeKey, ok := kms.GetActiveKey(); ok {
// 			return &api.KmsEncryptionProfile{
// 				ActiveKey: api.KmsKey{
// 					Name:      activeKey.KeyName(),
// 					VaultName: activeKey.KeyVaultName(),
// 					Version:   activeKey.KeyVersion(),
// 				},
// 			}
// 		}
// 	}
// 	return nil
// }

// func convertEtcdRPToCS(in api.EtcdProfile) *arohcpv1alpha1.AzureEtcdEncryptionBuilder {

// 	azureEtcdDataEncryptionCustomerManagedBuilder := arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().EncryptionType(string(in.DataEncryption.CustomerManaged.EncryptionType))

// 	if in.DataEncryption.CustomerManaged.Kms != nil {
// 		azureKmsKeyBuilder := arohcpv1alpha1.NewAzureKmsKey().KeyName(in.DataEncryption.CustomerManaged.Kms.ActiveKey.Name).KeyVaultName(in.DataEncryption.CustomerManaged.Kms.ActiveKey.VaultName).KeyVersion(in.DataEncryption.CustomerManaged.Kms.ActiveKey.Version)
// 		azureKmsEncryptionBuilder := arohcpv1alpha1.NewAzureKmsEncryption().ActiveKey(azureKmsKeyBuilder)
// 		azureEtcdDataEncryptionCustomerManagedBuilder = azureEtcdDataEncryptionCustomerManagedBuilder.Kms(azureKmsEncryptionBuilder)
// 	}

// 	azureEtcdDataEncryptionBuilder := arohcpv1alpha1.NewAzureEtcdDataEncryption().KeyManagementMode(string(in.DataEncryption.KeyManagementMode)).CustomerManaged(azureEtcdDataEncryptionCustomerManagedBuilder)

// 	return arohcpv1alpha1.NewAzureEtcdEncryption().DataEncryption(azureEtcdDataEncryptionBuilder)

// }

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
			Version: api.VersionProfile{
				ID:           cluster.Version().ID(),
				ChannelGroup: cluster.Version().ChannelGroup(),
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
			Platform: api.PlatformProfile{
				ManagedResourceGroup:   cluster.Azure().ManagedResourceGroupName(),
				SubnetID:               cluster.Azure().SubnetResourceID(),
				OutboundType:           convertOutboundTypeCSToRP(cluster.Azure().NodesOutboundConnectivity().OutboundType()),
				NetworkSecurityGroupID: cluster.Azure().NetworkSecurityGroupResourceID(),
				IssuerURL:              "",
			},
			NodeDrainTimeoutMinutes: convertNodeDrainTimeoutCSToRP(cluster),
			// TODO: Reconcile this with the other comment below when CS supports this.
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
				},
			},
		},
	}
	// TODO: Uncomment when CS supports it.
	// // Only set etcd encryption settings if they exist in the cluster service response
	// if cluster.Azure().EtcdEncryption() != nil && cluster.Azure().EtcdEncryption().DataEncryption() != nil {
	// 	hcpcluster.Properties.Etcd = api.EtcdProfile{
	// 		DataEncryption: api.EtcdDataEncryptionProfile{
	// 			CustomerManaged:   convertCustomerManagedEncryptionCSToRP(cluster),
	// 			KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeType(cluster.Azure().EtcdEncryption().DataEncryption().KeyManagementMode()),
	// 		},
	// 	}
	// }

	// Each managed identity retrieved from Cluster Service needs to be added
	// to the HCPOpenShiftCluster in two places:
	// - The top-level Identity.UserAssignedIdentities map will need both the
	//   resourceID (as keys) and principal+client IDs (as values).
	// - The operator-specific maps under OperatorsAuthentication mimics the
	//   Cluster Service maps but just has operator-to-resourceID pairings.
	if cluster.Azure().OperatorsAuthentication() != nil {
		if mi, ok := cluster.Azure().OperatorsAuthentication().GetManagedIdentities(); ok {
			hcpcluster.Identity.UserAssignedIdentities = make(map[string]*arm.UserAssignedIdentity)
			hcpcluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = make(map[string]string)
			hcpcluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = make(map[string]string)
			for operatorName, operatorIdentity := range mi.ControlPlaneOperatorsManagedIdentities() {
				clientID, _ := operatorIdentity.GetClientID()
				principalID, _ := operatorIdentity.GetPrincipalID()
				hcpcluster.Identity.UserAssignedIdentities[operatorIdentity.ResourceID()] = &arm.UserAssignedIdentity{ClientID: &clientID,
					PrincipalID: &principalID}
				hcpcluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[operatorName] = operatorIdentity.ResourceID()
			}
			for operatorName, operatorIdentity := range mi.DataPlaneOperatorsManagedIdentities() {
				// Skip adding to hcpcluster.Identity.UserAssignedIdentities map as it is not needed for the dataplane operator MIs.
				hcpcluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[operatorName] = operatorIdentity.ResourceID()
			}
			clientID, _ := mi.ServiceManagedIdentity().GetClientID()
			principalID, _ := mi.ServiceManagedIdentity().GetPrincipalID()
			hcpcluster.Identity.UserAssignedIdentities[mi.ServiceManagedIdentity().ResourceID()] = &arm.UserAssignedIdentity{ClientID: &clientID,
				PrincipalID: &principalID}
			hcpcluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = mi.ServiceManagedIdentity().ResourceID()
		}
	}

	return hcpcluster
}

// ensureManagedResourceGroupName makes sure the ManagedResourceGroupName field is set.
// If the field is empty a default is generated.
func ensureManagedResourceGroupName(hcpCluster *api.HCPOpenShiftCluster) string {
	if hcpCluster.Properties.Platform.ManagedResourceGroup != "" {
		return hcpCluster.Properties.Platform.ManagedResourceGroup
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
	tenantID := requestHeader.Get(arm.HeaderNameHomeTenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("missing " + arm.HeaderNameHomeTenantID + " header")
	}

	clusterBuilder := arohcpv1alpha1.NewCluster()

	// FIXME HcpOpenShiftCluster attributes not being passed:
	//       ExternalAuth                        (TODO, complicated)

	// These attributes cannot be updated after cluster creation.
	if !updating {
		// Add attributes that cannot be updated after cluster creation.
		clusterBuilder = withImmutableAttributes(clusterBuilder, hcpCluster,
			resourceID.SubscriptionID,
			resourceID.ResourceGroupName,
			f.location,
			tenantID,
			requestHeader.Get(arm.HeaderNameIdentityURL),
		)
	}

	clusterBuilder = clusterBuilder.
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(azureNodePoolNodeDrainGracePeriodUnit).
			Value(float64(hcpCluster.Properties.NodeDrainTimeoutMinutes)))

	clusterBuilder = f.clusterServiceClient.AddProperties(clusterBuilder)

	return clusterBuilder.Build()
}

func withImmutableAttributes(clusterBuilder *arohcpv1alpha1.ClusterBuilder, hcpCluster *api.HCPOpenShiftCluster, subscriptionID, resourceGroupName, location, tenantID, identityURL string) *arohcpv1alpha1.ClusterBuilder {
	clusterBuilder = clusterBuilder.
		Name(hcpCluster.Name).
		Flavour(cmv1.NewFlavour().
			ID(csFlavourId)).
		Region(cmv1.NewCloudRegion().
			ID(location)).
		CloudProvider(cmv1.NewCloudProvider().
			ID(csCloudProvider)).
		Product(cmv1.NewProduct().
			ID(csProductId)).
		Hypershift(arohcpv1alpha1.NewHypershift().
			Enabled(csHypershifEnabled)).
		CCS(arohcpv1alpha1.NewCCS().Enabled(csCCSEnabled)).
		Version(arohcpv1alpha1.NewVersion().
			ID(hcpCluster.Properties.Version.ID).
			ChannelGroup(hcpCluster.Properties.Version.ChannelGroup)).
		Network(arohcpv1alpha1.NewNetwork().
			Type(string(hcpCluster.Properties.Network.NetworkType)).
			PodCIDR(hcpCluster.Properties.Network.PodCIDR).
			ServiceCIDR(hcpCluster.Properties.Network.ServiceCIDR).
			MachineCIDR(hcpCluster.Properties.Network.MachineCIDR).
			HostPrefix(int(hcpCluster.Properties.Network.HostPrefix))).
		API(arohcpv1alpha1.NewClusterAPI().
			Listening(convertVisibilityToListening(hcpCluster.Properties.API.Visibility)))

	azureBuilder := arohcpv1alpha1.NewAzure().
		TenantID(tenantID).
		SubscriptionID(subscriptionID).
		ResourceGroupName(resourceGroupName).
		ResourceName(hcpCluster.Name).
		ManagedResourceGroupName(ensureManagedResourceGroupName(hcpCluster)).
		SubnetResourceID(hcpCluster.Properties.Platform.SubnetID).
		NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
			OutboundType(convertOutboundTypeRPToCS(hcpCluster.Properties.Platform.OutboundType)))

	// TODO: Uncomment when CS supports this.
	// Only add etcd encryption if it's actually configured
	// if hcpCluster.Properties.Etcd.DataEncryption.KeyManagementMode != "" || hcpCluster.Properties.Etcd.DataEncryption.CustomerManaged.EncryptionType != "" || hcpCluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
	// 	azureBuilder = azureBuilder.EtcdEncryption(convertEtcdRPToCS(hcpCluster.Properties.Etcd))
	// }

	// Cluster Service rejects an empty NetworkSecurityGroupResourceID string.
	if hcpCluster.Properties.Platform.NetworkSecurityGroupID != "" {
		azureBuilder = azureBuilder.
			NetworkSecurityGroupResourceID(hcpCluster.Properties.Platform.NetworkSecurityGroupID)
	}

	// Only pass managed identity information if the x-ms-identity-url header is present.
	if identityURL != "" {
		controlPlaneOperators := make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)
		for operatorName, identityResourceID := range hcpCluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
			controlPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureControlPlaneManagedIdentity().ResourceID(identityResourceID)
		}

		dataPlaneOperators := make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)
		for operatorName, identityResourceID := range hcpCluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
			dataPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureDataPlaneManagedIdentity().ResourceID(identityResourceID)
		}

		managedIdentitiesBuilder := arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
			ManagedIdentitiesDataPlaneIdentityUrl(identityURL).
			ControlPlaneOperatorsManagedIdentities(controlPlaneOperators).
			DataPlaneOperatorsManagedIdentities(dataPlaneOperators)

		if hcpCluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity != "" {
			managedIdentitiesBuilder = managedIdentitiesBuilder.ServiceManagedIdentity(arohcpv1alpha1.NewAzureServiceManagedIdentity().
				ResourceID(hcpCluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity))
		}

		azureBuilder = azureBuilder.OperatorsAuthentication(
			arohcpv1alpha1.NewAzureOperatorsAuthentication().ManagedIdentities(managedIdentitiesBuilder))
	}

	clusterBuilder = clusterBuilder.Azure(azureBuilder)

	// Cluster Service rejects an empty DomainPrefix string.
	if hcpCluster.Properties.DNS.BaseDomainPrefix != "" {
		clusterBuilder = clusterBuilder.
			DomainPrefix(hcpCluster.Properties.DNS.BaseDomainPrefix)
	}
	return clusterBuilder
}

// ConvertCStoNodePool converts a CS Node Pool object into HCPOpenShiftClusterNodePool object
func ConvertCStoNodePool(resourceID *azcorearm.ResourceID, np *arohcpv1alpha1.NodePool) *api.HCPOpenShiftClusterNodePool {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID.String(),
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID:           np.Version().ID(),
				ChannelGroup: np.Version().ChannelGroup(),
			},
			Platform: api.NodePoolPlatformProfile{
				SubnetID:               np.Subnet(),
				VMSize:                 np.AzureNodePool().VMSize(),
				EnableEncryptionAtHost: np.AzureNodePool().EncryptionAtHost().State() == azureNodePoolEncryptionAtHostEnabled,
				OSDisk: api.OSDiskProfile{
					SizeGiB:                int32(np.AzureNodePool().OSDiskSizeGibibytes()),
					DiskStorageAccountType: api.DiskStorageAccountType(np.AzureNodePool().OSDiskStorageAccountType()),
				},
				AvailabilityZone: np.AvailabilityZone(),
			},
			AutoRepair: np.AutoRepair(),
			Labels:     np.Labels(),
		},
	}

	if replicas, ok := np.GetReplicas(); ok {
		nodePool.Properties.Replicas = int32(replicas)
	}

	if autoscaling, ok := np.GetAutoscaling(); ok {
		nodePool.Properties.AutoScaling = &api.NodePoolAutoScaling{
			Min: int32(autoscaling.MinReplica()),
			Max: int32(autoscaling.MaxReplica()),
		}
	}

	taints := make([]api.Taint, 0, len(np.Taints()))
	for _, t := range np.Taints() {
		taints = append(taints, api.Taint{
			Effect: api.Effect(t.Effect()),
			Key:    t.Key(),
			Value:  t.Value(),
		})
	}
	nodePool.Properties.Taints = taints

	if nodeDrainGracePeriod, ok := np.GetNodeDrainGracePeriod(); ok {
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == azureNodePoolNodeDrainGracePeriodUnit {
			nodePool.Properties.NodeDrainTimeoutMinutes = api.Ptr(int32(nodeDrainGracePeriod.Value()))
		}
	}

	return nodePool
}

// BuildCSNodePool creates a CS Node Pool object from an HCPOpenShiftClusterNodePool object
func (f *Frontend) BuildCSNodePool(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool, updating bool) (*arohcpv1alpha1.NodePool, error) {
	npBuilder := arohcpv1alpha1.NewNodePool()

	// These attributes cannot be updated after node pool creation.
	if !updating {
		npBuilder = npBuilder.
			ID(nodePool.Name).
			Version(arohcpv1alpha1.NewVersion().
				ID(nodePool.Properties.Version.ID).
				ChannelGroup(nodePool.Properties.Version.ChannelGroup)).
			Subnet(nodePool.Properties.Platform.SubnetID).
			AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
				ResourceName(nodePool.Name).
				VMSize(nodePool.Properties.Platform.VMSize).
				EncryptionAtHost(convertEnableEncryptionAtHostToCSBuilder(nodePool.Properties.Platform)).
				OSDiskSizeGibibytes(int(nodePool.Properties.Platform.OSDisk.SizeGiB)).
				OSDiskStorageAccountType(string(nodePool.Properties.Platform.OSDisk.DiskStorageAccountType))).
			AvailabilityZone(nodePool.Properties.Platform.AvailabilityZone).
			AutoRepair(nodePool.Properties.AutoRepair)
	}

	npBuilder = npBuilder.
		Labels(nodePool.Properties.Labels)

	if nodePool.Properties.AutoScaling != nil {
		npBuilder.Autoscaling(arohcpv1alpha1.NewNodePoolAutoscaling().
			MinReplica(int(nodePool.Properties.AutoScaling.Min)).
			MaxReplica(int(nodePool.Properties.AutoScaling.Max)))
	} else {
		npBuilder.Replicas(int(nodePool.Properties.Replicas))
	}

	for _, t := range nodePool.Properties.Taints {
		npBuilder = npBuilder.Taints(arohcpv1alpha1.NewTaint().
			Effect(string(t.Effect)).
			Key(t.Key).
			Value(t.Value))
	}

	if nodePool.Properties.NodeDrainTimeoutMinutes != nil {
		npBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(azureNodePoolNodeDrainGracePeriodUnit).
			Value(float64(*nodePool.Properties.NodeDrainTimeoutMinutes)))
	}

	return npBuilder.Build()
}

// ConvertCStoAdminCredential converts a CS BreakGlassCredential object into an HCPOpenShiftClusterAdminCredential.
func ConvertCStoAdminCredential(breakGlassCredential *cmv1.BreakGlassCredential) *api.HCPOpenShiftClusterAdminCredential {
	return &api.HCPOpenShiftClusterAdminCredential{
		ExpirationTimestamp: breakGlassCredential.ExpirationTimestamp(),
		Kubeconfig:          breakGlassCredential.Kubeconfig(),
	}
}

func ConvertCStoHCPOpenshiftVersion(resourceID azcorearm.ResourceID, version *arohcpv1alpha1.Version) *api.HCPOpenShiftVersion {
	return &api.HCPOpenShiftVersion{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID.String(),
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			}},
		Properties: api.HCPOpenShiftVersionProperties{
			ChannelGroup:       version.ChannelGroup(),
			Enabled:            version.Enabled(),
			EndOfLifeTimestamp: version.EndOfLifeTimestamp(),
		},
	}
}

// CSErrorToCloudError attempts to convert various 4xx status codes from
// Cluster Service to an ARM-compliant error structure, with 500 Internal
// Server Error as a last-ditch fallback.
func CSErrorToCloudError(err error, resourceID *azcorearm.ResourceID) *arm.CloudError {
	var ocmError *ocmerrors.Error

	if errors.As(err, &ocmError) {
		switch statusCode := ocmError.Status(); statusCode {
		case http.StatusBadRequest:
			// BadRequest can be returned when an object fails validation.
			//
			// We try our best to mimic Cluster Service's validation for a
			// couple reasons:
			//
			// 1) Whereas Cluster Service aborts on the first validation error,
			//    we try to report as many validation errors as possible at once
			//    for a better user experience.
			//
			// 2) CloudErrorBody.Target should reference the erroneous field but
			//    validation errors from Cluster Service cannot easily be mapped
			//    to a field without extensive pattern matching of the reason.
			//
			// That said, Cluster Service's validation is more comprehensive and
			// probably always will be. So it's important we try to handle their
			// errors as best we can.
			return arm.NewCloudError(
				statusCode,
				arm.CloudErrorCodeInvalidRequestContent,
				"", "%s", ocmError.Reason())
		case http.StatusNotFound:
			if resourceID != nil {
				return arm.NewResourceNotFoundError(resourceID)
			}
			return arm.NewCloudError(
				statusCode,
				arm.CloudErrorCodeNotFound,
				"", "%s", ocmError.Reason())
		case http.StatusConflict:
			var target string
			if resourceID != nil {
				target = resourceID.String()
			}
			return arm.NewCloudError(
				statusCode,
				arm.CloudErrorCodeConflict,
				target, "%s", ocmError.Reason())
		}
	}

	return arm.NewInternalServerError()
}

// transportFunc implements the http.RoundTripper interface.
type transportFunc func(*http.Request) (*http.Response, error)

var _ = http.RoundTripper(transportFunc(nil))

func (rtf transportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return rtf(r)
}

const clusterServiceRequestIDHeader = "X-Request-ID"

// RequestIDPropagator returns an http.RoundTripper interface which reads the
// request ID from the request's context and propagates it to the Clusters
// Service API via the "X-Request-ID" header.
func RequestIDPropagator(next http.RoundTripper) http.RoundTripper {
	return transportFunc(func(r *http.Request) (*http.Response, error) {
		correlationData, err := CorrelationDataFromContext(r.Context())
		if err == nil {
			r = r.Clone(r.Context())
			r.Header.Set(clusterServiceRequestIDHeader, correlationData.RequestID.String())
		}

		return next.RoundTrip(r)
	})
}

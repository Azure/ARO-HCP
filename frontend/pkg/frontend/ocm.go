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
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	csFlavourId        string = "osd-4" // managed cluster
	csCloudProvider    string = "azure"
	csProductId        string = "aro"
	csHypershifEnabled bool   = true
	csCCSEnabled       bool   = true

	// The OCM SDK does not provide these constants.

	csCustomerManagedEncryptionTypeKms  string = "kms"
	csEncryptionAtHostStateDisabled     string = "disabled"
	csEncryptionAtHostStateEnabled      string = "enabled"
	csImageRegistryStateDisabled        string = "disabled"
	csImageRegistryStateEnabled         string = "enabled"
	csKeyManagementModeCustomerManaged  string = "customer_managed"
	csKeyManagementModePlatformManaged  string = "platform_managed"
	csNodeDrainGracePeriodUnit          string = "minutes"
	csOutboundType                      string = "load_balancer"
	csUsernameClaimPrefixPolicyNoPrefix string = "NoPrefix"
	csUsernameClaimPrefixPolicyPrefix   string = "Prefix"
)

// Sentinel error for use with errors.Is
var ErrUnknownValue = errors.New("unknown value")

func conversionError[T any](v any) error {
	return fmt.Errorf("cannot convert %T(%q) to %T: %w", v, v, *new(T), ErrUnknownValue)
}

func convertListeningToVisibility(listening arohcpv1alpha1.ListeningMethod) (api.Visibility, error) {
	switch listening {
	case arohcpv1alpha1.ListeningMethodExternal:
		return api.VisibilityPublic, nil
	case arohcpv1alpha1.ListeningMethodInternal:
		return api.VisibilityPrivate, nil
	default:
		return "", conversionError[api.Visibility](listening)
	}
}

func convertVisibilityToListening(visibility api.Visibility) (arohcpv1alpha1.ListeningMethod, error) {
	switch visibility {
	case api.VisibilityPublic:
		return arohcpv1alpha1.ListeningMethodExternal, nil
	case api.VisibilityPrivate:
		return arohcpv1alpha1.ListeningMethodInternal, nil
	default:
		return "", conversionError[arohcpv1alpha1.ListeningMethod](visibility)
	}
}

func convertOutboundTypeCSToRP(outboundTypeCS string) (api.OutboundType, error) {
	switch outboundTypeCS {
	case csOutboundType:
		return api.OutboundTypeLoadBalancer, nil
	default:
		return "", conversionError[api.OutboundType](outboundTypeCS)
	}
}

func convertOutboundTypeRPToCS(outboundTypeRP api.OutboundType) (string, error) {
	switch outboundTypeRP {
	case api.OutboundTypeLoadBalancer:
		return csOutboundType, nil
	default:
		return "", conversionError[string](outboundTypeRP)
	}
}

func convertCustomerManagedEncryptionTypeCSToRP(encryptionTypeCS string) (api.CustomerManagedEncryptionType, error) {
	switch encryptionTypeCS {
	case csCustomerManagedEncryptionTypeKms:
		return api.CustomerManagedEncryptionTypeKMS, nil
	default:
		return "", conversionError[api.CustomerManagedEncryptionType](encryptionTypeCS)
	}
}

func convertCustomerManagedEncryptionTypeRPToCS(encryptionTypeRP api.CustomerManagedEncryptionType) (string, error) {
	switch encryptionTypeRP {
	case api.CustomerManagedEncryptionTypeKMS:
		return csCustomerManagedEncryptionTypeKms, nil
	default:
		return "", conversionError[string](encryptionTypeRP)
	}
}

func convertUsernameClaimPrefixPolicyCSToRP(prefixPolicyCS string) (api.UsernameClaimPrefixPolicyType, error) {
	switch prefixPolicyCS {
	case csUsernameClaimPrefixPolicyPrefix:
		return api.UsernameClaimPrefixPolicyTypePrefix, nil
	case csUsernameClaimPrefixPolicyNoPrefix:
		return api.UsernameClaimPrefixPolicyTypeNoPrefix, nil
	case "":
		return api.UsernameClaimPrefixPolicyTypeNone, nil
	default:
		return "", conversionError[api.UsernameClaimPrefixPolicyType](prefixPolicyCS)
	}
}

func convertUsernameClaimPrefixPolicyRPToCS(prefixPolicyRP api.UsernameClaimPrefixPolicyType) (string, error) {
	switch prefixPolicyRP {
	case api.UsernameClaimPrefixPolicyTypePrefix:
		return csUsernameClaimPrefixPolicyPrefix, nil
	case api.UsernameClaimPrefixPolicyTypeNoPrefix:
		return csUsernameClaimPrefixPolicyNoPrefix, nil
	case api.UsernameClaimPrefixPolicyTypeNone:
		return "", nil
	default:
		return "", conversionError[string](prefixPolicyRP)
	}
}

func convertEnableEncryptionAtHostToCSBuilder(in api.NodePoolPlatformProfile) *arohcpv1alpha1.AzureNodePoolEncryptionAtHostBuilder {
	var state string

	if in.EnableEncryptionAtHost {
		state = csEncryptionAtHostStateEnabled
	} else {
		state = csEncryptionAtHostStateDisabled
	}

	return arohcpv1alpha1.NewAzureNodePoolEncryptionAtHost().State(state)
}

func convertClusterImageRegistryStateRPToCS(in api.ClusterImageRegistryProfile) (string, error) {
	switch in.State {
	case api.ClusterImageRegistryProfileStateDisabled:
		return csImageRegistryStateDisabled, nil
	case api.ClusterImageRegistryProfileStateEnabled:
		return csImageRegistryStateEnabled, nil
	default:
		return "", conversionError[string](in)
	}
}

func convertClusterImageRegistryStateCSToRP(state string) (api.ClusterImageRegistryProfileState, error) {
	switch state {
	case csImageRegistryStateDisabled:
		return api.ClusterImageRegistryProfileStateDisabled, nil
	case csImageRegistryStateEnabled:
		return api.ClusterImageRegistryProfileStateEnabled, nil
	default:
		return "", conversionError[api.ClusterImageRegistryProfileState](state)
	}
}

func convertNodeDrainTimeoutCSToRP(in *arohcpv1alpha1.Cluster) int32 {
	if nodeDrainGracePeriod, ok := in.GetNodeDrainGracePeriod(); ok {
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == csNodeDrainGracePeriodUnit {
			return int32(nodeDrainGracePeriod.Value())
		}
	}
	return 0
}

func convertKeyManagementModeTypeCSToRP(keyManagementModeCS string) (api.EtcdDataEncryptionKeyManagementModeType, error) {
	switch keyManagementModeCS {
	case csKeyManagementModePlatformManaged:
		return api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged, nil
	case csKeyManagementModeCustomerManaged:
		return api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, nil
	default:
		return "", conversionError[api.EtcdDataEncryptionKeyManagementModeType](keyManagementModeCS)
	}
}

func convertKeyManagementModeTypeRPToCS(keyManagementModeRP api.EtcdDataEncryptionKeyManagementModeType) (string, error) {
	switch keyManagementModeRP {
	case api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged:
		return csKeyManagementModePlatformManaged, nil
	case api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged:
		return csKeyManagementModeCustomerManaged, nil
	default:
		return "", conversionError[string](keyManagementModeRP)
	}
}

func convertExternalAuthClientTypeCSToRP(externalAuthClientTypeCS arohcpv1alpha1.ExternalAuthClientType) (api.ExternalAuthClientType, error) {
	switch externalAuthClientTypeCS {
	case arohcpv1alpha1.ExternalAuthClientTypeConfidential:
		return api.ExternalAuthClientTypeConfidential, nil
	case arohcpv1alpha1.ExternalAuthClientTypePublic:
		return api.ExternalAuthClientTypePublic, nil
	default:
		return "", conversionError[api.ExternalAuthClientType](externalAuthClientTypeCS)
	}
}

func convertExternalAuthClientTypeRPToCS(externalAuthClientTypeRP api.ExternalAuthClientType) (arohcpv1alpha1.ExternalAuthClientType, error) {
	switch externalAuthClientTypeRP {
	case api.ExternalAuthClientTypeConfidential:
		return arohcpv1alpha1.ExternalAuthClientTypeConfidential, nil
	case api.ExternalAuthClientTypePublic:
		return arohcpv1alpha1.ExternalAuthClientTypePublic, nil
	default:
		return "", conversionError[arohcpv1alpha1.ExternalAuthClientType](externalAuthClientTypeRP)
	}
}

func convertCustomerManagedEncryptionCSToRP(in *arohcpv1alpha1.AzureEtcdDataEncryption) (*api.CustomerManagedEncryptionProfile, error) {
	if customerManaged, ok := in.GetCustomerManaged(); ok {
		encryptionType, err := convertCustomerManagedEncryptionTypeCSToRP(customerManaged.EncryptionType())
		if err != nil {
			return nil, err
		}

		return &api.CustomerManagedEncryptionProfile{
			EncryptionType: encryptionType,
			Kms:            convertKmsEncryptionCSToRP(in.CustomerManaged()),
		}, nil
	}

	return nil, nil
}

func convertKmsEncryptionCSToRP(in *arohcpv1alpha1.AzureEtcdDataEncryptionCustomerManaged) *api.KmsEncryptionProfile {
	if kms, ok := in.GetKms(); ok {
		if activeKey, ok := kms.GetActiveKey(); ok {
			return &api.KmsEncryptionProfile{
				ActiveKey: api.KmsKey{
					Name:      activeKey.KeyName(),
					VaultName: activeKey.KeyVaultName(),
					Version:   activeKey.KeyVersion(),
				},
			}
		}
	}
	return nil
}

func convertEtcdRPToCS(in api.EtcdProfile) (*arohcpv1alpha1.AzureEtcdEncryptionBuilder, error) {
	keyManagementMode, err := convertKeyManagementModeTypeRPToCS(in.DataEncryption.KeyManagementMode)
	if err != nil {
		return nil, err
	}

	azureEtcdDataEncryptionBuilder := arohcpv1alpha1.NewAzureEtcdDataEncryption().KeyManagementMode(keyManagementMode)
	if in.DataEncryption.CustomerManaged != nil {
		encryptionType, err := convertCustomerManagedEncryptionTypeRPToCS(in.DataEncryption.CustomerManaged.EncryptionType)
		if err != nil {
			return nil, err
		}

		azureEtcdDataEncryptionCustomerManagedBuilder := arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
			EncryptionType(encryptionType)
		azureKmsKeyBuilder := arohcpv1alpha1.NewAzureKmsKey().
			KeyName(in.DataEncryption.CustomerManaged.Kms.ActiveKey.Name).
			KeyVaultName(in.DataEncryption.CustomerManaged.Kms.ActiveKey.VaultName).
			KeyVersion(in.DataEncryption.CustomerManaged.Kms.ActiveKey.Version)
		azureKmsEncryptionBuilder := arohcpv1alpha1.NewAzureKmsEncryption().ActiveKey(azureKmsKeyBuilder)
		azureEtcdDataEncryptionCustomerManagedBuilder = azureEtcdDataEncryptionCustomerManagedBuilder.Kms(azureKmsEncryptionBuilder)
		azureEtcdDataEncryptionBuilder.CustomerManaged(azureEtcdDataEncryptionCustomerManagedBuilder)
	}
	return arohcpv1alpha1.NewAzureEtcdEncryption().DataEncryption(azureEtcdDataEncryptionBuilder), nil
}

// ConvertCStoHCPOpenShiftCluster converts a CS Cluster object into HCPOpenShiftCluster object
func ConvertCStoHCPOpenShiftCluster(resourceID *azcorearm.ResourceID, cluster *arohcpv1alpha1.Cluster) (*api.HCPOpenShiftCluster, error) {
	// A word about ProvisioningState:
	// ProvisioningState is stored in Cosmos and is applied to the
	// HCPOpenShiftCluster struct along with the ARM metadata that
	// is also stored in Cosmos. We could convert the ClusterState
	// from Cluster Service to a ProvisioningState, but instead we
	// defer that to the backend pod so that the ProvisioningState
	// stays consistent with the Status of any active non-terminal
	// operation on the cluster.

	apiVisibility, err := convertListeningToVisibility(cluster.API().Listening())
	if err != nil {
		return nil, err
	}
	outboundType, err := convertOutboundTypeCSToRP(cluster.Azure().NodesOutboundConnectivity().OutboundType())
	if err != nil {
		return nil, err
	}
	clusterImageRegistryState, err := convertClusterImageRegistryStateCSToRP(cluster.ImageRegistry().State())
	if err != nil {
		return nil, err
	}

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
				ID:           ocm.NewOpenShiftVersionXY(cluster.Version().ID()),
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
				Visibility: apiVisibility,
			},
			Platform: api.PlatformProfile{
				ManagedResourceGroup:   cluster.Azure().ManagedResourceGroupName(),
				SubnetID:               cluster.Azure().SubnetResourceID(),
				OutboundType:           outboundType,
				NetworkSecurityGroupID: cluster.Azure().NetworkSecurityGroupResourceID(),
				IssuerURL:              "",
			},
			NodeDrainTimeoutMinutes: convertNodeDrainTimeoutCSToRP(cluster),
			ClusterImageRegistry: api.ClusterImageRegistryProfile{
				State: clusterImageRegistryState,
			},
		},
	}

	// Only set etcd encryption settings if they exist in the cluster service response
	if cluster.Azure().EtcdEncryption() != nil {
		dataEncryption := cluster.Azure().EtcdEncryption().DataEncryption()
		if dataEncryption != nil {
			customerManaged, err := convertCustomerManagedEncryptionCSToRP(dataEncryption)
			if err != nil {
				return nil, err
			}
			keyManagementMode, err := convertKeyManagementModeTypeCSToRP(dataEncryption.KeyManagementMode())
			if err != nil {
				return nil, err
			}

			hcpcluster.Properties.Etcd = api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					CustomerManaged:   customerManaged,
					KeyManagementMode: keyManagementMode,
				},
			}
		}
	}

	// Each managed identity retrieved from Cluster Service needs to be added
	// to the HCPOpenShiftCluster in two places:
	// - The top-level Identity.UserAssignedIdentities map will need both the
	//   resourceID (as keys) and principal+client IDs (as values).
	// - The operator-specific maps under OperatorsAuthentication mimics the
	//   Cluster Service maps but just has operator-to-resourceID pairings.
	if cluster.Azure().OperatorsAuthentication() != nil {
		hcpcluster.Identity = &arm.ManagedServiceIdentity{
			UserAssignedIdentities: make(map[string]*arm.UserAssignedIdentity),
		}
		if mi, ok := cluster.Azure().OperatorsAuthentication().GetManagedIdentities(); ok {
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

	return hcpcluster, nil
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
	var err error

	// Ensure required headers are present.
	tenantID := requestHeader.Get(arm.HeaderNameHomeTenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("missing " + arm.HeaderNameHomeTenantID + " header")
	}

	clusterBuilder := arohcpv1alpha1.NewCluster()

	// These attributes cannot be updated after cluster creation.
	if !updating {
		// Add attributes that cannot be updated after cluster creation.
		clusterBuilder, err = withImmutableAttributes(clusterBuilder, hcpCluster,
			resourceID.SubscriptionID,
			resourceID.ResourceGroupName,
			f.location,
			tenantID,
			requestHeader.Get(arm.HeaderNameIdentityURL),
		)
		if err != nil {
			return nil, err
		}
	}

	clusterBuilder = clusterBuilder.
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(csNodeDrainGracePeriodUnit).
			Value(float64(hcpCluster.Properties.NodeDrainTimeoutMinutes)))

	clusterBuilder = f.clusterServiceClient.AddProperties(clusterBuilder)

	return clusterBuilder.Build()
}

func withImmutableAttributes(clusterBuilder *arohcpv1alpha1.ClusterBuilder, hcpCluster *api.HCPOpenShiftCluster, subscriptionID, resourceGroupName, location, tenantID, identityURL string) (*arohcpv1alpha1.ClusterBuilder, error) {
	apiListening, err := convertVisibilityToListening(hcpCluster.Properties.API.Visibility)
	if err != nil {
		return nil, err
	}
	clusterImageRegistryState, err := convertClusterImageRegistryStateRPToCS(hcpCluster.Properties.ClusterImageRegistry)
	if err != nil {
		return nil, err
	}
	outboundType, err := convertOutboundTypeRPToCS(hcpCluster.Properties.Platform.OutboundType)
	if err != nil {
		return nil, err
	}

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
			ID(ocm.NewOpenShiftVersionXYZ(hcpCluster.Properties.Version.ID)).
			ChannelGroup(hcpCluster.Properties.Version.ChannelGroup)).
		Network(arohcpv1alpha1.NewNetwork().
			Type(string(hcpCluster.Properties.Network.NetworkType)).
			PodCIDR(hcpCluster.Properties.Network.PodCIDR).
			ServiceCIDR(hcpCluster.Properties.Network.ServiceCIDR).
			MachineCIDR(hcpCluster.Properties.Network.MachineCIDR).
			HostPrefix(int(hcpCluster.Properties.Network.HostPrefix))).
		API(arohcpv1alpha1.NewClusterAPI().
			Listening(apiListening)).
		ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
			State(clusterImageRegistryState))
	azureBuilder := arohcpv1alpha1.NewAzure().
		TenantID(tenantID).
		SubscriptionID(subscriptionID).
		ResourceGroupName(resourceGroupName).
		ResourceName(hcpCluster.Name).
		ManagedResourceGroupName(ensureManagedResourceGroupName(hcpCluster)).
		SubnetResourceID(hcpCluster.Properties.Platform.SubnetID).
		NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
			OutboundType(outboundType))

	// Only add etcd encryption if it's actually configured
	if hcpCluster.Properties.Etcd.DataEncryption.KeyManagementMode != "" || hcpCluster.Properties.Etcd.DataEncryption.CustomerManaged != nil {
		etcdEncryption, err := convertEtcdRPToCS(hcpCluster.Properties.Etcd)
		if err != nil {
			return nil, err
		}
		azureBuilder = azureBuilder.EtcdEncryption(etcdEncryption)
	}

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

	return clusterBuilder, nil
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
				ID:           ocm.ConvertOpenShiftVersionNoPrefix(np.Version().ID()),
				ChannelGroup: np.Version().ChannelGroup(),
			},
			Platform: api.NodePoolPlatformProfile{
				SubnetID:               np.Subnet(),
				VMSize:                 np.AzureNodePool().VMSize(),
				EnableEncryptionAtHost: np.AzureNodePool().EncryptionAtHost().State() == csEncryptionAtHostStateEnabled,
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
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == csNodeDrainGracePeriodUnit {
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
				ID(ocm.ConvertOpenShiftVersionAddPrefix(nodePool.Properties.Version.ID)).
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

	if len(nodePool.Properties.Taints) > 0 {
		taintBuilders := []*arohcpv1alpha1.TaintBuilder{}
		for _, t := range nodePool.Properties.Taints {
			newTaintBuilder := arohcpv1alpha1.NewTaint().
				Effect(string(t.Effect)).
				Key(t.Key).
				Value(t.Value)
			taintBuilders = append(taintBuilders, newTaintBuilder)
		}
		npBuilder = npBuilder.Taints(taintBuilders...)
	}

	if nodePool.Properties.NodeDrainTimeoutMinutes != nil {
		npBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(csNodeDrainGracePeriodUnit).
			Value(float64(*nodePool.Properties.NodeDrainTimeoutMinutes)))
	}

	return npBuilder.Build()
}

// ConvertCStoExternalAuth converts a CS External Auth object into HCPOpenShiftClusterExternalAuth object
func ConvertCStoExternalAuth(resourceID *azcorearm.ResourceID, csExternalAuth *arohcpv1alpha1.ExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	usernameClaimPrefixPolicy, err := convertUsernameClaimPrefixPolicyCSToRP(csExternalAuth.Claim().Mappings().UserName().PrefixPolicy())
	if err != nil {
		return nil, err
	}

	externalAuth := &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID.String(),
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			// TODO fill these out later when CS supports Conditions fully
			// Condition: api.ExternalAuthCondition{},
			Issuer: api.TokenIssuerProfile{
				Url:       csExternalAuth.Issuer().URL(),
				Ca:        csExternalAuth.Issuer().CA(),
				Audiences: csExternalAuth.Issuer().Audiences(),
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        csExternalAuth.Claim().Mappings().UserName().Claim(),
						Prefix:       csExternalAuth.Claim().Mappings().UserName().Prefix(),
						PrefixPolicy: usernameClaimPrefixPolicy,
					},
				},
			},
		},
	}

	if groups, ok := csExternalAuth.Claim().Mappings().GetGroups(); ok {
		externalAuth.Properties.Claim.Mappings.Groups = &api.GroupClaimProfile{
			Claim:  groups.Claim(),
			Prefix: groups.Prefix(),
		}
	}

	clients := make([]api.ExternalAuthClientProfile, 0, len(csExternalAuth.Clients()))
	for _, client := range csExternalAuth.Clients() {
		clientType, err := convertExternalAuthClientTypeCSToRP(client.Type())
		if err != nil {
			return nil, err
		}

		clients = append(clients, api.ExternalAuthClientProfile{
			Component: api.ExternalAuthClientComponentProfile{
				Name:                client.Component().Name(),
				AuthClientNamespace: client.Component().Namespace(),
			},
			ClientId:                      client.ID(),
			ExtraScopes:                   client.ExtraScopes(),
			ExternalAuthClientProfileType: clientType,
		})
	}
	externalAuth.Properties.Clients = clients

	validationRules := make([]api.TokenClaimValidationRule, 0, len(csExternalAuth.Claim().ValidationRules()))
	if csExternalAuth.Claim().ValidationRules() != nil {
		for _, validationRule := range csExternalAuth.Claim().ValidationRules() {
			validationRules = append(validationRules, api.TokenClaimValidationRule{
				// We hard code the type here because CS only supports this type currently and doesn't reference the type.
				TokenClaimValidationRuleType: api.TokenValidationRuleTypeRequiredClaim,
				RequiredClaim: api.TokenRequiredClaim{
					Claim:         validationRule.Claim(),
					RequiredValue: validationRule.RequiredValue(),
				},
			})
		}
	}
	externalAuth.Properties.Claim.ValidationRules = validationRules

	return externalAuth, nil
}

// BuildCSExternalAuth creates a CS External Auth object from an HCPOpenShiftClusterExternalAuth object
func (f *Frontend) BuildCSExternalAuth(ctx context.Context, externalAuth *api.HCPOpenShiftClusterExternalAuth, updating bool) (*arohcpv1alpha1.ExternalAuth, error) {
	externalAuthBuilder := arohcpv1alpha1.NewExternalAuth()

	// These attributes cannot be updated after node pool creation.
	if !updating {
		externalAuthBuilder = externalAuthBuilder.ID(externalAuth.Name)
	}

	externalAuthBuilder.Issuer(arohcpv1alpha1.NewTokenIssuer().
		URL(externalAuth.Properties.Issuer.Url).
		CA(externalAuth.Properties.Issuer.Ca).
		Audiences(externalAuth.Properties.Issuer.Audiences...),
	)

	clientConfigs := []*arohcpv1alpha1.ExternalAuthClientConfigBuilder{}
	for _, t := range externalAuth.Properties.Clients {
		clientType, err := convertExternalAuthClientTypeRPToCS(t.ExternalAuthClientProfileType)
		if err != nil {
			return nil, err
		}

		newClientConfig := arohcpv1alpha1.NewExternalAuthClientConfig().
			ID(t.ClientId).
			Component(arohcpv1alpha1.NewClientComponent().
				Name(t.Component.Name).
				Namespace(t.Component.AuthClientNamespace),
			).
			ExtraScopes(t.ExtraScopes...).
			Type(clientType)
		clientConfigs = append(clientConfigs, newClientConfig)
	}
	externalAuthBuilder = externalAuthBuilder.Clients(clientConfigs...)

	err := buildClaims(externalAuthBuilder, *externalAuth)
	if err != nil {
		return nil, err
	}

	return externalAuthBuilder.Build()
}

func buildClaims(externalAuthBuilder *arohcpv1alpha1.ExternalAuthBuilder, hcpExternalAuth api.HCPOpenShiftClusterExternalAuth) error {
	usernameClaimPrefixPolicy, err := convertUsernameClaimPrefixPolicyRPToCS(hcpExternalAuth.Properties.Claim.Mappings.Username.PrefixPolicy)
	if err != nil {
		return err
	}

	claimBuilder := arohcpv1alpha1.NewExternalAuthClaim()

	mappingsBuilder := arohcpv1alpha1.NewTokenClaimMappings()
	mappingsBuilder.UserName(arohcpv1alpha1.NewUsernameClaim().
		Claim(hcpExternalAuth.Properties.Claim.Mappings.Username.Claim).
		Prefix(hcpExternalAuth.Properties.Claim.Mappings.Username.Prefix).
		PrefixPolicy(usernameClaimPrefixPolicy),
	)

	if hcpExternalAuth.Properties.Claim.Mappings.Groups != nil {
		mappingsBuilder.Groups(arohcpv1alpha1.NewGroupsClaim().
			Claim(hcpExternalAuth.Properties.Claim.Mappings.Groups.Claim).
			Prefix(hcpExternalAuth.Properties.Claim.Mappings.Groups.Prefix),
		)
	}
	claimBuilder.Mappings(mappingsBuilder)

	if len(hcpExternalAuth.Properties.Claim.ValidationRules) > 0 {
		validationRules := []*arohcpv1alpha1.TokenClaimValidationRuleBuilder{}
		for _, t := range hcpExternalAuth.Properties.Claim.ValidationRules {
			newClientConfig := arohcpv1alpha1.NewTokenClaimValidationRule().
				Claim(t.RequiredClaim.Claim).
				RequiredValue(t.RequiredClaim.RequiredValue)
			validationRules = append(validationRules, newClientConfig)
		}
		claimBuilder.ValidationRules(validationRules...)
	}

	externalAuthBuilder.Claim(claimBuilder)

	return nil
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

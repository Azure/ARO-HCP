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

package ocm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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

	serviceUnavailableRetryAfterInterval string = "60" // seconds
)

// Sentinel error for use with errors.Is
var ErrUnknownValue = errors.New("unknown value")

func conversionError[T any](v any) error {
	return fmt.Errorf("cannot convert %T(%q) to %T: %w", v, v, *new(T), ErrUnknownValue)
}

func convertListeningToVisibility(listening arohcpv1alpha1.ListeningMethod) (api.Visibility, error) {
	switch listening {
	case "":
		// We convert illegal values because zero-value is the state for an object and while the value may not be valid
		// We need to convert it and let validation worry about whether it is legal or illegal in the context of its usage.
		// Zero values are preserved through round tripping these, the difference we're seeing here is the expression of unset
		// in ocm-api-model is different than the expression of unset in canonical golang.
		return "", nil
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
	case "":
		// We convert illegal values because zero-value is the state for an object and while the value may not be valid
		// We need to convert it and let validation worry about whether it is legal or illegal in the context of its usage.
		// Zero values are preserved through round tripping these, the difference we're seeing here is the expression of unset
		// in ocm-api-model is different than the expression of unset in canonical golang.
		return "", nil
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

func convertUsernameClaimPrefixPolicyCSToRP(prefixPolicyCS string) (api.UsernameClaimPrefixPolicy, error) {
	switch prefixPolicyCS {
	case csUsernameClaimPrefixPolicyPrefix:
		return api.UsernameClaimPrefixPolicyPrefix, nil
	case csUsernameClaimPrefixPolicyNoPrefix:
		return api.UsernameClaimPrefixPolicyNoPrefix, nil
	case "":
		return api.UsernameClaimPrefixPolicyNone, nil
	default:
		return "", conversionError[api.UsernameClaimPrefixPolicy](prefixPolicyCS)
	}
}

func convertUsernameClaimPrefixPolicyRPToCS(prefixPolicyRP api.UsernameClaimPrefixPolicy) (string, error) {
	switch prefixPolicyRP {
	case api.UsernameClaimPrefixPolicyPrefix:
		return csUsernameClaimPrefixPolicyPrefix, nil
	case api.UsernameClaimPrefixPolicyNoPrefix:
		return csUsernameClaimPrefixPolicyNoPrefix, nil
	case api.UsernameClaimPrefixPolicyNone:
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
	case "":
		// We convert illegal values because zero-value is the state for an object and while the value may not be valid
		// We need to convert it and let validation worry about whether it is legal or illegal in the context of its usage.
		// Zero values are preserved through round tripping these, the difference we're seeing here is the expression of unset
		// in ocm-api-model is different than the expression of unset in canonical golang.
		return "", nil
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

func convertAutoscalarCSToRP(in *arohcpv1alpha1.ClusterAutoscaler) (api.ClusterAutoscalingProfile, error) {
	if in == nil {
		return api.ClusterAutoscalingProfile{}, nil
	}

	var maxNodeProvisionTime int32
	if len(in.MaxNodeProvisionTime()) > 0 {
		// maxNodeProvisionTime (string) - minutes e.g - “15m”
		// https://gitlab.cee.redhat.com/service/uhc-clusters-service/-/blob/master/pkg/api/autoscaler.go?ref_type=heads#L30-42
		maxNodeProvisionTimeDuration, err := time.ParseDuration(in.MaxNodeProvisionTime())
		if err != nil {
			return api.ClusterAutoscalingProfile{}, err
		}
		maxNodeProvisionTime = int32(maxNodeProvisionTimeDuration.Seconds())
	}

	return api.ClusterAutoscalingProfile{
		MaxNodesTotal: int32(in.ResourceLimits().MaxNodesTotal()),
		// MaxPodGracePeriod (int) - seconds e.g - 300
		// https://gitlab.cee.redhat.com/service/uhc-clusters-service/-/blob/master/pkg/api/autoscaler.go?ref_type=heads#L30-42
		MaxPodGracePeriodSeconds:    int32(in.MaxPodGracePeriod()),
		MaxNodeProvisionTimeSeconds: maxNodeProvisionTime,
		PodPriorityThreshold:        int32(in.PodPriorityThreshold()),
	}, nil
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
		azureEtcdDataEncryptionCustomerManagedBuilder.Kms(azureKmsEncryptionBuilder)
		azureEtcdDataEncryptionBuilder.CustomerManaged(azureEtcdDataEncryptionCustomerManagedBuilder)
	}
	return arohcpv1alpha1.NewAzureEtcdEncryption().DataEncryption(azureEtcdDataEncryptionBuilder), nil
}

// ConvertCStoHCPOpenShiftCluster converts a CS Cluster object into an HCPOpenShiftCluster object.
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
	clusterAutoscaler, err := convertAutoscalarCSToRP(cluster.Autoscaler())
	if err != nil {
		return nil, err
	}

	hcpcluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			},
			Location: arm.GetAzureLocation(),
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           NewOpenShiftVersionXY(cluster.Version().ID()),
				ChannelGroup: cluster.Version().ChannelGroup(),
			},
			DNS: api.CustomerDNSProfile{
				BaseDomainPrefix: cluster.DomainPrefix(),
			},
			Network: api.NetworkProfile{
				NetworkType: api.NetworkType(cluster.Network().Type()),
				PodCIDR:     cluster.Network().PodCIDR(),
				ServiceCIDR: cluster.Network().ServiceCIDR(),
				MachineCIDR: cluster.Network().MachineCIDR(),
				HostPrefix:  int32(cluster.Network().HostPrefix()),
			},

			API: api.CustomerAPIProfile{
				Visibility: apiVisibility,
			},
			Platform: api.CustomerPlatformProfile{
				ManagedResourceGroup:   cluster.Azure().ManagedResourceGroupName(),
				SubnetID:               cluster.Azure().SubnetResourceID(),
				OutboundType:           outboundType,
				NetworkSecurityGroupID: cluster.Azure().NetworkSecurityGroupResourceID(),
			},
			Autoscaling:             clusterAutoscaler,
			NodeDrainTimeoutMinutes: convertNodeDrainTimeoutCSToRP(cluster),
			ClusterImageRegistry: api.ClusterImageRegistryProfile{
				State: clusterImageRegistryState,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			DNS: api.ServiceProviderDNSProfile{
				BaseDomain: cluster.DNS().BaseDomain(),
			},
			Console: api.ServiceProviderConsoleProfile{
				URL: cluster.Console().URL(),
			},
			API: api.ServiceProviderAPIProfile{
				URL: cluster.API().URL(),
			},
			Platform: api.ServiceProviderPlatformProfile{
				IssuerURL: "",
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

			hcpcluster.CustomerProperties.Etcd = api.EtcdProfile{
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
		if mi, ok := cluster.Azure().OperatorsAuthentication().GetManagedIdentities(); ok {
			for operatorName, operatorIdentity := range mi.ControlPlaneOperatorsManagedIdentities() {
				if hcpcluster.Identity == nil {
					hcpcluster.Identity = &arm.ManagedServiceIdentity{}
				}
				if hcpcluster.Identity.UserAssignedIdentities == nil {
					hcpcluster.Identity.UserAssignedIdentities = make(map[string]*arm.UserAssignedIdentity)
				}
				if hcpcluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators == nil {
					hcpcluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = make(map[string]string)
				}

				clientID, _ := operatorIdentity.GetClientID()
				principalID, _ := operatorIdentity.GetPrincipalID()
				hcpcluster.Identity.UserAssignedIdentities[operatorIdentity.ResourceID()] = &arm.UserAssignedIdentity{ClientID: &clientID,
					PrincipalID: &principalID}
				hcpcluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[operatorName] = operatorIdentity.ResourceID()
			}
			for operatorName, operatorIdentity := range mi.DataPlaneOperatorsManagedIdentities() {
				if hcpcluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators == nil {
					hcpcluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = make(map[string]string)
				}

				// Skip adding to hcpcluster.Identity.UserAssignedIdentities map as it is not needed for the dataplane operator MIs.
				hcpcluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[operatorName] = operatorIdentity.ResourceID()
			}
			if len(mi.ServiceManagedIdentity().ResourceID()) > 0 {
				if hcpcluster.Identity == nil {
					hcpcluster.Identity = &arm.ManagedServiceIdentity{}
				}
				if hcpcluster.Identity.UserAssignedIdentities == nil {
					hcpcluster.Identity.UserAssignedIdentities = make(map[string]*arm.UserAssignedIdentity)
				}

				clientID, _ := mi.ServiceManagedIdentity().GetClientID()
				principalID, _ := mi.ServiceManagedIdentity().GetPrincipalID()
				hcpcluster.Identity.UserAssignedIdentities[mi.ServiceManagedIdentity().ResourceID()] = &arm.UserAssignedIdentity{ClientID: &clientID,
					PrincipalID: &principalID}
				hcpcluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = mi.ServiceManagedIdentity().ResourceID()
			}
		}
	}

	return hcpcluster, nil
}

// ensureManagedResourceGroupName makes sure the ManagedResourceGroupName field is set.
// If the field is empty a default is generated.
func ensureManagedResourceGroupName(hcpCluster *api.HCPOpenShiftCluster) string {
	if hcpCluster.CustomerProperties.Platform.ManagedResourceGroup != "" {
		return hcpCluster.CustomerProperties.Platform.ManagedResourceGroup
	}
	var clusterName string
	if len(hcpCluster.Name) >= 45 {
		clusterName = (hcpCluster.Name)[:45]
	} else {
		clusterName = hcpCluster.Name
	}

	return "arohcp-" + clusterName + "-" + uuid.New().String()
}

func convertRpAutoscalarToCSBuilder(in *api.ClusterAutoscalingProfile) (*arohcpv1alpha1.ClusterAutoscalerBuilder, error) {

	// MaxNodeProvisionTime (string) - minutes e.g - “15m”
	// https://gitlab.cee.redhat.com/service/uhc-clusters-service/-/blob/master/pkg/api/autoscaler.go?ref_type=heads#L30-42
	maxNodeProvisionDuration, err := time.ParseDuration(fmt.Sprint(in.MaxNodeProvisionTimeSeconds, "s"))
	if err != nil {
		return nil, err
	}

	return arohcpv1alpha1.NewClusterAutoscaler().
		MaxNodeProvisionTime(fmt.Sprint(maxNodeProvisionDuration.Minutes(), "m")).
		MaxPodGracePeriod(int(in.MaxPodGracePeriodSeconds)).
		PodPriorityThreshold(int(in.PodPriorityThreshold)).
		ResourceLimits(
			arohcpv1alpha1.NewAutoscalerResourceLimits().
				MaxNodesTotal(int(in.MaxNodesTotal)),
		), nil
}

// BuildCSCluster creates a CS ClusterBuilder object from an HCPOpenShiftCluster object.
func BuildCSCluster(resourceID *azcorearm.ResourceID, requestHeader http.Header, hcpCluster *api.HCPOpenShiftCluster, updating bool) (*arohcpv1alpha1.ClusterBuilder, error) {
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
			tenantID,
			requestHeader.Get(arm.HeaderNameIdentityURL),
		)
		if err != nil {
			return nil, err
		}
	}

	clusterBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
		Unit(csNodeDrainGracePeriodUnit).
		Value(float64(hcpCluster.CustomerProperties.NodeDrainTimeoutMinutes)))

	clusterAutoscalerBuilder, err := convertRpAutoscalarToCSBuilder(&hcpCluster.CustomerProperties.Autoscaling)
	if err != nil {
		return nil, err
	}

	clusterBuilder.Autoscaler(clusterAutoscalerBuilder)

	return clusterBuilder, nil
}

func withImmutableAttributes(clusterBuilder *arohcpv1alpha1.ClusterBuilder, hcpCluster *api.HCPOpenShiftCluster, subscriptionID, resourceGroupName, tenantID, identityURL string) (*arohcpv1alpha1.ClusterBuilder, error) {
	apiListening, err := convertVisibilityToListening(hcpCluster.CustomerProperties.API.Visibility)
	if err != nil {
		return nil, err
	}
	clusterImageRegistryState, err := convertClusterImageRegistryStateRPToCS(hcpCluster.CustomerProperties.ClusterImageRegistry)
	if err != nil {
		return nil, err
	}
	outboundType, err := convertOutboundTypeRPToCS(hcpCluster.CustomerProperties.Platform.OutboundType)
	if err != nil {
		return nil, err
	}

	clusterBuilder.
		Name(strings.ToLower(hcpCluster.Name)).
		Flavour(arohcpv1alpha1.NewFlavour().
			ID(csFlavourId)).
		Region(arohcpv1alpha1.NewCloudRegion().
			ID(arm.GetAzureLocation())).
		CloudProvider(arohcpv1alpha1.NewCloudProvider().
			ID(csCloudProvider)).
		Product(arohcpv1alpha1.NewProduct().
			ID(csProductId)).
		Hypershift(arohcpv1alpha1.NewHypershift().
			Enabled(csHypershifEnabled)).
		CCS(arohcpv1alpha1.NewCCS().Enabled(csCCSEnabled)).
		Version(arohcpv1alpha1.NewVersion().
			ID(NewOpenShiftVersionXYZ(hcpCluster.CustomerProperties.Version.ID, hcpCluster.CustomerProperties.Version.ChannelGroup)).
			ChannelGroup(hcpCluster.CustomerProperties.Version.ChannelGroup)).
		Network(arohcpv1alpha1.NewNetwork().
			Type(string(hcpCluster.CustomerProperties.Network.NetworkType)).
			PodCIDR(hcpCluster.CustomerProperties.Network.PodCIDR).
			ServiceCIDR(hcpCluster.CustomerProperties.Network.ServiceCIDR).
			MachineCIDR(hcpCluster.CustomerProperties.Network.MachineCIDR).
			HostPrefix(int(hcpCluster.CustomerProperties.Network.HostPrefix))).
		API(arohcpv1alpha1.NewClusterAPI().
			Listening(apiListening)).
		ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
			State(clusterImageRegistryState))
	azureBuilder := arohcpv1alpha1.NewAzure().
		TenantID(tenantID).
		SubscriptionID(strings.ToLower(subscriptionID)).
		ResourceGroupName(strings.ToLower(resourceGroupName)).
		ResourceName(strings.ToLower(hcpCluster.Name)).
		ManagedResourceGroupName(ensureManagedResourceGroupName(hcpCluster)).
		SubnetResourceID(hcpCluster.CustomerProperties.Platform.SubnetID).
		NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
			OutboundType(outboundType))

	// Only add etcd encryption if it's actually configured
	if hcpCluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode != "" || hcpCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil {
		etcdEncryption, err := convertEtcdRPToCS(hcpCluster.CustomerProperties.Etcd)
		if err != nil {
			return nil, err
		}
		azureBuilder.EtcdEncryption(etcdEncryption)
	}

	// Cluster Service rejects an empty NetworkSecurityGroupResourceID string.
	if hcpCluster.CustomerProperties.Platform.NetworkSecurityGroupID != "" {
		azureBuilder.NetworkSecurityGroupResourceID(hcpCluster.CustomerProperties.Platform.NetworkSecurityGroupID)
	}

	controlPlaneOperators := make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)
	for operatorName, identityResourceID := range hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		controlPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureControlPlaneManagedIdentity().ResourceID(identityResourceID)
	}

	dataPlaneOperators := make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)
	for operatorName, identityResourceID := range hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		dataPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureDataPlaneManagedIdentity().ResourceID(identityResourceID)
	}

	managedIdentitiesBuilder := arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
		ManagedIdentitiesDataPlaneIdentityUrl(identityURL).
		ControlPlaneOperatorsManagedIdentities(controlPlaneOperators).
		DataPlaneOperatorsManagedIdentities(dataPlaneOperators)

	if hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity != "" {
		managedIdentitiesBuilder.ServiceManagedIdentity(arohcpv1alpha1.NewAzureServiceManagedIdentity().
			ResourceID(hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity))
	}

	azureBuilder.OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().ManagedIdentities(managedIdentitiesBuilder))

	clusterBuilder.Azure(azureBuilder)

	// Cluster Service rejects an empty DomainPrefix string.
	if hcpCluster.CustomerProperties.DNS.BaseDomainPrefix != "" {
		clusterBuilder.DomainPrefix(hcpCluster.CustomerProperties.DNS.BaseDomainPrefix)
	}

	return clusterBuilder, nil
}

// ConvertCStoNodePool converts a CS NodePool object into an HCPOpenShiftClusterNodePool object.
func ConvertCStoNodePool(resourceID *azcorearm.ResourceID, np *arohcpv1alpha1.NodePool) *api.HCPOpenShiftClusterNodePool {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			},
			Location: arm.GetAzureLocation(),
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID:           ConvertOpenShiftVersionNoPrefix(np.Version().ID()),
				ChannelGroup: np.Version().ChannelGroup(),
			},
			Platform: api.NodePoolPlatformProfile{
				SubnetID:               np.Subnet(),
				VMSize:                 np.AzureNodePool().VMSize(),
				EnableEncryptionAtHost: np.AzureNodePool().EncryptionAtHost().State() == csEncryptionAtHostStateEnabled,
				OSDisk: api.OSDiskProfile{
					SizeGiB:                int32(np.AzureNodePool().OsDisk().SizeGibibytes()),
					DiskStorageAccountType: api.DiskStorageAccountType(np.AzureNodePool().OsDisk().StorageAccountType()),
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

	if np.Taints() != nil {
		taints := make([]api.Taint, 0, len(np.Taints()))
		for _, t := range np.Taints() {
			taints = append(taints, api.Taint{
				Effect: api.Effect(t.Effect()),
				Key:    t.Key(),
				Value:  t.Value(),
			})
		}
		nodePool.Properties.Taints = taints
	}

	if nodeDrainGracePeriod, ok := np.GetNodeDrainGracePeriod(); ok {
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == csNodeDrainGracePeriodUnit {
			nodePool.Properties.NodeDrainTimeoutMinutes = api.Ptr(int32(nodeDrainGracePeriod.Value()))
		}
	}

	return nodePool
}

// BuildCSNodePool creates a CS NodePoolBuilder object from an HCPOpenShiftClusterNodePool object.
func BuildCSNodePool(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool, updating bool) (*arohcpv1alpha1.NodePoolBuilder, error) {
	nodePoolBuilder := arohcpv1alpha1.NewNodePool()

	// These attributes cannot be updated after node pool creation.
	if !updating {
		nodePoolBuilder.
			ID(strings.ToLower(nodePool.Name)).
			Version(arohcpv1alpha1.NewVersion().
				ID(NewOpenShiftVersionXYZ(nodePool.Properties.Version.ID, nodePool.Properties.Version.ChannelGroup)).
				ChannelGroup(nodePool.Properties.Version.ChannelGroup)).
			Subnet(nodePool.Properties.Platform.SubnetID).
			AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
				ResourceName(strings.ToLower(nodePool.Name)).
				VMSize(nodePool.Properties.Platform.VMSize).
				EncryptionAtHost(convertEnableEncryptionAtHostToCSBuilder(nodePool.Properties.Platform)).
				OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().
					SizeGibibytes(int(nodePool.Properties.Platform.OSDisk.SizeGiB)).
					StorageAccountType(string(nodePool.Properties.Platform.OSDisk.DiskStorageAccountType)))).
			AvailabilityZone(nodePool.Properties.Platform.AvailabilityZone).
			AutoRepair(nodePool.Properties.AutoRepair)
	}

	nodePoolBuilder.Labels(nodePool.Properties.Labels)

	if nodePool.Properties.AutoScaling != nil {
		nodePoolBuilder.Autoscaling(arohcpv1alpha1.NewNodePoolAutoscaling().
			MinReplica(int(nodePool.Properties.AutoScaling.Min)).
			MaxReplica(int(nodePool.Properties.AutoScaling.Max)))
	} else {
		nodePoolBuilder.Replicas(int(nodePool.Properties.Replicas))
	}

	if nodePool.Properties.Taints != nil {
		taintBuilders := []*arohcpv1alpha1.TaintBuilder{}
		for _, t := range nodePool.Properties.Taints {
			newTaintBuilder := arohcpv1alpha1.NewTaint().
				Effect(string(t.Effect)).
				Key(t.Key).
				Value(t.Value)
			taintBuilders = append(taintBuilders, newTaintBuilder)
		}
		nodePoolBuilder.Taints(taintBuilders...)
	}

	if nodePool.Properties.NodeDrainTimeoutMinutes != nil {
		nodePoolBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(csNodeDrainGracePeriodUnit).
			Value(float64(*nodePool.Properties.NodeDrainTimeoutMinutes)))
	}

	return nodePoolBuilder, nil
}

// ConvertCStoExternalAuth converts a CS ExternalAuth object into HCPOpenShiftClusterExternalAuth object.
func ConvertCStoExternalAuth(resourceID *azcorearm.ResourceID, csExternalAuth *arohcpv1alpha1.ExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	usernameClaimPrefixPolicy, err := convertUsernameClaimPrefixPolicyCSToRP(csExternalAuth.Claim().Mappings().UserName().PrefixPolicy())
	if err != nil {
		return nil, err
	}

	externalAuth := &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: resourceID.Name,
				Type: resourceID.ResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			// TODO fill these out later when CS supports Conditions fully
			// Condition: api.ExternalAuthCondition{},
			Issuer: api.TokenIssuerProfile{
				URL:       csExternalAuth.Issuer().URL(),
				CA:        csExternalAuth.Issuer().CA(),
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
			ClientID:    client.ID(),
			ExtraScopes: client.ExtraScopes(),
			Type:        clientType,
		})
	}
	externalAuth.Properties.Clients = clients

	validationRules := make([]api.TokenClaimValidationRule, 0, len(csExternalAuth.Claim().ValidationRules()))
	if csExternalAuth.Claim().ValidationRules() != nil {
		for _, validationRule := range csExternalAuth.Claim().ValidationRules() {
			validationRules = append(validationRules, api.TokenClaimValidationRule{
				// We hard code the type here because CS only supports this type currently and doesn't reference the type.
				Type: api.TokenValidationRuleTypeRequiredClaim,
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

// BuildCSExternalAuth creates a CS ExternalAuthBuilder object from an HCPOpenShiftClusterExternalAuth object.
func BuildCSExternalAuth(ctx context.Context, externalAuth *api.HCPOpenShiftClusterExternalAuth, updating bool) (*arohcpv1alpha1.ExternalAuthBuilder, error) {
	externalAuthBuilder := arohcpv1alpha1.NewExternalAuth()

	// These attributes cannot be updated after node pool creation.
	if !updating {
		externalAuthBuilder.ID(strings.ToLower(externalAuth.Name))
	}

	externalAuthBuilder.Issuer(arohcpv1alpha1.NewTokenIssuer().
		URL(externalAuth.Properties.Issuer.URL).
		CA(externalAuth.Properties.Issuer.CA).
		Audiences(externalAuth.Properties.Issuer.Audiences...),
	)

	clientConfigs := []*arohcpv1alpha1.ExternalAuthClientConfigBuilder{}
	for _, t := range externalAuth.Properties.Clients {
		clientType, err := convertExternalAuthClientTypeRPToCS(t.Type)
		if err != nil {
			return nil, err
		}

		newClientConfig := arohcpv1alpha1.NewExternalAuthClientConfig().
			ID(t.ClientID).
			Component(arohcpv1alpha1.NewClientComponent().
				Name(t.Component.Name).
				Namespace(t.Component.AuthClientNamespace),
			).
			ExtraScopes(t.ExtraScopes...).
			Type(clientType)
		clientConfigs = append(clientConfigs, newClientConfig)
	}
	externalAuthBuilder.Clients(clientConfigs...)

	err := buildClaims(externalAuthBuilder, *externalAuth)
	if err != nil {
		return nil, err
	}

	return externalAuthBuilder, nil
}

func buildClaims(externalAuthBuilder *arohcpv1alpha1.ExternalAuthBuilder, hcpExternalAuth api.HCPOpenShiftClusterExternalAuth) error {
	usernameClaimPrefixPolicy, err := convertUsernameClaimPrefixPolicyRPToCS(hcpExternalAuth.Properties.Claim.Mappings.Username.PrefixPolicy)
	if err != nil {
		return err
	}

	tokenClaimMappingsBuilder := arohcpv1alpha1.NewTokenClaimMappings().
		UserName(arohcpv1alpha1.NewUsernameClaim().
			Claim(hcpExternalAuth.Properties.Claim.Mappings.Username.Claim).
			Prefix(hcpExternalAuth.Properties.Claim.Mappings.Username.Prefix).
			PrefixPolicy(usernameClaimPrefixPolicy),
		)
	if hcpExternalAuth.Properties.Claim.Mappings.Groups != nil {
		tokenClaimMappingsBuilder = tokenClaimMappingsBuilder.Groups(
			arohcpv1alpha1.NewGroupsClaim().
				Claim(hcpExternalAuth.Properties.Claim.Mappings.Groups.Claim).
				Prefix(hcpExternalAuth.Properties.Claim.Mappings.Groups.Prefix),
		)
	}

	validationRules := []*arohcpv1alpha1.TokenClaimValidationRuleBuilder{}
	for _, t := range hcpExternalAuth.Properties.Claim.ValidationRules {
		newClientConfig := arohcpv1alpha1.NewTokenClaimValidationRule().
			Claim(t.RequiredClaim.Claim).
			RequiredValue(t.RequiredClaim.RequiredValue)
		validationRules = append(validationRules, newClientConfig)
	}

	externalAuthBuilder.
		Claim(arohcpv1alpha1.NewExternalAuthClaim().
			Mappings(tokenClaimMappingsBuilder).
			ValidationRules(validationRules...),
		)

	return nil
}

// ConvertCStoAdminCredential converts a CS BreakGlassCredential object into an HCPOpenShiftClusterAdminCredential object.
func ConvertCStoAdminCredential(breakGlassCredential *cmv1.BreakGlassCredential) *api.HCPOpenShiftClusterAdminCredential {
	return &api.HCPOpenShiftClusterAdminCredential{
		ExpirationTimestamp: breakGlassCredential.ExpirationTimestamp(),
		Kubeconfig:          breakGlassCredential.Kubeconfig(),
	}
}

// ConvertCStoHCPOpenShiftVersion converts a CS Version object into an HCPOpenShiftVersion object.
func ConvertCStoHCPOpenShiftVersion(resourceID *azcorearm.ResourceID, version *arohcpv1alpha1.Version) *api.HCPOpenShiftVersion {
	return &api.HCPOpenShiftVersion{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID,
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
func CSErrorToCloudError(err error, resourceID *azcorearm.ResourceID, header http.Header) *arm.CloudError {
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
		case http.StatusServiceUnavailable:
			// ServiceUnavailable can be returned immediately on a cluster
			// creation request if cluster capacity would breach the value
			// of the --provision-shard-cluster-limit option.
			//
			// ocm-sdk-go already retries on this response status (see its
			// retry/transport_wrapper.go) so there is no point in the RP
			// retrying as well. Instead we add a Retry-After header to the
			// response.
			if header != nil {
				header.Set("Retry-After", serviceUnavailableRetryAfterInterval)
			}
			return arm.NewCloudError(
				statusCode,
				arm.CloudErrorCodeServiceUnavailable,
				"", "%s", ocmError.Reason())
		}
	}

	return arm.NewInternalServerError()
}

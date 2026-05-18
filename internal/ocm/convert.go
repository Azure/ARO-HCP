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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Conversion Flow Architecture
//
// This file handles bidirectional conversion between Azure Resource Manager (ARM)
// and OpenShift Cluster Manager (OCM/Cluster Service) representations.
//
// CRITICAL: Both Frontend (FE) and Backend (BE) read through CosmosToInternal*()
// conversion functions in internal/database/convert_*.go. Any defaults applied
// during that conversion affect both FE and BE consistently.
//
// FLOW PATHS:
// 1. FE API Request -> internal API -> Cosmos (via InternalToCosmos)
// 2. BE reads Cosmos -> internal API (via CosmosToInternal) -> CS format (via RPToCS functions here)
// 3. CS state -> internal API (via CSToRP functions here) -> Cosmos (via InternalToCosmos)
// 4. FE GET response: Cosmos -> internal API (via CosmosToInternal), merged with CS data (via mergeToInternal*)
//
// INVARIANTS:
// - Canonical defaults (in EnsureDefaults) and CS->RP defaults (here) must match
// - GET-then-PUT must preserve all explicit values (use Ptr, not PtrOrNil for bools)
// - MigrateCosmosOrDie persists defaults via Get->Replace during FE deployment startup
//
// See docs/api-version-defaults-and-storage.md for the full design rationale.

const (
	csCloudProvider    string = "azure"
	csProductId        string = "aro"
	csHypershifEnabled bool   = true
	csCCSEnabled       bool   = true

	// The OCM SDK does not provide these constants.

	csCustomerManagedEncryptionTypeKms   string = "kms"
	csEncryptionAtHostStateDisabled      string = "disabled"
	csEncryptionAtHostStateEnabled       string = "enabled"
	csImageRegistryStateDisabled         string = "disabled"
	csImageRegistryStateEnabled          string = "enabled"
	csKeyManagementModeCustomerManaged   string = "customer_managed"
	csKeyManagementModePlatformManaged   string = "platform_managed"
	csNodeDrainGracePeriodUnit           string = "minutes"
	csOutboundType                       string = "load_balancer"
	csUsernameClaimPrefixPolicyNoPrefix  string = "NoPrefix"
	csUsernameClaimPrefixPolicyPrefix    string = "Prefix"
	csCIDRBlockAllowAccessModeAllowAll   string = "allow_all"
	csCIDRBlockAllowAccessModeAllowList  string = "allow_list"
	csOsDiskPersistencePersistent        string = "persistent"
	csOsDiskPersistenceEphemeral         string = "ephemeral"
	csProvisioningShardStatusActive      string = "active"
	csProvisioningShardStatusMaintenance string = "maintenance"
	csProvisioningShardStatusOffline     string = "offline"
)

// Sentinel error for use with errors.Is
var ErrUnknownValue = errors.New("unknown value")

func conversionError[T any](v any) error {
	return fmt.Errorf("cannot convert %T(%q) to %T: %w", v, v, *new(T), ErrUnknownValue)
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

func convertKeyVaultVisibilityRPToCS(visibility api.KeyVaultVisibility) (arohcpv1alpha1.AzureKmsEncryptionVisibility, error) {
	switch visibility {
	case api.KeyVaultVisibilityPublic:
		return arohcpv1alpha1.AzureKmsEncryptionVisibilityPublic, nil
	case api.KeyVaultVisibilityPrivate:
		return arohcpv1alpha1.AzureKmsEncryptionVisibilityPrivate, nil
	default:
		return "", conversionError[arohcpv1alpha1.AzureKmsEncryptionVisibility](visibility)
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

func convertDiskStorageAccountTypeRPToCS(storageAccountTypeRP api.DiskStorageAccountType) (string, error) {
	switch storageAccountTypeRP {
	case api.DiskStorageAccountTypePremium_LRS:
		return string(api.DiskStorageAccountTypePremium_LRS), nil
	case api.DiskStorageAccountTypeStandardSSD_LRS:
		return string(api.DiskStorageAccountTypeStandardSSD_LRS), nil
	case api.DiskStorageAccountTypeStandard_LRS:
		return string(api.DiskStorageAccountTypeStandard_LRS), nil
	default:
		// Do not add a "" case here. Canonical defaults in EnsureDefaults()
		// and API-version defaults in SetDefaultValues*() guarantee non-empty
		// values before this function is called on the write path.
		// An empty value here indicates a bug in the defaults pipeline.
		return "", conversionError[string](storageAccountTypeRP)
	}
}

func convertDiskTypeRPToCS(diskType api.OsDiskType) (string, error) {
	switch diskType {
	case api.OsDiskTypeManaged:
		return csOsDiskPersistencePersistent, nil
	case api.OsDiskTypeEphemeral:
		return csOsDiskPersistenceEphemeral, nil
	default:
		// Do not add a "" case here. Storage defaults and constructor defaults
		// guarantee DiskType is never empty when this function is called.
		return "", conversionError[string](diskType)
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
	case api.ClusterImageRegistryStateDisabled:
		return csImageRegistryStateDisabled, nil
	case api.ClusterImageRegistryStateEnabled:
		return csImageRegistryStateEnabled, nil
	default:
		return "", conversionError[string](in)
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

		if in.DataEncryption.CustomerManaged.Kms != nil {
			azureKmsKeyBuilder := arohcpv1alpha1.NewAzureKmsKey().
				KeyName(in.DataEncryption.CustomerManaged.Kms.ActiveKey.Name).
				KeyVaultName(in.DataEncryption.CustomerManaged.Kms.ActiveKey.VaultName).
				KeyVersion(in.DataEncryption.CustomerManaged.Kms.ActiveKey.Version)
			azureKmsEncryptionBuilder := arohcpv1alpha1.NewAzureKmsEncryption().ActiveKey(azureKmsKeyBuilder)

			// Add KeyVault visibility if specified
			if len(in.DataEncryption.CustomerManaged.Kms.Visibility) != 0 {
				visibility, err := convertKeyVaultVisibilityRPToCS(in.DataEncryption.CustomerManaged.Kms.Visibility)
				if err != nil {
					return nil, err
				}
				azureKmsEncryptionBuilder.Visibility(visibility)
			}

			azureEtcdDataEncryptionCustomerManagedBuilder.Kms(azureKmsEncryptionBuilder)
		}
		azureEtcdDataEncryptionBuilder.CustomerManaged(azureEtcdDataEncryptionCustomerManagedBuilder)
	}
	return arohcpv1alpha1.NewAzureEtcdEncryption().DataEncryption(azureEtcdDataEncryptionBuilder), nil
}

func convertCIDRBlockAllowAccessRPToCS(in api.CustomerAPIProfile) (*arohcpv1alpha1.CIDRBlockAccessBuilder, error) {
	cidrBlockAllowAccess := arohcpv1alpha1.NewCIDRBlockAllowAccess()

	if in.AuthorizedCIDRs == nil {
		cidrBlockAllowAccess.Mode(csCIDRBlockAllowAccessModeAllowAll)
	} else if len(in.AuthorizedCIDRs) > 0 {
		cidrBlockAllowAccess.Mode(csCIDRBlockAllowAccessModeAllowList)
		cidrBlockAllowAccess.Values(in.AuthorizedCIDRs...)
	} else {
		// Unreachable: empty AuthorizedCIDRs list is disallowed by validation
		return nil, fmt.Errorf("AuthorizedCIDRs cannot be an empty list")
	}

	return arohcpv1alpha1.NewCIDRBlockAccess().Allow(cidrBlockAllowAccess), nil
}

// GetClusterServiceUserAssignedIdentities extracts user-assigned identities from a CS Cluster object, keyed by resource ID.
func GetClusterServiceUserAssignedIdentities(clusterServiceCluster *arohcpv1alpha1.Cluster) map[string]*arm.UserAssignedIdentity {
	ret := make(map[string]*arm.UserAssignedIdentity)

	// the clientID and principalID are currently only known to cluster-service. We'll need to determine them somewhere else.
	if clusterServiceCluster.Azure().OperatorsAuthentication() != nil {
		if mi, ok := clusterServiceCluster.Azure().OperatorsAuthentication().GetManagedIdentities(); ok {
			for _, operatorIdentity := range mi.ControlPlaneOperatorsManagedIdentities() {
				clientID, _ := operatorIdentity.GetClientID()
				principalID, _ := operatorIdentity.GetPrincipalID()
				if len(clientID) > 0 && len(principalID) > 0 {
					ret[operatorIdentity.ResourceID()] = &arm.UserAssignedIdentity{
						ClientID:    &clientID,
						PrincipalID: &principalID,
					}
				} else {
					ret[operatorIdentity.ResourceID()] = &arm.UserAssignedIdentity{} // empty, but valid
				}
			}
			if len(mi.ServiceManagedIdentity().ResourceID()) > 0 {
				clientID, _ := mi.ServiceManagedIdentity().GetClientID()
				principalID, _ := mi.ServiceManagedIdentity().GetPrincipalID()
				if len(clientID) > 0 && len(principalID) > 0 {
					ret[mi.ServiceManagedIdentity().ResourceID()] = &arm.UserAssignedIdentity{
						ClientID:    &clientID,
						PrincipalID: &principalID,
					}
				} else {
					ret[mi.ServiceManagedIdentity().ResourceID()] = &arm.UserAssignedIdentity{} // empty, but valid
				}
			}
		}
	}

	return ret
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

func convertImageDigestMirrorsToCSBuilder(in []api.ImageDigestMirror) []*arohcpv1alpha1.ImageMirrorBuilder {
	if in == nil {
		return nil
	}

	builders := make([]*arohcpv1alpha1.ImageMirrorBuilder, 0)

	for _, item := range in {
		builders = append(builders, arohcpv1alpha1.NewImageMirror().
			Source(item.Source).
			Mirrors(item.Mirrors...))
	}

	return builders
}

// BuildCSCluster creates a CS ClusterBuilder object from an HCPOpenShiftCluster object.
// requiredProperties are caller-specified properties (e.g. provision shard, noop flags).
// oldClusterServiceCluster, if non-nil, indicates an update and its existing properties
// are preserved as a base layer.
func BuildCSCluster(resourceID *azcorearm.ResourceID, tenantID string, hcpCluster *api.HCPOpenShiftCluster, requiredProperties map[string]string, oldClusterServiceCluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.ClusterBuilder, *arohcpv1alpha1.ClusterAutoscalerBuilder, error) {
	var err error

	clusterBuilder := arohcpv1alpha1.NewCluster()
	clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()

	// These attributes cannot be updated after cluster creation.
	if oldClusterServiceCluster == nil {
		// Add attributes that cannot be updated after cluster creation.
		clusterBuilder, err = withImmutableAttributes(clusterBuilder, hcpCluster,
			resourceID.SubscriptionID,
			resourceID.ResourceGroupName,
			tenantID,
			hcpCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
		)
		if err != nil {
			return nil, nil, err
		}
		apiListening, err := convertVisibilityToListening(hcpCluster.CustomerProperties.API.Visibility)
		if err != nil {
			return nil, nil, err
		}
		clusterAPIBuilder.Listening(apiListening)
	}

	clusterBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
		Unit(csNodeDrainGracePeriodUnit).
		Value(float64(hcpCluster.CustomerProperties.NodeDrainTimeoutMinutes)))

	cidrBlockAccess, err := convertCIDRBlockAllowAccessRPToCS(hcpCluster.CustomerProperties.API)
	if err != nil {
		return nil, nil, err
	}
	clusterBuilder.API(clusterAPIBuilder.CIDRBlockAccess(cidrBlockAccess))

	clusterBuilder.RegistryConfig(arohcpv1alpha1.NewClusterRegistryConfig().
		ImageDigestMirrors(convertImageDigestMirrorsToCSBuilder(hcpCluster.CustomerProperties.ImageDigestMirrors)...))

	clusterAutoscalerBuilder, err := convertRpAutoscalarToCSBuilder(&hcpCluster.CustomerProperties.Autoscaling)
	if err != nil {
		return nil, nil, err
	}

	// Property layering: preserve existing CS properties (on update), then
	// overlay caller-specified properties, then experimental features.
	// Experimental feature properties are added when enabled and deleted
	// when disabled to ensure tag removal clears previously set values.
	properties := map[string]string{}
	if oldClusterServiceCluster != nil {
		for k, v := range oldClusterServiceCluster.Properties() {
			properties[k] = v
		}
	}
	for k, v := range requiredProperties {
		properties[k] = v
	}
	experimentalFeatures := hcpCluster.ServiceProviderProperties.ExperimentalFeatures
	if experimentalFeatures.ControlPlaneAvailability == api.SingleReplicaControlPlane {
		properties[CSPropertySingleReplica] = CSPropertyEnabled
	} else {
		delete(properties, CSPropertySingleReplica)
	}
	if experimentalFeatures.ControlPlanePodSizing == api.MinimalControlPlanePodSizing {
		properties[CSPropertySizeOverride] = CSPropertyEnabled
	} else {
		delete(properties, CSPropertySizeOverride)
	}
	clusterBuilder = clusterBuilder.Properties(properties)

	return clusterBuilder, clusterAutoscalerBuilder, nil
}

func withImmutableAttributes(clusterBuilder *arohcpv1alpha1.ClusterBuilder, hcpCluster *api.HCPOpenShiftCluster, subscriptionID, resourceGroupName, tenantID, identityURL string) (*arohcpv1alpha1.ClusterBuilder, error) {
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
		Region(arohcpv1alpha1.NewCloudRegion().
			ID(hcpCluster.Location)).
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
		ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
			State(clusterImageRegistryState))
	azureBuilder := arohcpv1alpha1.NewAzure().
		TenantID(tenantID).
		SubscriptionID(strings.ToLower(subscriptionID)).
		ResourceGroupName(strings.ToLower(resourceGroupName)).
		ResourceName(strings.ToLower(hcpCluster.Name)).
		ManagedResourceGroupName(hcpCluster.CustomerProperties.Platform.ManagedResourceGroup).
		SubnetResourceID(hcpCluster.CustomerProperties.Platform.SubnetID.String()).
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
	if hcpCluster.CustomerProperties.Platform.NetworkSecurityGroupID != nil {
		azureBuilder.NetworkSecurityGroupResourceID(hcpCluster.CustomerProperties.Platform.NetworkSecurityGroupID.String())
	}

	// Cluster Service rejects an empty VnetIntegrationSubnetResourceID string.
	if hcpCluster.CustomerProperties.Platform.VnetIntegrationSubnetID != nil {
		azureBuilder.VnetIntegrationSubnetResourceID(hcpCluster.CustomerProperties.Platform.VnetIntegrationSubnetID.String())
	}

	controlPlaneOperators := make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)
	for operatorName, identityResourceID := range hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		controlPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureControlPlaneManagedIdentity().ResourceID(identityResourceID.String())
	}

	dataPlaneOperators := make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)
	for operatorName, identityResourceID := range hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		dataPlaneOperators[operatorName] = arohcpv1alpha1.NewAzureDataPlaneManagedIdentity().ResourceID(identityResourceID.String())
	}

	managedIdentitiesBuilder := arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
		ManagedIdentitiesDataPlaneIdentityUrl(identityURL).
		ControlPlaneOperatorsManagedIdentities(controlPlaneOperators).
		DataPlaneOperatorsManagedIdentities(dataPlaneOperators)

	if hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity != nil {
		managedIdentitiesBuilder.ServiceManagedIdentity(arohcpv1alpha1.NewAzureServiceManagedIdentity().
			ResourceID(hcpCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity.String()))
	}

	azureBuilder.OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().ManagedIdentities(managedIdentitiesBuilder))

	clusterBuilder.Azure(azureBuilder)

	// Cluster Service rejects an empty DomainPrefix string.
	if hcpCluster.CustomerProperties.DNS.BaseDomainPrefix != "" {
		clusterBuilder.DomainPrefix(hcpCluster.CustomerProperties.DNS.BaseDomainPrefix)
	}

	return clusterBuilder, nil
}

// BuildCSNodePool creates a CS NodePoolBuilder object from an HCPOpenShiftClusterNodePool object.
func BuildCSNodePool(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool, updating bool) (*arohcpv1alpha1.NodePoolBuilder, error) {
	nodePoolBuilder := arohcpv1alpha1.NewNodePool()

	// These attributes cannot be updated after node pool creation.
	if !updating {
		subnetResourceIDString := ""
		if nodePool.Properties.Platform.SubnetID != nil {
			subnetResourceIDString = nodePool.Properties.Platform.SubnetID.String()
		}
		csDiskStorageAccountType, err := convertDiskStorageAccountTypeRPToCS(nodePool.Properties.Platform.OSDisk.DiskStorageAccountType)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		csPersistence, err := convertDiskTypeRPToCS(nodePool.Properties.Platform.OSDisk.DiskType)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		nodePoolBuilder.
			ID(strings.ToLower(nodePool.Name)).
			Version(arohcpv1alpha1.NewVersion().
				ID(NewOpenShiftVersionXYZ(nodePool.Properties.Version.ID, nodePool.Properties.Version.ChannelGroup)).
				ChannelGroup(nodePool.Properties.Version.ChannelGroup)).
			Subnet(subnetResourceIDString).
			AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
				ResourceName(strings.ToLower(nodePool.Name)).
				VMSize(nodePool.Properties.Platform.VMSize).
				EncryptionAtHost(convertEnableEncryptionAtHostToCSBuilder(nodePool.Properties.Platform)).
				OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().
					SizeGibibytes(int(*nodePool.Properties.Platform.OSDisk.SizeGiB)).
					StorageAccountType(csDiskStorageAccountType).
					Persistence(csPersistence))).
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
		case http.StatusServiceUnavailable:
			// ServiceUnavailable can be returned immediately on a cluster
			// creation request if cluster capacity would breach the value
			// of the --provision-shard-cluster-limit option.
			//
			// ocm-sdk-go already retries on this response status (see its
			// retry/transport_wrapper.go) so there is no point in the RP
			// retrying as well. Instead we add a Retry-After header to the
			// response.
			return arm.NewCloudError(
				statusCode,
				arm.CloudErrorCodeServiceUnavailable,
				"", "%s", ocmError.Reason())
		}
	}

	return arm.NewInternalServerError()
}

// ConvertCSManagementClusterToInternal converts a Cluster Service ProvisionShard
// to the internal ManagementCluster representation.
func ConvertCSManagementClusterToInternal(csShard *arohcpv1alpha1.ProvisionShard) (*fleet.ManagementCluster, error) {
	if csShard == nil {
		return nil, fmt.Errorf("provision shard is nil")
	}

	shardHREF := csShard.HREF()
	if len(shardHREF) == 0 {
		return nil, fmt.Errorf("provision shard has empty HREF")
	}
	shardID, err := api.NewInternalID(shardHREF)
	if err != nil {
		return nil, fmt.Errorf("provision shard has invalid HREF %q: %w", shardHREF, err)
	}

	azureShard := csShard.AzureShard()
	if azureShard == nil {
		return nil, fmt.Errorf("provision shard %q has no azure shard", shardID)
	}

	managementClusterAKSResourceID, err := azcorearm.ParseResourceID(azureShard.AksManagementClusterResourceId())
	if err != nil {
		return nil, fmt.Errorf("failed to parse management cluster AKS resource ID %q: %w", azureShard.AksManagementClusterResourceId(), err)
	}

	publicDNSZoneResourceID, err := azcorearm.ParseResourceID(azureShard.PublicDnsZoneResourceId())
	if err != nil {
		return nil, fmt.Errorf("failed to parse public DNS zone resource ID %q: %w", azureShard.PublicDnsZoneResourceId(), err)
	}

	maestroConfig := csShard.MaestroConfig()
	if maestroConfig == nil {
		return nil, fmt.Errorf("management cluster %q has no maestro config", shardID)
	}
	restConfig := maestroConfig.RestApiConfig()
	if restConfig == nil {
		return nil, fmt.Errorf("management cluster %q has no maestro REST API config", shardID)
	}
	grpcConfig := maestroConfig.GrpcApiConfig()
	if grpcConfig == nil {
		return nil, fmt.Errorf("management cluster %q has no maestro GRPC API config", shardID)
	}

	hostedClustersSecretsKeyVaultURL := azureShard.CxSecretsKeyVaultUrl()
	hostedClustersManagedIdentitiesKeyVaultURL := azureShard.CxManagedIdentitiesKeyVaultUrl()
	hostedClustersSecretsKeyVaultManagedIdentityClientID := azureShard.CxSecretsKeyVaultManagedIdentityClientId()

	readyCondition := metav1.Condition{
		Type:               string(fleet.ManagementClusterConditionReady),
		LastTransitionTime: metav1.Now(),
	}
	switch csShard.Status() {
	case csProvisioningShardStatusActive:
		readyCondition.Status = metav1.ConditionTrue
		readyCondition.Reason = string(fleet.ManagementClusterConditionReasonProvisionShardActive)
	case csProvisioningShardStatusMaintenance:
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = string(fleet.ManagementClusterConditionReasonProvisionShardMaintenance)
		readyCondition.Message = fmt.Sprintf("provision shard status is %q", csShard.Status())
	case csProvisioningShardStatusOffline:
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = string(fleet.ManagementClusterConditionReasonProvisionShardOffline)
		readyCondition.Message = fmt.Sprintf("provision shard status is %q", csShard.Status())
	default:
		readyCondition.Status = metav1.ConditionUnknown
		readyCondition.Reason = string(fleet.ManagementClusterConditionReasonProvisionShardStatusUnknown)
		readyCondition.Message = fmt.Sprintf("provision shard has unrecognized status %q", csShard.Status())
	}

	// The stamp identifier is derived from the AKS cluster name, which must
	// follow the {env}-{region}-mgmt-{stamp} convention (e.g. "prod-westus3-mgmt-1"
	// yields stamp identifier "1"). This pattern is enforced by our rollout pipelines.
	// Once the mgmt cluster enhancement enters phase 2, we can remove this logic
	// and use the original stamp identifier to fill mgmt clusters instead of deriving
	// it from the AKS cluster name.
	aksName := managementClusterAKSResourceID.Name
	lastDash := strings.LastIndex(aksName, "-")
	if lastDash < 0 || lastDash == len(aksName)-1 {
		return nil, fmt.Errorf("AKS cluster name %q does not contain a stamp suffix after the last '-'", aksName)
	}
	stampIdentifier := aksName[lastDash+1:]

	resourceID, err := fleet.ToManagementClusterResourceID(stampIdentifier)
	if err != nil {
		return nil, fmt.Errorf("failed to construct management cluster resource ID from stamp identifier %q: %w", stampIdentifier, err)
	}

	mc := &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: convertShardStatusToSchedulingPolicy(csShard.Status()),
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        managementClusterAKSResourceID,
			PublicDNSZoneResourceID:                              publicDNSZoneResourceID,
			HostedClustersSecretsKeyVaultURL:                     hostedClustersSecretsKeyVaultURL,
			HostedClustersManagedIdentitiesKeyVaultURL:           hostedClustersManagedIdentitiesKeyVaultURL,
			HostedClustersSecretsKeyVaultManagedIdentityClientID: hostedClustersSecretsKeyVaultManagedIdentityClientID,
			MaestroConsumerName:                                  maestroConfig.ConsumerName(),
			MaestroRESTAPIURL:                                    restConfig.Url(),
			MaestroGRPCTarget:                                    grpcConfig.Url(),
			ClusterServiceProvisionShardID:                       &shardID,
			Conditions:                                           []metav1.Condition{readyCondition},
		},
	}

	return mc, nil
}

// convertShardStatusToSchedulingPolicy maps a Cluster Service provision shard
// status to a ManagementClusterSchedulingPolicy.
func convertShardStatusToSchedulingPolicy(status string) fleet.ManagementClusterSchedulingPolicy {
	if status == csProvisioningShardStatusActive {
		return fleet.ManagementClusterSchedulingPolicySchedulable
	}
	return fleet.ManagementClusterSchedulingPolicyUnschedulable
}

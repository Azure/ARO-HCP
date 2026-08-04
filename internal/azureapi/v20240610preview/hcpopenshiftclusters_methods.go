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

package v20240610preview

import (
	"strings"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview/generated"
)

type HcpOpenShiftCluster struct {
	generated.HcpOpenShiftCluster
}

var _ coreapi.VersionedCreatableResource[coreapi.HCPOpenShiftCluster] = &HcpOpenShiftCluster{}

func (h *HcpOpenShiftCluster) NewExternal() any {
	return &HcpOpenShiftCluster{}
}

func SetDefaultValuesCluster(obj *HcpOpenShiftCluster) {
	if obj.Properties == nil {
		obj.Properties = &generated.HcpOpenShiftClusterProperties{}
	}
	if obj.Properties.Version == nil {
		obj.Properties.Version = &generated.VersionProfile{}
	}
	if obj.Properties.Version.ChannelGroup == nil {
		obj.Properties.Version.ChannelGroup = ptr.To(coreapi.DefaultClusterVersionChannelGroup)
	}
	if obj.Properties.Network == nil {
		obj.Properties.Network = &generated.NetworkProfile{}
	}
	if obj.Properties.Network.NetworkType == nil {
		obj.Properties.Network.NetworkType = ptr.To(generated.NetworkTypeOVNKubernetes)
	}
	if obj.Properties.Network.PodCIDR == nil {
		obj.Properties.Network.PodCIDR = ptr.To(coreapi.DefaultClusterNetworkPodCIDR)
	}
	if obj.Properties.Network.ServiceCIDR == nil {
		obj.Properties.Network.ServiceCIDR = ptr.To(coreapi.DefaultClusterNetworkServiceCIDR)
	}
	if obj.Properties.Network.MachineCIDR == nil {
		obj.Properties.Network.MachineCIDR = ptr.To(coreapi.DefaultClusterNetworkMachineCIDR)
	}
	if obj.Properties.Network.HostPrefix == nil {
		obj.Properties.Network.HostPrefix = ptr.To(coreapi.DefaultClusterNetworkHostPrefix)
	}
	if obj.Properties.API == nil {
		obj.Properties.API = &generated.APIProfile{}
	}
	if obj.Properties.API.Visibility == nil {
		obj.Properties.API.Visibility = ptr.To(generated.VisibilityPublic)
	}
	if obj.Properties.Platform == nil {
		obj.Properties.Platform = &generated.PlatformProfile{}
	}
	if obj.Properties.Platform.OutboundType == nil {
		obj.Properties.Platform.OutboundType = ptr.To(generated.OutboundTypeLoadBalancer)
	}
	if obj.Properties.Platform.ManagedResourceGroup == nil || len(*obj.Properties.Platform.ManagedResourceGroup) == 0 {
		clusterName := ptr.Deref(obj.Name, "")
		if len(clusterName) >= 45 {
			clusterName = clusterName[:45]
		}
		obj.Properties.Platform.ManagedResourceGroup = ptr.To("arohcp-" + clusterName + "-" + uuid.New().String())
	}
	if obj.Properties.Autoscaling == nil {
		obj.Properties.Autoscaling = &generated.ClusterAutoscalingProfile{}
	}
	if obj.Properties.Autoscaling.MaxPodGracePeriodSeconds == nil {
		obj.Properties.Autoscaling.MaxPodGracePeriodSeconds = ptr.To(coreapi.DefaultClusterMaxPodGracePeriodSeconds)
	}
	if obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds == nil {
		obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds = ptr.To(coreapi.DefaultClusterMaxNodeProvisionTimeSeconds)
	}
	if obj.Properties.Autoscaling.PodPriorityThreshold == nil {
		obj.Properties.Autoscaling.PodPriorityThreshold = ptr.To(coreapi.DefaultClusterPodPriorityThreshold)
	}
	//Even though PlatformManaged Mode is currently not supported by CS . This is the default value .
	// TODO cannot change the default value for this version, but why keep it in our new version?
	if obj.Properties.Etcd == nil {
		obj.Properties.Etcd = &generated.EtcdProfile{}
	}
	if obj.Properties.Etcd.DataEncryption == nil {
		obj.Properties.Etcd.DataEncryption = &generated.EtcdDataEncryptionProfile{}
	}
	if obj.Properties.Etcd.DataEncryption.KeyManagementMode == nil {
		obj.Properties.Etcd.DataEncryption.KeyManagementMode = ptr.To(generated.EtcdDataEncryptionKeyManagementModeTypePlatformManaged)
	}
	if obj.Properties.ClusterImageRegistry == nil {
		obj.Properties.ClusterImageRegistry = &generated.ClusterImageRegistryProfile{}
	}
	if obj.Properties.ClusterImageRegistry.State == nil {
		obj.Properties.ClusterImageRegistry.State = ptr.To(generated.ClusterImageRegistryStateEnabled)
	}
}

func newVersionProfile(from *coreapi.VersionProfile) generated.VersionProfile {
	if from == nil {
		return generated.VersionProfile{}
	}
	return generated.VersionProfile{
		ID:           metadataapi.PtrOrNil(from.ID),
		ChannelGroup: metadataapi.PtrOrNil(from.ChannelGroup),
	}
}

func newDNSProfile(from *coreapi.CustomerDNSProfile, from2 *coreapi.ServiceProviderDNSProfile) generated.DNSProfile {
	if from == nil {
		return generated.DNSProfile{}
	}
	return generated.DNSProfile{
		BaseDomain:       metadataapi.PtrOrNil(from2.BaseDomain),
		BaseDomainPrefix: metadataapi.PtrOrNil(from.BaseDomainPrefix),
	}
}

func newNetworkProfile(from *coreapi.NetworkProfile) generated.NetworkProfile {
	if from == nil {
		return generated.NetworkProfile{}
	}
	return generated.NetworkProfile{
		NetworkType: metadataapi.PtrOrNil(generated.NetworkType(from.NetworkType)),
		PodCIDR:     metadataapi.PtrOrNil(from.PodCIDR),
		ServiceCIDR: metadataapi.PtrOrNil(from.ServiceCIDR),
		MachineCIDR: metadataapi.PtrOrNil(from.MachineCIDR),
		HostPrefix:  metadataapi.PtrOrNil(from.HostPrefix),
	}
}

func newConsoleProfile(from *coreapi.ServiceProviderConsoleProfile) generated.ConsoleProfile {
	if from == nil {
		return generated.ConsoleProfile{}
	}
	return generated.ConsoleProfile{
		URL: metadataapi.PtrOrNil(from.URL),
	}
}

func newAPIProfile(from *coreapi.CustomerAPIProfile, from2 *coreapi.ServiceProviderAPIProfile) generated.APIProfile {
	if from == nil {
		return generated.APIProfile{}
	}
	return generated.APIProfile{
		URL:             metadataapi.PtrOrNil(from2.URL),
		Visibility:      metadataapi.PtrOrNil(generated.Visibility(from.Visibility)),
		AuthorizedCIDRs: metadataapi.StringSliceToStringPtrSlice(from.AuthorizedCIDRs),
	}
}

func newPlatformProfile(from *coreapi.CustomerPlatformProfile, from2 *coreapi.ServiceProviderPlatformProfile) generated.PlatformProfile {
	if from == nil {
		return generated.PlatformProfile{}
	}
	return generated.PlatformProfile{
		ManagedResourceGroup:    metadataapi.PtrOrNil(from.ManagedResourceGroup),
		SubnetID:                metadataapi.ResourceIDToStringPtr(from.SubnetID),
		OutboundType:            metadataapi.PtrOrNil(generated.OutboundType(from.OutboundType)),
		NetworkSecurityGroupID:  metadataapi.ResourceIDToStringPtr(from.NetworkSecurityGroupID),
		OperatorsAuthentication: metadataapi.PtrOrNil(newOperatorsAuthenticationProfile(&from.OperatorsAuthentication)),
		IssuerURL:               metadataapi.PtrOrNil(from2.IssuerURL),
	}
}

func newClusterAutoscalingProfile(from *coreapi.ClusterAutoscalingProfile) generated.ClusterAutoscalingProfile {
	if from == nil {
		return generated.ClusterAutoscalingProfile{}
	}
	return generated.ClusterAutoscalingProfile{
		MaxNodeProvisionTimeSeconds: metadataapi.PtrOrNil(from.MaxNodeProvisionTimeSeconds),
		MaxNodesTotal:               metadataapi.PtrOrNil(from.MaxNodesTotal),
		MaxPodGracePeriodSeconds:    metadataapi.PtrOrNil(from.MaxPodGracePeriodSeconds),
		PodPriorityThreshold:        metadataapi.PtrOrNil(from.PodPriorityThreshold),
	}
}

func newEtcdProfile(from *coreapi.EtcdProfile) generated.EtcdProfile {
	if from == nil {
		return generated.EtcdProfile{}
	}
	return generated.EtcdProfile{
		DataEncryption: metadataapi.PtrOrNil(newEtcdDataEncryptionProfile(&from.DataEncryption)),
	}
}
func newEtcdDataEncryptionProfile(from *coreapi.EtcdDataEncryptionProfile) generated.EtcdDataEncryptionProfile {
	if from == nil {
		return generated.EtcdDataEncryptionProfile{}
	}
	return generated.EtcdDataEncryptionProfile{
		CustomerManaged:   newCustomerManagedEncryptionProfile(from.CustomerManaged),
		KeyManagementMode: metadataapi.PtrOrNil(generated.EtcdDataEncryptionKeyManagementModeType(from.KeyManagementMode)),
	}
}
func newCustomerManagedEncryptionProfile(from *coreapi.CustomerManagedEncryptionProfile) *generated.CustomerManagedEncryptionProfile {
	if from == nil {
		return nil
	}
	return &generated.CustomerManagedEncryptionProfile{
		Kms:            metadataapi.PtrOrNil(newKmsEncryptionProfile(from.Kms)),
		EncryptionType: metadataapi.PtrOrNil(generated.CustomerManagedEncryptionType(from.EncryptionType)),
	}
}
func newKmsEncryptionProfile(from *coreapi.KmsEncryptionProfile) generated.KmsEncryptionProfile {
	if from == nil {
		return generated.KmsEncryptionProfile{}
	}
	return generated.KmsEncryptionProfile{
		ActiveKey: metadataapi.PtrOrNil(newKmsKey(&from.ActiveKey)),
	}
}
func newKmsKey(from *coreapi.KmsKey) generated.KmsKey {
	if from == nil {
		return generated.KmsKey{}
	}
	return generated.KmsKey{
		Name:      metadataapi.PtrOrNil(from.Name),
		VaultName: metadataapi.PtrOrNil(from.VaultName),
		Version:   metadataapi.PtrOrNil(from.Version),
	}
}

func newClusterImageRegistryProfile(from *coreapi.ClusterImageRegistryProfile) generated.ClusterImageRegistryProfile {
	if from == nil {
		return generated.ClusterImageRegistryProfile{}
	}
	return generated.ClusterImageRegistryProfile{
		State: metadataapi.PtrOrNil(generated.ClusterImageRegistryState(from.State)),
	}
}

func newOperatorsAuthenticationProfile(from *coreapi.OperatorsAuthenticationProfile) generated.OperatorsAuthenticationProfile {
	if from == nil {
		return generated.OperatorsAuthenticationProfile{}
	}
	return generated.OperatorsAuthenticationProfile{
		UserAssignedIdentities: metadataapi.PtrOrNil(newUserAssignedIdentitiesProfile(&from.UserAssignedIdentities)),
	}
}

func newUserAssignedIdentitiesProfile(from *coreapi.UserAssignedIdentitiesProfile) generated.UserAssignedIdentitiesProfile {
	if from == nil {
		return generated.UserAssignedIdentitiesProfile{}
	}
	return generated.UserAssignedIdentitiesProfile{
		ControlPlaneOperators:  metadataapi.ResourceIDMapToStringPtrMap(from.ControlPlaneOperators),
		DataPlaneOperators:     metadataapi.ResourceIDMapToStringPtrMap(from.DataPlaneOperators),
		ServiceManagedIdentity: metadataapi.ResourceIDToStringPtr(from.ServiceManagedIdentity),
	}
}

func newSystemData(from *coreapi.SystemData) generated.SystemData {
	if from == nil {
		return generated.SystemData{}
	}
	return generated.SystemData{
		CreatedBy:          metadataapi.PtrOrNil(from.CreatedBy),
		CreatedByType:      metadataapi.PtrOrNil(generated.CreatedByType(from.CreatedByType)),
		CreatedAt:          from.CreatedAt,
		LastModifiedBy:     metadataapi.PtrOrNil(from.LastModifiedBy),
		LastModifiedByType: metadataapi.PtrOrNil(generated.CreatedByType(from.LastModifiedByType)),
		LastModifiedAt:     from.LastModifiedAt,
	}
}

func newManagedServiceIdentity(from *coreapi.ManagedServiceIdentity) *generated.ManagedServiceIdentity {
	if from == nil {
		return nil
	}
	return &generated.ManagedServiceIdentity{
		Type:                   metadataapi.PtrOrNil(generated.ManagedServiceIdentityType(from.Type)),
		PrincipalID:            metadataapi.PtrOrNil(from.PrincipalID),
		TenantID:               metadataapi.PtrOrNil(from.TenantID),
		UserAssignedIdentities: convertUserAssignedIdentities(from.UserAssignedIdentities),
	}
}

// NewHCPOpenShiftCluster converts an internal representation to this API version.
// If from is nil, returns a defaulted external object for use on the write path
// where defaults are applied before unmarshaling the request body.
func (v version) NewHCPOpenShiftCluster(from *coreapi.HCPOpenShiftCluster) coreapi.VersionedHCPOpenShiftCluster {
	if from == nil {
		ret := &HcpOpenShiftCluster{}
		SetDefaultValuesCluster(ret)
		return ret
	}

	idString := ""
	if from.ResourceID != nil {
		idString = from.ResourceID.String()
	}

	out := &HcpOpenShiftCluster{
		generated.HcpOpenShiftCluster{
			ID:         metadataapi.PtrOrNil(idString),
			Name:       metadataapi.PtrOrNil(from.Name),
			Type:       metadataapi.PtrOrNil(from.Type),
			SystemData: metadataapi.PtrOrNil(newSystemData(from.SystemData)),
			Location:   metadataapi.PtrOrNil(from.Location),
			Tags:       metadataapi.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.HcpOpenShiftClusterProperties{
				ProvisioningState:       metadataapi.PtrOrNil(generated.ProvisioningState(from.ServiceProviderProperties.ProvisioningState)),
				Version:                 metadataapi.PtrOrNil(newVersionProfile(&from.CustomerProperties.Version)),
				DNS:                     metadataapi.PtrOrNil(newDNSProfile(&from.CustomerProperties.DNS, &from.ServiceProviderProperties.DNS)),
				Network:                 metadataapi.PtrOrNil(newNetworkProfile(&from.CustomerProperties.Network)),
				Console:                 metadataapi.PtrOrNil(newConsoleProfile(&from.ServiceProviderProperties.Console)),
				API:                     metadataapi.PtrOrNil(newAPIProfile(&from.CustomerProperties.API, &from.ServiceProviderProperties.API)),
				Platform:                metadataapi.PtrOrNil(newPlatformProfile(&from.CustomerProperties.Platform, &from.ServiceProviderProperties.Platform)),
				Autoscaling:             metadataapi.PtrOrNil(newClusterAutoscalingProfile(&from.CustomerProperties.Autoscaling)),
				NodeDrainTimeoutMinutes: metadataapi.PtrOrNil(from.CustomerProperties.NodeDrainTimeoutMinutes),
				ClusterImageRegistry:    metadataapi.PtrOrNil(newClusterImageRegistryProfile(&from.CustomerProperties.ClusterImageRegistry)),
				Etcd:                    metadataapi.PtrOrNil(newEtcdProfile(&from.CustomerProperties.Etcd)),
			},
			Identity: newManagedServiceIdentity(from.Identity),
		},
	}

	return out
}

func (c *HcpOpenShiftCluster) GetVersion() coreapi.Version {
	return versionedInterface
}

func (c *HcpOpenShiftCluster) ConvertToInternal(existing *coreapi.HCPOpenShiftCluster) (*coreapi.HCPOpenShiftCluster, error) {
	out := &coreapi.HCPOpenShiftCluster{}
	errs := field.ErrorList{}

	if c.ID != nil {
		out.ID = metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(*c.ID)))
		out.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(*c.ID)))
	}
	if c.Name != nil {
		out.Name = *c.Name
	}
	if c.Type != nil {
		out.Type = *c.Type
	}
	if c.SystemData != nil {
		out.SystemData = &coreapi.SystemData{
			CreatedAt:      c.SystemData.CreatedAt,
			LastModifiedAt: c.SystemData.LastModifiedAt,
		}
		if c.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *c.SystemData.CreatedBy
		}
		if c.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = coreapi.CreatedByType(*c.SystemData.CreatedByType)
		}
		if c.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *c.SystemData.LastModifiedBy
		}
		if c.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = coreapi.CreatedByType(*c.SystemData.LastModifiedByType)
		}
	}
	if c.Location != nil {
		out.Location = *c.Location
	}
	out.Identity = normalizeManagedIdentity(c.Identity)
	// Per RPC-Patch-V1-04, the Tags field does NOT follow
	// JSON merge-patch (RFC 7396) semantics:
	//
	//   When Tags are patched, the tags from the request
	//   replace all existing tags for the resource
	//
	out.Tags = metadataapi.StringPtrMapToStringMap(c.Tags)
	if c.Properties != nil {
		if c.Properties.ProvisioningState != nil {
			out.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningState(*c.Properties.ProvisioningState)
		}
		if c.Properties.Version != nil {
			normalizeVersion(c.Properties.Version, &out.CustomerProperties.Version)
		}
		if c.Properties.DNS != nil {
			normalizeDNS(c.Properties.DNS, &out.CustomerProperties.DNS, &out.ServiceProviderProperties.DNS)
		}
		if c.Properties.Network != nil {
			normalizeNetwork(c.Properties.Network, &out.CustomerProperties.Network)
		}
		if c.Properties.Console != nil {
			normalizeConsole(c.Properties.Console, &out.ServiceProviderProperties.Console)
		}
		if c.Properties.API != nil {
			normalizeAPI(c.Properties.API, &out.CustomerProperties.API, &out.ServiceProviderProperties.API)
		}
		if c.Properties.Platform != nil {
			errs = append(errs, normalizePlatform(field.NewPath("properties", "platform"), c.Properties.Platform, &out.CustomerProperties.Platform, &out.ServiceProviderProperties.Platform)...)
		}
		if c.Properties.Autoscaling != nil {
			normalizeAutoscaling(c.Properties.Autoscaling, &out.CustomerProperties.Autoscaling)
		}
		if c.Properties.NodeDrainTimeoutMinutes != nil {
			out.CustomerProperties.NodeDrainTimeoutMinutes = *c.Properties.NodeDrainTimeoutMinutes
		}
		if c.Properties.ClusterImageRegistry != nil {
			normalizeClusterImageRegistry(c.Properties.ClusterImageRegistry, &out.CustomerProperties.ClusterImageRegistry)
		}
		if c.Properties.Etcd != nil {
			normalizeEtcd(c.Properties.Etcd, &out.CustomerProperties.Etcd)
		}
	}

	if existing != nil {
		preserveUnknownClusterFields(existing, out)
	}

	return out, coreapi.CloudErrorFromFieldErrors(errs)
}

// preserveUnknownClusterFields copies customer-facing fields from existing that
// this API version doesn't know about.
func preserveUnknownClusterFields(from, to *coreapi.HCPOpenShiftCluster) {
	for _, idmFrom := range from.CustomerProperties.ImageDigestMirrors {
		to.CustomerProperties.ImageDigestMirrors = append(
			to.CustomerProperties.ImageDigestMirrors, *idmFrom.DeepCopy())
	}
	// VnetIntegrationSubnetID was added in v2025_12_23_preview.
	to.CustomerProperties.Platform.VnetIntegrationSubnetID = from.CustomerProperties.Platform.VnetIntegrationSubnetID
	// Ingress was added in v2026_06_30_preview.
	to.CustomerProperties.Ingress = from.CustomerProperties.Ingress
	// Visibility was added in v2025_12_23_preview.
	if from.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil && from.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
		if to.CustomerProperties.Etcd.DataEncryption.CustomerManaged == nil {
			to.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{}
		}
		if to.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms == nil {
			to.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms = &coreapi.KmsEncryptionProfile{}
		}
		to.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = from.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility
	}
	// CryptoRestrictions was added in v2026_06_30_preview
	to.CustomerProperties.CryptoRestrictions = from.CustomerProperties.CryptoRestrictions
	// KeyEncryptionKeyURL was added in v2026_09_01_preview.
	if from.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil && from.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil &&
		to.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil && to.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
		to.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.KeyEncryptionKeyURL = from.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.KeyEncryptionKeyURL
	}
}

func normalizeManagedIdentity(identity *generated.ManagedServiceIdentity) *coreapi.ManagedServiceIdentity {
	if identity == nil {
		return nil
	}

	ret := &coreapi.ManagedServiceIdentity{}
	if identity.PrincipalID != nil {
		ret.PrincipalID = *identity.PrincipalID
	}
	if identity.TenantID != nil {
		ret.TenantID = *identity.TenantID
	}
	if identity.Type != nil {
		ret.Type = (coreapi.ManagedServiceIdentityType)(*identity.Type)
	}
	if identity.UserAssignedIdentities != nil {
		normalizeIdentityUserAssignedIdentities(identity.UserAssignedIdentities, &ret.UserAssignedIdentities)
	}

	return ret
}

func normalizeVersion(p *generated.VersionProfile, out *coreapi.VersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
}

func normalizeDNS(p *generated.DNSProfile, out *coreapi.CustomerDNSProfile, out2 *coreapi.ServiceProviderDNSProfile) {
	if p.BaseDomain != nil {
		out2.BaseDomain = *p.BaseDomain
	}
	if p.BaseDomainPrefix != nil {
		out.BaseDomainPrefix = *p.BaseDomainPrefix
	}
}

func normalizeNetwork(p *generated.NetworkProfile, out *coreapi.NetworkProfile) {
	if p.NetworkType != nil {
		out.NetworkType = metadataapi.NetworkType(*p.NetworkType)
	}
	if p.PodCIDR != nil {
		out.PodCIDR = *p.PodCIDR
	}
	if p.ServiceCIDR != nil {
		out.ServiceCIDR = *p.ServiceCIDR
	}
	if p.MachineCIDR != nil {
		out.MachineCIDR = *p.MachineCIDR
	}
	if p.HostPrefix != nil {
		out.HostPrefix = *p.HostPrefix
	}
}

func normalizeConsole(p *generated.ConsoleProfile, out *coreapi.ServiceProviderConsoleProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
}

func normalizeAPI(p *generated.APIProfile, out *coreapi.CustomerAPIProfile, out2 *coreapi.ServiceProviderAPIProfile) {
	if p.URL != nil {
		out2.URL = *p.URL
	}
	if p.Visibility != nil {
		out.Visibility = metadataapi.Visibility(*p.Visibility)
	}
	out.AuthorizedCIDRs = metadataapi.StringPtrSliceToStringSlice(p.AuthorizedCIDRs)
}

func normalizePlatform(fldPath *field.Path, p *generated.PlatformProfile, out *coreapi.CustomerPlatformProfile, out2 *coreapi.ServiceProviderPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	if p.ManagedResourceGroup != nil {
		out.ManagedResourceGroup = *p.ManagedResourceGroup
	}
	if p.SubnetID != nil && len(*p.SubnetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.SubnetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("subnetID"), *p.SubnetID, err.Error()))
		} else {
			out.SubnetID = resourceID
		}
	}
	if p.OutboundType != nil {
		out.OutboundType = metadataapi.OutboundType(*p.OutboundType)
	}
	if p.NetworkSecurityGroupID != nil && len(*p.NetworkSecurityGroupID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.NetworkSecurityGroupID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("networkSecurityGroupID"), *p.NetworkSecurityGroupID, err.Error()))
		} else {
			out.NetworkSecurityGroupID = resourceID
		}
	}
	if p.OperatorsAuthentication != nil {
		errs = append(errs, normalizeOperatorsAuthentication(fldPath.Child("operatorsAuthentication"), p.OperatorsAuthentication, &out.OperatorsAuthentication)...)
	}
	if p.IssuerURL != nil {
		out2.IssuerURL = *p.IssuerURL
	}

	return errs
}

func normalizeAutoscaling(p *generated.ClusterAutoscalingProfile, out *coreapi.ClusterAutoscalingProfile) {
	if p.MaxNodeProvisionTimeSeconds != nil {
		out.MaxNodeProvisionTimeSeconds = *p.MaxNodeProvisionTimeSeconds
	}
	if p.MaxNodesTotal != nil {
		out.MaxNodesTotal = *p.MaxNodesTotal
	}
	if p.MaxPodGracePeriodSeconds != nil {
		out.MaxPodGracePeriodSeconds = *p.MaxPodGracePeriodSeconds
	}
	if p.PodPriorityThreshold != nil {
		out.PodPriorityThreshold = *p.PodPriorityThreshold
	}
}

func normalizeEtcd(p *generated.EtcdProfile, out *coreapi.EtcdProfile) {
	if p.DataEncryption != nil {
		normalizeEtcdDataEncryptionProfile(p.DataEncryption, &out.DataEncryption)
	}
}

func normalizeEtcdDataEncryptionProfile(p *generated.EtcdDataEncryptionProfile, out *coreapi.EtcdDataEncryptionProfile) {
	if p.CustomerManaged != nil {
		if out.CustomerManaged == nil {
			out.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{}
		}
		normalizeCustomerManaged(p.CustomerManaged, out.CustomerManaged)
	}
	if p.KeyManagementMode != nil {
		out.KeyManagementMode = metadataapi.EtcdDataEncryptionKeyManagementModeType(*p.KeyManagementMode)
	}
}

func normalizeCustomerManaged(p *generated.CustomerManagedEncryptionProfile, out *coreapi.CustomerManagedEncryptionProfile) {
	if p.EncryptionType != nil {
		out.EncryptionType = metadataapi.CustomerManagedEncryptionType(*p.EncryptionType)
	}
	if p.Kms != nil && p.Kms.ActiveKey != nil {
		if out.Kms == nil {
			out.Kms = &coreapi.KmsEncryptionProfile{}
		}
		normalizeActiveKey(p.Kms.ActiveKey, &out.Kms.ActiveKey)
	}
}

func normalizeActiveKey(p *generated.KmsKey, out *coreapi.KmsKey) {
	if p.Name != nil {
		out.Name = *p.Name
	}
	if p.VaultName != nil {
		out.VaultName = *p.VaultName
	}
	if p.Version != nil {
		out.Version = *p.Version
	}
}

func normalizeClusterImageRegistry(p *generated.ClusterImageRegistryProfile, out *coreapi.ClusterImageRegistryProfile) {
	if p.State != nil {
		out.State = metadataapi.ClusterImageRegistryState(*p.State)
	}
}

func normalizeOperatorsAuthentication(fldPath *field.Path, p *generated.OperatorsAuthenticationProfile, out *coreapi.OperatorsAuthenticationProfile) field.ErrorList {
	if p.UserAssignedIdentities != nil {
		return normalizeUserAssignedIdentities(fldPath.Child("userAssignedIdentities"), p.UserAssignedIdentities, &out.UserAssignedIdentities)
	}
	return nil
}

func normalizeUserAssignedIdentities(fldPath *field.Path, p *generated.UserAssignedIdentitiesProfile, out *coreapi.UserAssignedIdentitiesProfile) field.ErrorList {
	errs := field.ErrorList{}

	switch {
	case p.ControlPlaneOperators != nil && out.ControlPlaneOperators == nil:
		out.ControlPlaneOperators = make(map[string]*azcorearm.ResourceID)
	case p.ControlPlaneOperators == nil && out.ControlPlaneOperators != nil:
		out.ControlPlaneOperators = nil
	}
	switch {
	case p.DataPlaneOperators != nil && out.DataPlaneOperators == nil:
		out.DataPlaneOperators = make(map[string]*azcorearm.ResourceID)
	case p.DataPlaneOperators == nil && out.DataPlaneOperators != nil:
		out.DataPlaneOperators = nil
	}

	errs = append(errs, metadataapi.MergeStringPtrMapIntoResourceIDMap(fldPath.Child("controlPlaneOperators"), p.ControlPlaneOperators, &out.ControlPlaneOperators)...)
	errs = append(errs, metadataapi.MergeStringPtrMapIntoResourceIDMap(fldPath.Child("dataPlaneOperators"), p.DataPlaneOperators, &out.DataPlaneOperators)...)
	if p.ServiceManagedIdentity != nil && len(*p.ServiceManagedIdentity) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.ServiceManagedIdentity); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("serviceManagedIdentity"), *p.ServiceManagedIdentity, err.Error()))
		} else {
			out.ServiceManagedIdentity = resourceID
		}
	}

	return errs
}

func normalizeIdentityUserAssignedIdentities(p map[string]*generated.UserAssignedIdentity, out *map[string]*coreapi.UserAssignedIdentity) {
	if *out == nil {
		*out = make(map[string]*coreapi.UserAssignedIdentity)
	}
	for key, value := range p {
		if value != nil {
			(*out)[key] = &coreapi.UserAssignedIdentity{
				ClientID:    value.ClientID,
				PrincipalID: value.PrincipalID,
			}
		} else {
			(*out)[key] = nil
		}
	}
}

func convertUserAssignedIdentities(from map[string]*coreapi.UserAssignedIdentity) map[string]*generated.UserAssignedIdentity {
	if from == nil {
		return nil
	}
	converted := make(map[string]*generated.UserAssignedIdentity)
	for key, value := range from {
		if value != nil {
			converted[key] = &generated.UserAssignedIdentity{
				ClientID:    value.ClientID,
				PrincipalID: value.PrincipalID,
			}
		} else {
			converted[key] = nil
		}
	}
	return converted
}

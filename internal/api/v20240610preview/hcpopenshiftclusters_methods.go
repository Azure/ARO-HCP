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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type HcpOpenShiftCluster struct {
	generated.HcpOpenShiftCluster
}

var _ api.VersionedCreatableResource[api.HCPOpenShiftCluster] = &HcpOpenShiftCluster{}

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
		obj.Properties.Version.ChannelGroup = ptr.To(api.DefaultClusterVersionChannelGroup)
	}
	if obj.Properties.Network == nil {
		obj.Properties.Network = &generated.NetworkProfile{}
	}
	if obj.Properties.Network.NetworkType == nil {
		obj.Properties.Network.NetworkType = ptr.To(generated.NetworkTypeOVNKubernetes)
	}
	if obj.Properties.Network.PodCIDR == nil {
		obj.Properties.Network.PodCIDR = ptr.To(api.DefaultClusterNetworkPodCIDR)
	}
	if obj.Properties.Network.ServiceCIDR == nil {
		obj.Properties.Network.ServiceCIDR = ptr.To(api.DefaultClusterNetworkServiceCIDR)
	}
	if obj.Properties.Network.MachineCIDR == nil {
		obj.Properties.Network.MachineCIDR = ptr.To(api.DefaultClusterNetworkMachineCIDR)
	}
	if obj.Properties.Network.HostPrefix == nil {
		obj.Properties.Network.HostPrefix = ptr.To(api.DefaultClusterNetworkHostPrefix)
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
		obj.Properties.Autoscaling.MaxPodGracePeriodSeconds = ptr.To(api.DefaultClusterMaxPodGracePeriodSeconds)
	}
	if obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds == nil {
		obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds = ptr.To(api.DefaultClusterMaxNodeProvisionTimeSeconds)
	}
	if obj.Properties.Autoscaling.PodPriorityThreshold == nil {
		obj.Properties.Autoscaling.PodPriorityThreshold = ptr.To(api.DefaultClusterPodPriorityThreshold)
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

func newVersionProfile(from *api.VersionProfile) generated.VersionProfile {
	if from == nil {
		return generated.VersionProfile{}
	}
	return generated.VersionProfile{
		ID:           api.PtrOrNil(from.ID),
		ChannelGroup: api.PtrOrNil(from.ChannelGroup),
	}
}

func newDNSProfile(from *api.CustomerDNSProfile, from2 *api.ServiceProviderDNSProfile) generated.DNSProfile {
	if from == nil {
		return generated.DNSProfile{}
	}
	return generated.DNSProfile{
		BaseDomain:       api.PtrOrNil(from2.BaseDomain),
		BaseDomainPrefix: api.PtrOrNil(from.BaseDomainPrefix),
	}
}

func newNetworkProfile(from *api.NetworkProfile) generated.NetworkProfile {
	if from == nil {
		return generated.NetworkProfile{}
	}
	return generated.NetworkProfile{
		NetworkType: api.PtrOrNil(generated.NetworkType(from.NetworkType)),
		PodCIDR:     api.PtrOrNil(from.PodCIDR),
		ServiceCIDR: api.PtrOrNil(from.ServiceCIDR),
		MachineCIDR: api.PtrOrNil(from.MachineCIDR),
		HostPrefix:  api.PtrOrNil(from.HostPrefix),
	}
}

func newConsoleProfile(from *api.ServiceProviderConsoleProfile) generated.ConsoleProfile {
	if from == nil {
		return generated.ConsoleProfile{}
	}
	return generated.ConsoleProfile{
		URL: api.PtrOrNil(from.URL),
	}
}

func newAPIProfile(from *api.CustomerAPIProfile, from2 *api.ServiceProviderAPIProfile) generated.APIProfile {
	if from == nil {
		return generated.APIProfile{}
	}
	return generated.APIProfile{
		URL:             api.PtrOrNil(from2.URL),
		Visibility:      api.PtrOrNil(generated.Visibility(from.Visibility)),
		AuthorizedCIDRs: api.StringSliceToStringPtrSlice(from.AuthorizedCIDRs),
	}
}

func newPlatformProfile(from *api.CustomerPlatformProfile, from2 *api.ServiceProviderPlatformProfile) generated.PlatformProfile {
	if from == nil {
		return generated.PlatformProfile{}
	}
	return generated.PlatformProfile{
		ManagedResourceGroup:    api.PtrOrNil(from.ManagedResourceGroup),
		SubnetID:                api.ResourceIDToStringPtr(from.SubnetID),
		OutboundType:            api.PtrOrNil(generated.OutboundType(from.OutboundType)),
		NetworkSecurityGroupID:  api.ResourceIDToStringPtr(from.NetworkSecurityGroupID),
		OperatorsAuthentication: api.PtrOrNil(newOperatorsAuthenticationProfile(&from.OperatorsAuthentication)),
		IssuerURL:               api.PtrOrNil(from2.IssuerURL),
	}
}

func newClusterAutoscalingProfile(from *api.ClusterAutoscalingProfile) generated.ClusterAutoscalingProfile {
	if from == nil {
		return generated.ClusterAutoscalingProfile{}
	}
	return generated.ClusterAutoscalingProfile{
		MaxNodeProvisionTimeSeconds: api.PtrOrNil(from.MaxNodeProvisionTimeSeconds),
		MaxNodesTotal:               api.PtrOrNil(from.MaxNodesTotal),
		MaxPodGracePeriodSeconds:    api.PtrOrNil(from.MaxPodGracePeriodSeconds),
		PodPriorityThreshold:        api.PtrOrNil(from.PodPriorityThreshold),
	}
}

func newEtcdProfile(from *api.EtcdProfile) generated.EtcdProfile {
	if from == nil {
		return generated.EtcdProfile{}
	}
	return generated.EtcdProfile{
		DataEncryption: api.PtrOrNil(newEtcdDataEncryptionProfile(&from.DataEncryption)),
	}
}
func newEtcdDataEncryptionProfile(from *api.EtcdDataEncryptionProfile) generated.EtcdDataEncryptionProfile {
	if from == nil {
		return generated.EtcdDataEncryptionProfile{}
	}
	return generated.EtcdDataEncryptionProfile{
		CustomerManaged:   newCustomerManagedEncryptionProfile(from.CustomerManaged),
		KeyManagementMode: api.PtrOrNil(generated.EtcdDataEncryptionKeyManagementModeType(from.KeyManagementMode)),
	}
}
func newCustomerManagedEncryptionProfile(from *api.CustomerManagedEncryptionProfile) *generated.CustomerManagedEncryptionProfile {
	if from == nil {
		return nil
	}
	return &generated.CustomerManagedEncryptionProfile{
		Kms:            api.PtrOrNil(newKmsEncryptionProfile(from.Kms)),
		EncryptionType: api.PtrOrNil(generated.CustomerManagedEncryptionType(from.EncryptionType)),
	}
}
func newKmsEncryptionProfile(from *api.KmsEncryptionProfile) generated.KmsEncryptionProfile {
	if from == nil {
		return generated.KmsEncryptionProfile{}
	}
	return generated.KmsEncryptionProfile{
		ActiveKey: api.PtrOrNil(newKmsKey(&from.ActiveKey)),
	}
}
func newKmsKey(from *api.KmsKey) generated.KmsKey {
	if from == nil {
		return generated.KmsKey{}
	}
	return generated.KmsKey{
		Name:      api.PtrOrNil(from.Name),
		VaultName: api.PtrOrNil(from.VaultName),
		Version:   api.PtrOrNil(from.Version),
	}
}

func newClusterImageRegistryProfile(from *api.ClusterImageRegistryProfile) generated.ClusterImageRegistryProfile {
	if from == nil {
		return generated.ClusterImageRegistryProfile{}
	}
	return generated.ClusterImageRegistryProfile{
		State: api.PtrOrNil(generated.ClusterImageRegistryState(from.State)),
	}
}

func newOperatorsAuthenticationProfile(from *api.OperatorsAuthenticationProfile) generated.OperatorsAuthenticationProfile {
	if from == nil {
		return generated.OperatorsAuthenticationProfile{}
	}
	return generated.OperatorsAuthenticationProfile{
		UserAssignedIdentities: api.PtrOrNil(newUserAssignedIdentitiesProfile(&from.UserAssignedIdentities)),
	}
}

func newUserAssignedIdentitiesProfile(from *api.UserAssignedIdentitiesProfile) generated.UserAssignedIdentitiesProfile {
	if from == nil {
		return generated.UserAssignedIdentitiesProfile{}
	}
	return generated.UserAssignedIdentitiesProfile{
		ControlPlaneOperators:  api.ResourceIDMapToStringPtrMap(from.ControlPlaneOperators),
		DataPlaneOperators:     api.ResourceIDMapToStringPtrMap(from.DataPlaneOperators),
		ServiceManagedIdentity: api.ResourceIDToStringPtr(from.ServiceManagedIdentity),
	}
}

func newSystemData(from *arm.SystemData) generated.SystemData {
	if from == nil {
		return generated.SystemData{}
	}
	return generated.SystemData{
		CreatedBy:          api.PtrOrNil(from.CreatedBy),
		CreatedByType:      api.PtrOrNil(generated.CreatedByType(from.CreatedByType)),
		CreatedAt:          from.CreatedAt,
		LastModifiedBy:     api.PtrOrNil(from.LastModifiedBy),
		LastModifiedByType: api.PtrOrNil(generated.CreatedByType(from.LastModifiedByType)),
		LastModifiedAt:     from.LastModifiedAt,
	}
}

func newManagedServiceIdentity(from *arm.ManagedServiceIdentity) *generated.ManagedServiceIdentity {
	if from == nil {
		return nil
	}
	return &generated.ManagedServiceIdentity{
		Type:                   api.PtrOrNil(generated.ManagedServiceIdentityType(from.Type)),
		PrincipalID:            api.PtrOrNil(from.PrincipalID),
		TenantID:               api.PtrOrNil(from.TenantID),
		UserAssignedIdentities: convertUserAssignedIdentities(from.UserAssignedIdentities),
	}
}

// NewHCPOpenShiftCluster converts an internal representation to this API version.
// If from is nil, returns a defaulted external object for use on the write path
// where defaults are applied before unmarshaling the request body.
func (v version) NewHCPOpenShiftCluster(from *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	if from == nil {
		ret := &HcpOpenShiftCluster{}
		SetDefaultValuesCluster(ret)
		return ret
	}

	idString := ""
	if from.ID != nil {
		idString = from.ID.String()
	}

	out := &HcpOpenShiftCluster{
		generated.HcpOpenShiftCluster{
			ID:         api.PtrOrNil(idString),
			Name:       api.PtrOrNil(from.Name),
			Type:       api.PtrOrNil(from.Type),
			SystemData: api.PtrOrNil(newSystemData(from.SystemData)),
			Location:   api.PtrOrNil(from.Location),
			Tags:       api.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.HcpOpenShiftClusterProperties{
				ProvisioningState:       api.PtrOrNil(generated.ProvisioningState(from.ServiceProviderProperties.ProvisioningState)),
				Version:                 api.PtrOrNil(newVersionProfile(&from.CustomerProperties.Version)),
				DNS:                     api.PtrOrNil(newDNSProfile(&from.CustomerProperties.DNS, &from.ServiceProviderProperties.DNS)),
				Network:                 api.PtrOrNil(newNetworkProfile(&from.CustomerProperties.Network)),
				Console:                 api.PtrOrNil(newConsoleProfile(&from.ServiceProviderProperties.Console)),
				API:                     api.PtrOrNil(newAPIProfile(&from.CustomerProperties.API, &from.ServiceProviderProperties.API)),
				Platform:                api.PtrOrNil(newPlatformProfile(&from.CustomerProperties.Platform, &from.ServiceProviderProperties.Platform)),
				Autoscaling:             api.PtrOrNil(newClusterAutoscalingProfile(&from.CustomerProperties.Autoscaling)),
				NodeDrainTimeoutMinutes: api.PtrOrNil(from.CustomerProperties.NodeDrainTimeoutMinutes),
				ClusterImageRegistry:    api.PtrOrNil(newClusterImageRegistryProfile(&from.CustomerProperties.ClusterImageRegistry)),
				Etcd:                    api.PtrOrNil(newEtcdProfile(&from.CustomerProperties.Etcd)),
			},
			Identity: newManagedServiceIdentity(from.Identity),
		},
	}

	return out
}

func (c *HcpOpenShiftCluster) GetVersion() api.Version {
	return versionedInterface
}

// ZeroOwnedFields zeros the fields of 'internal' that this API version (v20240610preview) owns.
// Fields introduced in later API versions (ImageDigestMirrors, VnetIntegrationSubnetID,
// Kms.Visibility) are intentionally NOT zeroed so they are preserved from the existing
// Cosmos document verbatim.
func (c *HcpOpenShiftCluster) ZeroOwnedFields(internal *api.HCPOpenShiftCluster) {
	// ARM Resource metadata — owned by all versions
	internal.ID = nil
	internal.Name = ""
	internal.Type = ""
	internal.Location = ""
	internal.Tags = nil
	internal.Identity = nil
	internal.SystemData = nil

	// Customer-visible properties known to v20240610preview
	internal.CustomerProperties.Version = api.VersionProfile{}
	internal.CustomerProperties.DNS = api.CustomerDNSProfile{}
	internal.CustomerProperties.Network = api.NetworkProfile{}
	internal.CustomerProperties.API = api.CustomerAPIProfile{}

	// Platform: zero only the fields v20240610preview exposes.
	// VnetIntegrationSubnetID is NOT zeroed — it was added in v20251223preview
	// and must be preserved from the existing Cosmos document.
	internal.CustomerProperties.Platform.ManagedResourceGroup = ""
	internal.CustomerProperties.Platform.SubnetID = nil
	internal.CustomerProperties.Platform.OutboundType = ""
	internal.CustomerProperties.Platform.NetworkSecurityGroupID = nil
	internal.CustomerProperties.Platform.OperatorsAuthentication = api.OperatorsAuthenticationProfile{}

	internal.CustomerProperties.Autoscaling = api.ClusterAutoscalingProfile{}
	internal.CustomerProperties.NodeDrainTimeoutMinutes = 0

	// Etcd: zero only the fields v20240610preview exposes at the leaf level.
	// CustomerManaged is NOT zeroed as a whole — that would destroy the
	// v20251223preview-exclusive Kms.Visibility field.
	internal.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = ""
	if internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil {
		internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.EncryptionType = ""
		if internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
			internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Name = ""
			internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version = ""
			internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.VaultName = ""
			// Kms.Visibility is NOT zeroed — it was added in v20251223preview.
		}
	}

	internal.CustomerProperties.ClusterImageRegistry = api.ClusterImageRegistryProfile{}

	// ImageDigestMirrors is NOT zeroed — it was added in v20251223preview
	// and must be preserved from the existing Cosmos document.
}

// ApplyOwnedFields copies values from the receiver (external object populated from the request
// body) into 'internal', for all fields this v20240610preview version owns.
// Null checks on required fields are at the TOP before any normalization.
// Note: VnetIntegrationSubnetID is NOT checked here — it is a v20251223preview requirement.
func (c *HcpOpenShiftCluster) ApplyOwnedFields(internal *api.HCPOpenShiftCluster) error {
	errs := field.ErrorList{}

	// No v20240610preview-specific required-field null checks beyond what
	// SetDefaultValuesCluster guarantees. VnetIntegrationSubnetID null rejection
	// is a v20251223preview concern (that version exposes and requires it).

	if c.ID != nil {
		internal.ID = api.Must(azcorearm.ParseResourceID(strings.ToLower(*c.ID)))
	}
	if c.Name != nil {
		internal.Name = *c.Name
	}
	if c.Type != nil {
		internal.Type = *c.Type
	}
	if c.SystemData != nil {
		internal.SystemData = &arm.SystemData{
			CreatedAt:      c.SystemData.CreatedAt,
			LastModifiedAt: c.SystemData.LastModifiedAt,
		}
		if c.SystemData.CreatedBy != nil {
			internal.SystemData.CreatedBy = *c.SystemData.CreatedBy
		}
		if c.SystemData.CreatedByType != nil {
			internal.SystemData.CreatedByType = arm.CreatedByType(*c.SystemData.CreatedByType)
		}
		if c.SystemData.LastModifiedBy != nil {
			internal.SystemData.LastModifiedBy = *c.SystemData.LastModifiedBy
		}
		if c.SystemData.LastModifiedByType != nil {
			internal.SystemData.LastModifiedByType = arm.CreatedByType(*c.SystemData.LastModifiedByType)
		}
	}
	if c.Location != nil {
		internal.Location = *c.Location
	}
	internal.Identity = normalizeManagedIdentity(c.Identity)
	// Per RPC-Patch-V1-04, the Tags field does NOT follow
	// JSON merge-patch (RFC 7396) semantics:
	//
	//   When Tags are patched, the tags from the request
	//   replace all existing tags for the resource
	//
	internal.Tags = api.StringPtrMapToStringMap(c.Tags)
	if c.Properties != nil {
		if c.Properties.Version != nil {
			normalizeVersion(c.Properties.Version, &internal.CustomerProperties.Version)
		}
		if c.Properties.DNS != nil {
			normalizeDNS(c.Properties.DNS, &internal.CustomerProperties.DNS, &internal.ServiceProviderProperties.DNS)
		}
		if c.Properties.Network != nil {
			normalizeNetwork(c.Properties.Network, &internal.CustomerProperties.Network)
		}
		if c.Properties.Console != nil {
			normalizeConsole(c.Properties.Console, &internal.ServiceProviderProperties.Console)
		}
		if c.Properties.API != nil {
			normalizeAPI(c.Properties.API, &internal.CustomerProperties.API, &internal.ServiceProviderProperties.API)
		}
		if c.Properties.Platform != nil {
			errs = append(errs, normalizePlatform(field.NewPath("properties", "platform"), c.Properties.Platform, &internal.CustomerProperties.Platform, &internal.ServiceProviderProperties.Platform)...)
		}
		if c.Properties.Autoscaling != nil {
			normalizeAutoscaling(c.Properties.Autoscaling, &internal.CustomerProperties.Autoscaling)
		}
		if c.Properties.NodeDrainTimeoutMinutes != nil {
			internal.CustomerProperties.NodeDrainTimeoutMinutes = *c.Properties.NodeDrainTimeoutMinutes
		}
		if c.Properties.ClusterImageRegistry != nil {
			normalizeClusterImageRegistry(c.Properties.ClusterImageRegistry, &internal.CustomerProperties.ClusterImageRegistry)
		}
		if c.Properties.Etcd != nil {
			normalizeEtcd(c.Properties.Etcd, &internal.CustomerProperties.Etcd)
		}
	}

	// Field preservation is structural: fields not in v20240610preview's owned set
	// (ImageDigestMirrors, VnetIntegrationSubnetID, Kms.Visibility) are preserved
	// verbatim from the base via ZeroOwnedFields leaving them untouched.

	return arm.CloudErrorFromFieldErrors(errs)
}

func normalizeManagedIdentity(identity *generated.ManagedServiceIdentity) *arm.ManagedServiceIdentity {
	if identity == nil {
		return nil
	}

	ret := &arm.ManagedServiceIdentity{}
	if identity.PrincipalID != nil {
		ret.PrincipalID = *identity.PrincipalID
	}
	if identity.TenantID != nil {
		ret.TenantID = *identity.TenantID
	}
	if identity.Type != nil {
		ret.Type = (arm.ManagedServiceIdentityType)(*identity.Type)
	}
	if identity.UserAssignedIdentities != nil {
		normalizeIdentityUserAssignedIdentities(identity.UserAssignedIdentities, &ret.UserAssignedIdentities)
	}

	return ret
}

func normalizeVersion(p *generated.VersionProfile, out *api.VersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
}

func normalizeDNS(p *generated.DNSProfile, out *api.CustomerDNSProfile, out2 *api.ServiceProviderDNSProfile) {
	if p.BaseDomain != nil {
		out2.BaseDomain = *p.BaseDomain
	}
	if p.BaseDomainPrefix != nil {
		out.BaseDomainPrefix = *p.BaseDomainPrefix
	}
}

func normalizeNetwork(p *generated.NetworkProfile, out *api.NetworkProfile) {
	if p.NetworkType != nil {
		out.NetworkType = api.NetworkType(*p.NetworkType)
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

func normalizeConsole(p *generated.ConsoleProfile, out *api.ServiceProviderConsoleProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
}

func normalizeAPI(p *generated.APIProfile, out *api.CustomerAPIProfile, out2 *api.ServiceProviderAPIProfile) {
	if p.URL != nil {
		out2.URL = *p.URL
	}
	if p.Visibility != nil {
		out.Visibility = api.Visibility(*p.Visibility)
	}
	out.AuthorizedCIDRs = api.StringPtrSliceToStringSlice(p.AuthorizedCIDRs)
}

func normalizePlatform(fldPath *field.Path, p *generated.PlatformProfile, out *api.CustomerPlatformProfile, out2 *api.ServiceProviderPlatformProfile) field.ErrorList {
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
		out.OutboundType = api.OutboundType(*p.OutboundType)
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

func normalizeAutoscaling(p *generated.ClusterAutoscalingProfile, out *api.ClusterAutoscalingProfile) {
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

func normalizeEtcd(p *generated.EtcdProfile, out *api.EtcdProfile) {
	if p.DataEncryption != nil {
		normalizeEtcdDataEncryptionProfile(p.DataEncryption, &out.DataEncryption)
	} else {
		out.DataEncryption = api.EtcdDataEncryptionProfile{}
	}
}

func normalizeEtcdDataEncryptionProfile(p *generated.EtcdDataEncryptionProfile, out *api.EtcdDataEncryptionProfile) {
	if p.CustomerManaged != nil {
		if out.CustomerManaged == nil {
			out.CustomerManaged = &api.CustomerManagedEncryptionProfile{}
		}
		normalizeCustomerManaged(p.CustomerManaged, out.CustomerManaged)
	} else {
		out.CustomerManaged = nil
	}
	if p.KeyManagementMode != nil {
		out.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeType(*p.KeyManagementMode)
	}
}

func normalizeCustomerManaged(p *generated.CustomerManagedEncryptionProfile, out *api.CustomerManagedEncryptionProfile) {
	if p.EncryptionType != nil {
		out.EncryptionType = api.CustomerManagedEncryptionType(*p.EncryptionType)
	}
	if p.Kms != nil && p.Kms.ActiveKey != nil {
		if out.Kms == nil {
			out.Kms = &api.KmsEncryptionProfile{}
		}
		normalizeActiveKey(p.Kms.ActiveKey, &out.Kms.ActiveKey)
	} else {
		out.Kms = nil
	}
}

func normalizeActiveKey(p *generated.KmsKey, out *api.KmsKey) {
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

func normalizeClusterImageRegistry(p *generated.ClusterImageRegistryProfile, out *api.ClusterImageRegistryProfile) {
	if p.State != nil {
		out.State = api.ClusterImageRegistryState(*p.State)
	}
}

func normalizeOperatorsAuthentication(fldPath *field.Path, p *generated.OperatorsAuthenticationProfile, out *api.OperatorsAuthenticationProfile) field.ErrorList {
	if p.UserAssignedIdentities != nil {
		return normalizeUserAssignedIdentities(fldPath.Child("userAssignedIdentities"), p.UserAssignedIdentities, &out.UserAssignedIdentities)
	}
	return nil
}

func normalizeUserAssignedIdentities(fldPath *field.Path, p *generated.UserAssignedIdentitiesProfile, out *api.UserAssignedIdentitiesProfile) field.ErrorList {
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

	errs = append(errs, api.MergeStringPtrMapIntoResourceIDMap(fldPath.Child("controlPlaneOperators"), p.ControlPlaneOperators, &out.ControlPlaneOperators)...)
	errs = append(errs, api.MergeStringPtrMapIntoResourceIDMap(fldPath.Child("dataPlaneOperators"), p.DataPlaneOperators, &out.DataPlaneOperators)...)
	if p.ServiceManagedIdentity != nil && len(*p.ServiceManagedIdentity) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.ServiceManagedIdentity); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("serviceManagedIdentity"), *p.ServiceManagedIdentity, err.Error()))
		} else {
			out.ServiceManagedIdentity = resourceID
		}
	}

	return errs
}

func normalizeIdentityUserAssignedIdentities(p map[string]*generated.UserAssignedIdentity, out *map[string]*arm.UserAssignedIdentity) {
	if *out == nil {
		*out = make(map[string]*arm.UserAssignedIdentity)
	}
	for key, value := range p {
		if value != nil {
			(*out)[key] = &arm.UserAssignedIdentity{
				ClientID:    value.ClientID,
				PrincipalID: value.PrincipalID,
			}
		} else {
			(*out)[key] = nil
		}
	}
}

func convertUserAssignedIdentities(from map[string]*arm.UserAssignedIdentity) map[string]*generated.UserAssignedIdentity {
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

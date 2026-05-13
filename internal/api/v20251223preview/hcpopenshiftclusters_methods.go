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

package v20251223preview

import (
	"strings"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/v20251223preview/generated"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

type HcpOpenShiftCluster struct {
	generated.HcpOpenShiftCluster
}

var _ resourcesapi.VersionedCreatableResource[resourcesapi.HCPOpenShiftCluster] = &HcpOpenShiftCluster{}

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
		obj.Properties.Version.ChannelGroup = ptr.To(resourcesapi.DefaultClusterVersionChannelGroup)
	}
	if obj.Properties.Network == nil {
		obj.Properties.Network = &generated.NetworkProfile{}
	}
	if obj.Properties.Network.NetworkType == nil {
		obj.Properties.Network.NetworkType = ptr.To(generated.NetworkTypeOVNKubernetes)
	}
	if obj.Properties.Network.PodCIDR == nil {
		obj.Properties.Network.PodCIDR = ptr.To(resourcesapi.DefaultClusterNetworkPodCIDR)
	}
	if obj.Properties.Network.ServiceCIDR == nil {
		obj.Properties.Network.ServiceCIDR = ptr.To(resourcesapi.DefaultClusterNetworkServiceCIDR)
	}
	if obj.Properties.Network.MachineCIDR == nil {
		obj.Properties.Network.MachineCIDR = ptr.To(resourcesapi.DefaultClusterNetworkMachineCIDR)
	}
	if obj.Properties.Network.HostPrefix == nil {
		obj.Properties.Network.HostPrefix = ptr.To(resourcesapi.DefaultClusterNetworkHostPrefix)
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
		obj.Properties.Autoscaling.MaxPodGracePeriodSeconds = ptr.To(resourcesapi.DefaultClusterMaxPodGracePeriodSeconds)
	}
	if obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds == nil {
		obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds = ptr.To(resourcesapi.DefaultClusterMaxNodeProvisionTimeSeconds)
	}
	if obj.Properties.Autoscaling.PodPriorityThreshold == nil {
		obj.Properties.Autoscaling.PodPriorityThreshold = ptr.To(resourcesapi.DefaultClusterPodPriorityThreshold)
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

func newVersionProfile(from *resourcesapi.VersionProfile) generated.VersionProfile {
	if from == nil {
		return generated.VersionProfile{}
	}
	return generated.VersionProfile{
		ID:           resourcesapi.PtrOrNil(from.ID),
		ChannelGroup: resourcesapi.PtrOrNil(from.ChannelGroup),
	}
}

func newDNSProfile(from *resourcesapi.CustomerDNSProfile, from2 *resourcesapi.ServiceProviderDNSProfile) generated.DNSProfile {
	if from == nil {
		return generated.DNSProfile{}
	}
	return generated.DNSProfile{
		BaseDomain:       resourcesapi.PtrOrNil(from2.BaseDomain),
		BaseDomainPrefix: resourcesapi.PtrOrNil(from.BaseDomainPrefix),
	}
}

func newNetworkProfile(from *resourcesapi.NetworkProfile) generated.NetworkProfile {
	if from == nil {
		return generated.NetworkProfile{}
	}
	return generated.NetworkProfile{
		NetworkType: resourcesapi.PtrOrNil(generated.NetworkType(from.NetworkType)),
		PodCIDR:     resourcesapi.PtrOrNil(from.PodCIDR),
		ServiceCIDR: resourcesapi.PtrOrNil(from.ServiceCIDR),
		MachineCIDR: resourcesapi.PtrOrNil(from.MachineCIDR),
		// Use Ptr (not PtrOrNil) to ensure int32 zero value is preserved in JSON response.
		HostPrefix: resourcesapi.Ptr(from.HostPrefix),
	}
}

func newConsoleProfile(from *resourcesapi.ServiceProviderConsoleProfile) generated.ConsoleProfile {
	if from == nil {
		return generated.ConsoleProfile{}
	}
	return generated.ConsoleProfile{
		URL: resourcesapi.PtrOrNil(from.URL),
	}
}

func newAPIProfile(from *resourcesapi.CustomerAPIProfile, from2 *resourcesapi.ServiceProviderAPIProfile) generated.APIProfile {
	if from == nil {
		return generated.APIProfile{}
	}
	return generated.APIProfile{
		URL:             resourcesapi.PtrOrNil(from2.URL),
		Visibility:      resourcesapi.PtrOrNil(generated.Visibility(from.Visibility)),
		AuthorizedCIDRs: resourcesapi.StringSliceToStringPtrSlice(from.AuthorizedCIDRs),
	}
}

func newPlatformProfile(from *resourcesapi.CustomerPlatformProfile, from2 *resourcesapi.ServiceProviderPlatformProfile) generated.PlatformProfile {
	if from == nil {
		return generated.PlatformProfile{}
	}
	return generated.PlatformProfile{
		ManagedResourceGroup:    resourcesapi.PtrOrNil(from.ManagedResourceGroup),
		SubnetID:                resourcesapi.ResourceIDToStringPtr(from.SubnetID),
		VnetIntegrationSubnetID: resourcesapi.ResourceIDToStringPtr(from.VnetIntegrationSubnetID),
		OutboundType:            resourcesapi.PtrOrNil(generated.OutboundType(from.OutboundType)),
		NetworkSecurityGroupID:  resourcesapi.ResourceIDToStringPtr(from.NetworkSecurityGroupID),
		OperatorsAuthentication: resourcesapi.PtrOrNil(newOperatorsAuthenticationProfile(&from.OperatorsAuthentication)),
		IssuerURL:               resourcesapi.PtrOrNil(from2.IssuerURL),
	}
}

func newClusterAutoscalingProfile(from *resourcesapi.ClusterAutoscalingProfile) generated.ClusterAutoscalingProfile {
	if from == nil {
		return generated.ClusterAutoscalingProfile{}
	}
	return generated.ClusterAutoscalingProfile{
		// Use Ptr (not PtrOrNil) for int32 fields where zero is a valid user value,
		// ensuring explicit zeros are preserved in JSON responses.
		MaxNodeProvisionTimeSeconds: resourcesapi.Ptr(from.MaxNodeProvisionTimeSeconds),
		MaxNodesTotal:               resourcesapi.PtrOrNil(from.MaxNodesTotal),
		MaxPodGracePeriodSeconds:    resourcesapi.Ptr(from.MaxPodGracePeriodSeconds),
		PodPriorityThreshold:        resourcesapi.Ptr(from.PodPriorityThreshold),
	}
}

func newEtcdProfile(from *resourcesapi.EtcdProfile) generated.EtcdProfile {
	if from == nil {
		return generated.EtcdProfile{}
	}
	return generated.EtcdProfile{
		DataEncryption: resourcesapi.PtrOrNil(newEtcdDataEncryptionProfile(&from.DataEncryption)),
	}
}
func newEtcdDataEncryptionProfile(from *resourcesapi.EtcdDataEncryptionProfile) generated.EtcdDataEncryptionProfile {
	if from == nil {
		return generated.EtcdDataEncryptionProfile{}
	}
	return generated.EtcdDataEncryptionProfile{
		CustomerManaged:   newCustomerManagedEncryptionProfile(from.CustomerManaged),
		KeyManagementMode: resourcesapi.PtrOrNil(generated.EtcdDataEncryptionKeyManagementModeType(from.KeyManagementMode)),
	}
}
func newCustomerManagedEncryptionProfile(from *resourcesapi.CustomerManagedEncryptionProfile) *generated.CustomerManagedEncryptionProfile {
	if from == nil {
		return nil
	}
	return &generated.CustomerManagedEncryptionProfile{
		Kms:            resourcesapi.PtrOrNil(newKmsEncryptionProfile(from.Kms)),
		EncryptionType: resourcesapi.PtrOrNil(generated.CustomerManagedEncryptionType(from.EncryptionType)),
	}
}
func newKmsEncryptionProfile(from *resourcesapi.KmsEncryptionProfile) generated.KmsEncryptionProfile {
	if from == nil {
		return generated.KmsEncryptionProfile{}
	}
	return generated.KmsEncryptionProfile{
		ActiveKey:  resourcesapi.PtrOrNil(newKmsKey(&from.ActiveKey)),
		VaultName:  resourcesapi.PtrOrNil(from.ActiveKey.VaultName),
		Visibility: resourcesapi.PtrOrNil(generated.KeyVaultVisibility(from.Visibility)),
	}
}
func newKmsKey(from *resourcesapi.KmsKey) generated.KmsKey {
	if from == nil {
		return generated.KmsKey{}
	}
	return generated.KmsKey{
		Name:    resourcesapi.PtrOrNil(from.Name),
		Version: resourcesapi.PtrOrNil(from.Version),
	}
}

func newClusterImageRegistryProfile(from *resourcesapi.ClusterImageRegistryProfile) generated.ClusterImageRegistryProfile {
	if from == nil {
		return generated.ClusterImageRegistryProfile{}
	}
	return generated.ClusterImageRegistryProfile{
		State: resourcesapi.PtrOrNil(generated.ClusterImageRegistryState(from.State)),
	}
}

func newImageDigestMirrors(from []resourcesapi.ImageDigestMirror) []*generated.ImageDigestMirror {
	if from == nil {
		return nil
	}
	out := make([]*generated.ImageDigestMirror, 0, len(from))
	for _, item := range from {
		out = append(out, &generated.ImageDigestMirror{
			Source:  resourcesapi.PtrOrNil(item.Source),
			Mirrors: resourcesapi.StringSliceToStringPtrSlice(item.Mirrors),
		})
	}
	return out
}

func newOperatorsAuthenticationProfile(from *resourcesapi.OperatorsAuthenticationProfile) generated.OperatorsAuthenticationProfile {
	if from == nil {
		return generated.OperatorsAuthenticationProfile{}
	}
	return generated.OperatorsAuthenticationProfile{
		UserAssignedIdentities: resourcesapi.PtrOrNil(newUserAssignedIdentitiesProfile(&from.UserAssignedIdentities)),
	}
}

func newUserAssignedIdentitiesProfile(from *resourcesapi.UserAssignedIdentitiesProfile) generated.UserAssignedIdentitiesProfile {
	if from == nil {
		return generated.UserAssignedIdentitiesProfile{}
	}
	return generated.UserAssignedIdentitiesProfile{
		ControlPlaneOperators:  resourcesapi.ResourceIDMapToStringPtrMap(from.ControlPlaneOperators),
		DataPlaneOperators:     resourcesapi.ResourceIDMapToStringPtrMap(from.DataPlaneOperators),
		ServiceManagedIdentity: resourcesapi.ResourceIDToStringPtr(from.ServiceManagedIdentity),
	}
}

func newSystemData(from *armresourcesapi.SystemData) generated.SystemData {
	if from == nil {
		return generated.SystemData{}
	}
	return generated.SystemData{
		CreatedBy:          resourcesapi.PtrOrNil(from.CreatedBy),
		CreatedByType:      resourcesapi.PtrOrNil(generated.CreatedByType(from.CreatedByType)),
		CreatedAt:          from.CreatedAt,
		LastModifiedBy:     resourcesapi.PtrOrNil(from.LastModifiedBy),
		LastModifiedByType: resourcesapi.PtrOrNil(generated.CreatedByType(from.LastModifiedByType)),
		LastModifiedAt:     from.LastModifiedAt,
	}
}

func newManagedServiceIdentity(from *armresourcesapi.ManagedServiceIdentity) *generated.ManagedServiceIdentity {
	if from == nil {
		return nil
	}
	return &generated.ManagedServiceIdentity{
		Type:                   resourcesapi.PtrOrNil(generated.ManagedServiceIdentityType(from.Type)),
		PrincipalID:            resourcesapi.PtrOrNil(from.PrincipalID),
		TenantID:               resourcesapi.PtrOrNil(from.TenantID),
		UserAssignedIdentities: convertUserAssignedIdentities(from.UserAssignedIdentities),
	}
}

// NewHCPOpenShiftCluster converts an internal representation to this API version.
// If from is nil, returns a defaulted external object for use on the write path
// where defaults are applied before unmarshaling the request body.
func (v version) NewHCPOpenShiftCluster(from *resourcesapi.HCPOpenShiftCluster) resourcesapi.VersionedHCPOpenShiftCluster {
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
			ID:         resourcesapi.PtrOrNil(idString),
			Name:       resourcesapi.PtrOrNil(from.Name),
			Type:       resourcesapi.PtrOrNil(from.Type),
			SystemData: resourcesapi.PtrOrNil(newSystemData(from.SystemData)),
			Location:   resourcesapi.PtrOrNil(from.Location),
			Tags:       resourcesapi.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.HcpOpenShiftClusterProperties{
				ProvisioningState: resourcesapi.PtrOrNil(generated.ProvisioningState(from.ServiceProviderProperties.ProvisioningState)),
				Version:           resourcesapi.PtrOrNil(newVersionProfile(&from.CustomerProperties.Version)),
				DNS:               resourcesapi.PtrOrNil(newDNSProfile(&from.CustomerProperties.DNS, &from.ServiceProviderProperties.DNS)),
				Network:           resourcesapi.PtrOrNil(newNetworkProfile(&from.CustomerProperties.Network)),
				Console:           resourcesapi.PtrOrNil(newConsoleProfile(&from.ServiceProviderProperties.Console)),
				API:               resourcesapi.PtrOrNil(newAPIProfile(&from.CustomerProperties.API, &from.ServiceProviderProperties.API)),
				Platform:          resourcesapi.PtrOrNil(newPlatformProfile(&from.CustomerProperties.Platform, &from.ServiceProviderProperties.Platform)),
				Autoscaling:       resourcesapi.PtrOrNil(newClusterAutoscalingProfile(&from.CustomerProperties.Autoscaling)),
				// Use Ptr (not PtrOrNil) to ensure int32 zero value is preserved in JSON response.
				NodeDrainTimeoutMinutes: resourcesapi.Ptr(from.CustomerProperties.NodeDrainTimeoutMinutes),
				ClusterImageRegistry:    resourcesapi.PtrOrNil(newClusterImageRegistryProfile(&from.CustomerProperties.ClusterImageRegistry)),
				Etcd:                    resourcesapi.PtrOrNil(newEtcdProfile(&from.CustomerProperties.Etcd)),
				ImageDigestMirrors:      newImageDigestMirrors(from.CustomerProperties.ImageDigestMirrors),
			},
			Identity: newManagedServiceIdentity(from.Identity),
		},
	}

	return out
}

func (c *HcpOpenShiftCluster) GetVersion() resourcesapi.Version {
	return versionedInterface
}

func (c *HcpOpenShiftCluster) ConvertToInternal(existing *resourcesapi.HCPOpenShiftCluster) (*resourcesapi.HCPOpenShiftCluster, error) {
	out := &resourcesapi.HCPOpenShiftCluster{}
	errs := field.ErrorList{}

	// Reject null on required fields. On the PATCH path, JSON merge-patch
	// converts explicit null to a nil pointer. On the PUT path, defaults
	// are applied before the request body so nil here means the user
	// explicitly sent null (mergo does not override with nil).
	if c.Properties != nil {
		if c.Properties.Network != nil && c.Properties.Network.HostPrefix == nil {
			errs = append(errs, field.Required(field.NewPath("properties", "network", "hostPrefix"), "field cannot be null"))
		}
		if c.Properties.Autoscaling != nil {
			if c.Properties.Autoscaling.MaxPodGracePeriodSeconds == nil {
				errs = append(errs, field.Required(field.NewPath("properties", "autoscaling", "maxPodGracePeriodSeconds"), "field cannot be null"))
			}
			if c.Properties.Autoscaling.MaxNodeProvisionTimeSeconds == nil {
				errs = append(errs, field.Required(field.NewPath("properties", "autoscaling", "maxNodeProvisionTimeSeconds"), "field cannot be null"))
			}
			if c.Properties.Autoscaling.PodPriorityThreshold == nil {
				errs = append(errs, field.Required(field.NewPath("properties", "autoscaling", "podPriorityThreshold"), "field cannot be null"))
			}
		}
		if c.Properties.Etcd != nil && c.Properties.Etcd.DataEncryption != nil && c.Properties.Etcd.DataEncryption.CustomerManaged != nil && c.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
			if c.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility == nil {
				errs = append(errs, field.Required(field.NewPath("properties", "etcd", "dataEncryption", "customerManaged", "kms", "visibility"), "field cannot be null"))
			}
		}
		if c.Properties.Platform != nil {
			if c.Properties.Platform.VnetIntegrationSubnetID == nil {
				// TODO: Remove this check when v20240610preview is removed and
				// vnetIntegrationSubnetId is enforced via validate.RequiredPointer
				// in validateCustomerPlatformProfile.
				errs = append(errs, field.Required(field.NewPath("properties", "platform", "vnetIntegrationSubnetId"), "field cannot be null"))
			} else if len(*c.Properties.Platform.VnetIntegrationSubnetID) == 0 {
				errs = append(errs, field.Invalid(field.NewPath("properties", "platform", "vnetIntegrationSubnetId"), "", "field cannot be empty string"))
			}
		}
	}

	if c.ID != nil {
		out.ID = resourcesapi.Must(azcorearm.ParseResourceID(strings.ToLower(*c.ID)))
	}
	if c.Name != nil {
		out.Name = *c.Name
	}
	if c.Type != nil {
		out.Type = *c.Type
	}
	if c.SystemData != nil {
		out.SystemData = &armresourcesapi.SystemData{
			CreatedAt:      c.SystemData.CreatedAt,
			LastModifiedAt: c.SystemData.LastModifiedAt,
		}
		if c.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *c.SystemData.CreatedBy
		}
		if c.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = armresourcesapi.CreatedByType(*c.SystemData.CreatedByType)
		}
		if c.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *c.SystemData.LastModifiedBy
		}
		if c.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = armresourcesapi.CreatedByType(*c.SystemData.LastModifiedByType)
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
	out.Tags = resourcesapi.StringPtrMapToStringMap(c.Tags)
	if c.Properties != nil {
		if c.Properties.ProvisioningState != nil {
			out.ServiceProviderProperties.ProvisioningState = armresourcesapi.ProvisioningState(*c.Properties.ProvisioningState)
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
		out.CustomerProperties.NodeDrainTimeoutMinutes = resourcesapi.Deref(c.Properties.NodeDrainTimeoutMinutes)
		if c.Properties.ClusterImageRegistry != nil {
			normalizeClusterImageRegistry(c.Properties.ClusterImageRegistry, &out.CustomerProperties.ClusterImageRegistry)
		}
		if c.Properties.Etcd != nil {
			normalizeEtcd(c.Properties.Etcd, &out.CustomerProperties.Etcd)
		}
		if c.Properties.ImageDigestMirrors != nil {
			normalizeImageDigestMirrors(c.Properties.ImageDigestMirrors, &out.CustomerProperties.ImageDigestMirrors)
		}
	}

	if existing != nil {
		preserveUnknownClusterFields(existing, out)
	}

	return out, armresourcesapi.CloudErrorFromFieldErrors(errs)
}

// preserveUnknownClusterFields copies customer-facing fields from existing that
// this API version doesn't know about. Currently empty — no cross-version
// customer fields exist yet between v20240610preview and v20251223preview.
func preserveUnknownClusterFields(from, to *resourcesapi.HCPOpenShiftCluster) {
}

func normalizeManagedIdentity(identity *generated.ManagedServiceIdentity) *armresourcesapi.ManagedServiceIdentity {
	if identity == nil {
		return nil
	}

	ret := &armresourcesapi.ManagedServiceIdentity{}
	if identity.PrincipalID != nil {
		ret.PrincipalID = *identity.PrincipalID
	}
	if identity.TenantID != nil {
		ret.TenantID = *identity.TenantID
	}
	if identity.Type != nil {
		ret.Type = (armresourcesapi.ManagedServiceIdentityType)(*identity.Type)
	}
	if identity.UserAssignedIdentities != nil {
		normalizeIdentityUserAssignedIdentities(identity.UserAssignedIdentities, &ret.UserAssignedIdentities)
	}

	return ret
}

func normalizeVersion(p *generated.VersionProfile, out *resourcesapi.VersionProfile) {
	out.ID = resourcesapi.Deref(p.ID)
	out.ChannelGroup = resourcesapi.Deref(p.ChannelGroup)
}

func normalizeDNS(p *generated.DNSProfile, out *resourcesapi.CustomerDNSProfile, out2 *resourcesapi.ServiceProviderDNSProfile) {
	out2.BaseDomain = resourcesapi.Deref(p.BaseDomain)
	out.BaseDomainPrefix = resourcesapi.Deref(p.BaseDomainPrefix)
}

func normalizeNetwork(p *generated.NetworkProfile, out *resourcesapi.NetworkProfile) {
	out.NetworkType = resourcesapi.NetworkType(resourcesapi.Deref(p.NetworkType))
	out.PodCIDR = resourcesapi.Deref(p.PodCIDR)
	out.ServiceCIDR = resourcesapi.Deref(p.ServiceCIDR)
	out.MachineCIDR = resourcesapi.Deref(p.MachineCIDR)
	out.HostPrefix = resourcesapi.Deref(p.HostPrefix)
}

func normalizeConsole(p *generated.ConsoleProfile, out *resourcesapi.ServiceProviderConsoleProfile) {
	out.URL = resourcesapi.Deref(p.URL)
}

func normalizeAPI(p *generated.APIProfile, out *resourcesapi.CustomerAPIProfile, out2 *resourcesapi.ServiceProviderAPIProfile) {
	out2.URL = resourcesapi.Deref(p.URL)
	out.Visibility = resourcesapi.Visibility(resourcesapi.Deref(p.Visibility))
	out.AuthorizedCIDRs = resourcesapi.StringPtrSliceToStringSlice(p.AuthorizedCIDRs)
}

func normalizePlatform(fldPath *field.Path, p *generated.PlatformProfile, out *resourcesapi.CustomerPlatformProfile, out2 *resourcesapi.ServiceProviderPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	out.ManagedResourceGroup = resourcesapi.Deref(p.ManagedResourceGroup)
	if p.SubnetID != nil && len(*p.SubnetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.SubnetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("subnetID"), *p.SubnetID, err.Error()))
		} else {
			out.SubnetID = resourceID
		}
	} else {
		out.SubnetID = nil
	}
	out.OutboundType = resourcesapi.OutboundType(resourcesapi.Deref(p.OutboundType))
	if p.VnetIntegrationSubnetID != nil && len(*p.VnetIntegrationSubnetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.VnetIntegrationSubnetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("vnetIntegrationSubnetId"), *p.VnetIntegrationSubnetID, err.Error()))
		} else {
			out.VnetIntegrationSubnetID = resourceID
		}
	}
	if p.NetworkSecurityGroupID != nil && len(*p.NetworkSecurityGroupID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.NetworkSecurityGroupID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("networkSecurityGroupID"), *p.NetworkSecurityGroupID, err.Error()))
		} else {
			out.NetworkSecurityGroupID = resourceID
		}
	} else {
		out.NetworkSecurityGroupID = nil
	}
	if p.OperatorsAuthentication != nil {
		errs = append(errs, normalizeOperatorsAuthentication(fldPath.Child("operatorsAuthentication"), p.OperatorsAuthentication, &out.OperatorsAuthentication)...)
	} else {
		out.OperatorsAuthentication = resourcesapi.OperatorsAuthenticationProfile{}
	}
	out2.IssuerURL = resourcesapi.Deref(p.IssuerURL)

	return errs
}

func normalizeAutoscaling(p *generated.ClusterAutoscalingProfile, out *resourcesapi.ClusterAutoscalingProfile) {
	out.MaxNodeProvisionTimeSeconds = resourcesapi.Deref(p.MaxNodeProvisionTimeSeconds)
	out.MaxNodesTotal = resourcesapi.Deref(p.MaxNodesTotal)
	out.MaxPodGracePeriodSeconds = resourcesapi.Deref(p.MaxPodGracePeriodSeconds)
	out.PodPriorityThreshold = resourcesapi.Deref(p.PodPriorityThreshold)
}

func normalizeEtcd(p *generated.EtcdProfile, out *resourcesapi.EtcdProfile) {
	if p.DataEncryption != nil {
		normalizeEtcdDataEncryptionProfile(p.DataEncryption, &out.DataEncryption)
	} else {
		out.DataEncryption = resourcesapi.EtcdDataEncryptionProfile{}
	}
}

func normalizeEtcdDataEncryptionProfile(p *generated.EtcdDataEncryptionProfile, out *resourcesapi.EtcdDataEncryptionProfile) {
	if p.CustomerManaged != nil {
		if out.CustomerManaged == nil {
			out.CustomerManaged = &resourcesapi.CustomerManagedEncryptionProfile{}
		}
		normalizeCustomerManaged(p.CustomerManaged, out.CustomerManaged)
	} else {
		out.CustomerManaged = nil
	}
	out.KeyManagementMode = resourcesapi.EtcdDataEncryptionKeyManagementModeType(resourcesapi.Deref(p.KeyManagementMode))
}

func normalizeCustomerManaged(p *generated.CustomerManagedEncryptionProfile, out *resourcesapi.CustomerManagedEncryptionProfile) {
	out.EncryptionType = resourcesapi.CustomerManagedEncryptionType(resourcesapi.Deref(p.EncryptionType))
	if p.Kms != nil && p.Kms.ActiveKey != nil && (p.Kms.ActiveKey.Name != nil || p.Kms.ActiveKey.Version != nil) {
		if out.Kms == nil {
			out.Kms = &resourcesapi.KmsEncryptionProfile{}
		}

		normalizeActiveKey(p.Kms.ActiveKey, &out.Kms.ActiveKey)
		out.Kms.ActiveKey.VaultName = resourcesapi.Deref(p.Kms.VaultName)
		out.Kms.Visibility = resourcesapi.KeyVaultVisibility(resourcesapi.Deref(p.Kms.Visibility))
	} else {
		out.Kms = nil
	}
}

func normalizeActiveKey(p *generated.KmsKey, out *resourcesapi.KmsKey) {
	out.Name = resourcesapi.Deref(p.Name)
	out.Version = resourcesapi.Deref(p.Version)
}

func normalizeClusterImageRegistry(p *generated.ClusterImageRegistryProfile, out *resourcesapi.ClusterImageRegistryProfile) {
	out.State = resourcesapi.ClusterImageRegistryState(resourcesapi.Deref(p.State))
}

func normalizeImageDigestMirror(p *generated.ImageDigestMirror, out *resourcesapi.ImageDigestMirror) {
	if p == nil {
		return
	}
	if p.Source != nil {
		out.Source = *p.Source
	}
	out.Mirrors = resourcesapi.StringPtrSliceToStringSlice(p.Mirrors)
	out.MirrorSourcePolicy = resourcesapi.MirrorSourcePolicyAllowContactingSource
}

func normalizeImageDigestMirrors(p []*generated.ImageDigestMirror, out *[]resourcesapi.ImageDigestMirror) {
	slice := make([]resourcesapi.ImageDigestMirror, len(p))
	for i := range p {
		if p[i] != nil {
			normalizeImageDigestMirror(p[i], &slice[i])
		}
	}
	*out = slice
}

func normalizeOperatorsAuthentication(fldPath *field.Path, p *generated.OperatorsAuthenticationProfile, out *resourcesapi.OperatorsAuthenticationProfile) field.ErrorList {
	errs := field.ErrorList{}

	if p.UserAssignedIdentities != nil {
		errs = append(errs, normalizeUserAssignedIdentities(fldPath.Child("userAssignedIdentities"), p.UserAssignedIdentities, &out.UserAssignedIdentities)...)
	}
	return errs
}

func normalizeUserAssignedIdentities(fldPath *field.Path, p *generated.UserAssignedIdentitiesProfile, out *resourcesapi.UserAssignedIdentitiesProfile) field.ErrorList {
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

	errs = append(errs, resourcesapi.MergeStringPtrMapIntoResourceIDMap(fldPath.Child("controlPlaneOperators"), p.ControlPlaneOperators, &out.ControlPlaneOperators)...)
	errs = append(errs, resourcesapi.MergeStringPtrMapIntoResourceIDMap(fldPath.Child("dataPlaneOperators"), p.DataPlaneOperators, &out.DataPlaneOperators)...)
	if p.ServiceManagedIdentity != nil && len(*p.ServiceManagedIdentity) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.ServiceManagedIdentity); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("serviceManagedIdentity"), *p.ServiceManagedIdentity, err.Error()))
		} else {
			out.ServiceManagedIdentity = resourceID
		}
	}
	return errs
}

func normalizeIdentityUserAssignedIdentities(p map[string]*generated.UserAssignedIdentity, out *map[string]*armresourcesapi.UserAssignedIdentity) {
	if *out == nil {
		*out = make(map[string]*armresourcesapi.UserAssignedIdentity)
	}
	for key, value := range p {
		if value != nil {
			(*out)[key] = &armresourcesapi.UserAssignedIdentity{
				ClientID:    value.ClientID,
				PrincipalID: value.PrincipalID,
			}
		} else {
			(*out)[key] = nil
		}
	}
}

func convertUserAssignedIdentities(from map[string]*armresourcesapi.UserAssignedIdentity) map[string]*generated.UserAssignedIdentity {
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

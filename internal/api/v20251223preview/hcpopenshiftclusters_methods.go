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
	"fmt"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview/generated"
)

type HcpOpenShiftCluster struct {
	generated.HcpOpenShiftCluster
}

var _ api.VersionedCreatableResource[api.HCPOpenShiftCluster] = &HcpOpenShiftCluster{}

func (h *HcpOpenShiftCluster) NewExternal() any {
	return &HcpOpenShiftCluster{}
}

func (h *HcpOpenShiftCluster) SetDefaultValues(uncast any) error {
	obj, ok := uncast.(*HcpOpenShiftCluster)
	if !ok {
		return fmt.Errorf("unexpected type %T", uncast)
	}

	SetDefaultValuesCluster(obj)
	return nil
}

func SetDefaultValuesCluster(obj *HcpOpenShiftCluster) {
	if obj.Properties == nil {
		obj.Properties = &generated.HcpOpenShiftClusterProperties{}
	}
	if obj.Properties.Version == nil {
		obj.Properties.Version = &generated.VersionProfile{}
	}
	if obj.Properties.Version.ChannelGroup == nil {
		obj.Properties.Version.ChannelGroup = ptr.To("stable")
	}
	if obj.Properties.Network == nil {
		obj.Properties.Network = &generated.NetworkProfile{}
	}
	if obj.Properties.Network.NetworkType == nil {
		obj.Properties.Network.NetworkType = ptr.To(generated.NetworkTypeOVNKubernetes)
	}
	if obj.Properties.Network.PodCIDR == nil {
		obj.Properties.Network.PodCIDR = ptr.To("10.128.0.0/14")
	}
	if obj.Properties.Network.ServiceCIDR == nil {
		obj.Properties.Network.ServiceCIDR = ptr.To("172.30.0.0/16")
	}
	if obj.Properties.Network.MachineCIDR == nil {
		obj.Properties.Network.MachineCIDR = ptr.To("10.0.0.0/16")
	}
	if obj.Properties.Network.HostPrefix == nil {
		obj.Properties.Network.HostPrefix = ptr.To(int32(23))
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
	if obj.Properties.Autoscaling == nil {
		obj.Properties.Autoscaling = &generated.ClusterAutoscalingProfile{}
	}
	if obj.Properties.Autoscaling.MaxPodGracePeriodSeconds == nil {
		obj.Properties.Autoscaling.MaxPodGracePeriodSeconds = ptr.To(int32(600))
	}
	if obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds == nil {
		obj.Properties.Autoscaling.MaxNodeProvisionTimeSeconds = ptr.To(int32(900))
	}
	if obj.Properties.Autoscaling.PodPriorityThreshold == nil {
		obj.Properties.Autoscaling.PodPriorityThreshold = ptr.To(int32(-10))
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
		obj.Properties.ClusterImageRegistry.State = ptr.To(generated.ClusterImageRegistryProfileStateEnabled)
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

func newDNSProfile(from *api.DNSProfile) generated.DNSProfile {
	if from == nil {
		return generated.DNSProfile{}
	}
	return generated.DNSProfile{
		BaseDomain:       api.PtrOrNil(from.BaseDomain),
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

func newConsoleProfile(from *api.ConsoleProfile) generated.ConsoleProfile {
	if from == nil {
		return generated.ConsoleProfile{}
	}
	return generated.ConsoleProfile{
		URL: api.PtrOrNil(from.URL),
	}
}

func newAPIProfile(from *api.APIProfile) generated.APIProfile {
	if from == nil {
		return generated.APIProfile{}
	}
	return generated.APIProfile{
		URL:             api.PtrOrNil(from.URL),
		Visibility:      api.PtrOrNil(generated.Visibility(from.Visibility)),
		AuthorizedCIDRs: api.StringSliceToStringPtrSlice(from.AuthorizedCIDRs),
	}
}

func newPlatformProfile(from *api.PlatformProfile) generated.PlatformProfile {
	if from == nil {
		return generated.PlatformProfile{}
	}
	return generated.PlatformProfile{
		ManagedResourceGroup:    api.PtrOrNil(from.ManagedResourceGroup),
		SubnetID:                api.PtrOrNil(from.SubnetID),
		OutboundType:            api.PtrOrNil(generated.OutboundType(from.OutboundType)),
		NetworkSecurityGroupID:  api.PtrOrNil(from.NetworkSecurityGroupID),
		OperatorsAuthentication: api.PtrOrNil(newOperatorsAuthenticationProfile(&from.OperatorsAuthentication)),
		IssuerURL:               api.PtrOrNil(from.IssuerURL),
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
		State: api.PtrOrNil(generated.ClusterImageRegistryProfileState(from.State)),
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
		ControlPlaneOperators:  api.StringMapToStringPtrMap(from.ControlPlaneOperators),
		DataPlaneOperators:     api.StringMapToStringPtrMap(from.DataPlaneOperators),
		ServiceManagedIdentity: api.PtrOrNil(from.ServiceManagedIdentity),
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

func (v version) NewHCPOpenShiftCluster(from *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	if from == nil {
		ret := &HcpOpenShiftCluster{}
		SetDefaultValuesCluster(ret)
		return ret
	}

	out := &HcpOpenShiftCluster{
		generated.HcpOpenShiftCluster{
			ID:         api.PtrOrNil(from.ID),
			Name:       api.PtrOrNil(from.Name),
			Type:       api.PtrOrNil(from.Type),
			SystemData: api.PtrOrNil(newSystemData(from.SystemData)),
			Location:   api.PtrOrNil(from.Location),
			Tags:       api.StringMapToStringPtrMap(from.Tags),
			Properties: &generated.HcpOpenShiftClusterProperties{
				ProvisioningState:       api.PtrOrNil(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Version:                 api.PtrOrNil(newVersionProfile(&from.Properties.Version)),
				DNS:                     api.PtrOrNil(newDNSProfile(&from.Properties.DNS)),
				Network:                 api.PtrOrNil(newNetworkProfile(&from.Properties.Network)),
				Console:                 api.PtrOrNil(newConsoleProfile(&from.Properties.Console)),
				API:                     api.PtrOrNil(newAPIProfile(&from.Properties.API)),
				Platform:                api.PtrOrNil(newPlatformProfile(&from.Properties.Platform)),
				Autoscaling:             api.PtrOrNil(newClusterAutoscalingProfile(&from.Properties.Autoscaling)),
				NodeDrainTimeoutMinutes: api.PtrOrNil(from.Properties.NodeDrainTimeoutMinutes),
				ClusterImageRegistry:    api.PtrOrNil(newClusterImageRegistryProfile(&from.Properties.ClusterImageRegistry)),
				Etcd:                    api.PtrOrNil(newEtcdProfile(&from.Properties.Etcd)),
			},
			Identity: newManagedServiceIdentity(from.Identity),
		},
	}

	return out
}

func (c *HcpOpenShiftCluster) GetVersion() api.Version {
	return versionedInterface
}

func (c *HcpOpenShiftCluster) Normalize(out *api.HCPOpenShiftCluster) {
	if c.ID != nil {
		out.ID = *c.ID
	}
	if c.Name != nil {
		out.Name = *c.Name
	}
	if c.Type != nil {
		out.Type = *c.Type
	}
	if c.SystemData != nil {
		out.SystemData = &arm.SystemData{
			CreatedAt:      c.SystemData.CreatedAt,
			LastModifiedAt: c.SystemData.LastModifiedAt,
		}
		if c.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *c.SystemData.CreatedBy
		}
		if c.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = arm.CreatedByType(*c.SystemData.CreatedByType)
		}
		if c.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *c.SystemData.LastModifiedBy
		}
		if c.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = arm.CreatedByType(*c.SystemData.LastModifiedByType)
		}
	}
	if c.Location != nil {
		out.Location = *c.Location
	}
	if c.Identity != nil {
		out.Identity = &arm.ManagedServiceIdentity{}
		if c.Identity.PrincipalID != nil {
			out.Identity.PrincipalID = *c.Identity.PrincipalID
		}
		if c.Identity.TenantID != nil {
			out.Identity.TenantID = *c.Identity.TenantID
		}
		if c.Identity.Type != nil {
			out.Identity.Type = (arm.ManagedServiceIdentityType)(*c.Identity.Type)
		}
		if c.Identity.UserAssignedIdentities != nil {
			normalizeIdentityUserAssignedIdentities(c.Identity.UserAssignedIdentities, &out.Identity.UserAssignedIdentities)
		}
	}
	// Per RPC-Patch-V1-04, the Tags field does NOT follow
	// JSON merge-patch (RFC 7396) semantics:
	//
	//   When Tags are patched, the tags from the request
	//   replace all existing tags for the resource
	//
	out.Tags = api.StringPtrMapToStringMap(c.Tags)
	if c.Properties != nil {
		if c.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = arm.ProvisioningState(*c.Properties.ProvisioningState)
		}
		if c.Properties != nil {
			if c.Properties.Version != nil {
				normalizeVersion(c.Properties.Version, &out.Properties.Version)
			}
			if c.Properties.DNS != nil {
				normailzeDNS(c.Properties.DNS, &out.Properties.DNS)
			}
			if c.Properties.Network != nil {
				normalizeNetwork(c.Properties.Network, &out.Properties.Network)
			}
			if c.Properties.Console != nil {
				normalizeConsole(c.Properties.Console, &out.Properties.Console)
			}
			if c.Properties.API != nil {
				normalizeAPI(c.Properties.API, &out.Properties.API)
			}
			if c.Properties.Platform != nil {
				normalizePlatform(c.Properties.Platform, &out.Properties.Platform)
			}
			if c.Properties.Autoscaling != nil {
				normalizeAutoscaling(c.Properties.Autoscaling, &out.Properties.Autoscaling)
			}
			if c.Properties.NodeDrainTimeoutMinutes != nil {
				out.Properties.NodeDrainTimeoutMinutes = *c.Properties.NodeDrainTimeoutMinutes
			}
			if c.Properties.ClusterImageRegistry != nil {
				normalizeClusterImageRegistry(c.Properties.ClusterImageRegistry, &out.Properties.ClusterImageRegistry)
			}
			if c.Properties.Etcd != nil {
				normalizeEtcd(c.Properties.Etcd, &out.Properties.Etcd)
			}
		}
	}
}

func (c *HcpOpenShiftCluster) GetVisibility(path string) (api.VisibilityFlags, bool) {
	flags, ok := clusterVisibilityMap[path]
	return flags, ok
}

func (c *HcpOpenShiftCluster) ValidateVisibility(current api.VersionedCreatableResource[api.HCPOpenShiftCluster], updating bool) []arm.CloudErrorBody {
	var structTagMap = api.GetStructTagMap[api.HCPOpenShiftCluster]()
	return api.ValidateVisibility(c, current.(*HcpOpenShiftCluster), clusterVisibilityMap, structTagMap, updating)
}

func normalizeVersion(p *generated.VersionProfile, out *api.VersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
}

func normailzeDNS(p *generated.DNSProfile, out *api.DNSProfile) {
	if p.BaseDomain != nil {
		out.BaseDomain = *p.BaseDomain
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

func normalizeConsole(p *generated.ConsoleProfile, out *api.ConsoleProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
}

func normalizeAPI(p *generated.APIProfile, out *api.APIProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
	if p.Visibility != nil {
		out.Visibility = api.Visibility(*p.Visibility)
	}
	out.AuthorizedCIDRs = api.StringPtrSliceToStringSlice(p.AuthorizedCIDRs)
}

func normalizePlatform(p *generated.PlatformProfile, out *api.PlatformProfile) {
	if p.ManagedResourceGroup != nil {
		out.ManagedResourceGroup = *p.ManagedResourceGroup
	}
	if p.SubnetID != nil {
		out.SubnetID = *p.SubnetID
	}
	if p.OutboundType != nil {
		out.OutboundType = api.OutboundType(*p.OutboundType)
	}
	if p.NetworkSecurityGroupID != nil {
		out.NetworkSecurityGroupID = *p.NetworkSecurityGroupID
	}
	if p.OperatorsAuthentication != nil {
		normalizeOperatorsAuthentication(p.OperatorsAuthentication, &out.OperatorsAuthentication)
	}
	if p.IssuerURL != nil {
		out.IssuerURL = *p.IssuerURL
	}
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
	}
}

func normalizeEtcdDataEncryptionProfile(p *generated.EtcdDataEncryptionProfile, out *api.EtcdDataEncryptionProfile) {
	if p.CustomerManaged != nil {
		if out.CustomerManaged == nil {
			out.CustomerManaged = &api.CustomerManagedEncryptionProfile{}
		}
		normalizeCustomerManaged(p.CustomerManaged, out.CustomerManaged)
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
		out.State = api.ClusterImageRegistryProfileState(*p.State)
	}
}

func normalizeOperatorsAuthentication(p *generated.OperatorsAuthenticationProfile, out *api.OperatorsAuthenticationProfile) {
	if p.UserAssignedIdentities != nil {
		normalizeUserAssignedIdentities(p.UserAssignedIdentities, &out.UserAssignedIdentities)
	}
}

func normalizeUserAssignedIdentities(p *generated.UserAssignedIdentitiesProfile, out *api.UserAssignedIdentitiesProfile) {
	switch {
	case p.ControlPlaneOperators != nil && out.ControlPlaneOperators == nil:
		out.ControlPlaneOperators = make(map[string]string)
	case p.ControlPlaneOperators == nil && out.ControlPlaneOperators != nil:
		out.ControlPlaneOperators = nil
	}
	switch {
	case p.DataPlaneOperators != nil && out.DataPlaneOperators == nil:
		out.DataPlaneOperators = make(map[string]string)
	case p.DataPlaneOperators == nil && out.DataPlaneOperators != nil:
		out.DataPlaneOperators = nil
	}

	api.MergeStringPtrMap(p.ControlPlaneOperators, &out.ControlPlaneOperators)
	api.MergeStringPtrMap(p.DataPlaneOperators, &out.DataPlaneOperators)
	if p.ServiceManagedIdentity != nil {
		out.ServiceManagedIdentity = *p.ServiceManagedIdentity
	}
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

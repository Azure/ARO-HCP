// Copyright 2026 Microsoft Corporation
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

package v20260901preview

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview/generated"
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
	if obj.Properties.Ingress == nil {
		obj.Properties.Ingress = &generated.IngressProfile{}
	}
	if obj.Properties.Ingress.Type == nil {
		obj.Properties.Ingress.Type = ptr.To(generated.IngressTypePublic)
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
	if obj.Properties.Etcd == nil {
		obj.Properties.Etcd = &generated.EtcdProfile{}
	}
	if obj.Properties.Etcd.DataEncryption == nil {
		obj.Properties.Etcd.DataEncryption = &generated.EtcdDataEncryptionProfile{}
	}
	if obj.Properties.ClusterImageRegistry == nil {
		obj.Properties.ClusterImageRegistry = &generated.ClusterImageRegistryProfile{}
	}
	if obj.Properties.ClusterImageRegistry.State == nil {
		obj.Properties.ClusterImageRegistry.State = ptr.To(generated.ClusterImageRegistryStateEnabled)
	}
	if obj.Properties.CryptoRestrictions == nil {
		obj.Properties.CryptoRestrictions = ptr.To(generated.CryptoRestrictionsNone)
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
		// Use Ptr (not PtrOrNil) to ensure int32 zero value is preserved in JSON response.
		HostPrefix: metadataapi.Ptr(from.HostPrefix),
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

func newIngressProfile(from *coreapi.CustomerIngressProfile) generated.IngressProfile {
	if from == nil {
		return generated.IngressProfile{}
	}
	return generated.IngressProfile{
		Type: metadataapi.PtrOrNil(generated.IngressType(from.Type)),
	}
}

func newPlatformProfile(from *coreapi.CustomerPlatformProfile, from2 *coreapi.ServiceProviderPlatformProfile) generated.PlatformProfile {
	if from == nil {
		return generated.PlatformProfile{}
	}
	return generated.PlatformProfile{
		ManagedResourceGroup:    metadataapi.PtrOrNil(from.ManagedResourceGroup),
		SubnetID:                metadataapi.ResourceIDToStringPtr(from.SubnetID),
		VnetIntegrationSubnetID: metadataapi.ResourceIDToStringPtr(from.VnetIntegrationSubnetID),
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
		// Use Ptr (not PtrOrNil) for int32 fields where zero is a valid user value,
		// ensuring explicit zeros are preserved in JSON responses.
		MaxNodeProvisionTimeSeconds: metadataapi.Ptr(from.MaxNodeProvisionTimeSeconds),
		MaxNodesTotal:               metadataapi.PtrOrNil(from.MaxNodesTotal),
		MaxPodGracePeriodSeconds:    metadataapi.Ptr(from.MaxPodGracePeriodSeconds),
		PodPriorityThreshold:        metadataapi.Ptr(from.PodPriorityThreshold),
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
		EncryptionType: metadataapi.PtrOrNil(generated.CustomerManagedEncryptionType(from.EncryptionType)),
		Kms:            metadataapi.PtrOrNil(newKmsEncryptionProfile(from.Kms)),
	}
}

func newKmsEncryptionProfile(from *coreapi.KmsEncryptionProfile) generated.KmsEncryptionProfile {
	if from == nil {
		return generated.KmsEncryptionProfile{}
	}
	out := generated.KmsEncryptionProfile{
		Visibility: metadataapi.PtrOrNil(generated.KeyVaultVisibility(from.Visibility)),
	}
	if from.KeyEncryptionKeyURL != "" {
		out.KeyEncryptionKeyURL = metadataapi.PtrOrNil(from.KeyEncryptionKeyURL)
	} else if from.ActiveKey.Name != "" {
		// Fallback for old clusters created before keyEncryptionKeyUrl was stored.
		// Safe to assume vault.azure.net — mHSM did not exist before this field.
		keyURL := buildKeyEncryptionKeyURL(from.ActiveKey.VaultName, from.ActiveKey.Name, from.ActiveKey.Version)
		out.KeyEncryptionKeyURL = metadataapi.PtrOrNil(keyURL)
	}
	return out
}

func buildKeyEncryptionKeyURL(vaultName, keyName, version string) string {
	base := fmt.Sprintf("https://%s.vault.azure.net/keys/%s", vaultName, keyName)
	if version != "" {
		return base + "/" + version
	}
	return base
}

func parseKeyEncryptionKeyURL(keyURL string) (vaultName, keyName, version string, err error) {
	u, err := url.Parse(keyURL)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: %w", err)
	}
	if u.Scheme != "https" {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: scheme must be https, got %q", u.Scheme)
	}
	hostname := u.Hostname()
	if !strings.HasSuffix(hostname, ".vault.azure.net") && !strings.HasSuffix(hostname, ".managedhsm.azure.net") {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: host must end in .vault.azure.net or .managedhsm.azure.net, got %q", hostname)
	}
	if idx := strings.IndexByte(hostname, '.'); idx > 0 {
		vaultName = hostname[:idx]
	} else {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: cannot extract vault name from host %q", hostname)
	}
	if vaultName == "" {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: vault name is empty")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "keys" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("invalid keyEncryptionKeyUrl: path must be /keys/{name}/{version}")
	}
	keyName = parts[1]
	version = parts[2]
	return vaultName, keyName, version, nil
}

func newConditions(from []metav1.Condition) []*generated.Condition {
	if from == nil {
		return nil
	}

	out := make([]*generated.Condition, 0, len(from))
	for i := range from {
		c := from[i]
		cond := &generated.Condition{
			Type:    metadataapi.Ptr(generated.ConditionType(c.Type)),
			Status:  metadataapi.Ptr(generated.StatusType(c.Status)),
			Reason:  metadataapi.Ptr(c.Reason),
			Message: metadataapi.Ptr(c.Message),
		}
		if !c.LastTransitionTime.IsZero() {
			t := c.LastTransitionTime.Time
			cond.LastTransitionTime = &t
		}
		out = append(out, cond)
	}

	return out
}

func newClusterResourceStatus(from *coreapi.HCPOpenShiftClusterStatus) generated.ResourceStatus {
	if from == nil {
		return generated.ResourceStatus{}
	}
	return generated.ResourceStatus{
		Conditions: newConditions(from.UserFacingConditions),
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

func newImageDigestMirrors(from []coreapi.ImageDigestMirror) []*generated.ImageDigestMirror {
	if from == nil {
		return nil
	}
	out := make([]*generated.ImageDigestMirror, 0, len(from))
	for _, item := range from {
		out = append(out, &generated.ImageDigestMirror{
			Source:  metadataapi.PtrOrNil(item.Source),
			Mirrors: metadataapi.StringSliceToStringPtrSlice(item.Mirrors),
		})
	}
	return out
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
				ProvisioningState: metadataapi.PtrOrNil(generated.ProvisioningState(from.ServiceProviderProperties.ProvisioningState)),
				Version:           metadataapi.PtrOrNil(newVersionProfile(&from.CustomerProperties.Version)),
				DNS:               metadataapi.PtrOrNil(newDNSProfile(&from.CustomerProperties.DNS, &from.ServiceProviderProperties.DNS)),
				Network:           metadataapi.PtrOrNil(newNetworkProfile(&from.CustomerProperties.Network)),
				Console:           metadataapi.PtrOrNil(newConsoleProfile(&from.ServiceProviderProperties.Console)),
				API:               metadataapi.PtrOrNil(newAPIProfile(&from.CustomerProperties.API, &from.ServiceProviderProperties.API)),
				Ingress:           metadataapi.PtrOrNil(newIngressProfile(&from.CustomerProperties.Ingress)),
				Platform:          metadataapi.PtrOrNil(newPlatformProfile(&from.CustomerProperties.Platform, &from.ServiceProviderProperties.Platform)),
				Autoscaling:       metadataapi.PtrOrNil(newClusterAutoscalingProfile(&from.CustomerProperties.Autoscaling)),
				// Use Ptr (not PtrOrNil) to ensure int32 zero value is preserved in JSON response.
				NodeDrainTimeoutMinutes: metadataapi.Ptr(from.CustomerProperties.NodeDrainTimeoutMinutes),
				ClusterImageRegistry:    metadataapi.PtrOrNil(newClusterImageRegistryProfile(&from.CustomerProperties.ClusterImageRegistry)),
				Etcd:                    metadataapi.PtrOrNil(newEtcdProfile(&from.CustomerProperties.Etcd)),
				ImageDigestMirrors:      newImageDigestMirrors(from.CustomerProperties.ImageDigestMirrors),
				Status:                  metadataapi.PtrOrNil(newClusterResourceStatus(&from.Status)),
				CryptoRestrictions:      metadataapi.PtrOrNil(generated.CryptoRestrictions(from.CustomerProperties.CryptoRestrictions)),
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
		if c.Properties.Ingress != nil {
			normalizeIngress(c.Properties.Ingress, &out.CustomerProperties.Ingress)
		}
		if c.Properties.Platform != nil {
			errs = append(errs, normalizePlatform(field.NewPath("properties", "platform"), c.Properties.Platform, &out.CustomerProperties.Platform, &out.ServiceProviderProperties.Platform)...)
		}
		if c.Properties.Autoscaling != nil {
			normalizeAutoscaling(c.Properties.Autoscaling, &out.CustomerProperties.Autoscaling)
		}
		out.CustomerProperties.NodeDrainTimeoutMinutes = metadataapi.Deref(c.Properties.NodeDrainTimeoutMinutes)
		if c.Properties.ClusterImageRegistry != nil {
			normalizeClusterImageRegistry(c.Properties.ClusterImageRegistry, &out.CustomerProperties.ClusterImageRegistry)
		}
		if c.Properties.Etcd != nil {
			errs = append(errs, normalizeEtcd(c.Properties.Etcd, &out.CustomerProperties.Etcd)...)
		}
		if c.Properties.ImageDigestMirrors != nil {
			normalizeImageDigestMirrors(c.Properties.ImageDigestMirrors, &out.CustomerProperties.ImageDigestMirrors)
		}
		if c.Properties.CryptoRestrictions != nil {
			normalizeCryptoRestrictions(c.Properties.CryptoRestrictions, &out.CustomerProperties.CryptoRestrictions)
		}
	}

	if existing != nil {
		preserveUnknownClusterFields(existing, out)
	}

	return out, coreapi.CloudErrorFromFieldErrors(errs)
}

// preserveUnknownClusterFields copies customer-facing fields from existing that
// this API version doesn't know about. Currently empty — no cross-version
// customer fields exist yet between v20240610preview and v20260630preview.
func preserveUnknownClusterFields(from, to *coreapi.HCPOpenShiftCluster) {
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
	out.ID = metadataapi.Deref(p.ID)
	out.ChannelGroup = metadataapi.Deref(p.ChannelGroup)
}

func normalizeDNS(p *generated.DNSProfile, out *coreapi.CustomerDNSProfile, out2 *coreapi.ServiceProviderDNSProfile) {
	out2.BaseDomain = metadataapi.Deref(p.BaseDomain)
	out.BaseDomainPrefix = metadataapi.Deref(p.BaseDomainPrefix)
}

func normalizeNetwork(p *generated.NetworkProfile, out *coreapi.NetworkProfile) {
	out.NetworkType = metadataapi.NetworkType(metadataapi.Deref(p.NetworkType))
	out.PodCIDR = metadataapi.Deref(p.PodCIDR)
	out.ServiceCIDR = metadataapi.Deref(p.ServiceCIDR)
	out.MachineCIDR = metadataapi.Deref(p.MachineCIDR)
	out.HostPrefix = metadataapi.Deref(p.HostPrefix)
}

func normalizeConsole(p *generated.ConsoleProfile, out *coreapi.ServiceProviderConsoleProfile) {
	out.URL = metadataapi.Deref(p.URL)
}

func normalizeAPI(p *generated.APIProfile, out *coreapi.CustomerAPIProfile, out2 *coreapi.ServiceProviderAPIProfile) {
	out2.URL = metadataapi.Deref(p.URL)
	out.Visibility = metadataapi.Visibility(metadataapi.Deref(p.Visibility))
	out.AuthorizedCIDRs = metadataapi.StringPtrSliceToStringSlice(p.AuthorizedCIDRs)
}

func normalizeIngress(p *generated.IngressProfile, out *coreapi.CustomerIngressProfile) {
	out.Type = metadataapi.IngressType(metadataapi.Deref(p.Type))
}

func normalizeCryptoRestrictions(p *generated.CryptoRestrictions, out *metadataapi.CryptoRestrictions) {
	*out = metadataapi.CryptoRestrictions(metadataapi.Deref(p))
}

func normalizePlatform(fldPath *field.Path, p *generated.PlatformProfile, out *coreapi.CustomerPlatformProfile, out2 *coreapi.ServiceProviderPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	out.ManagedResourceGroup = metadataapi.Deref(p.ManagedResourceGroup)
	if p.SubnetID != nil && len(*p.SubnetID) > 0 {
		if resourceID, err := azcorearm.ParseResourceID(*p.SubnetID); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("subnetID"), *p.SubnetID, err.Error()))
		} else {
			out.SubnetID = resourceID
		}
	} else {
		out.SubnetID = nil
	}
	out.OutboundType = metadataapi.OutboundType(metadataapi.Deref(p.OutboundType))
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
		out.OperatorsAuthentication = coreapi.OperatorsAuthenticationProfile{}
	}
	out2.IssuerURL = metadataapi.Deref(p.IssuerURL)

	return errs
}

func normalizeAutoscaling(p *generated.ClusterAutoscalingProfile, out *coreapi.ClusterAutoscalingProfile) {
	out.MaxNodeProvisionTimeSeconds = metadataapi.Deref(p.MaxNodeProvisionTimeSeconds)
	out.MaxNodesTotal = metadataapi.Deref(p.MaxNodesTotal)
	out.MaxPodGracePeriodSeconds = metadataapi.Deref(p.MaxPodGracePeriodSeconds)
	out.PodPriorityThreshold = metadataapi.Deref(p.PodPriorityThreshold)
}

func normalizeEtcd(p *generated.EtcdProfile, out *coreapi.EtcdProfile) field.ErrorList {
	if p.DataEncryption != nil {
		return normalizeEtcdDataEncryptionProfile(p.DataEncryption, &out.DataEncryption)
	}
	out.DataEncryption = coreapi.EtcdDataEncryptionProfile{}
	return nil
}

func normalizeEtcdDataEncryptionProfile(p *generated.EtcdDataEncryptionProfile, out *coreapi.EtcdDataEncryptionProfile) field.ErrorList {
	errs := field.ErrorList{}
	if p.CustomerManaged != nil {
		if out.CustomerManaged == nil {
			out.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{}
		}
		errs = append(errs, normalizeCustomerManaged(field.NewPath("properties", "etcd", "dataEncryption", "customerManaged"), p.CustomerManaged, out.CustomerManaged)...)
	} else {
		out.CustomerManaged = nil
	}
	out.KeyManagementMode = metadataapi.EtcdDataEncryptionKeyManagementModeType(metadataapi.Deref(p.KeyManagementMode))
	return errs
}

func normalizeCustomerManaged(fldPath *field.Path, p *generated.CustomerManagedEncryptionProfile, out *coreapi.CustomerManagedEncryptionProfile) field.ErrorList {
	errs := field.ErrorList{}
	out.EncryptionType = metadataapi.CustomerManagedEncryptionType(metadataapi.Deref(p.EncryptionType))
	if p.Kms != nil {
		if p.Kms.KeyEncryptionKeyURL == nil || len(*p.Kms.KeyEncryptionKeyURL) == 0 {
			errs = append(errs, field.Required(fldPath.Child("kms", "keyEncryptionKeyUrl"), "keyEncryptionKeyUrl is required when kms is specified"))
		} else {
			if out.Kms == nil {
				out.Kms = &coreapi.KmsEncryptionProfile{}
			}
			out.Kms.KeyEncryptionKeyURL = *p.Kms.KeyEncryptionKeyURL
			out.Kms.Visibility = metadataapi.KeyVaultVisibility(metadataapi.Deref(p.Kms.Visibility))
			vaultName, keyName, version, err := parseKeyEncryptionKeyURL(*p.Kms.KeyEncryptionKeyURL)
			if err != nil {
				errs = append(errs, field.Invalid(fldPath.Child("kms", "keyEncryptionKeyUrl"), *p.Kms.KeyEncryptionKeyURL, err.Error()))
			} else {
				out.Kms.ActiveKey.VaultName = vaultName
				out.Kms.ActiveKey.Name = keyName
				out.Kms.ActiveKey.Version = version
			}
		}
	} else {
		out.Kms = nil
	}
	return errs
}

func normalizeClusterImageRegistry(p *generated.ClusterImageRegistryProfile, out *coreapi.ClusterImageRegistryProfile) {
	out.State = metadataapi.ClusterImageRegistryState(metadataapi.Deref(p.State))
}

func normalizeImageDigestMirror(p *generated.ImageDigestMirror, out *coreapi.ImageDigestMirror) {
	if p == nil {
		return
	}
	if p.Source != nil {
		out.Source = *p.Source
	}
	out.Mirrors = metadataapi.StringPtrSliceToStringSlice(p.Mirrors)
	out.MirrorSourcePolicy = metadataapi.MirrorSourcePolicyAllowContactingSource
}

func normalizeImageDigestMirrors(p []*generated.ImageDigestMirror, out *[]coreapi.ImageDigestMirror) {
	slice := make([]coreapi.ImageDigestMirror, len(p))
	for i := range p {
		if p[i] != nil {
			normalizeImageDigestMirror(p[i], &slice[i])
		}
	}
	*out = slice
}

func normalizeOperatorsAuthentication(fldPath *field.Path, p *generated.OperatorsAuthenticationProfile, out *coreapi.OperatorsAuthenticationProfile) field.ErrorList {
	errs := field.ErrorList{}

	if p.UserAssignedIdentities != nil {
		errs = append(errs, normalizeUserAssignedIdentities(fldPath.Child("userAssignedIdentities"), p.UserAssignedIdentities, &out.UserAssignedIdentities)...)
	}
	return errs
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

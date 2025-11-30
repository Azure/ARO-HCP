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

package api

import (
	"github.com/google/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftCluster represents an ARO HCP OpenShift cluster resource.
type HCPOpenShiftCluster struct {
	arm.TrackedResource

	CustomerProperties        HCPOpenShiftClusterCustomerProperties        `json:"customerProperties,omitempty" validate:"required"`
	ServiceProviderProperties HCPOpenShiftClusterServiceProviderProperties `json:"serviceProviderProperties,omitempty" validate:"required"`
	Identity                  *arm.ManagedServiceIdentity                  `json:"identity,omitempty"   validate:"omitempty"`
}

var _ CosmosPersistable = &HCPOpenShiftCluster{}

func (o *HCPOpenShiftCluster) GetCosmosData() CosmosData {
	return CosmosData{
		ID:                o.ID,
		ProvisioningState: o.ServiceProviderProperties.ProvisioningState,
		ClusterServiceID:  o.ServiceProviderProperties.ClusterServiceID,
	}
}

func (o *HCPOpenShiftCluster) SetCosmosDocumentData(cosmosUID uuid.UUID) {
	o.ServiceProviderProperties.CosmosUID = cosmosUID.String()
}

// HCPOpenShiftClusterCustomerProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterCustomerProperties struct {
	Version                 VersionProfile              `json:"version,omitempty"`
	DNS                     CustomerDNSProfile          `json:"dns,omitempty"`
	Network                 NetworkProfile              `json:"network,omitempty"                 visibility:"read create"`
	API                     CustomerAPIProfile          `json:"api,omitempty"`
	Platform                CustomerPlatformProfile     `json:"platform,omitempty"                visibility:"read create"`
	Autoscaling             ClusterAutoscalingProfile   `json:"autoscaling,omitempty"             visibility:"read create update"`
	NodeDrainTimeoutMinutes int32                       `json:"nodeDrainTimeoutMinutes,omitempty" visibility:"read create update" validate:"omitempty,min=0,max=10080"`
	Etcd                    EtcdProfile                 `json:"etcd,omitempty"                    visibility:"read create"`
	ClusterImageRegistry    ClusterImageRegistryProfile `json:"clusterImageRegistry,omitempty"    visibility:"read create"`
}

// HCPOpenShiftClusterCustomerProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterServiceProviderProperties struct {
	ProvisioningState arm.ProvisioningState          `json:"provisioningState,omitempty"       visibility:"read"`
	CosmosUID         string                         `json:"cosmosUID,omitempty"`
	ClusterServiceID  InternalID                     `json:"clusterServiceID,omitempty"                visibility:"read"`
	DNS               ServiceProviderDNSProfile      `json:"dns,omitempty"`
	Console           ServiceProviderConsoleProfile  `json:"console,omitempty"                 visibility:"read"`
	API               ServiceProviderAPIProfile      `json:"api,omitempty"`
	Platform          ServiceProviderPlatformProfile `json:"platform,omitempty"                visibility:"read create"`
}

// VersionProfile represents the cluster control plane version.
type VersionProfile struct {
	ID           string `json:"id,omitempty"                visibility:"read create"        validate:"required_unless=ChannelGroup stable,omitempty,openshift_version"`
	ChannelGroup string `json:"channelGroup,omitempty"      visibility:"read create update"`
}

// CustomerDNSProfile represents the DNS configuration of the cluster.
type CustomerDNSProfile struct {
	BaseDomainPrefix string `json:"baseDomainPrefix,omitempty" visibility:"read create" validate:"omitempty,dns_rfc1035_label,max=15"`
}

// ServiceProviderDNSProfile represents the DNS configuration of the cluster.
type ServiceProviderDNSProfile struct {
	BaseDomain string `json:"baseDomain,omitempty"       visibility:"read"`
}

// NetworkProfile represents a cluster network configuration.
// Visibility for the entire struct is "read create".
type NetworkProfile struct {
	NetworkType NetworkType `json:"networkType,omitempty" validate:"enum_networktype"`
	PodCIDR     string      `json:"podCidr,omitempty"     validate:"omitempty,cidrv4"`
	ServiceCIDR string      `json:"serviceCidr,omitempty" validate:"omitempty,cidrv4"`
	MachineCIDR string      `json:"machineCidr,omitempty" validate:"omitempty,cidrv4"`
	HostPrefix  int32       `json:"hostPrefix,omitempty"  validate:"omitempty,min=23,max=26"`
}

// ServiceProviderConsoleProfile represents a cluster web console configuration.
// Visibility for the entire struct is "read".
type ServiceProviderConsoleProfile struct {
	URL string `json:"url,omitempty"`
}

// CustomerAPIProfile represents a cluster API server configuration.
type CustomerAPIProfile struct {
	Visibility      Visibility `json:"visibility,omitempty"      visibility:"read create"        validate:"enum_visibility"`
	AuthorizedCIDRs []string   `json:"authorizedCidrs,omitempty" visibility:"read create update" validate:"max=500,dive,ipv4|cidrv4"`
}

type ServiceProviderAPIProfile struct {
	URL string `json:"url,omitempty"             visibility:"read"`
}

// CustomerPlatformProfile represents the Azure platform configuration.
// Visibility for (almost) the entire struct is "read create".
type CustomerPlatformProfile struct {
	ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty"`
	SubnetID                string                         `json:"subnetId,omitempty"                                  validate:"required,resource_id=Microsoft.Network/virtualNetworks/subnets"`
	OutboundType            OutboundType                   `json:"outboundType,omitempty"                              validate:"enum_outboundtype"`
	NetworkSecurityGroupID  string                         `json:"networkSecurityGroupId,omitempty"                    validate:"required,resource_id=Microsoft.Network/networkSecurityGroups"`
	OperatorsAuthentication OperatorsAuthenticationProfile `json:"operatorsAuthentication,omitempty"`
}

type ServiceProviderPlatformProfile struct {
	IssuerURL string `json:"issuerUrl,omitempty"               visibility:"read"`
}

// Cluster autoscaling configuration
// ClusterAutoscaling specifies auto-scaling behavior that
// applies to all NodePools associated with a control plane.
type ClusterAutoscalingProfile struct {
	MaxNodesTotal               int32 `json:"maxNodesTotal,omitempty"`
	MaxPodGracePeriodSeconds    int32 `json:"maxPodGracePeriodSeconds,omitempty"`
	MaxNodeProvisionTimeSeconds int32 `json:"maxNodeProvisionTimeSeconds,omitempty"`
	PodPriorityThreshold        int32 `json:"podPriorityThreshold,omitempty"`
}

// EtcdProfile represents an ETCD configuration.
// Visibility for the entire struct is "read create".
type EtcdProfile struct {
	DataEncryption EtcdDataEncryptionProfile `json:"dataEncryption,omitempty"`
}

// EtcdDataEncryptionProfile represents a data encryption configuration for ETCD.
// Visibility for the entire struct is "read create".
type EtcdDataEncryptionProfile struct {
	KeyManagementMode EtcdDataEncryptionKeyManagementModeType `json:"keyManagementMode,omitempty" validate:"enum_etcddataencryptionkeymanagementmodetype"`
	CustomerManaged   *CustomerManagedEncryptionProfile       `json:"customerManaged,omitempty"   validate:"required_if=KeyManagementMode CustomerManaged,excluded_unless=KeyManagementMode CustomerManaged,omitempty"`
}

// CustomerManagedEncryptionProfile repesents a data encryption configuration for
// ETCD using customer-managed keys.
// Visibility for the entire struct is "read create".
type CustomerManagedEncryptionProfile struct {
	EncryptionType CustomerManagedEncryptionType `json:"encryptionType,omitempty" validate:"enum_customermanagedencryptiontype"`
	Kms            *KmsEncryptionProfile         `json:"kms,omitempty"            validate:"required_if=EncryptionType KMS,excluded_unless=EncryptionType KMS,omitempty"`
}

// KmsEncryptionProfile represents a data encryption configuration for ETCD using
// customer-managed Key Management Service (KMS) keys.
// Visibility for the entire struct is "read create".
type KmsEncryptionProfile struct {
	ActiveKey KmsKey `json:"activeKey,omitempty"`
}

// KmsKey represents an Azure KeyVault secret.
// Visibility for the entire struct is "read create".
type KmsKey struct {
	Name      string `json:"name"      validate:"required,min=1,max=255"`
	VaultName string `json:"vaultName" validate:"required,min=1,max=255"`
	Version   string `json:"version"   validate:"required,min=1,max=255"`
}

// OperatorsAuthenticationProfile represents authentication configuration for
// OpenShift operators.
// Visibility for the entire struct is "read create".
type OperatorsAuthenticationProfile struct {
	UserAssignedIdentities UserAssignedIdentitiesProfile `json:"userAssignedIdentities,omitempty"`
}

// UserAssignedIdentitiesProfile represents authentication configuration for
// OpenShift operators using user-assigned managed identities.
// Visibility for the entire struct is "read create".
type UserAssignedIdentitiesProfile struct {
	ControlPlaneOperators  map[string]string `json:"controlPlaneOperators,omitempty"  validate:"dive,keys,required,endkeys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	DataPlaneOperators     map[string]string `json:"dataPlaneOperators,omitempty"     validate:"dive,keys,required,endkeys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	ServiceManagedIdentity string            `json:"serviceManagedIdentity,omitempty" validate:"omitempty,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
}

// ClusterImageRegistryProfile - OpenShift cluster image registry
type ClusterImageRegistryProfile struct {
	// state indicates the desired ImageStream-backed cluster image registry installation mode. This can only be set during cluster
	// creation and cannot be changed after cluster creation. Enabled means the
	// ImageStream-backed image registry will be run as pods on worker nodes in the cluster. Disabled means the ImageStream-backed
	// image registry will not be present in the cluster. The default is Enabled.
	State ClusterImageRegistryProfileState `json:"state,omitempty" validate:"enum_clusterimageregistryprofilestate"`
}

// Creates an HCPOpenShiftCluster with any non-zero default values.
func NewDefaultHCPOpenShiftCluster(resourceID *azcorearm.ResourceID) *HCPOpenShiftCluster {
	return &HCPOpenShiftCluster{
		TrackedResource: arm.NewTrackedResource(resourceID),
		CustomerProperties: HCPOpenShiftClusterCustomerProperties{
			Version: VersionProfile{
				ChannelGroup: "stable",
			},
			Network: NetworkProfile{
				NetworkType: NetworkTypeOVNKubernetes,
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
				HostPrefix:  23,
			},
			API: CustomerAPIProfile{
				Visibility: VisibilityPublic,
			},
			Platform: CustomerPlatformProfile{
				OutboundType: OutboundTypeLoadBalancer,
			},
			Autoscaling: ClusterAutoscalingProfile{
				MaxPodGracePeriodSeconds:    600,
				MaxNodeProvisionTimeSeconds: 900,
				PodPriorityThreshold:        -10,
			},
			//Even though PlatformManaged Mode is currently not supported by CS . This is the default value .
			Etcd: EtcdProfile{
				DataEncryption: EtcdDataEncryptionProfile{
					KeyManagementMode: EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
				},
			},
			ClusterImageRegistry: ClusterImageRegistryProfile{
				State: ClusterImageRegistryProfileStateEnabled,
			},
		},
	}
}

func (o *HCPOpenShiftCluster) Validate() []arm.CloudErrorBody {
	return nil
}

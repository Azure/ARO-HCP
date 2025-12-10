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
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// HCPOpenShiftCluster represents an ARO HCP OpenShift cluster resource.
type HCPOpenShiftCluster struct {
	arm.TrackedResource

	CustomerProperties        HCPOpenShiftClusterCustomerProperties        `json:"customerProperties,omitempty"`
	ServiceProviderProperties HCPOpenShiftClusterServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
	Identity                  *arm.ManagedServiceIdentity                  `json:"identity,omitempty"`
}

var _ CosmosPersistable = &HCPOpenShiftCluster{}

func (o *HCPOpenShiftCluster) GetCosmosData() CosmosData {
	return CosmosData{
		CosmosUID:    o.ServiceProviderProperties.CosmosUID,
		PartitionKey: strings.ToLower(o.ID.SubscriptionID),
		ItemID:       o.ID,
	}
}

func (o *HCPOpenShiftCluster) SetCosmosDocumentData(cosmosUID string) {
	o.ServiceProviderProperties.CosmosUID = cosmosUID
}

// HCPOpenShiftClusterCustomerProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterCustomerProperties struct {
	Version                 VersionProfile              `json:"version,omitempty"`
	DNS                     CustomerDNSProfile          `json:"dns,omitempty"`
	Network                 NetworkProfile              `json:"network,omitempty"`
	API                     CustomerAPIProfile          `json:"api,omitempty"`
	Platform                CustomerPlatformProfile     `json:"platform,omitempty"`
	Autoscaling             ClusterAutoscalingProfile   `json:"autoscaling,omitempty"`
	NodeDrainTimeoutMinutes int32                       `json:"nodeDrainTimeoutMinutes,omitempty"`
	Etcd                    EtcdProfile                 `json:"etcd,omitempty"`
	ClusterImageRegistry    ClusterImageRegistryProfile `json:"clusterImageRegistry,omitempty"`
}

// HCPOpenShiftClusterCustomerProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterServiceProviderProperties struct {
	ProvisioningState arm.ProvisioningState          `json:"provisioningState,omitempty"`
	CosmosUID         string                         `json:"cosmosUID,omitempty"`
	ClusterServiceID  InternalID                     `json:"clusterServiceID,omitempty"`
	ActiveOperationID string                         `json:"activeOperationId,omitempty"`
	DNS               ServiceProviderDNSProfile      `json:"dns,omitempty"`
	Console           ServiceProviderConsoleProfile  `json:"console,omitempty"`
	API               ServiceProviderAPIProfile      `json:"api,omitempty"`
	Platform          ServiceProviderPlatformProfile `json:"platform,omitempty"`
}

// VersionProfile represents the cluster control plane version.
type VersionProfile struct {
	ID           string `json:"id,omitempty"`
	ChannelGroup string `json:"channelGroup,omitempty"`
}

// CustomerDNSProfile represents the DNS configuration of the cluster.
type CustomerDNSProfile struct {
	BaseDomainPrefix string `json:"baseDomainPrefix,omitempty"`
}

// ServiceProviderDNSProfile represents the DNS configuration of the cluster.
type ServiceProviderDNSProfile struct {
	BaseDomain string `json:"baseDomain,omitempty"`
}

// NetworkProfile represents a cluster network configuration.
// Visibility for the entire struct is "read create".
type NetworkProfile struct {
	NetworkType NetworkType `json:"networkType,omitempty"`
	PodCIDR     string      `json:"podCidr,omitempty"`
	ServiceCIDR string      `json:"serviceCidr,omitempty"`
	MachineCIDR string      `json:"machineCidr,omitempty"`
	HostPrefix  int32       `json:"hostPrefix,omitempty"`
}

// ServiceProviderConsoleProfile represents a cluster web console configuration.
// Visibility for the entire struct is "read".
type ServiceProviderConsoleProfile struct {
	URL string `json:"url,omitempty"`
}

// CustomerAPIProfile represents a cluster API server configuration.
type CustomerAPIProfile struct {
	Visibility      Visibility `json:"visibility,omitempty"`
	AuthorizedCIDRs []string   `json:"authorizedCidrs,omitempty"`
}

type ServiceProviderAPIProfile struct {
	URL string `json:"url,omitempty"`
}

// CustomerPlatformProfile represents the Azure platform configuration.
// Visibility for (almost) the entire struct is "read create".
type CustomerPlatformProfile struct {
	ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty"`
	SubnetID                string                         `json:"subnetId,omitempty"`
	OutboundType            OutboundType                   `json:"outboundType,omitempty"`
	NetworkSecurityGroupID  string                         `json:"networkSecurityGroupId,omitempty"`
	OperatorsAuthentication OperatorsAuthenticationProfile `json:"operatorsAuthentication,omitempty"`
}

type ServiceProviderPlatformProfile struct {
	IssuerURL string `json:"issuerUrl,omitempty"`
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
	KeyManagementMode EtcdDataEncryptionKeyManagementModeType `json:"keyManagementMode,omitempty"`
	CustomerManaged   *CustomerManagedEncryptionProfile       `json:"customerManaged,omitempty"`
}

// CustomerManagedEncryptionProfile repesents a data encryption configuration for
// ETCD using customer-managed keys.
// Visibility for the entire struct is "read create".
type CustomerManagedEncryptionProfile struct {
	EncryptionType CustomerManagedEncryptionType `json:"encryptionType,omitempty"`
	Kms            *KmsEncryptionProfile         `json:"kms,omitempty"`
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
	Name      string `json:"name"`
	VaultName string `json:"vaultName"`
	Version   string `json:"version"`
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
	ControlPlaneOperators  map[string]string `json:"controlPlaneOperators,omitempty"`
	DataPlaneOperators     map[string]string `json:"dataPlaneOperators,omitempty"`
	ServiceManagedIdentity string            `json:"serviceManagedIdentity,omitempty"`
}

// ClusterImageRegistryProfile - OpenShift cluster image registry
type ClusterImageRegistryProfile struct {
	// state indicates the desired ImageStream-backed cluster image registry installation mode. This can only be set during cluster
	// creation and cannot be changed after cluster creation. Enabled means the
	// ImageStream-backed image registry will be run as pods on worker nodes in the cluster. Disabled means the ImageStream-backed
	// image registry will not be present in the cluster. The default is Enabled.
	State ClusterImageRegistryProfileState `json:"state,omitempty"`
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

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftCluster represents an ARO HCP OpenShift cluster resource.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPOpenShiftCluster struct {
	// PartitionKey holds the lowercased subscriptionID.
	CosmosMetadata `json:"cosmosMetadata" redact:"nonsecret"`

	arm.TrackedResource

	CustomerProperties        HCPOpenShiftClusterCustomerProperties        `json:"customerProperties,omitempty" redact:"nonsecret"`
	ServiceProviderProperties HCPOpenShiftClusterServiceProviderProperties `json:"serviceProviderProperties,omitempty" redact:"nonsecret"`
	Identity                  *arm.ManagedServiceIdentity                  `json:"identity,omitempty" redact:"nonsecret"`
	Status                    HCPOpenShiftClusterStatus                    `json:"status" redact:"nonsecret"`
}

// HCPOpenShiftClusterStatus contains the observed state of the cluster.
type HCPOpenShiftClusterStatus struct {
	// Conditions are the top-level HCPOpenShiftCluster status conditions.
	// Each Condition Type represents a condition and it should be unique among all conditions.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" redact:"notraverse"`
}

var _ arm.CosmosPersistable = &HCPOpenShiftCluster{}

// HCPOpenShiftClusterCustomerProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterCustomerProperties struct {
	Version                 VersionProfile              `json:"version,omitempty" redact:"nonsecret"`
	DNS                     CustomerDNSProfile          `json:"dns,omitempty" redact:"nonsecret"`
	Network                 NetworkProfile              `json:"network,omitempty" redact:"nonsecret"`
	API                     CustomerAPIProfile          `json:"api,omitempty" redact:"nonsecret"`
	Ingress                 CustomerIngressProfile      `json:"ingress,omitempty" redact:"nonsecret"`
	Platform                CustomerPlatformProfile     `json:"platform,omitempty" redact:"nonsecret"`
	Autoscaling             ClusterAutoscalingProfile   `json:"autoscaling,omitempty" redact:"nonsecret"`
	NodeDrainTimeoutMinutes int32                       `json:"nodeDrainTimeoutMinutes,omitempty" redact:"nonsecret"`
	Etcd                    EtcdProfile                 `json:"etcd,omitempty" redact:"nonsecret"`
	ClusterImageRegistry    ClusterImageRegistryProfile `json:"clusterImageRegistry,omitempty" redact:"nonsecret"`
	ImageDigestMirrors      []ImageDigestMirror         `json:"imageDigestMirrors,omitempty" redact:"nonsecret"`
}

// HCPOpenShiftClusterCustomerProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterServiceProviderProperties struct {
	ProvisioningState            arm.ProvisioningState          `json:"provisioningState,omitempty" redact:"nonsecret"`
	ClusterServiceID             *InternalID                    `json:"clusterServiceID,omitempty" redact:"nonsecret"`
	ActiveOperationID            string                         `json:"activeOperationId,omitempty" redact:"nonsecret"`
	RevokeCredentialsOperationID string                         `json:"revokeCredentialsOperationId,omitempty" redact:"nonsecret"`
	DNS                          ServiceProviderDNSProfile      `json:"dns,omitempty" redact:"nonsecret"`
	Console                      ServiceProviderConsoleProfile  `json:"console,omitempty" redact:"nonsecret"`
	API                          ServiceProviderAPIProfile      `json:"api,omitempty" redact:"nonsecret"`
	Platform                     ServiceProviderPlatformProfile `json:"platform,omitempty" redact:"nonsecret"`

	// ExperimentalFeatures captures experimental feature state evaluated from
	// AFEC and per-resource tags. Stored in Cosmos but NOT exposed via ARM API.
	ExperimentalFeatures ExperimentalFeatures `json:"experimentalFeatures,omitzero" redact:"nonsecret"`
	// ManagedIdentitiesDataPlaneIdentityURL is the Managed Identities Data Plane
	// Identity URL associated with the cluster. It is the URL that will be used
	// to communicate with the Managed Identities Resource Provider (MI RP).
	// When an ARO-HCP Cluster is created, ARM sends a HTTP header X-Ms-Identity-Url
	// that contains the cluster's identity url. For ARO-HCP environments where
	// the Managed Identities Dataplane service is not available the http header
	// is set to a dummy value by our tools/testsuites/developers when
	// creating ARO-HCP Clusters
	ManagedIdentitiesDataPlaneIdentityURL string `json:"managedIdentitiesDataPlaneIdentityURL,omitempty" redact:"nonsecret"`
	ClusterUID                            string `json:"clusterUID,omitempty" redact:"nonsecret"`
	// BillingDocumentCosmosID is the Cosmos DB document ID of the billing document
	// associated with this cluster. It is set when the billing document is created
	// and used to avoid redundant creation attempts.
	BillingDocumentCosmosID string `json:"billingDocumentCosmosID,omitempty" redact:"nonsecret"`
	// DeletionTimestamp is the timestamp at which the Cluster deletion was requested.
	// The timestamp is in UTC.
	// A nil value indicates that the Cluster deletion has not been requested.
	DeletionTimestamp *metav1.Time `json:"deletionTimestamp,omitempty" redact:"nonsecret"`
	// ClusterServiceDeletionTimestamp is written when a dispatch of a Cluster
	// Service Delete Cluster request against Cluster Service for this cluster
	// has been handled. It is set after a successful DeleteCluster call to
	// Cluster Service, but also when it's determined that no delete call is
	// needed but we consider we should behave as if the delete call was
	// successfully issued.
	// A nil value indicates that the Cluster Service Deletion has not been requested.
	// The timestamp is in UTC.
	// TODO this attribute is not in use yet. Do not rely on it.
	ClusterServiceDeletionTimestamp *metav1.Time `json:"clusterServiceDeletionTimestamp,omitempty" redact:"nonsecret"`

	// TODO Temporary field to track whether the cluster operation is using the new deletion approach.
	// We are migrating from the cluster CS deletion synchronous in frontend to the backend, to be fully asynchronous.
	// This boolean is true for Cluster delete operations that are created with new deletion approach.
	// This will be removed once all clusters whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	UsesNewClusterDeletionApproach bool `json:"usesNewClusterDeletionApproach" redact:"nonsecret"`
}

// VersionProfile represents the cluster control plane version.
type VersionProfile struct {
	ID           string `json:"id,omitempty" redact:"nonsecret"`
	ChannelGroup string `json:"channelGroup,omitempty" redact:"nonsecret"`
}

// CustomerDNSProfile represents the DNS configuration of the cluster.
type CustomerDNSProfile struct {
	BaseDomainPrefix string `json:"baseDomainPrefix,omitempty" redact:"nonsecret"`
}

// ServiceProviderDNSProfile represents the DNS configuration of the cluster.
type ServiceProviderDNSProfile struct {
	BaseDomain string `json:"baseDomain,omitempty" redact:"nonsecret"`
}

// NetworkProfile represents a cluster network configuration.
// Visibility for the entire struct is "read create".
type NetworkProfile struct {
	NetworkType NetworkType `json:"networkType,omitempty" redact:"nonsecret"`
	PodCIDR     string      `json:"podCidr,omitempty" redact:"nonsecret"`
	ServiceCIDR string      `json:"serviceCidr,omitempty" redact:"nonsecret"`
	MachineCIDR string      `json:"machineCidr,omitempty" redact:"nonsecret"`
	HostPrefix  int32       `json:"hostPrefix,omitempty" redact:"nonsecret"`
}

// ServiceProviderConsoleProfile represents a cluster web console configuration.
// Visibility for the entire struct is "read".
type ServiceProviderConsoleProfile struct {
	URL string `json:"url,omitempty" redact:"nonsecret"`
}

// CustomerAPIProfile represents a cluster API server configuration.
type CustomerAPIProfile struct {
	Visibility      Visibility `json:"visibility,omitempty" redact:"nonsecret"`
	AuthorizedCIDRs []string   `json:"authorizedCidrs,omitempty" redact:"nonsecret"`
}

type ServiceProviderAPIProfile struct {
	URL string `json:"url,omitempty" redact:"nonsecret"`
}

// CustomerIngressProfile represents the cluster ingress configuration.
type CustomerIngressProfile struct {
	Type IngressType `json:"type,omitempty" redact:"nonsecret"`
}

// CustomerPlatformProfile represents the Azure platform configuration.
// Visibility for (almost) the entire struct is "read create".
type CustomerPlatformProfile struct {
	ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty" redact:"nonsecret"`
	SubnetID                *azcorearm.ResourceID          `json:"subnetId,omitempty" redact:"notraverse"`
	VnetIntegrationSubnetID *azcorearm.ResourceID          `json:"vnetIntegrationSubnetId,omitempty" redact:"notraverse"`
	OutboundType            OutboundType                   `json:"outboundType,omitempty" redact:"nonsecret"`
	NetworkSecurityGroupID  *azcorearm.ResourceID          `json:"networkSecurityGroupId,omitempty" redact:"notraverse"`
	OperatorsAuthentication OperatorsAuthenticationProfile `json:"operatorsAuthentication,omitempty" redact:"nonsecret"`
}

type ServiceProviderPlatformProfile struct {
	IssuerURL string `json:"issuerUrl,omitempty" redact:"nonsecret"`
}

// Cluster autoscaling configuration
// ClusterAutoscaling specifies auto-scaling behavior that
// applies to all NodePools associated with a control plane.
type ClusterAutoscalingProfile struct {
	MaxNodesTotal               int32 `json:"maxNodesTotal,omitempty" redact:"nonsecret"`
	MaxPodGracePeriodSeconds    int32 `json:"maxPodGracePeriodSeconds,omitempty" redact:"nonsecret"`
	MaxNodeProvisionTimeSeconds int32 `json:"maxNodeProvisionTimeSeconds,omitempty" redact:"nonsecret"`
	PodPriorityThreshold        int32 `json:"podPriorityThreshold,omitempty" redact:"nonsecret"`
}

// EtcdProfile represents an ETCD configuration.
// Visibility for the entire struct is "read create".
type EtcdProfile struct {
	DataEncryption EtcdDataEncryptionProfile `json:"dataEncryption,omitempty" redact:"nonsecret"`
}

// EtcdDataEncryptionProfile represents a data encryption configuration for ETCD.
// Visibility for the entire struct is "read create".
type EtcdDataEncryptionProfile struct {
	KeyManagementMode EtcdDataEncryptionKeyManagementModeType `json:"keyManagementMode,omitempty" redact:"nonsecret"`
	CustomerManaged   *CustomerManagedEncryptionProfile       `json:"customerManaged,omitempty" redact:"nonsecret"`
}

// CustomerManagedEncryptionProfile repesents a data encryption configuration for
// ETCD using customer-managed keys.
// Visibility for the entire struct is "read create".
type CustomerManagedEncryptionProfile struct {
	EncryptionType CustomerManagedEncryptionType `json:"encryptionType,omitempty" redact:"nonsecret"`
	Kms            *KmsEncryptionProfile         `json:"kms,omitempty" redact:"nonsecret"`
}

// KmsEncryptionProfile represents a data encryption configuration for ETCD using
// customer-managed Key Management Service (KMS) keys.
// Visibility for the entire struct is "read create".
type KmsEncryptionProfile struct {
	Visibility KeyVaultVisibility `json:"visibility,omitempty" redact:"nonsecret"`
	ActiveKey  KmsKey             `json:"activeKey,omitempty" redact:"nonsecret"`
}

// KmsKey represents an Azure KeyVault secret.
// Visibility for the entire struct is "read create".
type KmsKey struct {
	Name      string `json:"name" redact:"nonsecret"`
	VaultName string `json:"vaultName" redact:"nonsecret"`
	Version   string `json:"version" redact:"nonsecret"`
}

// OperatorsAuthenticationProfile represents authentication configuration for
// OpenShift operators.
// Visibility for the entire struct is "read create".
type OperatorsAuthenticationProfile struct {
	UserAssignedIdentities UserAssignedIdentitiesProfile `json:"userAssignedIdentities,omitempty" redact:"nonsecret"`
}

// UserAssignedIdentitiesProfile represents authentication configuration for
// OpenShift operators using user-assigned managed identities.
// Visibility for the entire struct is "read create".
type UserAssignedIdentitiesProfile struct {
	ControlPlaneOperators  map[string]*azcorearm.ResourceID `json:"controlPlaneOperators,omitempty" redact:"notraverse"`
	DataPlaneOperators     map[string]*azcorearm.ResourceID `json:"dataPlaneOperators,omitempty" redact:"notraverse"`
	ServiceManagedIdentity *azcorearm.ResourceID            `json:"serviceManagedIdentity,omitempty" redact:"notraverse"`
}

// ClusterImageRegistryProfile - OpenShift cluster image registry
type ClusterImageRegistryProfile struct {
	// state indicates the desired ImageStream-backed cluster image registry installation mode. This can only be set during cluster
	// creation and cannot be changed after cluster creation. Enabled means the
	// ImageStream-backed image registry will be run as pods on worker nodes in the cluster. Disabled means the ImageStream-backed
	// image registry will not be present in the cluster. The default is Enabled.
	State ClusterImageRegistryState `json:"state,omitempty" redact:"nonsecret"`
}

// ImageDigestMirror specifies image mirrors that can be used by cluster nodes
// to pull content.
type ImageDigestMirror struct {
	Source  string   `json:"source,omitempty" redact:"nonsecret"`
	Mirrors []string `json:"mirrors,omitempty" redact:"nonsecret"`

	// MirrorSourcePolicy is not exposed in the customer-facing API as of
	// v20251223preview, but is still recorded in CosmosDB so that, if we
	// ever do expose this field, existing cluster documents in CosmosDB
	// will not need to be migrated.
	MirrorSourcePolicy MirrorSourcePolicy `json:"mirrorSourcePolicy,omitempty" redact:"nonsecret"`
}

// Creates an HCPOpenShiftCluster with any non-zero default values.
func NewDefaultHCPOpenShiftCluster(resourceID *azcorearm.ResourceID, azureLocation string) *HCPOpenShiftCluster {
	return &HCPOpenShiftCluster{
		TrackedResource: arm.NewTrackedResource(resourceID, azureLocation),
		CustomerProperties: HCPOpenShiftClusterCustomerProperties{
			Version: VersionProfile{
				ChannelGroup: DefaultClusterVersionChannelGroup,
			},
			Network: NetworkProfile{
				NetworkType: NetworkTypeOVNKubernetes,
				PodCIDR:     DefaultClusterNetworkPodCIDR,
				ServiceCIDR: DefaultClusterNetworkServiceCIDR,
				MachineCIDR: DefaultClusterNetworkMachineCIDR,
				HostPrefix:  DefaultClusterNetworkHostPrefix,
			},
			API: CustomerAPIProfile{
				Visibility: VisibilityPublic,
			},
			Ingress: CustomerIngressProfile{
				Type: IngressTypePublic,
			},
			Platform: CustomerPlatformProfile{
				OutboundType: OutboundTypeLoadBalancer,
			},
			Autoscaling: ClusterAutoscalingProfile{
				MaxPodGracePeriodSeconds:    DefaultClusterMaxPodGracePeriodSeconds,
				MaxNodeProvisionTimeSeconds: DefaultClusterMaxNodeProvisionTimeSeconds,
				PodPriorityThreshold:        DefaultClusterPodPriorityThreshold,
			},
			//Even though PlatformManaged Mode is currently not supported by CS . This is the default value .
			Etcd: EtcdProfile{
				DataEncryption: EtcdDataEncryptionProfile{
					KeyManagementMode: EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
				},
			},
			ClusterImageRegistry: ClusterImageRegistryProfile{
				State: ClusterImageRegistryStateEnabled,
			},
		},
	}
}

// EnsureDefaults fills in default values for fields that may be absent in
// Cosmos documents created before the field was introduced, or on the create
// and preflight paths where the internal type is constructed from external input.
// Only fields where the zero value is never valid user input are safe to default
// here (string enums). See the DDR at docs/api-version-defaults-and-storage.md.
//
// This method should be treated as append-only. Avoid removing defaulting
// rules until all Cosmos documents have been verified to contain the field.
func (cluster *HCPOpenShiftCluster) EnsureDefaults() {
	if len(cluster.CustomerProperties.Network.NetworkType) == 0 {
		cluster.CustomerProperties.Network.NetworkType = NetworkTypeOVNKubernetes
	}
	if len(cluster.CustomerProperties.API.Visibility) == 0 {
		cluster.CustomerProperties.API.Visibility = VisibilityPublic
	}
	if len(cluster.CustomerProperties.Ingress.Type) == 0 {
		cluster.CustomerProperties.Ingress.Type = IngressTypePublic
	}
	if len(cluster.CustomerProperties.Platform.OutboundType) == 0 {
		cluster.CustomerProperties.Platform.OutboundType = OutboundTypeLoadBalancer
	}
	if len(cluster.CustomerProperties.ClusterImageRegistry.State) == 0 {
		cluster.CustomerProperties.ClusterImageRegistry.State = ClusterImageRegistryStateEnabled
	}
	if len(cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode) == 0 {
		cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = EtcdDataEncryptionKeyManagementModeTypePlatformManaged
	}
	for i := range cluster.CustomerProperties.ImageDigestMirrors {
		if len(cluster.CustomerProperties.ImageDigestMirrors[i].MirrorSourcePolicy) == 0 {
			cluster.CustomerProperties.ImageDigestMirrors[i].MirrorSourcePolicy = MirrorSourcePolicyAllowContactingSource
		}
	}
	// Default KMS Visibility to Public for clusters created via v2024_06_10_preview
	// (which doesn't expose the visibility field and assumes public KeyVaults).
	if cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil &&
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil &&
		len(cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility) == 0 {
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = KeyVaultVisibilityPublic
	}
}

func (o *HCPOpenShiftCluster) Validate() []arm.CloudErrorBody {
	return nil
}

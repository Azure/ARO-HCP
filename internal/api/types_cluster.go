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
	CosmosMetadata `json:"cosmosMetadata"`

	arm.TrackedResource

	// Written by: Frontend PUT/PATCH Cluster, ClusterBaseDomainPrefixSync
	CustomerProperties HCPOpenShiftClusterCustomerProperties `json:"customerProperties,omitempty"`
	// Written by: Frontend PUT/PATCH/DELETE Cluster, all Operation*Cluster controllers, ClusterPropertiesSync, ClusterClusterServiceCreate, ClusterDeletion* controllers, CreateBillingDoc
	ServiceProviderProperties HCPOpenShiftClusterServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster (Create/Update), IdentityMigration
	Identity *arm.ManagedServiceIdentity `json:"identity,omitempty"`
	// Written by: ClusterDegradedAggregator
	Status HCPOpenShiftClusterStatus `json:"status"`
}

// HCPOpenShiftClusterStatus contains the observed state of the cluster.
type HCPOpenShiftClusterStatus struct {
	// Conditions are the top-level HCPOpenShiftCluster status conditions.
	// Each Condition Type represents a condition and it should be unique among all conditions.
	// Written by: ClusterDegradedAggregator
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

var _ arm.CosmosPersistable = &HCPOpenShiftCluster{}

// HCPOpenShiftClusterCustomerProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterCustomerProperties struct {
	// Written by: Frontend PUT/PATCH Cluster
	Version VersionProfile `json:"version,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster, ClusterBaseDomainPrefixSync (BaseDomainPrefix only)
	DNS CustomerDNSProfile `json:"dns,omitempty"`
	// Written by: Frontend PUT Cluster (Create)
	Network NetworkProfile `json:"network,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	API CustomerAPIProfile `json:"api,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	Ingress CustomerIngressProfile `json:"ingress,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	Platform CustomerPlatformProfile `json:"platform,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	Autoscaling ClusterAutoscalingProfile `json:"autoscaling,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	NodeDrainTimeoutMinutes int32 `json:"nodeDrainTimeoutMinutes,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	NodeSshPublicKey string `json:"nodeSshPublicKey,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	Etcd EtcdProfile `json:"etcd,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	ClusterImageRegistry ClusterImageRegistryProfile `json:"clusterImageRegistry,omitempty"`
	// Written by: Frontend PUT/PATCH Cluster
	ImageDigestMirrors []ImageDigestMirror `json:"imageDigestMirrors,omitempty"`
}

// HCPOpenShiftClusterServiceProviderProperties represents the service-provider-managed property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterServiceProviderProperties struct {
	// Written by: Frontend PUT/PATCH/DELETE Cluster, OperationClusterCreate, OperationClusterUpdate, OperationClusterDelete
	ProvisioningState arm.ProvisioningState `json:"provisioningState,omitempty"`
	// Written by: Frontend PUT Cluster (Create), ClusterClusterServiceCreate, ClusterDeletionClusterServiceIDClearer
	ClusterServiceID *InternalID `json:"clusterServiceID,omitempty"`
	// Written by: Frontend PUT/PATCH/DELETE Cluster, OperationClusterCreate, OperationClusterUpdate, OperationClusterDelete
	ActiveOperationID string `json:"activeOperationId,omitempty"`
	// Written by: Frontend POST RevokeCredentials, OperationRevokeCredentials
	RevokeCredentialsOperationID string `json:"revokeCredentialsOperationId,omitempty"`
	// Written by: ClusterPropertiesSync
	DNS ServiceProviderDNSProfile `json:"dns,omitempty"`
	// Written by: ClusterPropertiesSync
	Console ServiceProviderConsoleProfile `json:"console,omitempty"`
	// Written by: ClusterPropertiesSync
	API ServiceProviderAPIProfile `json:"api,omitempty"`
	// Written by: ClusterPropertiesSync
	Platform ServiceProviderPlatformProfile `json:"platform,omitempty"`

	// ExperimentalFeatures captures experimental feature state evaluated from
	// AFEC and per-resource tags. Stored in Cosmos but NOT exposed via ARM API.
	// Written by: Frontend PUT Cluster (Create)
	ExperimentalFeatures ExperimentalFeatures `json:"experimentalFeatures,omitzero"`
	// ManagedIdentitiesDataPlaneIdentityURL is the Managed Identities Data Plane
	// Identity URL associated with the cluster. It is the URL that will be used
	// to communicate with the Managed Identities Resource Provider (MI RP).
	// Written by: Frontend PUT Cluster (Create)
	ManagedIdentitiesDataPlaneIdentityURL string `json:"managedIdentitiesDataPlaneIdentityURL,omitempty"`
	// Written by: BackfillClusterUID
	ClusterUID string `json:"clusterUID,omitempty"`
	// BillingDocumentCosmosID is the Cosmos DB document ID of the billing document
	// associated with this cluster. It is set when the billing document is created
	// and used to avoid redundant creation attempts.
	// Written by: CreateBillingDoc
	BillingDocumentCosmosID string `json:"billingDocumentCosmosID,omitempty"`
	// DeletionTimestamp is the timestamp at which the Cluster deletion was requested.
	// The timestamp is in UTC.
	// A nil value indicates that the Cluster deletion has not been requested.
	// Written by: Frontend DELETE Cluster
	DeletionTimestamp *metav1.Time `json:"deletionTimestamp,omitempty"`
	// ClusterServiceDeletionTimestamp is written when a dispatch of a Cluster
	// Service Delete Cluster request against Cluster Service for this cluster
	// has been handled. It is set after a successful DeleteCluster call to
	// Cluster Service, but also when it's determined that no delete call is
	// needed but we consider we should behave as if the delete call was
	// successfully issued.
	// A nil value indicates that the Cluster Service Deletion has not been requested.
	// The timestamp is in UTC.
	// Written by: ClusterClusterServiceDeleteDispatch
	ClusterServiceDeletionTimestamp *metav1.Time `json:"clusterServiceDeletionTimestamp,omitempty"`

	// TODO Temporary field to track whether the cluster operation is using the new deletion approach.
	// Written by: Frontend DELETE Cluster
	UsesNewClusterDeletionApproach bool `json:"usesNewClusterDeletionApproach"`

	// CreateOperationCompletionDeadline is the time by which the cluster creation operation must complete.
	// If it is not complete by this time, the operation will be marked as failed with the best message we can give at the time.
	// The default value is 60 minutes after the creation request is received.
	// When the subscription.HasRegisteredFeature(api.FeatureExperimentalReleaseFeatures), this value can be set
	// using the ExperimentalClusterTagPrefix + "max-creation-duration" tag, specified as a time.Duration.
	// The operation cluster create controller uses this value to decide about marking the install as failed.
	// The e2e tests set this value to one minute less than the default timeout.
	CreateOperationCompletionDeadline *metav1.Time `json:"createOperationCompletionDeadline,omitempty"`
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

// CustomerIngressProfile represents the cluster ingress configuration.
type CustomerIngressProfile struct {
	Type IngressType `json:"type,omitempty"`
}

// CustomerPlatformProfile represents the Azure platform configuration.
// Visibility for (almost) the entire struct is "read create".
type CustomerPlatformProfile struct {
	ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty"`
	SubnetID                *azcorearm.ResourceID          `json:"subnetId,omitempty"`
	VnetIntegrationSubnetID *azcorearm.ResourceID          `json:"vnetIntegrationSubnetId,omitempty"`
	OutboundType            OutboundType                   `json:"outboundType,omitempty"`
	NetworkSecurityGroupID  *azcorearm.ResourceID          `json:"networkSecurityGroupId,omitempty"`
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
	Visibility KeyVaultVisibility `json:"visibility,omitempty"`
	ActiveKey  KmsKey             `json:"activeKey,omitempty"`
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
	ControlPlaneOperators  map[string]*azcorearm.ResourceID `json:"controlPlaneOperators,omitempty"`
	DataPlaneOperators     map[string]*azcorearm.ResourceID `json:"dataPlaneOperators,omitempty"`
	ServiceManagedIdentity *azcorearm.ResourceID            `json:"serviceManagedIdentity,omitempty"`
}

// ClusterImageRegistryProfile - OpenShift cluster image registry
type ClusterImageRegistryProfile struct {
	// state indicates the desired ImageStream-backed cluster image registry installation mode. This can only be set during cluster
	// creation and cannot be changed after cluster creation. Enabled means the
	// ImageStream-backed image registry will be run as pods on worker nodes in the cluster. Disabled means the ImageStream-backed
	// image registry will not be present in the cluster. The default is Enabled.
	State ClusterImageRegistryState `json:"state,omitempty"`
}

// ImageDigestMirror specifies image mirrors that can be used by cluster nodes
// to pull content.
type ImageDigestMirror struct {
	Source  string   `json:"source,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`

	// MirrorSourcePolicy is not exposed in the customer-facing API as of
	// v20251223preview, but is still recorded in CosmosDB so that, if we
	// ever do expose this field, existing cluster documents in CosmosDB
	// will not need to be migrated.
	MirrorSourcePolicy MirrorSourcePolicy `json:"mirrorSourcePolicy,omitempty"`
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

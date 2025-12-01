import "@typespec/rest";
import "@typespec/http";
import "@typespec/versioning";
import "@azure-tools/typespec-azure-core";
import "@azure-tools/typespec-azure-resource-manager";
import "@azure-tools/typespec-client-generator-core";

using TypeSpec.Rest;
using TypeSpec.Versioning;
using Azure.Core;
using Azure.ResourceManager;
using Azure.ClientGenerator.Core;

namespace Microsoft.RedHatOpenShift;

/*
 * ===================================
 *   HCP cluster core resources
 * ===================================
 */

/** HCP cluster resource */
model HcpOpenShiftCluster is TrackedResource<HcpOpenShiftClusterProperties> {
  ...ResourceNameParameter<
    HcpOpenShiftCluster,
    NamePattern = "^[a-zA-Z][-a-zA-Z0-9]{1,52}[a-zA-Z0-9]$"
  >;
  ...ManagedServiceIdentityProperty;
}

// The NodePool needs to be TrackedResource for the following reasons:
// - allow tags to be in place, which allows billing to use tags, important for workers
// - allow nodepool to be tracked in the portal
// - deleted when resource group is deleted
// - allow cascade delete
// more: https://armwiki.azurewebsites.net/rp_onboarding/tracked_vs_proxy_resources.html
@parentResource(HcpOpenShiftCluster)
model NodePool is TrackedResource<NodePoolProperties> {
  ...ResourceNameParameter<
    NodePool,
    NamePattern = "^[a-zA-Z][-a-zA-Z0-9]{1,13}[a-zA-Z0-9]$"
  >;
  ...ManagedServiceIdentityProperty;
}

/** ExternalAuth resource */
@parentResource(HcpOpenShiftCluster)
model ExternalAuth is ProxyResource<ExternalAuthProperties> {
  ...ResourceNameParameter<
    ExternalAuth,
    NamePattern = "^[a-zA-Z][-a-zA-Z0-9]{1,15}$"
  >;
}

/** HCP cluster properties */
model HcpOpenShiftClusterProperties {
  /** The status of the last operation. */
  @visibility(Lifecycle.Read)
  provisioningState?: ProvisioningState;

  /** Version of the control plane components */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  version?: VersionProfile;

  /** Cluster DNS configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  dns?: DnsProfile;

  /** Cluster network configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  network?: NetworkProfile;

  /** Shows the cluster web console information */
  @visibility(Lifecycle.Read)
  console?: ConsoleProfile;

  /** Shows the cluster API server profile */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  api?: ApiProfile;

  /** Azure platform configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  platform: PlatformProfile;

  /** Configure ClusterAutoscaling . */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  autoscaling?: ClusterAutoscalingProfile;

  /** Configure ETCD. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  etcd?: EtcdProfile;

  /** nodeDrainTimeoutMinutes is the grace period for how long Pod Disruption Budget-protected workloads will be
   * respected during any node draining operation. After this grace period, any workloads protected by Pod Disruption
   * Budgets that have not been successfully drained from a node will be forcibly evicted. This is
   * especially relevant to cluster upgrades.
   *
   * Valid values are in minutes and from 0 to 10080 minutes (1 week).
   * 0 means that the MachinePool can be drained without any time limitation.
   *
   * This is the value is used a default for all NodePools. It can be overridden
   * by specifying nodeDrainTimeoutMinutes for a given NodePool
   */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @maxValue(10080)
  @minValue(0)
  nodeDrainTimeoutMinutes?: int32;

  // Optional features begin here. Each feature has
  // its own model with at least a "state" property.

  /** OpenShift internal image registry */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  clusterImageRegistry?: ClusterImageRegistryProfile;
}

/** The resource provisioning state. */
@lroStatus
union ProvisioningState {
  string,
  ResourceProvisioningState,

  /** Non-terminal state indicating the resource has been accepted */
  "Accepted",

  /** Non-terminal state indicating the resource is deleting */
  "Deleting",

  /** Non-terminal state indicating the resource is provisioning */
  "Provisioning",

  /** Non-terminal state indicating the resource is updating */
  "Updating",
}

/** The resource provisioning state. */
@lroStatus
union ExternalAuthProvisioningState {
  string,
  ProvisioningState,

  /** Non-terminal state indicating the resource is awaiting secret */
  "AwaitingSecret",
}

/** Versions represents an OpenShift version. */
model VersionProfile {
  /** ID is the unique identifier of the version. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  id?: string;

  /** ChannelGroup is the name of the set to which this version belongs.
   *  Each version belongs to only a single set.
   *
   *  If not specified, the default value is 'stable'.
   *
   *  Note: The default value is not declared in the API specification because
   *        of a TypeSpec bug with updatable fields. The default value will be
   *        declared in a future API version once the TypeSpec bug is fixed.
   *        https://github.com/Azure/typespec-azure/issues/1586
   */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  channelGroup?: string;
}

/** DNS contains the DNS settings of the cluster */
model DnsProfile {
  /** BaseDomain is the base DNS domain of the cluster. */
  @visibility(Lifecycle.Read)
  baseDomain?: string;

  /**  BaseDomainPrefix is the unique name of the cluster representing the OpenShift's cluster name.
   * BaseDomainPrefix is the name that will appear in the cluster's DNS, provisioned cloud providers resources
   * */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  @maxLength(15)
  @pattern(
    "^[a-z]([-a-z0-9]*[a-z0-9])?$",
    "Alphanumerics, underscores, and hyphens.  Must start and end with an alphanumeric."
  )
  baseDomainPrefix?: string;
}

/** OpenShift networking configuration */
model NetworkProfile {
  /** The main controller responsible for rendering the core networking components */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  networkType?: NetworkType = NetworkType.OVNKubernetes;

  /** The CIDR of the pod IP addresses */
  @clientName("podCIDR")
  @visibility(Lifecycle.Read, Lifecycle.Create)
  podCidr?: string = "10.128.0.0/14";

  /** The CIDR block for assigned service IPs */
  @clientName("serviceCIDR")
  @visibility(Lifecycle.Read, Lifecycle.Create)
  serviceCidr?: string = "172.30.0.0/16";

  /** The CIDR block from which to assign machine IP addresses */
  @clientName("machineCIDR")
  @visibility(Lifecycle.Read, Lifecycle.Create)
  machineCidr?: string = "10.0.0.0/16";

  /** Network host prefix */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  hostPrefix?: int32 = 23;
}

/** The cluster network type */
union NetworkType {
  string,

  /** The OVN network plugin for the OpenShift cluster */
  OVNKubernetes: "OVNKubernetes",

  /** Other network plugins */
  Other: "Other",
}

/** Configuration of the cluster web console */
model ConsoleProfile {
  /** The cluster web console URL endpoint */
  @visibility(Lifecycle.Read)
  url: url;
}

/** Information about the API of a cluster. */
model ApiProfile {
  /** URL endpoint for the API server */
  @visibility(Lifecycle.Read)
  url: url;

  /** The internet visibility of the OpenShift API server */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  visibility?: Visibility = Visibility.Public;

  /** The list of authorized IPv4 CIDR blocks allowed to access the API server. Maximum 500 entries. */
  @clientName("authorizedCIDRs")
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @maxItems(500)
  authorizedCidrs?: string[];
}

/** The internet visibility of the OpenShift API server */
union Visibility {
  string,

  /** The API server is visible from the internet. */
  Public: "Public",

  /** The API server is not visible from the internet. */
  Private: "Private",
}

/** Azure specific configuration */
model PlatformProfile {
  /** Resource group name to put cluster resources
   *
   * If not specified then a unique name is generated from the
   * following pattern
   *
   *   "aro-hcp-" + clusterName + "-" + UUID
   *
   * where clusterName means the hcpOpenShiftClusters resource name
   * (up to 45 characters) followed by a 16-byte universally unique
   * identifier per RFC 4122.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  managedResourceGroup?: string;

  /** The Azure resource ID of the worker subnet
   * Note that a subnet cannot be reused between ARO-HCP Clusters.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  subnetId: SubnetResourceId;

  /** The core outgoing configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  outboundType?: OutboundType = OutboundType.LoadBalancer;

  /** ResourceId for the NSG (network security group) attached to the cluster subnet
   *
   * Note that NSGs cannot be reused for other ARO-HCP clusters.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  networkSecurityGroupId: NetworkSecurityGroupResourceId;

  /** The configuration that the operators of the cluster have to authenticate to Azure */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  operatorsAuthentication: OperatorsAuthenticationProfile;

  /** URL for the OIDC provider to be used for authentication
   * to authenticate against user Azure cloud account
   */
  @visibility(Lifecycle.Read)
  issuerUrl: url;
}

/** ClusterAutoscaling specifies auto-scaling behavior that
 *  applies to all NodePools associated with a control plane. */
model ClusterAutoscalingProfile {
  /** maxNodesTotal is the maximum allowable number of nodes for the Autoscaler scale out to be operational.
   * The autoscaler will not grow the cluster beyond this number. If omitted, the autoscaler will not have a maximum limit. */
  @minValue(0)
  maxNodesTotal?: int32;

  /** maxPodGracePeriod is the maximum seconds to wait for graceful pod termination before scaling down a NodePool.
   * The default is 600 seconds.
   *
   *  Note: The default value is not declared in the API specification because
   *        of a TypeSpec bug with updatable fields. The default value will be
   *        declared in a future API version once the TypeSpec bug is fixed.
   *        https://github.com/Azure/typespec-azure/issues/1586
   */
  @minValue(0)
  maxPodGracePeriodSeconds?: int32;

  /** maxNodeProvisionTimeSeconds is the maximum time to wait for node provisioning before considering the
   *  provisioning to be unsuccessful. The default is 900 seconds, or 15 minutes.
   *
   *  Note: The default value is not declared in the API specification because
   *        of a TypeSpec bug with updatable fields. The default value will be
   *        declared in a future API version once the TypeSpec bug is fixed.
   *        https://github.com/Azure/typespec-azure/issues/1586
   */
  @minValue(0)
  maxNodeProvisionTimeSeconds?: int32;

  /** podPriorityThreshold enables users to schedule “best-effort” pods, which shouldn’t trigger autoscaler actions,
   * but only run when there are spare resources available. The default is -10.
   * See the following for more details:
   * https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption
   *
   *  Note: The default value is not declared in the API specification because
   *        of a TypeSpec bug with updatable fields. The default value will be
   *        declared in a future API version once the TypeSpec bug is fixed.
   *        https://github.com/Azure/typespec-azure/issues/1586
   */
  podPriorityThreshold?: int32;
}

/** OpenShift cluster image registry */
model ClusterImageRegistryProfile {
  /** state indicates the desired ImageStream-backed cluster image registry installation mode.
   * This can only be set during cluster creation and cannot be changed after cluster creation.
   * Enabled means the ImageStream-backed image registry will be run as pods on worker nodes in
   * the cluster. Disabled means the ImageStream-backed image registry will not be present in
   * the cluster. The default is Enabled. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  state?: "Enabled" | "Disabled" | string = "Enabled";
}

scalar SubnetResourceId
  extends Azure.Core.armResourceIdentifier<[
    {
      type: "Microsoft.Network/virtualNetworks/subnets",
    }
  ]>;

scalar NetworkSecurityGroupResourceId
  extends Azure.Core.armResourceIdentifier<[
    {
      type: "Microsoft.Network/networkSecurityGroups",
    }
  ]>;

/** The outbound routing strategy used to provide your cluster egress to the internet. */
union OutboundType {
  string,

  /** The load balancer configuration */
  LoadBalancer: "LoadBalancer",
}

/** The configuration that the operators of the cluster have to authenticate to Azure. */
model OperatorsAuthenticationProfile {
  /** Represents the information related to Azure User-Assigned managed identities needed
   * to perform Operators authentication based on Azure User-Assigned Managed Identities */
  userAssignedIdentities: UserAssignedIdentitiesProfile;
}

/** Represents the information related to Azure User-Assigned managed identities needed
 * to perform Operators authentication based on Azure User-Assigned Managed Identities */
model UserAssignedIdentitiesProfile {
  /** The set of Azure User-Assigned Managed Identities leveraged for the Control Plane
   * operators of the cluster. The set of required managed identities is dependent on the
   * Cluster's OpenShift version. */
  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record" "operator name to user assigned identity pairings"
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  controlPlaneOperators: Record<UserAssignedIdentityResourceId>;

  /** The set of Azure User-Assigned Managed Identities leveraged for the Data Plane
   * operators of the cluster. The set of required managed identities is dependent on the
   * Cluster's OpenShift version. */
  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record" "operator name to user assigned identity pairings"
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  dataPlaneOperators: Record<UserAssignedIdentityResourceId>;

  /** Represents the information associated to an Azure User-Assigned Managed Identity whose
   * purpose is to perform service level actions. */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  serviceManagedIdentity: UserAssignedIdentityResourceId;
}

scalar UserAssignedIdentityResourceId
  extends Azure.Core.armResourceIdentifier<[
    {
      type: "Microsoft.ManagedIdentity/userAssignedIdentities",
    }
  ]>;

/** A representation of a KeyVault Secret. */
model KmsKey {
  /** vaultName is the name of the keyvault that contains the secret. */
  @maxLength(255)
  @minLength(1)
  vaultName: string;

  /** name is the name of the keyvault key used for encryption/decryption. */
  @maxLength(255)
  @minLength(1)
  name: string;

  /** version contains the version of the key to use. */
  @maxLength(255)
  @minLength(1)
  version: string;
}

/** The ETCD settings and configuration options. */
model EtcdProfile {
  /** ETCD Data Encryption settings.
   * If not specified platform managed keys are used.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  dataEncryption?: EtcdDataEncryptionProfile;
}

/** The ETCD data encryption settings. */
model EtcdDataEncryptionProfile {
  /** Specify the key management strategy used for the encryption key that encrypts the ETCD data.
   * By default, "PlatformManaged" is used.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  keyManagementMode?: EtcdDataEncryptionKeyManagementModeType = EtcdDataEncryptionKeyManagementModeType.PlatformManaged;

  /** Specify customer managed encryption key details.
   * Required when keyManagementMode is "CustomerManaged".
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  customerManaged?: CustomerManagedEncryptionProfile;
}

/** Customer managed encryption key profile. */
model CustomerManagedEncryptionProfile {
  /** The encryption type used.
   * By default, "KMS" is used.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  encryptionType?: CustomerManagedEncryptionType = CustomerManagedEncryptionType.Kms;

  /** The Key Management Service (KMS) encryption key details.
   *
   * Required when encryptionType is "KMS".
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  kms?: KmsEncryptionProfile;
}

/** Configure etcd encryption Key Management Service (KMS) key.
 * Your Microsoft Entra application used to create the cluster must be authorized to access this keyvault,
 * e.g using the AzureCLI: `az keyvault set-policy -n $KEYVAULT_NAME --key-permissions decrypt encrypt --spn (YOUR APPLICATION CLIENT ID)`
 */
model KmsEncryptionProfile {
  /** The details of the active key. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  activeKey: KmsKey;
}

/** The encryption types supported for a customer managed key. */
union CustomerManagedEncryptionType {
  string,

  /** KMS encryption type. */
  Kms: "KMS",
}

/** The encryption key management mode types supported for ETCD data encryption. */
union EtcdDataEncryptionKeyManagementModeType {
  string,

  /** Customer managed encryption key management mode type. */
  CustomerManaged: "CustomerManaged",

  /** Platform managed encryption key management mode type. */
  PlatformManaged: "PlatformManaged",
}

/*
 * =======================================
 *   End HCP cluster core resources
 * =======================================
 */

/*
 * =======================================
 *  NodePool resources
 * =======================================
 */

/** Represents the node pool properties */
model NodePoolProperties {
  /** Provisioning state */
  @visibility(Lifecycle.Read)
  provisioningState?: ProvisioningState;

  /** OpenShift version for the nodepool */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  version?: NodePoolVersionProfile;

  /** Azure node pool platform configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  platform: NodePoolPlatformProfile;

  /**
   * The number of worker nodes, it cannot be used together with autoscaling.
   * Validation:
   * - Minimum: 0
   * - Maximum: 200 (only when availabilityZone is not specified)
   * - No maximum when availabilityZone is specified
   */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @minValue(0)
  replicas?: int32;

  /** Auto-repair */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  autoRepair?: boolean = true;

  /** Representation of a autoscaling in a node pool. */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  autoScaling?: NodePoolAutoScaling;

  /** Kubernetes labels to propagate to the NodePool Nodes
   * Note that when the labels are updated this is only applied to newly
   * create nodes in the Nodepool, existing node labels remain unchanged.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value"])
  labels?: Label[];

  /** Taints for the nodes */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value", "effect"])
  taints?: Taint[];

  /** nodeDrainTimeoutMinutes is the grace period for how long Pod Disruption Budget-protected workloads will be
   * respected during any node draining operation. After this grace period, any workloads protected by Pod Disruption
   * Budgets that have not been successfully drained from a node will be forcibly evicted. This is
   * especially relevant to cluster upgrades.
   *
   * Valid values are from 0 to 10080 minutes (1 week) .
   * 0 means that the NodePool can be drained without any time limitation.
   *
   * If unset the cluster nodeDrainTimeoutMinutes value is used as a default.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  nodeDrainTimeoutMinutes?: int32;
}

/** The taint effect the same as in Kubernetes */
union Effect {
  string,

  /** NoSchedule taint effect */
  NoSchedule: "NoSchedule",

  /** PreferNoSchedule taint effect */
  PreferNoSchedule: "PreferNoSchedule",

  /** NoExecute taint effect */
  NoExecute: "NoExecute",
}

/** Taint is controlling the node taint and its effects */
model Taint {
  /** The key of the taint
   * @example node-role.kubernetes.io/master
   */
  @minLength(1)
  @maxLength(316)
  key: string;

  /** The value of the taint
   * @example true
   */
  @maxLength(63)
  value?: string;

  /** The effect of the taint
   * @example NoSchedule
   */
  effect: Effect;
}

/** Label represents the Kubernetes label */
model Label {
  /** The key of the label
   * @example node-role.kubernetes.io/master
   */
  key: string;

  /** The value of the label */
  value?: string;
}

/** Versions represents an OpenShift version. */
model NodePoolVersionProfile {
  /** ID is the unique identifier of the version. */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  id?: string;

  /** ChannelGroup is the name of the set to which this version belongs.
   *  Each version belongs to only a single set.
   *
   *  If not specified, the default value is 'stable'.
   *
   *  Note: The default value is not declared in the API specification because
   *        of a TypeSpec bug with updatable fields. The default value will be
   *        declared in a future API version once the TypeSpec bug is fixed.
   *        https://github.com/Azure/typespec-azure/issues/1586
   */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  channelGroup?: string;
}

/** Azure node pool platform configuration */
model NodePoolPlatformProfile {
  /** The Azure resource ID of the worker subnet
   * Note that a subnet cannot be reused between ARO-HCP Clusters, however the
   * same subnet can be used for NodePools of the same cluster.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  subnetId?: SubnetResourceId;

  /** The VM size according to the documentation:
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/sizes */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  vmSize: string;

  /** Whether to enable host based OS and data drive encryption.
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#encryption-at-host---end-to-end-encryption-for-your-vm-data
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  enableEncryptionAtHost?: boolean = false;

  /** The settings and configuration options for OSDisk */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  osDisk?: OsDiskProfile;

  /** The availability zone for the node pool.
   * Please read the documentation to see which regions support availability zones
   * - https://learn.microsoft.com/en-us/azure/availability-zones/az-overview
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  availabilityZone?: string;
}

/** The settings and configuration options for OSDisk */
model OsDiskProfile {
  /** The OS disk size in GiB (minValue updated to 64 in 2025-12-23-preview) */
  @removed(Versions.v2025_12_23_preview)
  @visibility(Lifecycle.Read, Lifecycle.Create)
  @minValue(1)
  @renamedFrom(Versions.v2025_12_23_preview, "sizeGiB")
  oldSizeGiB?: int32 = 64;

  /** The OS disk size in GiB */
  @added(Versions.v2025_12_23_preview)
  @visibility(Lifecycle.Read, Lifecycle.Create)
  @minValue(64)
  sizeGiB?: int32 = 64;

  /** The type of the disk storage account
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  diskStorageAccountType?: DiskStorageAccountType = "Premium_LRS";

  /** The ID of the DiskEncryptionSet resource to use to encrypt the OS disks for the VMs.
   * This needs to exist in the same subscription id listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.SubscriptionID.
   * DiskEncryptionSetID should also exist in a resource group under the same subscription id and the same location
   * listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.Location.
   *
   * Details on how to create a Disk Encryption Set can be found here:
   * https://learn.microsoft.com/en-us/azure/virtual-machines/disks-enable-customer-managed-keys-portal#set-up-your-disk-encryption-set
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  encryptionSetId?: DiskEncryptionSetResourceId;
}

/** Supported Azure storage account types
 * - https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types
 */
union DiskStorageAccountType {
  string,

  /** Premium SSD with Locally Redundant Storage (LRS) */
  Premium_LRS: "Premium_LRS",

  /** Standard SSD with Locally Redundant Storage (LRS) */
  StandardSSD_LRS: "StandardSSD_LRS",

  /** Standard HDD with Locally Redundant Storage (LRS) */
  Standard_LRS: "Standard_LRS",
}

/** Node pool autoscaling */
model NodePoolAutoScaling {
  /**
   * The minimum number of nodes in the node pool.
   * Validation:
   * - Minimum: 0
   * - Maximum: 200 (only when availabilityZone is not specified)
   * - No maximum when availabilityZone is specified
   */
  @minValue(0)
  min?: int32;

  /**
   * The maximum number of nodes in the node pool.
   * Validation:
   * - Minimum: 0 (must be >= min)
   * - Maximum: 200 (only when availabilityZone is not specified)
   * - No maximum when availabilityZone is specified
   */
  @minValue(0)
  max?: int32;
}

scalar DiskEncryptionSetResourceId
  extends Azure.Core.armResourceIdentifier<[
    {
      type: "Microsoft.Compute/diskEncryptionSets",
    }
  ]>;

/*
 * =======================================
 * End NodePool resources
 * =======================================
 */

/*
 * =======================================
 *   HCP cluster credentials
 * =======================================
 */

/** HCP cluster admin credential */
model HcpOpenShiftClusterAdminCredential {
  /** Admin kubeconfig with a temporary client certificate */
  @visibility(Lifecycle.Read)
  @secret
  kubeconfig: string;

  /** Expiration timestamp for the kubeconfig's client certificate */
  @visibility(Lifecycle.Read)
  expirationTimestamp: utcDateTime;
}

/*
 * =======================================
 *   End HCP cluster credentials
 * =======================================
 */

/*
 * =======================================
 *  ExternalAuth resources
 * =======================================
 */

/** External Auth profile */
model ExternalAuthProperties {
  /** Provisioning state */
  @visibility(Lifecycle.Read)
  provisioningState?: ExternalAuthProvisioningState;

  /** An observation of the current state with additional information. */
  @visibility(Lifecycle.Read)
  condition?: ExternalAuthCondition;

  /** Token Issuer profile */
  issuer: TokenIssuerProfile;

  /** External Auth OIDC clients
   * There must not be more than 20 entries and entries must have unique namespace/name pairs.
   */
  @maxItems(20)
  @OpenAPI.extension(
    "x-ms-identifiers",
    #["component", "clientId", "extraScopes"]
  )
  clients?: ExternalAuthClientProfile[];

  /** External Auth claim
   * This configures how claims are validated and applied.
   */
  claim: ExternalAuthClaimProfile;
}

/** Condition defines an observation of the external auth state. */
model ExternalAuthCondition {
  /** This is a PascalCase (or in foo.example.com/PascalCase) code to represent the type of condition. */
  @visibility(Lifecycle.Read)
  type: ExternalAuthConditionType;

  /** The status of the condition. */
  @visibility(Lifecycle.Read)
  status: StatusType;

  /** The last time the condition transitioned from one status to another. */
  @visibility(Lifecycle.Read)
  lastTransitionTime: utcDateTime;

  /** This contains a programmatic identifier indicating the reason for the condition's last transition. */
  @visibility(Lifecycle.Read)
  reason: string;

  /** This is a human readable message indicating details about the transition.
   * This may be an empty string.
   */
  @visibility(Lifecycle.Read)
  message: string;
}

/** Representation of the possible values of a external auths condition status. */
union StatusType {
  string,

  /** Indicates that the condition status is True. */
  True: "True",

  /** Indicates that the condition status is False. */
  False: "False",

  /** Indicates that the condition status is unknown. */
  Unknown: "Unknown",
}

/** Representation of the possible types of a external auths condition. */
union ExternalAuthConditionType {
  string,

  /** Indicates that the external auth resource is available. */
  Available: "Available",

  /** Indicates that the external auth resource is in a degraded state. */
  Degraded: "Degraded",

  /** Indicates that the external auth resource is in a progressing state. */
  Progressing: "Progressing",
}

/** Token issuer profile
 * This configures how the platform interacts with the identity provider and
 * how tokens issued from the identity provider are evaluated by the Kubernetes API server.
 */
model TokenIssuerProfile {
  /** This configures the URL used to issue tokens by the identity provider.
   * The Kubernetes API server determines how authentication tokens should be handled
   * by matching the 'iss' claim in the JWT to the issuerURL of configured identity providers.
   *
   * issuerURL must use the 'https' scheme.
   * */
  url: url;

  /** This configures the acceptable audiences the JWT token, issued by the identity
   * provider, must be issued to. At least one of the entries must match the
   * 'aud' claim in the JWT token.
   *
   * audiences must contain at least one entry and must not exceed ten entries.
   */
  @minItems(1)
  @maxItems(10)
  audiences: string[];

  /** The issuer of the token
   *
   * Certificate bundle to use to validate server certificates for the configured URL.
   * It must be PEM encoded and when not specified, the system trust is used.
   */
  @clientName("CA")
  ca?: string;
}

/** External Auth client profile
 * This configures how on-cluster, platform clients should request tokens from the identity provider.
 */
model ExternalAuthClientProfile {
  /** External Auth client component */
  component: ExternalAuthClientComponentProfile;

  /** External Auth client id
   * The clientId must appear in the audience field of the TokenIssuerProfile.
   */
  @minLength(1)
  clientId: string;

  /** external auth client scopes
   *
   * This is useful if you have configured claim mappings that requires specific
   * scopes to be requested beyond the standard OIDC scopes.
   * When omitted, no additional scopes are requested.
   */
  extraScopes?: string[];

  /** Determines the OIDC provider client type. */
  type: ExternalAuthClientType;
}

/** Representation of the possible values of an external authentication client's type */
union ExternalAuthClientType {
  string,

  /** Indicates that the client is confidential.
   * Confidential clients must provide a client secret.
   * The secret should be provided within the cluster itself.
   */
  Confidential: "Confidential",

  /** Indicates that the client is public.
   * Public clients must not provide a client secret.
   */
  Public: "Public",
}

/** External Auth component profile
 * Must have unique namespace/name pairs.
 */
model ExternalAuthClientComponentProfile {
  /** The name of the external auth client
   *
   * This specifies the name of the platform component being configured to use
   * the identity provider as an authentication mode.
   * It is used in combination with namespace as a unique identifier.
   */
  @minLength(1)
  @maxLength(256)
  name: string;

  /** The namespace of the external Auth client
   * This specifies the namespace in which the platform component being configured
   * to use the identity provider as an authentication mode is running.
   *
   * It is used in combination with name as a unique identifier.
   */
  @minLength(1)
  @maxLength(63)
  authClientNamespace: string;
}

/** External Auth claim profile */
model ExternalAuthClaimProfile {
  /** The claim mappings */
  mappings: TokenClaimMappingsProfile;

  /** The claim validation rules */
  @OpenAPI.extension("x-ms-identifiers", #["type", "requiredClaim"])
  validationRules?: TokenClaimValidationRule[];
}

/** External Auth claim mappings profile.
 * At a minimum username or groups must be defined.
 */
model TokenClaimMappingsProfile {
  /** The claim mappings username. */
  username: UsernameClaimProfile;

  /** The claim mappings groups. */
  groups?: GroupClaimProfile;
}

/** External Auth claim profile
 * This configures how the groups of a cluster identity should be constructed
 * from the claims in a JWT token issued by the identity provider. When
 * referencing a claim, if the claim is present in the JWT token, its value
 * must be a list of groups separated by a comma (',').
 *
 * For example - '"example"' and '"exampleOne", "exampleTwo", "exampleThree"' are valid claim values.
 */
model GroupClaimProfile {
  /** Claim name of the external profile */
  @minLength(1)
  @maxLength(256)
  claim: string;

  /** Prefix for the claim external profile
   * If this is specified prefixPolicy will be set to "Prefix" by default
   */
  prefix?: string;
}

/** External Auth claim profile
 * This configures how the username of a cluster identity should be constructed
 * from the claims in a JWT token issued by the identity provider.
 */
model UsernameClaimProfile {
  /** Claim name of the external profile */
  @minLength(1)
  @maxLength(256)
  claim: string;

  /** Prefix for the claim external profile
   * Must be set when the prefixPolicy field is set to 'Prefix' and must be unset
   * otherwise.
   */
  prefix?: string;

  /** Prefix policy is an optional field that configures how a prefix should be
   * applied to the value of the JWT claim specified in the 'claim' field.
   *
   * Allowed values are 'Prefix', 'NoPrefix', and 'None'. If not specified, the
   * default policy is 'None'.
   *
   * When set to 'Prefix', the value specified in the prefix field will be
   * prepended to the value of the JWT claim.
   * The prefix field must be set when prefixPolicy is 'Prefix'.
   *
   * When set to 'NoPrefix', no prefix will be prepended to the value
   * of the JWT claim.
   *
   * When set to 'None', this means no opinion and the platform is left to choose
   * any prefixes that are applied which is subject to change over time.
   * Currently, the platform prepends `{issuerURL}#` to the value of the JWT claim
   * when the claim is not 'email'.
   * As an example, consider the following scenario:
   *    `prefix` is unset, `issuerURL` is set to `https://myoidc.tld`,
   *    the JWT claims include "username":"userA" and "email":"userA@myoidc.tld",
   *    and `claim` is set to:
   *    - "username": the mapped value will be "https://myoidc.tld#userA"
   *    - "email": the mapped value will be "userA@myoidc.tld"
   *
   * Note: The default value is not declared in the API specification because
   *       of a TypeSpec bug with updatable fields. The default value will be
   *       declared in a future API version once the TypeSpec bug is fixed.
   *       https://github.com/Azure/typespec-azure/issues/1586
   */
  prefixPolicy?: UsernameClaimPrefixPolicy;
}

/** UsernameClaimPrefixPolicy configures whether to add a prefix to a JWT claim. */
union UsernameClaimPrefixPolicy {
  string,

  /** No opinion; the platform is left to choose a prefix for the JWT claim */
  None: "None",

  /** Add a user-provided prefix to the JWT claim */
  Prefix: "Prefix",

  /** Do not add a prefix to the JWT claim */
  NoPrefix: "NoPrefix",
}

/** External Auth claim validation rule */
model TokenClaimValidationRule {
  /** This configures the type of the validation rule.
   * It defaults to "RequiredClaim"
   */
  type?: TokenValidationRuleType = TokenValidationRuleType.RequiredClaim;

  /** The required claim rule to be applied. */
  requiredClaim?: TokenRequiredClaim;
}

/** Token required claim validation rule. */
model TokenRequiredClaim {
  /** Claim name for the validation profile
   * claim is a required field that configures the name of the required claim.
   * */
  @minLength(1)
  claim: string;

  /** Required value
   * requiredValue is a required field that configures the value that 'claim' must
   * have when taken from the incoming JWT claims.
   * If the value in the JWT claims does not match, the token
   * will be rejected for authentication.
   */
  @minLength(1)
  requiredValue: string;
}

/** Representation of the different claim validation rule types that can be configured. */
union TokenValidationRuleType {
  string,

  /** Indicates that a Required Claim validation rule.
   *
   * When set to 'RequiredClaim', the Kubernetes API server will be configured to validate that the incoming JWT
   * contains the required claim and that its value matches the required value.
   */
  RequiredClaim: "RequiredClaim",
}

/*
 * =======================================
 *  End ExternalAuth resources
 * =======================================
 */

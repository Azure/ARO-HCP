import "@typespec/rest";
import "@typespec/http";
import "@azure-tools/typespec-azure-core";
import "@azure-tools/typespec-azure-resource-manager";

using TypeSpec.Rest;
using TypeSpec.Http;
using Azure.Core;
using Azure.ResourceManager;

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

  /** Configure cluster capabilities. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  capabilities?: ClusterCapabilitiesProfile;

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
  nodeDrainTimeoutMinutes?: int32 = 0;
}
/** Cluster capabilities configuration. */
model ClusterCapabilitiesProfile {
  /**
   * Immutable list of disabled capabilities. May only contain "ImageRegistry" at
   * this time. Additional capabilities may be available in the future. Clients
   * should expect to handle additional values.
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  disabled?: OptionalClusterCapability[];
}

/** Cluster capabilities that can be disabled. */
union OptionalClusterCapability {
  string,

  /** Enables the OpenShift internal image registry. */
  ImageRegistry: "ImageRegistry",
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

/** Versions represents an OpenShift version. */
model VersionProfile {
  /** ID is the unique identifier of the version. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  id?: string;

  /** ChannelGroup is the name of the set to which this version belongs. Each version belongs to only a single set. */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  channelGroup?: string = "stable";
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
  @visibility(Lifecycle.Read, Lifecycle.Create)
  podCidr?: string = "10.128.0.0/14";

  /** The CIDR block for assigned service IPs */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  serviceCidr?: string = "172.30.0.0/16";

  /** The CIDR block from which to assign machine IP addresses */
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
  /** Resource group to put cluster resources */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  managedResourceGroup?: string;

  /** The Azure resource ID of the worker subnet */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  subnetId: SubnetResourceId;

  /** The core outgoing configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  outboundType?: OutboundType = OutboundType.LoadBalancer;

  /** ResourceId for the network security group attached to the cluster subnet */
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
   * The autoscaler will not grow the cluster beyond this number. If omitted, the autoscaler will not have a maximum limit. number. */
  @minValue(0)
  maxNodesTotal?: int32;

  /** maxPodGracePeriod is the maximum seconds to wait for graceful pod termination before scaling down a NodePool.
   * The default is 600 seconds. */
  @minValue(0)
  maxPodGracePeriodSeconds?: int32 = 600;

  /** maxNodeProvisionTimeSeconds is the maximum time to wait for node provisioning before considering the
   *  provisioning to be unsuccessful. The default is 900 seconds, or 15 minutes. */
  @minValue(0)
  maxNodeProvisionTimeSeconds?: int32 = 900;

  /** podPriorityThreshold enables users to schedule “best-effort” pods, which shouldn’t trigger autoscaler actions,
   * but only run when there are spare resources available. The default is -10.
   * See the following for more details:
   * https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption */
  podPriorityThreshold?: int32 = -10;
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
 * e.g using the AzureCLI: `az keyvault set-policy -n $KEYVAULT_NAME --key-permissions decrypt encrypt --spn <YOUR APPLICATION CLIENT ID>`
 *
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

  /** The number of worker nodes, it cannot be used together with autoscaling */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  replicas?: int32;

  /** Auto-repair */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  autoRepair?: boolean = true;

  /** Representation of a autoscaling in a node pool. */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  autoScaling?: NodePoolAutoScaling;

  /** Kubernetes labels to propagate to the NodePool Nodes */
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
  @minLength(1)
  @maxLength(63)
  value: string;

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

  /** ChannelGroup is the name of the set to which this version belongs. Each version belongs to only a single set. */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  channelGroup?: string = "stable";
}

/** Azure node pool platform configuration */
model NodePoolPlatformProfile {
  /** The Azure resource ID of the worker subnet */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  subnetId?: string;

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
  osDiskProfile?: OsDiskProfile;

  /** The availability zone for the node pool.
   * Please read the documentation to see which regions support availability zones
   * - https://learn.microsoft.com/en-us/azure/availability-zones/az-overview
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  availabilityZone?: string;
}

/** The settings and configuration options for OSDisk */
model OsDiskProfile {
  /** The OS disk size in GiB */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  diskSizeGiB?: int32 = 64;

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
  @maxLength(285)
  diskEncryptionSetId?: DiskEncryptionSetResourceId;
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
  /** The minimum number of nodes in the node pool */
  @minValue(0)
  min?: int32;

  /** The maximum number of nodes in the node pool */
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

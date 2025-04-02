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
  @visibility(Lifecycle.Create, Lifecycle.Read)
  version?: VersionProfile;

  /** Cluster DNS configuration */
  dns?: DnsProfile;

  /** Cluster network configuration */
  @visibility(Lifecycle.Create, Lifecycle.Read)
  network?: NetworkProfile;

  /** Shows the cluster web console information */
  @visibility(Lifecycle.Read)
  console?: ConsoleProfile;

  /** Shows the cluster API server profile */
  @visibility(Lifecycle.Read)
  api?: ApiProfile;

  /** Disable user workload monitoring */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  disableUserWorkloadMonitoring?: boolean = false;

  /** Azure platform configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  platform: PlatformProfile;

  /** Configure cluter capabilities. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  capabilities?: ClusterCapabilitiesProfile;
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

  /** AvailableUpgrades is a list of version names the current version can be upgraded to. */
  @visibility(Lifecycle.Read)
  availableUpgrades: string[];
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
  visibility?: Visibility = "public";
}

/** The internet visibility of the OpenShift API server */
union Visibility {
  string,

  /** The API server is visible from the internet. */
  public: "public",

  /** The API server is not visible from the internet. */
  private: "private",
}

/** Azure specific configuration */
model PlatformProfile {
  /** Resource group to put cluster resources */
  managedResourceGroup?: string;

  /** The Azure resource ID of the worker subnet */
  subnetId: SubnetResourceId;

  /** The core outgoing configuration */
  outboundType?: OutboundType = OutboundType.loadBalancer;

  /** ResourceId for the network security group attached to the cluster subnet */
  networkSecurityGroupId: NetworkSecurityGroupResourceId;

  /** The configuration that the operators of the cluster have to authenticate to Azure */
  operatorsAuthentication: OperatorsAuthenticationProfile;

  /** URL for the OIDC provider to be used for authentication
   * to authenticate against user Azure cloud account
   */
  @visibility(Lifecycle.Read)
  issuerUrl: url;
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
  loadBalancer: "loadBalancer",
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
  @visibility(Lifecycle.Read, Lifecycle.Create)
  controlPlaneOperators: Record<UserAssignedIdentityResourceId>;

  /** The set of Azure User-Assigned Managed Identities leveraged for the Data Plane
   * operators of the cluster. The set of required managed identities is dependent on the
   * Cluster's OpenShift version. */
  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record" "operator name to user assigned identity pairings"
  @visibility(Lifecycle.Read, Lifecycle.Create)
  dataPlaneOperators: Record<UserAssignedIdentityResourceId>;

  /** Represents the information associated to an Azure User-Assigned Managed Identity whose
   * purpose is to perform service level actions. */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  serviceManagedIdentity: UserAssignedIdentityResourceId;
}

scalar UserAssignedIdentityResourceId
  extends Azure.Core.armResourceIdentifier<[
    {
      type: "Microsoft.ManagedIdentity/userAssignedIdentities",
    }
  ]>;

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
  @visibility(Lifecycle.Read, Lifecycle.Create)
  version?: NodePoolVersionProfile;

  /** Azure node pool platform configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  platform: NodePoolPlatformProfile;

  /** The number of worker nodes, it cannot be used together with autoscaling */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  replicas?: int32;

  /** Auto-repair */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  autoRepair?: boolean = false;

  /** Representation of a autoscaling in a node pool. */
  autoScaling?: NodePoolAutoScaling;

  // This warning means to ensure good customer experience, to not create
  // poorly defined types. However here the type literally is the map<string, value>
  /** K8s labels to propagate to the NodePool Nodes
   * The good example of the label is `node-role.kubernetes.io/master: ""`
   */
  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record" ""
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value"])
  labels?: Label[];

  /** Taints for the nodes */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value", "effect"])
  taints?: Taint[];
}

/** taintKey is the k8s valid key of the taint type on the nodepool nodes
 * @example node-role.kubernetes.io/master
 */
@minLength(1)
@maxLength(316)
scalar taintKey extends string;

/** taintValue is the k8s valid value of the taint type on the nodepool nodes
 * @example true
 */
@minLength(1)
@maxLength(63)
scalar taintValue extends string;

/** The taint effect the same as in K8s */
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
  key?: taintKey;

  /** The value of the taint
   * @example true
   */
  value?: taintValue;

  /** The effect of the taint
   * @example NoSchedule
   */
  effect?: Effect;
}

/** Label represents the k8s label */
model Label {
  /** The key of the label */
  key?: string;

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

  /** AvailableUpgrades is a list of version names the current version can be upgraded to. */
  @visibility(Lifecycle.Read)
  availableUpgrades: string[];
}

/** Azure node pool platform configuration */
model NodePoolPlatformProfile {
  /** The Azure resource ID of the worker subnet */
  subnetId?: string;

  /** The VM size according to the documentation:
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/sizes */
  vmSize: string;

  /** The OS disk size in GiB */
  diskSizeGiB?: int32;

  /** The type of the disk storage account
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types
   */
  diskStorageAccountType?: DiskStorageAccountType = "Premium_LRS";

  /** The availability zone for the node pool.
   * Please read the documentation to see which regions support availability zones
   * - https://learn.microsoft.com/en-us/azure/availability-zones/az-overview
   */
  availabilityZone?: string;
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

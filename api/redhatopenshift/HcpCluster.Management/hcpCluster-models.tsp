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
model HcpOpenShiftClusterResource
  is TrackedResource<HcpOpenShiftClusterProperties> {
  /** Name of HCP cluster */
  @pattern("^[a-zA-Z][a-zA-Z0-9-]$")
  @minLength(3)
  @maxLength(54)
  @key("hcpOpenShiftClusterName") // sets the alternative name for the name propery
  @path
  @segment("hcpOpenShiftClusters")
  name: string;

  ...ManagedServiceIdentityProperty;
}

// The NodePool needs to be TrackedResource for the following reasons:
// - allow tags to be in place, which allows billing to use tags, important for workers
// - allow nodepool to be tracked in the portal
// - deleted when resource group is deleted
// - allow cascade delete
// more: https://armwiki.azurewebsites.net/rp_onboarding/tracked_vs_proxy_resources.html
@parentResource(HcpOpenShiftClusterResource)
model HcpOpenShiftClusterNodePoolResource
  is TrackedResource<NodePoolProperties> {
  /** Name of HCP cluster * */
  @pattern("^[a-zA-Z][a-zA-Z0-9-]$")
  @minLength(3)
  @maxLength(15)
  @key("nodePoolName")
  @path
  @segment("nodePools")
  name: string;

  ...ManagedServiceIdentityProperty;
}

/** HCP cluster properties */
model HcpOpenShiftClusterProperties {
  /** The status of the last operation. */
  @visibility("read")
  provisioningState?: ProvisioningState;

  /** Version of the control plane components */
  @visibility("create", "read")
  version: VersionProfile;

  /** Cluster DNS configuration */
  dns?: DnsProfile;

  /** Cluster network configuration */
  @visibility("create", "read")
  network?: NetworkProfile;

  /** Shows the cluster web console information */
  @visibility("read")
  console: ConsoleProfile;

  /** Shows the cluster API server profile */
  @visibility("read")
  api: ApiProfile;

  /** Disable user workload monitoring */
  @visibility("create", "update")
  disableUserWorkloadMonitoring?: boolean = false;

  /** Azure platform configuration */
  @visibility("create", "read")
  platform?: PlatformProfile;

  /** URL for the OIDC provider to be used for authentication
   * to authenticate against user Azure cloud account
   */
  @visibility("read")
  issuerUrl: url;
}

/** HCP patchable cluster properties */
model HcpOpenShiftClusterPatchProperties {
  /** The status of the last operation. */
  @visibility("read")
  provisioningState?: ProvisioningState;

  /** Disable user workload monitoring */
  @visibility("update")
  disableUserWorkloadMonitoring?: boolean;
}

model ManagedServiceIdentityUpdate
  is OptionalProperties<UpdateableProperties<Azure.ResourceManager.Foundations.ManagedServiceIdentity>>;

model HcpOpenShiftClusterPatch
  is OptionalProperties<UpdateableProperties<OmitProperties<
    HcpOpenShiftClusterResource,
    "name" | "properties" | "identity"
  >>> {
  /** HCP patchable cluster properties */
  properties?: HcpOpenShiftClusterPatchProperties;

  /** Managed service identity */
  identity?: ManagedServiceIdentityUpdate;
}

model HcpOpenShiftClusterNodePoolPatch
  is OptionalProperties<UpdateableProperties<OmitProperties<
    HcpOpenShiftClusterNodePoolResource,
    "name" | "properties" | "identity"
  >>> {
  /** Represents the patchable node pool properties */
  properties?: NodePoolPatchProperties;

  /** Managed Service Identity */
  identity?: ManagedServiceIdentityUpdate;
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
  @visibility("create", "read")
  id: string;

  /** ChannelGroup is the name of the set to which this version belongs. Each version belongs to only a single set. */
  @visibility("create", "read")
  channelGroup: string;

  /** AvailableUpgrades is a list of version names the current version can be upgraded to. */
  @visibility("read")
  availableUpgrades: string[];
}

/** DNS contains the DNS settings of the cluster */
model DnsProfile {
  /** BaseDomain is the base DNS domain of the cluster. */
  @visibility("read")
  baseDomain?: string;

  /**  BaseDomainPrefix is the unique name of the cluster representing the OpenShift's cluster name.
   * BaseDomainPrefix is the name that will appear in the cluster's DNS, provisioned cloud providers resources
   * */
  @visibility("create", "read")
  @maxLength(15)
  @pattern(
    "^[a-z]([-a-z0-9]*[a-z0-9])?$",
    "Alphanumerics, underscores, and hyphens.  Must start and end with an alphanumeric."
  )
  baseDomainPrefix?: string;
}

/** Network profile of the cluster */
model NetworkProfile {
  /** The main controller responsible for rendering the core networking components */
  @visibility("create", "read")
  networkType?: NetworkType = NetworkType.OVNKubernetes;

  /** The CIDR of the pod IP addresses
   * example: 10.128.0.0/14
   */
  @visibility("create", "read")
  podCidr: string;

  /** The CIDR block for assigned service IPs,
   * example: 172.30.0.0/16
   */
  @visibility("create", "read")
  serviceCidr: string;

  /** from which to assign machine IP addresses,
   * example: 10.0.0.0/16
   */
  @visibility("create", "read")
  machineCidr: string;

  /** Network host prefix which is defaulted to 23 if not specified. */
  @visibility("create", "read")
  hostPrefix?: int32 = 23;
}

/** The cluster network type */
union NetworkType {
  string,

  /** THE OVN network plugin for the OpenShift cluster */
  OVNKubernetes: "OVNKubernetes",

  /** Other network plugins */
  Other: "Other",
}

/** Configuration of the cluster web console */
model ConsoleProfile {
  /** The cluster web console URL endpoint */
  @visibility("read")
  url: url;
}

/** Information about the API of a cluster. */
model ApiProfile {
  /** URL endpoint for the API server */
  @visibility("read")
  url: url;

  /** should the API server be accessible from the internet */
  @visibility("create", "read")
  visibility: Visibility;
}

/** The visibility of the API server */
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

  /** ResourceId for the subnet used by the control plane */
  subnetId: SubnetResourceId;

  /** The core outgoing configuration */
  outboundType?: OutboundType = OutboundType.loadBalancer;

  /** ResourceId for the network security group attached to the cluster subnet */
  networkSecurityGroupId?: NetworkSecurityGroupResourceId;

  /** The configuration that the operators of the cluster have to authenticate to Azure */
  operatorsAuthentication: OperatorsAuthenticationProfile;
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
  controlPlaneOperators: Record<UserAssignedIdentityResourceId>;

  /** The set of Azure User-Assigned Managed Identities leveraged for the Data Plane
   * operators of the cluster. The set of required managed identities is dependent on the
   * Cluster's OpenShift version. */
  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record" "operator name to user assigned identity pairings"
  dataPlaneOperators: Record<UserAssignedIdentityResourceId>;

  /** Represents the information associated to an Azure User-Assigned Managed Identity whose
   * purpose is to perform service level actions. */
  @visibility("create", "read")
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
  @visibility("read")
  provisioningState?: ProvisioningState;

  /** OpenShift version for the nodepool */
  @visibility("create", "read")
  version: VersionProfile;

  /** Azure node pool platform configuration */
  @visibility("create")
  platform: NodePoolPlatformProfile;

  /** The number of worker nodes, it cannot be used together with autoscaling */
  @visibility("create", "update")
  replicas?: int32;

  /** Auto-repair */
  @visibility("create", "read")
  autoRepair?: boolean = false;

  /** Representation of a autoscaling in a node pool. */
  autoScaling?: NodePoolAutoScaling;

  // This warning means to ensure good customer experience, to not create
  // poorly defined types. However here the type literarly is the map<string, value>
  /** K8s labels to propagate to the NodePool Nodes
   * The good example of the label is `node-role.kubernetes.io/master: ""`
   */
  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record" ""
  @visibility("create", "update")
  @OpenAPI.extension("x-ms-identifiers", ["key", "value"])
  labels?: Label[];

  /** Taints for the nodes */
  @visibility("create", "update")
  @OpenAPI.extension("x-ms-identifiers", ["key", "value", "effect"])
  taints?: Taint[];

  /*
   * The Tuned API is defined here:
   * - https://github.com/openshift/cluster-node-tuning-operator/blob/2c76314fb3cc8f12aef4a0dcd67ddc3677d5b54f/pkg/apis/tuned/v1/tuned_types.go
   *
   * The PerformanceProfile API is defined here:
   * - https://github.com/openshift/cluster-node-tuning-operator/tree/b41042d42d4ba5bb2e99960248cf1d6ae4935018/pkg/apis/performanceprofile/v2
   */
  /** Tuning configs, TODO provide meaningful explanation
   * TuningConfig is a list of references to ConfigMaps containing serialized
   * Tuned resources to define the tuning configuration to be applied to
   * nodes in the NodePool.
   * Each ConfigMap must have a single key named "tuned" whose value is the
   * JSON or YAML of a serialized Tuned or PerformanceProfile.
   */
  tuningConfigs?: string[];
}

/** Represents the patchable node pool properties */
model NodePoolPatchProperties {
  /** Provisioning state */
  @visibility("read")
  provisioningState?: ResourceProvisioningState;

  /** The number of worker nodes, it cannot be used together with autoscaling */
  @visibility("update")
  replicas?: int32;

  /** Representation of a autoscaling in a node pool. */
  autoScaling?: NodePoolAutoScaling;

  // This warning means to ensure good customer experience, to not create
  // poorly defined types. However here the type literarly is the map<string, value>
  /** K8s labels to propagate to the NodePool Nodes
   * The good example of the label is `node-role.kubernetes.io/master: ""`
   */
  @visibility("update")
  @OpenAPI.extension("x-ms-identifiers", ["key", "value"])
  labels?: Label[];

  /** Taints for the nodes */
  @visibility("update")
  @OpenAPI.extension("x-ms-identifiers", ["key", "value", "effect"])
  taints?: Taint[];

  /*
   * The Tuned API is defined here:
   * - https://github.com/openshift/cluster-node-tuning-operator/blob/2c76314fb3cc8f12aef4a0dcd67ddc3677d5b54f/pkg/apis/tuned/v1/tuned_types.go
   *
   * The PerformanceProfile API is defined here:
   * - https://github.com/openshift/cluster-node-tuning-operator/tree/b41042d42d4ba5bb2e99960248cf1d6ae4935018/pkg/apis/performanceprofile/v2
   */
  /** Tuning configs, TODO provide meaningful explanation
   * TuningConfig is a list of references to ConfigMaps containing serialized
   * Tuned resources to define the tuning configuration to be applied to
   * nodes in the NodePool.
   * Each ConfigMap must have a single key named "tuned" whose value is the
   * JSON or YAML of a serialized Tuned or PerformanceProfile.
   */
  tuningConfigs?: string[];
}

/** taintKey is the k8s valid key of the taint type on the nodepool nodes
 * The good example of the taint key is `node-role.kubernetes.io/master`
 */
@minLength(1)
@maxLength(316)
scalar taintKey extends string;

/** taintValue is the k8s valid value of the taint type on the nodepool nodes
 * The good example of the taint value is `NoSchedule`
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
   * The good example of the taint key is `node-role.kubernetes.io/master`
   */
  key?: taintKey;

  /** The value of the taint
   * The good example of the taint value is `NoSchedule`
   */
  value?: taintValue;

  /** The effect of the taint
   * The good example of the taint effect is `NoSchedule`
   */
  effect?: Effect;
}

/** Label represents the k8s label */
// Key and value are both optional, so there is no need to redefine them for PATCH
model Label {
  /** The key of the label */
  key?: string;

  /** The value of the label */
  value?: string;
}

/** Azure node pool platform configuration */
model NodePoolPlatformProfile {
  /** The resourceId for the subnet used by the workers */
  subnetId?: string;

  /** The VM size according to the documentation:
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/sizes */
  vmSize: string;

  /** The OS disk size in GiB */
  diskSizeGiB?: int32;

  /** The type of the disk storage account
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types
   */
  diskStorageAccountType?: string;

  /** The availability zone for the node pool.
   * Please read the documentation to see which regions support availability zones
   * - https://learn.microsoft.com/en-us/azure/availability-zones/az-overview
   */
  availabilityZone?: string;

  /** Whether the worker machines should be encrypted at host */
  encryptionAtHost?: boolean;

  /** Disk Encryption Set ID that will be used for encryption the Nodes disks
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption-overview
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption
   */
  diskEncryptionSetId?: string;

  /** Is the disk ephemeral */
  ephemeralOsDisk?: boolean;
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

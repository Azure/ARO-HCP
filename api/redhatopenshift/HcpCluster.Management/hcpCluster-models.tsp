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
  enableEncryptionAtHost?: boolean = false;

  /** The OS disk size in GiB */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  diskSizeGiB?: int32 = 64;

  /** The type of the disk storage account
   * - https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  diskStorageAccountType?: DiskStorageAccountType = "Premium_LRS";

  /** The availability zone for the node pool.
   * Please read the documentation to see which regions support availability zones
   * - https://learn.microsoft.com/en-us/azure/availability-zones/az-overview
   */
  @visibility(Lifecycle.Read, Lifecycle.Create)
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

  /** Token Issuer profile */
  issuer: TokenIssuerProfile;

  /** External Auth clients
   * OidcClients must not exceed 20 entries and entries must have unique namespace/name pairs. */
  @maxItems(20)
  @OpenAPI.extension(
    "x-ms-identifiers",
    #["component", "clientId", "extraScopes"]
  )
  clients?: ExternalAuthClientProfile[];

  /** External Auth claim */
  claim: ExternalAuthClaimProfile;
}

/** Token issuer profile */
model TokenIssuerProfile {
  /** The URL of the token issuer */
  url: url;

  /** The audience of the token issuer */
  @minItems(1)
  @maxItems(10)
  audiences: string[];

  /** The issuer of the token
   *
   * Certificate bundle to use to validate server certificates for the configured URL.
   * It must be PEM encoded. */
  ca?: string;
}

/** External Auth client profile */
model ExternalAuthClientProfile {
  /** External Auth client component */
  component: ExternalAuthClientComponentProfile;

  /** External Auth client id
   * The clientId must appear in the audience field of the TokenIssuerProfile. */
  @minLength(1)
  clientId: string;

  /** external auth client scopes
   *
   * This is useful if you have configured claim mappings that requires specific
   * scopes to be requested beyond the standard OIDC scopes.
   * When omitted, no additional scopes are requested. */
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
  Confidential: "confidential",

  /** Indicates that the client is public.
   * Public clients must not provide a client secret.
   */
  Public: "public",
}

/** External Auth component profile
 * Must have unique namespace/name pairs. */
model ExternalAuthClientComponentProfile {
  /** The name of the external Auth client */
  @minLength(1)
  @maxLength(256)
  name: string;

  /** The namespace of the external Auth client */
  @minLength(1)
  @maxLength(63)
  authClientNamespace?: string;
}

/** External Auth claim profile */
model ExternalAuthClaimProfile {
  /** The claim mappings */
  mappings: TokenClaimMappingsProfile;

  /** The claim validation rules */
  @OpenAPI.extension("x-ms-identifiers", #["claim", "requiredValue"])
  validationRules?: TokenClaimValidationRuleProfile[];
}

/** External Auth claim mappings profile.
 * At a minimum username or groups must be defined. */
model TokenClaimMappingsProfile {
  /** The claim mappings username */
  username?: UsernameClaimProfile;

  /** The claim mappings groups */
  groups?: GroupClaimProfile;
}

/** External Auth claim profile */
model GroupClaimProfile {
  /** Claim name of the external profile */
  @minLength(1)
  @maxLength(256)
  claim: string;

  /** Prefix for the claim external profile
   * If this is specified prefixPolicy will be set to "Prefix" by default */
  prefix?: string;
}

/** External Auth claim profile */
model UsernameClaimProfile {
  /** Claim name of the external profile */
  @minLength(1)
  @maxLength(256)
  claim: string;

  /** Prefix for the claim external profile
   * If this is specified prefixPolicy will be set to "Prefix" by default */
  prefix?: string;

  /** Prefix policy
   * More information here: https://github.com/openshift/api/blob/f9cb766287239d10d5baae431691348286f634c1/config/v1/types_authentication.go#L633 */
  prefixPolicy?: string;
}

/** External Auth claim validation rule */
model TokenClaimValidationRuleProfile {
  /** Claim name for the validation profile
   * claim is a required field that configures the name of the required claim. */
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

/*
 * =======================================
 *  End ExternalAuth resources
 * =======================================
 */

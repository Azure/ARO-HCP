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

  /** Azure platform configuration */
  @visibility(Lifecycle.Read, Lifecycle.Create)
  platform: PlatformProfile;
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

  /** K8s labels to propagate to the NodePool Nodes
   * The good example of the label is `node-role.kubernetes.io/master: ""`
   */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value"])
  labels?: Label[];

  /** Taints for the nodes */
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value", "effect"])
  taints?: Taint[];
}

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
  @minLength(1)
  @maxLength(316)
  key?: string;

  /** The value of the taint
   * @example true
   */
  @minLength(1)
  @maxLength(63)
  value?: string;

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
   * If this is specified prefixPolicy will be set to "Prefix" by default
   */
  prefix?: string;

  /** Prefix policy is an optional field that configures how a prefix should be
   * applied to the value of the JWT claim specified in the 'claim' field.
   *
   * Allowed values are 'Prefix', 'NoPrefix', and omitted (not provided or an empty string).
   *
   * When set to 'Prefix', the value specified in the prefix field will be
   * prepended to the value of the JWT claim.
   * The prefix field must be set when prefixPolicy is 'Prefix'.
   *
   * When set to 'NoPrefix', no prefix will be prepended to the value
   * of the JWT claim.
   *
   * When omitted, this means no opinion and the platform is left to choose
   * any prefixes that are applied which is subject to change over time.
   * Currently, the platform prepends `{issuerURL}#` to the value of the JWT claim
   * when the claim is not 'email'.
   * As an example, consider the following scenario:
   *    `prefix` is unset, `issuerURL` is set to `https://myoidc.tld`,
   *    the JWT claims include "username":"userA" and "email":"userA@myoidc.tld",
   *    and `claim` is set to:
   *    - "username": the mapped value will be "https://myoidc.tld#userA"
   *    - "email": the mapped value will be "userA@myoidc.tld"
   */
  prefixPolicy?: string;
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

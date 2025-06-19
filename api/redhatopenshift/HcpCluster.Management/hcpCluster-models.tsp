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
  @visibility(Lifecycle.Read)
  provisioningState?: ProvisioningState;

  @visibility(Lifecycle.Create, Lifecycle.Read)
  version?: VersionProfile;

  dns?: DnsProfile;

  @visibility(Lifecycle.Create, Lifecycle.Read)
  network?: NetworkProfile;

  @visibility(Lifecycle.Read)
  console?: ConsoleProfile;

  @visibility(Lifecycle.Read)
  api?: ApiProfile;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  platform: PlatformProfile;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  capabilities?: ClusterCapabilitiesProfile;
}

model ClusterCapabilitiesProfile {
  @visibility(Lifecycle.Read, Lifecycle.Create)
  disabled?: OptionalClusterCapability[];
}

union OptionalClusterCapability {
  string,
  ImageRegistry: "ImageRegistry",
}

@lroStatus
union ProvisioningState {
  string,
  ResourceProvisioningState,
  "Accepted",
  "Deleting",
  "Provisioning",
  "Updating",
}

model VersionProfile {
  @visibility(Lifecycle.Read, Lifecycle.Create)
  id?: string;

  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  channelGroup?: string = "stable";

  @visibility(Lifecycle.Read)
  availableUpgrades: string[];
}

model DnsProfile {
  @visibility(Lifecycle.Read)
  baseDomain?: string;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  @maxLength(15)
  @pattern(
    "^[a-z]([-a-z0-9]*[a-z0-9])?$",
    "Alphanumerics, underscores, and hyphens.  Must start and end with an alphanumeric."
  )
  baseDomainPrefix?: string;
}

model NetworkProfile {
  @visibility(Lifecycle.Read, Lifecycle.Create)
  networkType?: NetworkType = NetworkType.OVNKubernetes;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  podCidr?: string = "10.128.0.0/14";

  @visibility(Lifecycle.Read, Lifecycle.Create)
  serviceCidr?: string = "172.30.0.0/16";

  @visibility(Lifecycle.Read, Lifecycle.Create)
  machineCidr?: string = "10.0.0.0/16";

  @visibility(Lifecycle.Read, Lifecycle.Create)
  hostPrefix?: int32 = 23;
}

union NetworkType {
  string,
  OVNKubernetes: "OVNKubernetes",
  Other: "Other",
}

model ConsoleProfile {
  @visibility(Lifecycle.Read)
  url: url;
}

model ApiProfile {
  @visibility(Lifecycle.Read)
  url: url;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  visibility?: Visibility = "public";
}

union Visibility {
  string,
  public: "public",
  private: "private",
}

model PlatformProfile {
  managedResourceGroup?: string;
  subnetId: SubnetResourceId;
  outboundType?: OutboundType = OutboundType.loadBalancer;
  networkSecurityGroupId: NetworkSecurityGroupResourceId;
  operatorsAuthentication: OperatorsAuthenticationProfile;

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

union OutboundType {
  string,
  loadBalancer: "loadBalancer",
}

model OperatorsAuthenticationProfile {
  userAssignedIdentities: UserAssignedIdentitiesProfile;
}

model UserAssignedIdentitiesProfile {
  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record"
  @visibility(Lifecycle.Read, Lifecycle.Create)
  controlPlaneOperators: Record<UserAssignedIdentityResourceId>;

  #suppress "@azure-tools/typespec-azure-resource-manager/arm-no-record"
  @visibility(Lifecycle.Read, Lifecycle.Create)
  dataPlaneOperators: Record<UserAssignedIdentityResourceId>;

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
 *  NodePool resources
 * =======================================
 */

model NodePoolProperties {
  @visibility(Lifecycle.Read)
  provisioningState?: ProvisioningState;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  version?: NodePoolVersionProfile;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  platform: NodePoolPlatformProfile;

  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  replicas?: int32;

  @visibility(Lifecycle.Read, Lifecycle.Create)
  autoRepair?: boolean = true;

  autoScaling?: NodePoolAutoScaling;

  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value"])
  labels?: Label[];

  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  @OpenAPI.extension("x-ms-identifiers", #["key", "value", "effect"])
  taints?: Taint[];
}

union Effect {
  string,
  NoSchedule: "NoSchedule",
  PreferNoSchedule: "PreferNoSchedule",
  NoExecute: "NoExecute",
}

model Taint {
  @minLength(1)
  @maxLength(316)
  key?: string;

  @minLength(1)
  @maxLength(63)
  value?: string;

  effect?: Effect;
}

model Label {
  key?: string;
  value?: string;
}

model NodePoolVersionProfile {
  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  id?: string;

  @visibility(Lifecycle.Read, Lifecycle.Create, Lifecycle.Update)
  channelGroup?: string = "stable";

  @visibility(Lifecycle.Read)
  availableUpgrades: string[];
}

model NodePoolPlatformProfile {
  subnetId?: string;
  vmSize: string;
  enableEncryptionAtHost?: boolean = false;
  diskSizeGiB?: int32 = 64;
  diskStorageAccountType?: DiskStorageAccountType = "Premium_LRS";
  availabilityZone?: string;
  encryption?: Encryption;
}

union DiskStorageAccountType {
  string,
  Premium_LRS: "Premium_LRS",
  StandardSSD_LRS: "StandardSSD_LRS",
  Standard_LRS: "Standard_LRS",
}

model NodePoolAutoScaling {
  @minValue(0)
  min?: int32;

  @minValue(0)
  max?: int32;
}

// Disk encryption models with CMK support
model Encryption {
  infrastructureEncryption?: "enabled" | "disabled" = "disabled";

  /**
   * Optional Customer-managed key (CMK) encryption settings.
   * If not provided, disks use platform-managed keys.
   */
  customerManagedKeyEncryption?: CustomerManagedKeyEncryption;
}

model CustomerManagedKeyEncryption {
  keyEncryptionKeyIdentity: KeyEncryptionKeyIdentity;
  keyEncryptionKeyUrl: string;
}

model KeyEncryptionKeyIdentity {
  identityType: "systemAssignedIdentity" | "userAssignedIdentity";
  userAssignedIdentityResourceId?: UserAssignedIdentityResourceId;
  federatedClientId?: string;
}

/*
 * =======================================
 *   HCP cluster credentials
 * =======================================
 */

model HcpOpenShiftClusterAdminCredential {
  @visibility(Lifecycle.Read)
  @secret
  kubeconfig: string;

  @visibility(Lifecycle.Read)
  expirationTimestamp: utcDateTime;
}

/*
 * =======================================
 *   End HCP cluster credentials
 * =======================================
 */

import "./main.tsp";
import "@azure-tools/typespec-client-generator-core";
import "@typespec/versioning";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.Versioning;
using Azure.ClientGenerator.Core;
using Azure.ResourceManager;
using Microsoft.RedHatOpenShift;

// Client name overrides for azure-sdk-for-net
namespace Microsoft.RedHatOpenShift {
  // request-body model names must not end with 'Request'
  @@clientName(
    HcpOpenShiftClusterAdminCredentialRequest,
    "HcpOpenShiftClusterAdminCredentialRequestContent",
    "csharp"
  );

  // boolean property missing an "Is/Has/Should/Can" prefix
  @@clientName(NodePoolProperties.autoRepair, "canAutoRepair", "csharp");

  // DateTimeOffset properties must end in "On"
  @@clientName(
    HcpOpenShiftClusterAdminCredential.expirationTimestamp,
    "expirationTimestampOn",
    "csharp"
  );
  @@clientName(
    HcpOpenShiftVersionProperties.endOfLifeTimestamp,
    "endOfLifeTimestampOn",
    "csharp"
  );

  // Add top-level resource name prefix to cluster model and type names
  @@clientName(ApiProfile, "HcpOpenShiftClusterApiProfile", "csharp");
  @@clientName(
    ClusterAutoscalingProfile,
    "HcpOpenShiftClusterAutoscalingProfile",
    "csharp"
  );
  @@clientName(
    ClusterImageRegistryProfile,
    "HcpOpenShiftClusterImageRegistryProfile",
    "csharp"
  );
  @@clientName(
    ClusterImageRegistryState,
    "HcpOpenShiftClusterImageRegistryState",
    "csharp"
  );
  @@clientName(Condition, "HcpOpenShiftClusterCondition", "csharp");
  @@clientName(ConditionType, "HcpOpenShiftClusterConditionType", "csharp");
  @@clientName(ConsoleProfile, "HcpOpenShiftClusterConsoleProfile", "csharp");
  @@clientName(
    CustomerManagedEncryptionProfile,
    "HcpOpenShiftClusterCustomerManagedEncryptionProfile",
    "csharp"
  );
  @@clientName(
    CustomerManagedEncryptionType,
    "HcpOpenShiftClusterCustomerManagedEncryptionType",
    "csharp"
  );
  @@clientName(DnsProfile, "HcpOpenShiftClusterDnsProfile", "csharp");
  @@clientName(
    EtcdDataEncryptionKeyManagementModeType,
    "HcpOpenShiftClusterEtcdDataEncryptionKeyManagementModeType",
    "csharp"
  );
  @@clientName(
    EtcdDataEncryptionProfile,
    "HcpOpenShiftClusterEtcdDataEncryptionProfile",
    "csharp"
  );
  @@clientName(EtcdProfile, "HcpOpenShiftClusterEtcdProfile", "csharp");
  @@clientName(
    ImageDigestMirror,
    "HcpOpenShiftClusterImageDigestMirror",
    "csharp"
  );
  @@clientName(ImageRepository, "HcpOpenShiftClusterImageRepository", "csharp");
  @@clientName(IngressProfile, "HcpOpenShiftClusterIngressProfile", "csharp");
  @@clientName(IngressType, "HcpOpenShiftClusterIngressType", "csharp");
  @@clientName(
    KeyVaultVisibility,
    "HcpOpenShiftClusterKeyVaultVisibility",
    "csharp"
  );
  @@clientName(
    KmsEncryptionProfile,
    "HcpOpenShiftClusterKmsEncryptionProfile",
    "csharp"
  );
  @@clientName(KmsKey, "HcpOpenShiftClusterKmsKey", "csharp");
  @@clientName(NetworkProfile, "HcpOpenShiftClusterNetworkProfile", "csharp");
  @@clientName(NetworkType, "HcpOpenShiftClusterNetworkType", "csharp");
  @@clientName(
    OperatorsAuthenticationProfile,
    "HcpOpenShiftClusterOperatorsAuthenticationProfile",
    "csharp"
  );
  @@clientName(OutboundType, "HcpOpenShiftClusterOutboundType", "csharp");
  @@clientName(PlatformProfile, "HcpOpenShiftClusterPlatformProfile", "csharp");
  @@clientName(ResourceStatus, "HcpOpenShiftClusterResourceStatus", "csharp");
  @@clientName(StatusType, "HcpOpenShiftClusterConditionStatusType", "csharp");
  @@clientName(
    UserAssignedIdentitiesProfile,
    "HcpOpenShiftClusterUserAssignedIdentitiesProfile",
    "csharp"
  );
  @@clientName(VersionProfile, "HcpOpenShiftClusterVersionProfile", "csharp");
  @@clientName(Visibility, "HcpOpenShiftClusterApiVisibility", "csharp");

  // Add top-level resource name prefix to node pool model and type names
  @@clientName(
    DiskStorageAccountType,
    "HcpOpenShiftClusterNodePoolDiskStorageAccountType",
    "csharp"
  );
  @@clientName(Effect, "HcpOpenShiftClusterNodePoolTaintEffect", "csharp");
  @@clientName(Label, "HcpOpenShiftClusterNodePoolLabel", "csharp");
  @@clientName(NodePool, "HcpOpenShiftClusterNodePool", "csharp");
  @@clientName(
    NodePoolAutoScaling,
    "HcpOpenShiftClusterNodePoolAutoScaling",
    "csharp"
  );
  @@clientName(
    NodePoolPlatformProfile,
    "HcpOpenShiftClusterNodePoolPlatformProfile",
    "csharp"
  );
  @@clientName(
    NodePoolProperties,
    "HcpOpenShiftClusterNodePoolProperties",
    "csharp"
  );
  @@clientName(
    NodePoolVersionProfile,
    "HcpOpenShiftClusterNodePoolVersionProfile",
    "csharp"
  );
  @@clientName(NodePools, "HcpOpenShiftClusterNodePools", "csharp");
  @@clientName(
    OsDiskProfile,
    "HcpOpenShiftClusterNodePoolOsDiskProfile",
    "csharp"
  );
  @@clientName(OsDiskType, "HcpOpenShiftClusterNodePoolOsDiskType", "csharp");
  @@clientName(Taint, "HcpOpenShiftClusterNodePoolTaint", "csharp");

  // Add top-level resource name prefix to external auth model and type names
  @@clientName(ExternalAuth, "HcpOpenShiftClusterExternalAuth", "csharp");
  @@clientName(
    ExternalAuthClaimProfile,
    "HcpOpenShiftClusterExternalAuthClaimProfile",
    "csharp"
  );
  @@clientName(
    ExternalAuthClientComponentProfile,
    "HcpOpenShiftClusterExternalAuthClientComponentProfile",
    "csharp"
  );
  @@clientName(
    ExternalAuthClientProfile,
    "HcpOpenShiftClusterExternalAuthClientProfile",
    "csharp"
  );
  @@clientName(
    ExternalAuthClientType,
    "HcpOpenShiftClusterExternalAuthClientType",
    "csharp"
  );
  @@clientName(
    ExternalAuthCondition,
    "HcpOpenShiftClusterExternalAuthCondition",
    "csharp"
  );
  @@clientName(
    ExternalAuthConditionType,
    "HcpOpenShiftClusterExternalAuthConditionType",
    "csharp"
  );
  @@clientName(
    ExternalAuthProperties,
    "HcpOpenShiftClusterExternalAuthProperties",
    "csharp"
  );
  @@clientName(ExternalAuths, "HcpOpenShiftClusterExternalAuths", "csharp");
  @@clientName(
    GroupClaimProfile,
    "HcpOpenShiftClusterExternalAuthGroupClaimProfile",
    "csharp"
  );
  @@clientName(
    TokenClaimMappingsProfile,
    "HcpOpenShiftClusterExternalAuthTokenClaimMappingsProfile",
    "csharp"
  );
  @@clientName(
    TokenClaimValidationRule,
    "HcpOpenShiftClusterExternalAuthTokenClaimValidationRule",
    "csharp"
  );
  @@clientName(
    TokenIssuerProfile,
    "HcpOpenShiftClusterExternalAuthTokenIssuerProfile",
    "csharp"
  );
  @@clientName(
    TokenRequiredClaim,
    "HcpOpenShiftClusterExternalAuthTokenRequiredClaim",
    "csharp"
  );
  @@clientName(
    TokenValidationRuleType,
    "HcpOpenShiftClusterExternalAuthTokenValidationRuleType",
    "csharp"
  );
  @@clientName(
    UsernameClaimPrefixPolicy,
    "HcpOpenShiftClusterExternalAuthUsernameClaimPrefixPolicy",
    "csharp"
  );
  @@clientName(
    UsernameClaimProfile,
    "HcpOpenShiftClusterExternalAuthUsernameClaimProfile",
    "csharp"
  );
}

// JavaScript SDK visibility workaround:
// For write operations whose body models contain server-side read-only properties
// that are also required, provide write-specific models that omit those fields so
// JS SDK users are not forced to supply server-computed values.
// Pattern: https://github.com/Azure/azure-rest-api-specs/pull/42784
#suppress "@azure-tools/typespec-azure-resource-manager/missing-operations-endpoint" "Write-only helper models for JS SDK alternate types; operations are defined on the original resources in hcpCluster.tsp"
namespace Microsoft.RedHatOpenShift {
  @doc("API profile for create/update operations, omitting server-side read-only field url.")
  model ApiProfileCreate {
    /** The internet visibility of the OpenShift API server. */
    visibility?: Visibility = Visibility.Public;

    /** The list of authorized IPv4 CIDR blocks allowed to access the API server. Maximum 500 entries. */
    @clientName("authorizedCIDRs")
    @maxItems(500)
    authorizedCidrs?: string[];
  }

  @doc("Azure platform configuration for create/update operations, omitting server-side read-only field issuerUrl.")
  model PlatformProfileCreate {
    /** Resource group name to put cluster resources. */
    managedResourceGroup?: string;

    /** The Azure resource ID of the worker subnet. */
    subnetId: SubnetResourceId;

    /** The Azure resource ID of the VNet integration subnet. */
    @added(Versions.v2025_12_23_preview)
    vnetIntegrationSubnetId: SubnetResourceId;

    /** The core outgoing configuration. */
    outboundType?: OutboundType = OutboundType.LoadBalancer;

    /** Resource ID for the NSG attached to the cluster subnet. */
    networkSecurityGroupId: NetworkSecurityGroupResourceId;

    /** The configuration for operator authentication to Azure. */
    operatorsAuthentication: OperatorsAuthenticationProfile;

    /** Azure Container Registry configuration for authenticating image pulls on the cluster's worker nodes. */
    @added(Versions.v2026_10_01)
    containerRegistry?: ContainerRegistryProfile;
  }

  #suppress "@azure-tools/typespec-azure-resource-manager/arm-resource-provisioning-state" "Omitting provisioningState in this create/update model because it is a server-side read-only field"
  @doc("HCP cluster properties for create/update operations, omitting server-side read-only fields (console and status on the root model; api.url and platform.issuerUrl in nested models).")
  model HcpOpenShiftClusterPropertiesCreate {
    /** Version of the control plane components. */
    @madeRequired(Versions.v2025_12_23_preview)
    version: VersionProfile;

    /** Cluster DNS configuration. */
    dns?: DnsProfile;

    /** Cluster network configuration. */
    network?: NetworkProfile;

    /** Shows the cluster API server profile. */
    api?: ApiProfileCreate;

    /** The cluster ingress configuration. */
    @added(Versions.v2026_06_30_preview)
    ingress?: IngressProfile;

    /** Azure platform configuration. */
    platform: PlatformProfileCreate;

    /** Configure ClusterAutoscaling. */
    autoscaling?: ClusterAutoscalingProfile;

    /** Configure ETCD. */
    @removed(Versions.v2026_06_30_preview)
    @renamedFrom(Versions.v2026_06_30_preview, "etcd")
    etcdNoUpdate?: EtcdProfile;

    /** Configure ETCD. */
    @added(Versions.v2026_06_30_preview)
    etcd?: EtcdProfile;

    /** Rules to allow pulling images from mirrored registries by digest. */
    @added(Versions.v2025_12_23_preview)
    @identifiers(#["source"])
    @maxItems(240)
    imageDigestMirrors?: ImageDigestMirror[];

    /** Grace period in minutes for node draining during upgrades and scale operations. */
    @maxValue(10080)
    @minValue(0)
    nodeDrainTimeoutMinutes?: int32;

    /** OpenShift internal image registry. */
    clusterImageRegistry?: ClusterImageRegistryProfile;

    /** Cryptographic restrictions for kernel and userspace libraries. */
    @added(Versions.v2026_06_30_preview)
    cryptoRestrictions?: CryptoRestrictions = CryptoRestrictions.None;
  }

  @doc("HCP cluster resource for create/update operations.")
  model HcpOpenShiftClusterResourceCreate
    is TrackedResource<HcpOpenShiftClusterPropertiesCreate> {
    ...ResourceNameParameter<
      HcpOpenShiftClusterResourceCreate,
      KeyName = "hcpOpenShiftClusterName",
      SegmentName = "hcpOpenShiftClusters",
      NamePattern = "^[a-zA-Z]([-a-zA-Z0-9]{0,52}[a-zA-Z0-9])?$"
    >;
    ...ManagedServiceIdentityProperty;
  }
}

@@alternateType(
  HcpOpenShiftClusters.createOrUpdate::parameters.resource,
  HcpOpenShiftClusterResourceCreate,
  "javascript"
);

@@clientName(
  Microsoft.RedHatOpenShift,
  "RedHatOpenShiftHcpMgmtClient",
  "python"
);

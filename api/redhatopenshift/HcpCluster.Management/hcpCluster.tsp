import "@typespec/rest";
import "@typespec/http";
import "@azure-tools/typespec-azure-core";
import "@azure-tools/typespec-azure-resource-manager";

import "./hcpCluster-models.tsp";

using TypeSpec.Rest;
using TypeSpec.Http;
using Azure.Core;
using Azure.ResourceManager;

namespace Microsoft.RedHatOpenShift;

/** HCP cluster operations */
@armResourceOperations(HcpOpenShiftClusterResource)
interface HcpOpenShiftClusters {
  get is ArmResourceRead<HcpOpenShiftClusterResource>;
  createOrUpdate is ArmResourceCreateOrReplaceAsync<HcpOpenShiftClusterResource>;
  update is ArmCustomPatchAsync<
    HcpOpenShiftClusterResource,
    HcpOpenShiftClusterPatch
  >;
  delete is ArmResourceDeleteWithoutOkAsync<HcpOpenShiftClusterResource>;
  listByResourceGroup is ArmResourceListByParent<HcpOpenShiftClusterResource>;
  listBySubscription is ArmListBySubscription<HcpOpenShiftClusterResource>;

  // ------------------------------
  // The requestAdminCredential and revokeAdminCredentials are
  // asynchronous POST actions on an HcpOpenShiftClusterResource.
  /** Request a temporary admin kubeconfig for the cluster */
  requestAdminCredential is ArmResourceActionAsync<
    HcpOpenShiftClusterResource,
    void,
    HcpOpenShiftClusterAdminCredential
  >;
  /** Revoke all credentials issued by requestAdminCredential */
  revokeCredentials is ArmResourceActionNoResponseContentAsync<
    HcpOpenShiftClusterResource,
    void
  >;
}

/** HCP cluster node pools */
@armResourceOperations(HcpOpenShiftClusterNodePoolResource)
interface NodePools {
  get is ArmResourceRead<HcpOpenShiftClusterNodePoolResource>;
  createOrUpdate is ArmResourceCreateOrReplaceAsync<HcpOpenShiftClusterNodePoolResource>;
  update is ArmCustomPatchAsync<
    HcpOpenShiftClusterNodePoolResource,
    HcpOpenShiftClusterNodePoolPatch
  >;
  delete is ArmResourceDeleteWithoutOkAsync<HcpOpenShiftClusterNodePoolResource>;
  listByParent is ArmResourceListByParent<HcpOpenShiftClusterNodePoolResource>;
}

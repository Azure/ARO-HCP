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
  // The credentials and kubeconfig operations for the cluster
  // These are implemented as POST to not be forced to use the Provisioning state in the response
  /** Return the kubeconfig for the cluster */
  kubeConfig is ArmResourceActionSync<
    HcpOpenShiftClusterResource,
    void,
    HcpOpenShiftClusterKubeconfig
  >;
  /** Returns the admin cluster credentials */
  adminCredentials is ArmResourceActionSync<
    HcpOpenShiftClusterResource,
    void,
    HcpOpenShiftClusterCredentials
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

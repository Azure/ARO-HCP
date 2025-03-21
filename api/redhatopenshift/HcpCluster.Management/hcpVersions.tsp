import "@typespec/rest";
import "@typespec/http";
import "@azure-tools/typespec-azure-core";
import "@azure-tools/typespec-azure-resource-manager";

using TypeSpec.Rest;
using TypeSpec.Http;
using Azure.Core;
using Azure.ResourceManager;

namespace Microsoft.RedHatOpenShift;

/** HcpOpenShiftVersions represents a location based available HCP cluster versions */
//@locationResource
//model HcpOpenShiftVersion is ProxyResource<HcpOpenShiftVersionsProperties> {
//  ...ResourceNameParameter<HcpOpenShiftVersion>;
//}

/** HcpOpenShiftVersionsProperties is the installable cluster version */
//model HcpOpenShiftVersionsProperties {
//  ...DefaultProvisioningStateProperty;
//
//  /** The cluster version */
//  @visibility(Lifecycle.Read)
//  clusterVersion: string;
//}

//@armResourceOperations(HcpOpenShiftVersion)
//interface HcpClusterVersions {
//  list is ArmResourceListByParent<HcpOpenShiftVersion>;
//}

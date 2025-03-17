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
@locationResource
model HcpOpenShiftVersionResource
  is ProxyResource<HcpOpenShiftVersionsProperties> {
  /** The name of the resource */
  @pattern("^[a-zA-Z0-9-]{3,24}$")
  @segment("hcpOpenShiftVersions")
  @key("hcpOpenShiftVersions")
  @visibility(Lifecycle.Read)
  @path
  name: string;
}

/** HcpOpenShiftVersionsProperties is the installable cluster version */
model HcpOpenShiftVersionsProperties {
  ...DefaultProvisioningStateProperty;

  /** The cluster version */
  @visibility(Lifecycle.Read)
  clusterVersion: string;
}

@armResourceOperations(HcpOpenShiftVersionResource)
interface HcpClusterVersions {
  list is ArmResourceListByParent<HcpOpenShiftVersionResource>;
}

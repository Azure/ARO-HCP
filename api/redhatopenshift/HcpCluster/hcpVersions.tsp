import "@typespec/rest";
import "@typespec/http";
import "@azure-tools/typespec-azure-core";
import "@azure-tools/typespec-azure-resource-manager";

using TypeSpec.Rest;
using TypeSpec.Http;
using Azure.Core;
using Azure.ResourceManager;

namespace Microsoft.RedHatOpenshift;

/** HcpOpenShiftVersions represents a location based available HCP cluster versions */
@locationResource
model HcpOpenShiftVersions is ProxyResource<HcpOpenShiftVersionsProperties> {
    /** The name of the resource */
    @pattern("^[a-zA-Z0-9-]{3,24}$")
    @segment("hcpOpenShiftVersions")
    @key("hcpOpenShiftVersions")
    @visibility("read")
    @path
    name: string;
}

/** HcpOpenShiftVersionsProperties is the installable cluster version */
model HcpOpenShiftVersionsProperties {
    ...DefaultProvisioningStateProperty;

    /** The cluster version */
    @visibility("read")
    clusterVersion: string;
}

@armResourceOperations(HcpOpenShiftVersions)
interface HcpClusterVersionOperations {
    list is ArmResourceListByParent<HcpOpenShiftVersions>;
}
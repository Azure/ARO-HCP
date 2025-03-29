import "@typespec/versioning";
import "@azure-tools/typespec-azure-resource-manager";

import "./hcpCluster.tsp";
import "./hcpVersions.tsp";

using TypeSpec.Http;
using TypeSpec.Versioning;
using Azure.ResourceManager;

/** Microsoft.RedHatOpenShift Resource Provider management API. */
@armProviderNamespace
@service(#{ title: "Azure Red Hat OpenShift Hosted Control Planes Service" })
@versioned(Microsoft.RedHatOpenShift.Versions)
namespace Microsoft.RedHatOpenShift;

interface Operations extends Azure.ResourceManager.Operations {}

/** The available API versions. */
enum Versions {
  /** 2024-06-10-preview version */
  @useDependency(Azure.ResourceManager.Versions.v1_0_Preview_1)
  @useDependency(Azure.Core.Versions.v1_0_Preview_1)
  @armCommonTypesVersion(Azure.ResourceManager.CommonTypes.Versions.v6)
  v2024_06_10_preview: "2024-06-10-preview",
}

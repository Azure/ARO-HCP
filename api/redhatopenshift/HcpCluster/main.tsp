import "@typespec/http";
import "@typespec/rest";
import "@typespec/versioning";
import "@azure-tools/typespec-azure-core";
import "@azure-tools/typespec-azure-resource-manager";

import "./hcpCluster.tsp";
import "./hcpVersions.tsp";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.Versioning;
using Azure.Core;
using Azure.ResourceManager;

/** Microsoft.RedHatOpenshift Resource Provider management API. */
@armProviderNamespace
@service({
  title: "Azure Red Hat OpenShift Hosted Control Planes Service",
})
@versioned(Microsoft.RedHatOpenshift.Versions)
namespace Microsoft.RedHatOpenshift;

/** The available API versions. */
enum Versions {
  /** 2024-06-10-preview version */
  @useDependency(Azure.ResourceManager.Versions.v1_0_Preview_1)
  @useDependency(Azure.Core.Versions.v1_0_Preview_1)
  @armCommonTypesVersion(Azure.ResourceManager.CommonTypes.Versions.v5)
  v2024_06_10_preview: "2024-06-10-preview",
}

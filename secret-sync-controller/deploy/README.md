# Deploying the Secrets Store Sync Controller

This chart deploys the Secrets Store Sync Controller for this repository's
Azure environment. The chart lives in this directory (`deploy/`) and its
values are in `values-azure.yaml` — the image, ACR, and digests are rendered
from the cluster configuration (`config/config.yaml`,
`secretSyncController.image.*`) via the standard config-materialization flow
(`make -C config materialize`), so the controller image is pinned by digest
from the service ACR rather than by an upstream tag.

Deploy (or update) via the standard service pipeline — see
[docs/service-deployment-concept.md](../../docs/service-deployment-concept.md)
and the root `Makefile` `pipeline/` targets.

## Configuration and Parameters

Values are customized in `values-azure.yaml` (or per-environment rendered
config). Notable keys:

| Parameter Name                                   | Description                                                                                       | Default Value                                                                                                                                                                         |
|--------------------------------------------------|---------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `providerContainer`                              | The CSI provider installer container.                                                             | `provider-azure-installer` (Azure CSI provider)                                                                                                                                       |
| `controllerName`                                 | The name of the Secrets Store Sync Controller.                                                    | `secrets-store-sync-controller-manager`                                                                                                                                               |
| `validatingAdmissionPolicies.applyPolicies`      | Determines whether the Secrets Store Sync Controller should apply policies.                       | `true`                                                                                                                                                                                |
| `validatingAdmissionPolicies.allowedSecretTypes` | The types of secrets that the Secrets Store Sync Controller should allow.                         | `["Opaque", "kubernetes.io/basic-auth", "bootstrap.kubernetes.io/token", "kubernetes.io/dockerconfigjson", "kubernetes.io/dockercfg", "kubernetes.io/ssh-auth", "kubernetes.io/tls"]` |
| `validatingAdmissionPolicies.deniedSecretTypes`  | The types of secrets that the Secrets Store Sync Controller should deny.                          | `["kubernetes.io/service-account-token"]`                                                                                                                                             |
| `image`                                          | Controller image — a single templated string in `values-azure.yaml`; `repository` and `digest` come from `secretSyncController.image.*` in the rendered config, pinned by digest from the service ACR. | (rendered from config) |                                                                                                                                                                |

See [docs/secret-sync.md](../../docs/secret-sync.md) for the end-to-end
secret synchronization flow and the tooling in `tooling/secret-sync/`.

# Bump ARO HCP Service Component Image Digests

This guide describes how to update (bump) the image digests for ARO HCP service components in the [configuration](../configuration.md), applicable to both Red Hat and Microsoft environments.

## Image Digest Configuration Paths

Each service component defines its image digest in the [configuration](../configuration.md). The relevant configuration paths are:

| Service Component             | JSON Path in Configuration File                   |
| ----------------------------- | ------------------------------------------------- |
| Backend                       | `backend.image.digest`                            |
| Frontend                      | `frontend.image.digest`                           |
| Clusters Service              | `clustersService.image.digest`                    |
| Maestro                       | `maestro.image.digest`                            |
| Hypershift Operator           | `hypershift.image.digest`                         |
| Backplane API                 | `backplaneAPI.image.digest`                       |
| PKO Image Manager             | `pko.imageManager.digest`                         |
| PKO Image Package             | `pko.imagePackage.digest`                         |
| PKO Remote Phase Manager      | `pko.remotePhaseManager.digest`                   |
| ACR Pull                      | `acrPull.image.digest`                            |
| Image Sync (oc-mirror)        | `imageSync.ocMirror.image.digest`                 |
| Prometheus Operator (SVC)     | `svc.prometheus.prometheusOperator.image.digest`  |
| Prometheus Operator (MGMT)    | `mgmt.prometheus.prometheusOperator.image.digest` |
| Prometheus Server (SVC)       | `svc.prometheus.prometheusSpec.image.digest`      |
| Prometheus Server (MGMT)      | `mgmt.prometheus.prometheusSpec.image.digest`     |
| Maestro Agent Sidecar (nginx) | `maestro.agent.sidecar.image.digest`              |
| Mise                          | `mise.image.digest`                               |

> **Note**: Some components, such as `backend.image.digest` and `frontend.image.digest`, may be unset in development environments. In these cases, the commit SHA is used to resolve the image and digest. Components deployed to both service (SVC) and management (MGMT) clusters have distinct configuration paths per cluster type.

## Bumping Image Digests in Red Hat Environments

Image digests for Red Hat environments are defined in [config/config.yaml](../../config/config.yaml) under the `clouds.dev` section. To bump a digest:

1. Update the desired digest value.
2. Run `make -C config materialize`.
3. Open a pull request with the change.

Once merged, a GitHub Actions pipeline will propagate the updated configuration to the integrated DEV and CSPR environments.

> [!NOTE]
> Personal DEV environments are not updated automatically. Developers must manually apply the changes by running:
>
> ```bash
> make infra.all deployall
> ```

## Bumping Image Digests in Microsoft Environments

Image digests for Microsoft environments are defined in [sdp-pipelines/hcp/config.msft.public-cloud-overlay.yaml](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/config.msft.public-cloud-overlay.yaml), under the `clouds.public` section.

To update a digest:

1. Modify the digest in the appropriate `clouds.public.environments.$env` section.
2. Follow the [README instructions](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/README.md) in the `sdp-pipelines/hcp` directory to create a pull request.

> [!IMPORTANT]
> Changes to MSFT environment configurations are not applied automatically. You must manually trigger the relevant infrastructure or service component deployment pipeline via ADO. Refer to the [EV2 deployment documentation](../ev2-deployment.md#execute-an-ado-pipeline) for instructions.

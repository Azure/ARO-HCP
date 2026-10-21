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
| ACR Pull                      | `acrPull.image.digest`                            |
| Prometheus Operator (SVC)     | `svc.prometheus.prometheusOperator.image.digest`  |
| Prometheus Operator (MGMT)    | `mgmt.prometheus.prometheusOperator.image.digest` |
| Prometheus Server (SVC)       | `svc.prometheus.prometheusSpec.image.digest`      |
| Prometheus Server (MGMT)      | `mgmt.prometheus.prometheusSpec.image.digest`     |
| Maestro Agent Sidecar (nginx) | `maestro.agent.sidecar.image.digest`              |
| Mise                          | `mise.image.digest`                               |

> **Note**: Frontend, backend, mgmt-agent, kube-applier, admin-api, sessiongate, and Fleet have no baseline digest pins. Their digests must be supplied by deployment overrides, such as CI-built images, sdp-pipelines promotions or local builds. The optional-digest schema permits unresolved baseline configuration; it does not make a digest optional when deploying an image.

## Bumping Image Digests in Red Hat Environments

Image digests for Red Hat environments are defined in [config/config.yaml](../../config/config.yaml) under the `clouds.dev` section. To bump a digest:

1. Update the desired digest value.
2. Run `make -C config materialize`.
3. Open a pull request with the change.

Once merged, a Prow job will build and promote the updated images.

> [!NOTE]
> Personal DEV environments are not updated automatically. Developers must manually apply the changes by running:
>
> ```bash
> make infra.all deployall
> ```

## Bumping Image Digests in Microsoft Environments

Image digests for Microsoft environments are defined in [config/config.msft.clouds-overlay.yaml](../../config/config.msft.clouds-overlay.yaml).

To update a digest:

1. Modify the digest in the appropriate `clouds.public.environments.$env` section, open a PR and get it merged.
2. Follow the [README instructions](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/README.md) in the `sdp-pipelines/hcp` directory bring in the change to the `sdp-pipelines` ADO repository via a pull request.

> [!IMPORTANT]
> Changes to MSFT environment configurations are not applied automatically to the respective environments. You have to trigger the relevant infrastructure or service component deployment pipelines manually via ADO after the `sdp-pipelines` PR is merged. You can find all ARP HCP pipelines in [ADO](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionScope=%5COneBranch%5Csdp-pipelines%5Chcp). Refer to the [EV2 deployment documentation](../ev2-deployment.md#execute-an-ado-pipeline) for more instructions.

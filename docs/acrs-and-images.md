# ARO HCP ACR and Container Image Registries and Images Distribution

## Introduction

This document outlines the container image registry architecture and image distribution strategy for ARO HCP. It covers the use of ACRs to meet Microsoft security requirements, the mirroring processes that populate these registries with OCP and service images, and the mechanisms by which ARO HCP components and customer workloads pull images at runtime.

## ACRs Strategy

The ARO HCP container registry strategy addresses Microsoft compliance requirements while supporting both internal service operations and customer-facing OpenShift deployments. This section defines the registry types, image categories, and replication approach across ARO HCP environments.

### MSFT Requirements

Microsoft requires that all ARO HCP images must be pulled from private Azure Container Registries (ACRs) or Microsoft Container Registry (MCR). This applies to all workloads on AKS or Azure Container Apps. While ACRs can be populated from trusted Red Hat sources, production workloads must pull images exclusively from Microsoft-controlled registries rather than external public or private registries.

### ACR Types

**Service ACR** stores images for ARO HCP service components (RP, cluster-service, maestro, ACM, HyperShift). Access is restricted to service and management clusters. Service ACRs follow the naming convention `arohcpsvc${environment}` (e.g., `arohcpsvcstg`).

**OCP ACR** contains OpenShift release payload images for hosted control planes and data planes. Serves both management clusters and customer-side pulls. OCP ACRs follow the naming convention `arohcpocp${environment}` (e.g., `arohcpocpstg`).

### Geo Replication

Each environment (DEV, INT, STAGE, PROD) maintains dedicated service and OCP ACRs. Both registry types are geo-replicated across Azure regions for performance and resilience.

## Image Mirroring

Image mirroring ensures ARO HCP environments maintain current OpenShift releases and service components while adhering to Microsoft's private registry requirements. This section covers continuous mirroring of OCP and operator images via oc-mirror, and on-demand synchronization of service components during deployments.

### ACR Cache Rules

ACR Cache copies images from a connected registry over upon requesting the image the first time. It is configured for OCP Images in the corresponding bicep: [global-acr.bicep](..dev-infrastructure/templates/global-acr.bicep)
We rely on `quay.io/openshift-release-dev/*` images in all environments. 

### On-demand Sync

In contrast to the continuous mirroring of OCP images, the on-demand image sync handles service component images during rollouts via the deployment pipeline. The on-demand sync uses [ORAS](https://oras.land/) within an [EV2 shell steps](pipeline-concept.md#shell-step) to copy images from the source registry to the environment-specific service ACR. In case of a failure, the mirror task can be retried via EV2's retry mechanisms.

The image mirror step happens before Helm chart deployment. This ensures that the correct images are available in the target environment before any service components are deployed. Since we exclusively use digest-based image references, mirroring can be re-triggered without affecting the consistency of the deployment across different regions of an environment.

Each service component defines a source registry, repository and the digest to be used for deployments the [configuration management](configuration.md). There is a predefined type `"#/definitions/containerImage"` available in the [configuration schema](../config/config.schema.json), that can be used to define the image properties. An example configuration for the `clustersService` component looks like this ...

```yaml
clustersService:
  ...
  image:
    registry: "quay.io"
    repository: "app-sre/uhc-clusters-service"
    digest: "sha256:xyz"
```

... and is referenced in the [cluster service rollout pipeline](../cluster-service/pipeline.yaml).

```yaml
  steps:
  - name: mirror-image
    action: ImageMirror
    targetACR:
      configRef: acr.svc.name
    sourceRegistry:
      configRef: clustersService.image.registry
    repository:
      configRef: clustersService.image.repository
    digest:
      configRef: clustersService.image.digest
    pullSecretKeyVault:
      configRef: global.keyVault.name
    pullSecretName:
      configRef: imageSync.ondemandSync.pullSecretName
    shellIdentity:
      input:
          resourceGroup: global
          step: output
          name: globalMSIId
```

### oc-mirror

[oc-mirror](https://github.com/openshift/oc-mirror) is a Red Hat tool built for mirroring OCP release payloads and OLM operator bundles between registries. It is now only in use for ACM images. This tool runs within Azure Container App Cronjobs and continuously brings in new images as they appear in the source registries. `oc-mirror` runs within the global scope of the respective target environment and selectively mirrors images. The mirroring setup can be found in the [global-image-sync.bicep](../dev-infrastructure/templates/global-image-sync.bicep) template.

## Building ARO HCP Images

ARO HCP combines various service components to form the hosted control plane management stack. Images for components that are not ARO HCP specific are sourced from Red Hat registries and repositories like quay.io (OCP, CS, Maestro, Hypershift) or registry.redhat.io (ACM).

The component images that are ARO HCP specific (RP frontend, RP backend, Admin API, oc-mirror), are built directly within the [ARO HCP repository](https://github.com/Azure/aro-hcp) with [GitHub Actions](../.github/workflows/services-ci.yml) and pushed to the `arohcpsvcdev` ACR in the RH DEV environment. These images are then mirrored to the respective service ACRs in the other environments using the [on-demand sync](#on-demand-sync) process.

## Image Pulling

Once images are mirrored into the appropriate ACRs, they must be securely and efficiently pulled by various components across the ARO HCP infrastructure. This process involves different authentication mechanisms described in the sections below.

### MSI-Acrpull

The majority of ARO HCP service components running on AKS clusters use the [msi-acrpull](https://github.com/Azure/msi-acrpull/) controller to enable image pulls. This controller manages pull secrets to service accounts within Kubernetes clusters and manages their complete lifecycle, including periodic recycling to maintain security hygiene.

Inspect existing helm charts in this repo to see how the `AcrPullBinding` CRD is used to integrate with msi-acrpull.

### Kubelet Identity-based pull

Before the migration to `msi-acrpull`, most workloads relied on the kubelet managed identity to pull images from ACRs. This approach is still used by ACM but we will migrate to `msi-acrpull` in the future.

### Dedicated pull secrets

Customer-hosted cluster dataplane nodes rely on dedicated pull secrets that are provisioned specifically for accessing OpenShift release payload images in the OCP ACR. During HCP provisioning, cluster-service creates pull secrets with the necessary authentication credentials for accessing the OCP ACR. These secrets are provided to HyperShift, which takes responsibility for distributing them to worker nodes in customer subscriptions. This approach enables secure cross-boundary access while maintaining proper isolation between the ARO HCP service infrastructure and customer environments.

In the future, `msi-acrpull` will be used for these pull secrets as well, relieving CS from the duty to create and lifecycle such secrets.

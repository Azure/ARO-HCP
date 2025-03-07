# ARO HCP Personal DEV Environment

A personal DEV environment is a fully-fledged ARO HCP instance that provides all major infrastructure and service components required to create hosted control planes. Each ARO HCP team member creates their own personal DEV environment for development purposes.

These environments are hosted in the Red Hat Azure tenant, meaning that all [restrictions](environments.md#aro-hcp-azure-tenant-overview) of that tenant apply.

This document will describe how to create and manage a personal DEV environment and how to access them for development purposes.

## Prerequisites

Creating a personal DEV environment requires several prerequisites being met.

Ensure you have access to the RH Azure tenant:

- **RH account**: You need to have a Red Hat account to access the Red Hat Azure tenant (`redhat0.onmicrosoft.com`) where personal DEV environments are created
- **Subscription access**: You need access to the `ARO Hosted Control Planes (EA Subscription 1)` subscription in the Red Hat Azure tenant. Consult the [ARO HCP onboarding guide](https://docs.google.com/document/d/1KUZSLknIkSd6usFPe_OcEYWJyW6mFeotc2lIsLgE3JA/)
- `az` utility >= `2.68.0`
- `az login` with your Red Hat account

The following additional tools are also required:

- `jq`
- `make`
- `kubelogin` - download from <https://azure.github.io/kubelogin/install.html>
- `kubectl` version >= 1.30
- `helm` version >= 3.15
- `openssl`
- `psql` - client CLI for direct Postgres access

## Full Personal DEV Environment Setup

> [!IMPORTANT]
> A word of caution upfront: dev infrastructure is automatically deleted after 48h. If you want to keep your infrastructure indefinitely, run all the following commands with an env variable `PERSIST=true`.
> Please consider the implication on cost if you decide to keep your infrastructure indefinitely.

The creation process can take up to 45 minutes.

   ```bash
   make infra.all deployall
   ```

This command creates a personal DEV environment with a unique name that is derived from your username and deploys all required infrastructure components.

> [!TIP]
> This command can be used to update your personal DEV environment as well. It will apply the latest changes to the infrastructure and services.
> If you only want to update individual aspects of the environment, follow the [partial setup](#partial-personal-dev-environment-setup) instructions as it will save you a lot of time.

## Partial Personal DEV Environment Setup

The process described in the previous section takes a lot of time. In case you want to install or update only a specific part of the environment, you can use the following commands.

> [!IMPORTANT]
> Please understand the ARO HCP [architecture](high-level-architecture.md) and the [service deployment concept](service-deployment-concept.md) before proceeding with the partial setup. Not every command can be run in isolation without it's prerequisites being met, e.g. before deploying services, you need to provision the cluster

### Infrastructure Commands

| Command           | Description                                                                                                     |
| :---------------- | :-------------------------------------------------------------------------------------------------------------- |
| make infra.all    | Deploiys or updates a full environment with regional, service and management infrastructure resources           |
| make infra.region | Deploiys or updates the regional resources like eventgrid, DNS zones etc                                        |
| make infra.svc    | Deploiys or updates the service cluster including the supporting infrastructure for the services (postgres, KV) |
| make infra.mgmt   | Deploiys or updates the management cluster including the supporting infrastructure for the services (KVs)       |

### Services Commands

| Command                                 | Description                                                                                              |
| :-------------------------------------- | :------------------------------------------------------------------------------------------------------- |
| make deployall                          | Deploys or updates all service to the service and management cluster                                     |
| make svc.deployall                      | Deploys or updates all services to the service cluster                                                   |
| make mgmt.deployall                     | Deploys or updates all services to the management clusters                                               |
| make istio.deploy_pipeline              | Deploys or updates the istio configuration on the service cluster                                        |
| make backend.deploy_pipeline            | Deploys or updates the RP backend on the service cluster                                                 |
| make frontend.deploy_pipeline           | Deploys or updates the RP frontend on the service cluster                                                |
| make cluster-service.deploy_pipeline    | Deploys or updates CS on the service cluster                                                             |
| make maestro.server.deploy_pipeline     | Deploys or updates the Maestro server on the service cluster                                             |
| make acm.deploy_pipeline                | Deploys or updates ACM on the management cluster                                                         |
| make maestro.agent.deploy_pipeline      | Deploys or updates the Maestro agent on the management cluster and registers it with the service cluster |
| make hypershiftoperator.deploy_pipeline | Deploys or updates the Hypershift operator on the management cluster                                     |

## Developer Machine Environments

Service component development teams can choose to have only partial personal DEV environments set up to support their development flow. Respective documentation about the actual creation flow can be found in the service component documentation.

## Accessing the environment

Once the environment has been provisioned, you can inspect it in the Azure Portal. Look out for the following Resourcegroups:

- **hcp-underlay-$(regionShort)$(usernameShortPrefix)**: holds the regional resources like Eventgrid, DNS zones, ...
- **hcp-underlay-$(regionShort)$(usernameShortPrefix)-svc**: holds the service cluster and supporting infra for its components
- **hcp-underlay-$(regionShort)$(usernameShortPrefix)-mgmt-1**: holds the management cluster and supporting infra for its components

The `-svc` and `-mgmt-1` resource groups contain the service and management AKS clusters respectively. Access to these clusters has been granted as part of the provisioning process and you can find respective kubeconfigs in `~/.kube/` as files that are named after their Resourcegroups. You can also use the following helpers to setup the `KUBECONFIG` environment variable:

  ```bash
  export KUBECONFIG=$(make infra.svc.aks.kubeconfigfile)
  export KUBECONFIG=$(make infra.mgmt.aks.kubeconfigfile)
  ```

The cluster in personal DEV have no reachable ingress. To interact with the services you deploy use `kubectl port-forward`

  ```bash
  kubectl port-forward svc/aro-hcp-frontend 8443:8443 -n aro-hcp
  kubectl port-forward svc/clusters-service 8000:8000 -n cluster-service
  kubectl port-forward svc/maestro 8001:8000 -n maestro
  kubectl port-forward svc/maestro-grpc 8090 -n maestro
  ```

To access the CS Azure Postgres DB run

  ```sh
  eval $(make -C dev-infrastructure cs-miwi-pg-connect)
  psql -d clusters-service
  ```

To access the Maestro Azure Postgres DB run

  ```sh
  eval $(make -C dev-infrastructure maestro-miwi-pg-connect)
  psql -d maestro
  ```

## Cleanup

Besides the automated cleanup for non-persistent environments, you can manually delete your personal DEV environment with the following command:

  ```bash
  make infra.svc.clean
  make infra.mgmt.clean
  make infra.region.clean
  ```

## Responsibilities

- **Lifecycle**: The personal DEV environments lifecycle is the responsibility of the individual team member. This includes creating, updating, and deleting the environment as well as keeping track of recent change and bugfixes and applying them.

  > [!TIP]
  > Delete personal DEV environments when they are no longer needed to free up resources and prevent unnecessary costs.
  > If you require only a temporary personal DEV environment, don't mark it with `PERSIST=true`.

- **Security**: Your AKS clusters will have access to the shared Service Key Vault which contains certificates and credentials for identities that can act in the Red Hat ARO HCP subscription. Keep this in mind when working with your infrastructure.

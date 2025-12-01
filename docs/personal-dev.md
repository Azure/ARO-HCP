# ARO HCP Personal DEV Environment

A personal DEV environment is a fully-fledged ARO HCP service stack that provides all major infrastructure and service components required to create hosted control planes in a region. Each ARO HCP team member creates their own personal DEV environment for development purposes.

These environments are hosted in the Red Hat Azure tenant, meaning that all [restrictions](environments.md#aro-hcp-azure-tenant-overview) of that tenant apply.

This document will describe how to create and manage a personal DEV environment and how to access them for development purposes.

## Prerequisites

Creating a personal DEV environment requires several prerequisites being met.

Ensure you have access to the RH Azure tenant:

- **RH account**: You need to have a Red Hat account to access the Red Hat Azure tenant (`redhat0.onmicrosoft.com`) where personal DEV environments are created
- **Subscription access**: You need access to the `ARO Hosted Control Planes (EA Subscription 1)` subscription in the Red Hat Azure tenant. Consult the [ARO HCP onboarding guide](https://docs.google.com/document/d/1KUZSLknIkSd6usFPe_OcEYWJyW6mFeotc2lIsLgE3JA/)
- `az` utility >= `2.68.0`
- `az bicep` at the latest version, use `az bicep install` to add this to your system
- `az login` with your Red Hat account:

```bash
az login --tenant 64dc69e4-d083-49fc-9569-ebece1dd1408 --use-device-code
az account set --subscription 1d3378d3-5a3f-4712-85a1-2485495dfc4b
```

> [!TIP]
> If you connect to more than one tenant in Azure regularly, use the `$AZURE_CONFIG_DIR` to segregate your logins.
> The `az` CLI will ask you to re-login when switching tenants normally, but passing different configuration directories
> allows for seamless switching without having to log in again.

The following additional tools are also required:

- `make`
- `kubectl` at the latest version - use `az aks install-cli` to add this to your system

All other tools should be transparently installed by the `make` targets that require them - if you find any missing dependencies, please send a pull request to make sure that the next person to onboard doesn't hit any issues!

## Full Personal DEV Environment Setup

> [!IMPORTANT]
> A word of caution upfront: dev infrastructure is automatically deleted after 48h. If you want to keep your infrastructure indefinitely, run all the following commands with an env variable `PERSIST=true`.
> Please consider the implication on cost if you decide to keep your infrastructure indefinitely.

The creation process can take up to 20 minutes.

   ```bash
   make personal-dev-env
   ```

This command creates a personal DEV environment with a unique name that is derived from your username and deploys all required infrastructure components.

> [!TIP]
> This command can be used to update your personal DEV environment as well. It will apply the latest changes to the infrastructure and services. Steps are cached, so it's quick and safe to re-run the entire environment setup.
> If you only want to update individual aspects of the environment, follow the [partial setup](#partial-personal-dev-environment-setup) instructions.

## Partial Personal DEV Environment Setup

The process described in the previous section caches steps, but even the process of determining that a step should not run again will take a second or two. In case you want to install or update only a specific part of the environment for maximum speed, you can use the following commands.

> [!IMPORTANT]
> Please understand the ARO HCP [architecture](high-level-architecture.md) and the [service deployment concept](service-deployment-concept.md) before proceeding with the partial setup. Not every command can be run in isolation without it's prerequisites being met, e.g. before deploying services, you need to provision the cluster

### Partial Commands

The `make entrypoint/<name>` and `make pipeline/<name>` targets select entrypoints or pipelines from the [`topology.yaml`](./pipeline-topology.md) to run. Use tab completion in your editor to find the available options.

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
  kubectl port-forward svc/clusters-service 8000:8000 -n clusters-service
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

## Logging

In order to enable Kusto logging, Ingest permissions need to be granted to your personal environment. This requires both mgmt and svc cluster to be created. Run following to grant permissions:

```bash
make -C dev-infrastructure kusto.grant.ingest
```

## Observability

By default, metrics from infra/management services are ingested into Azure Managed Prometheus (AMP).

To enable tracing and collect traces into a Jaeger all-in-one instance, run:

  ```bash
  make infra.tracing
  ```

Refer to the [Tracing docs](../observability/tracing/README.md) for more details.

## Cleanup

Besides the automated cleanup for non-persistent environments, you can manually delete your personal DEV environment with the following command, choosing to wait for the deletion to complete or not:

  ```bash
  make cleanup-entrypoint/Region CLEANUP_DRY_RUN=false CLEANUP_WAIT=true
  ```

## Responsibilities

- **Lifecycle**: The personal DEV environments lifecycle is the responsibility of the individual team member. This includes creating, updating, and deleting the environment as well as keeping track of recent change and bugfixes and applying them.

  > [!TIP]
  > Delete personal DEV environments when they are no longer needed to free up resources and prevent unnecessary costs.
  > If you require only a temporary personal DEV environment, don't mark it with `PERSIST=true`.

- **Security**: Your AKS clusters will have access to the shared Service Key Vault which contains certificates and credentials for identities that can act in the Red Hat ARO HCP subscription. Keep this in mind when working with your infrastructure.

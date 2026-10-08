# ARO HCP Personal DEV Environment

A personal DEV environment is a fully-fledged ARO HCP service stack that provides all major infrastructure and service
components required to create hosted control planes in a region. Each ARO HCP team member creates their own personal DEV
environment for development purposes.

These environments are hosted in the Red Hat Azure tenant, meaning that all
[restrictions](environments.md#azure-tenants) of that tenant apply.

This document will describe how to create and manage a personal DEV environment and how to access them for development
purposes.

## Prerequisites

Creating a personal DEV environment requires several prerequisites being met.

Ensure you have access to the RH Azure tenant:

- **RH account**: You need to have a Red Hat account to access the Red Hat Azure tenant (`redhat0.onmicrosoft.com`)
  where personal DEV environments are created
- **Subscription access**: You need access to the `ARO Hosted Control Planes (EA Subscription 1)` subscription in the
  Red Hat Azure tenant. Consult the [ARO HCP onboarding
  guide](https://docs.google.com/document/d/1KUZSLknIkSd6usFPe_OcEYWJyW6mFeotc2lIsLgE3JA/)
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

All other tools should be transparently installed by the `make` targets that require them - if you find any missing
dependencies, please send a pull request to make sure that the next person to onboard doesn't hit any issues!

## Full Personal DEV Environment Setup

> [!IMPORTANT] Environment Cleanup
> A word of caution upfront: dev infrastructure is automatically deleted after 48h. Setting `PERSIST=true` extends
> retention to 15 days instead of permanent.
> Please consider the implication on cost if you decide to persist your infrastructure.

> [!CAUTION] Cleanup-sensitive naming patterns
> The following resource group naming patterns trigger special cleanup rules. Avoid them for custom `DEPLOY_ENV` values
> or ad-hoc resource groups. See [`resourcegroups.policy.yaml`](../tooling/cleanup-sweeper/resourcegroups.policy.yaml)
> for details.
>
> **Prefixes:** `hcp-underlay-pers-`, `hcp-underlay-prow-`, `hcp-underlay-ci`, `hcp-underlay-cspr-`,
> `hcp-underlay-dev-`, `hcp-underlay-perf-`, `hcp-underlay-int-`, `hcp-underlay-stg-`, `hcp-underlay-prod-`
>
> **Suffixes:** `-shared-resources`

The creation process can take up to 20 minutes.

   ```bash
   make personal-dev-env
   ```

This command creates a personal DEV environment with a unique name that is derived from your username. It builds and
pushes all in-repo service images (frontend, backend, admin, sessiongate) from your local checkout and deploys them
along with all required infrastructure components.

> [!NOTE] Update Personal DEV Environment
> This command can be used to update your personal DEV environment as well. It will apply the latest changes to the
> infrastructure and services. Steps are cached, so it's quick and safe to re-run the entire environment setup.
> If you only want to update individual aspects of the environment, follow the [partial
> setup](#partial-personal-dev-environment-setup) instructions.

> [!TIP] Make Options
> [Make Options](./make-options.md) describes how to customize the make build e.g. by defining the container engine to
> be used or limiting parallel jobs. This can be of help when experiencing problems with the build.

### Local Cluster Service Development Setup

If you plan to run the Cluster Service locally (not deployed to Kubernetes) for development, use the following command
instead:

   ```bash
   make local-pers-dev-env
   ```

This command performs all the steps of `personal-dev-env` plus:

- Grants your user permissions to access Key Vaults (service and management)
- Grants your user permissions to access OIDC storage
- Generates the local configuration files for cluster service: provision-shards, ocp-versions, azure-runtime,
  azure-operators-mi

After this, the config files are created in `cluster-service/local/`. This contains the configuration needed to run the
Cluster Service locally against your personal dev environment.

## Partial Personal DEV Environment Setup

The process described in the previous section caches steps, but even the process of determining that a step should not
run again will take a second or two. In case you want to install or update only a specific part of the environment for
maximum speed, you can use the following commands.

> [!IMPORTANT]
> Please understand the ARO HCP [architecture](high-level-architecture.md) and the [service deployment
> concept](service-deployment-concept.md) before proceeding with the partial setup. Not every command can be run in
> isolation without it's prerequisites being met, e.g. before deploying services, you need to provision the cluster

### Partial Commands

The `make entrypoint/<name>` and `make pipeline/<name>` targets select entrypoints or pipelines from the
[`topology.yaml`](./pipeline-topology.md) to run. Use tab completion in your editor to find the available options.

## Troubleshooting a failed or slow setup

Setup is long-running and talks to a lot of Azure APIs, so partial failures and timeouts are normal rather than
exceptional. Work through these in order.

### First: just re-run it

```bash
make personal-dev-env
```

Steps are cached in `.step-cache` (gitignored), keyed by a hash of each step's inputs. A re-run skips everything that
already succeeded with unchanged inputs and picks up where it stopped, so a transient Azure error or a timeout usually
costs only the failed step. This is the right first response to almost any failure.

### Find out what actually failed

The run writes three artifacts, all gitignored:

| Path | What it tells you |
|---|---|
| `_artifacts/junit_entrypoint.xml` | Per-step pass/fail — the quickest way to see *which* step broke |
| `timing/steps.yaml` | How long every step took — use this when the run is slow rather than broken |
| `_artifacts/config.yaml` | The fully rendered config the run actually used, after all templating and overrides |

Turn `timing/steps.yaml` into something readable:

```bash
make visualize
```

To see the dependency graph — what runs, in what order, and what a failed step blocks:

```bash
make graph/entrypoint/Region   # writes .graph.dot and .graph.html
```

### Turn up the logs

`LOG_LEVEL` defaults to `3` and maps to templatize's `--verbosity`. Raise it when a step fails without a useful message:

```bash
make personal-dev-env LOG_LEVEL=7
```

### When it times out

Two different timeouts are worth separating.

A step that fails while *waiting for an earlier deployment of the same pipeline to finish* is hitting
`--deployment-timeout-seconds`, which defaults to 180:

```bash
make personal-dev-env EXTRA_ARGS="--deployment-timeout-seconds=600"
```

A run that fails under Azure API throttling, or is hard to read because many steps interleave, benefits from running
serially. Concurrency is unbounded by default:

```bash
make personal-dev-env EXTRA_ARGS="--concurrency=1"
```

Both `LOG_LEVEL` and `EXTRA_ARGS` propagate down to the underlying `entrypoint/Region` run, so they work on
`make personal-dev-env`, `make entrypoint/<name>` and `make pipeline/<name>` alike.

If the failure is in the image build rather than the deployment, limit build parallelism instead — see
[Make Options](./make-options.md):

```bash
make personal-dev-env BUILD_SERVICES_OPTS="-j1"
```

### Re-run only the broken part

Once you know which service group failed, re-run that alone instead of the whole region:

```bash
make pipeline/RP.Frontend
make entrypoint/Region        # everything, still cached
```

### Force a clean re-run

If you suspect the cache is masking the real problem — for example a step that "succeeds" from cache but left behind
broken state — bypass or discard it:

```bash
make personal-dev-env STEP_CACHE_DIR=""   # ignore the cache for this run
rm -rf .step-cache                        # discard it permanently
```

### Check the inputs before a long run

Catch a malformed pipeline or config before spending 20 minutes finding out:

```bash
make validate-config-pipelines
```

If a step fails because a tool is missing or out of date:

```bash
make install-tools
```

### Start over

Preview what a teardown would remove without touching anything (`CLEANUP_DRY_RUN` defaults to `true`):

```bash
make cleanup-entrypoint/Region
```

Then delete for real with the command in [Cleanup](#cleanup). Note that a non-dry-run cleanup also deletes
`.step-cache`, so the next `make personal-dev-env` is a full, uncached rebuild.

## Accessing the environment

Once the environment has been provisioned, you can inspect it in the Azure Portal. Look out for the following
Resourcegroups:

- **hcp-underlay-$(regionShort)$(usernameShortPrefix)**: holds the regional resources like Eventgrid, DNS zones, ...
- **hcp-underlay-$(regionShort)$(usernameShortPrefix)-svc**: holds the service cluster and supporting infra for its
  components
- **hcp-underlay-$(regionShort)$(usernameShortPrefix)-mgmt-1**: holds the management cluster and supporting infra for
  its components

The `-svc` and `-mgmt-1` resource groups contain the service and management AKS clusters respectively. Access to these
clusters has been granted as part of the provisioning process and you can find respective kubeconfigs in `~/.kube/` as
files that are named after their Resourcegroups. You can also use the following helpers to setup the `KUBECONFIG`
environment variable:

  ```bash
  export KUBECONFIG=$(make infra.svc.aks.kubeconfigfile)
  export KUBECONFIG=$(make infra.mgmt.aks.kubeconfigfile)
  ```

The cluster in personal DEV have no reachable ingress. To interact with the services you deploy use `kubectl
port-forward`

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

## Observability

By default, metrics from infra/management services are ingested into Azure Managed Prometheus (AMP).

## Cleanup

Besides the automated cleanup for non-persistent environments, you can manually delete your personal DEV environment
with the following command, choosing to wait for the deletion to complete or not:

  ```bash
  make cleanup-entrypoint/Region CLEANUP_DRY_RUN=false CLEANUP_WAIT=true
  ```

## Responsibilities

- **Lifecycle**: The personal DEV environments lifecycle is the responsibility of the individual team member. This
  includes creating, updating, and deleting the environment as well as keeping track of recent change and bugfixes and
  applying them.

  > [!TIP]
  > Delete personal DEV environments when they are no longer needed to free up resources and prevent unnecessary costs.
  > If you require only a temporary personal DEV environment, don't mark it with `PERSIST=true`.

- **Security**: Your AKS clusters will have access to the shared Service Key Vault which contains certificates and
  credentials for identities that can act in the Red Hat ARO HCP subscription. Keep this in mind when working with your
  infrastructure.

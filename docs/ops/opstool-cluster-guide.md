# Opstool Cluster Guide

The `opstool` cluster is a standalone dev-only AKS cluster used for operational tooling workloads that do not fit into the main service or management cluster deployment paths.

It uses the same `templatize` + `pipeline.yaml` rollout system as the rest of the repository, but it is intentionally configured and deployed separately from the normal `Global` and `Region` topologies.

## At A Glance

| Item | Value |
| --- | --- |
| Purpose | Host operational tooling workloads |
| Cloud / environment | `dev` / `dev-ci` |
| Current region | `westus3` |
| Standalone config | `config/config-dev-ci.yaml` |
| Standalone topology | `topology-dev-ci.yaml` |
| Infra service group | `Microsoft.Azure.ARO.HCP.DevCI.Infra` |
| Canonical workload example | `tooling/tenant-quota` |

## How Opstool Differs From The Main Environments

The rest of the repo mostly follows the shared deployment model built around `config/config.yaml`, `topology.yaml`, and service groups such as `Microsoft.Azure.ARO.HCP.Global` and `Microsoft.Azure.ARO.HCP.Region`.

The `dev-ci` topology does not plug into that graph automatically.

- It has its own config file: `config/config-dev-ci.yaml`.
- It has its own topology: `topology-dev-ci.yaml`.
- Its infra rollout lives in `dev-infrastructure/dev-ci/cluster/opstool-aks-pipeline.yaml`.
- Workloads are added as child service groups under the `DevCI.Infra` entrypoint.

This means `opstool` gets all the benefits of the shared tooling, but none of the implicit shared-environment wiring. If it needs a shared resource, that dependency must be wired explicitly in the standalone config or pipeline.

## Topology And Rollout Model

`topology-dev-ci.yaml` defines a single entrypoint:

- `Microsoft.Azure.ARO.HCP.DevCI.Infra`

That infra service group owns the cluster and shared cluster-level dependencies. Workloads are added beneath it as children. Today, `tenant-quota` is the concrete example of this pattern.

In practice the rollout shape is:

1. Deploy the standalone AKS and supporting Azure resources.
2. Export shared outputs for child workloads.
3. Deploy shared alerting infrastructure.
4. Deploy the in-cluster Prometheus stack.
5. Deploy child workloads such as `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota`.

For local execution of the full standalone topology, use:

```bash
make dev-ci-local-run
```

For a dry run:

```bash
DRY_RUN=true make dev-ci-local-run
```

## What The Infra Pipeline Creates

`dev-infrastructure/dev-ci/cluster/opstool-aks-pipeline.yaml` owns the shared cluster infrastructure for workloads running in `opstool`.

The infra template provisions:

- The standalone AKS cluster.
- A workload Key Vault for app secrets.
- A dedicated Azure Monitor Workspace for the cluster.
- Data collection resources for Prometheus remote write into Azure Monitor.
- A shared email Action Group for alert notifications.
- Shared user-assigned managed identities already used by the cluster, such as `opstool`, `prometheus`, and `tenant-quota`.

The output template `dev-infrastructure/templates/output-opstool-cluster.bicep` exposes the shared values that child workload pipelines consume, including:

- `azureMonitorWorkspaceId`
- `sharedActionGroupId`
- `workloadKVName`
- `dcrRemoteWriteUrl`
- workload UAMI IDs and client IDs

It also exposes placeholder Kusto outputs for pipeline schema compatibility. `opstool` does not currently use Kusto logging, so empty Kusto outputs are expected here.

## Shared Global Resource Wiring

Unlike the standard `Region` rollout, `opstool` does not inherit the shared dev environment outputs from `config/config.yaml`. Shared resources have to be wired intentionally.

Today the most important shared dependency is the dev SVC ACR:

- `config/config-dev-ci.yaml` declares `svc.acr`.
- That config points at the shared dev registry in the global resource group.
- `dev-infrastructure/templates/opstool-cluster.bicep` uses that information to grant the cluster pull access to the registry.

There are also important non-wirings to be aware of:

- `opstool` uses its own Azure Monitor Workspace, not the normal regional AMW flow.
- The shared dev Grafana datasource integration used by the regular `Region` rollout is not automatically applied here.
- If `opstool` needs additional shared global resources in the future, that hookup must be added explicitly instead of assuming the main environment behavior applies.

## Observability Model

Observability in `opstool` is intentionally simple:

- In-cluster Prometheus is deployed by the infra pipeline.
- Prometheus remote writes metrics into the standalone `opstool` Azure Monitor Workspace.
- Alerting is defined in Azure Monitor Prometheus rule groups, not in-cluster `PrometheusRule` objects.
- All workloads share the `opstool-email-alerts` Action Group for notifications.

When adding workload monitoring:

- Expose metrics through a `Service`.
- Add a `ServiceMonitor` so the shared Prometheus instance scrapes the workload.
- Put alert logic in `alerting.bicep` if the app needs alerts.
- Prefer normal `up` and `absent` style expressions in Azure Monitor rule groups for target health.

## Workload Pattern

New workloads should follow the same layout as `tooling/tenant-quota`:

```text
tooling/my-new-app/
├── deploy/
├── pipeline.yaml
├── alerting.bicep        # optional
├── Makefile
└── source code
```

The standard pattern is:

1. Put code and Helm assets together under `tooling/<app>/`.
2. Add a child service group to `topology-dev-ci.yaml`.
3. Use a workload pipeline that reads shared outputs from `output-opstool-cluster.bicep`.
4. Reuse the shared `opstool` cluster resources unless the workload truly needs new infra.

If the app needs a new Azure identity or new cluster-level shared output, update both:

- `dev-infrastructure/templates/opstool-cluster.bicep`
- `dev-infrastructure/templates/output-opstool-cluster.bicep`

If the app only needs Helm deployment and existing shared outputs, no infra pipeline change is required.

## Identity and Secrets

Most `opstool` workloads should follow the same mechanics already used by `tenant-quota`.

### Workload Identity

- Give the workload a dedicated user-assigned managed identity when it needs Azure access.
- Annotate the Kubernetes `ServiceAccount` with the workload identity client ID.
- Keep the workload pipeline wired to the matching UAMI output from `output-opstool-cluster.bicep`.

### Key Vault Secrets

- Store shared app secrets in the `opstool` workload Key Vault.
- Mount secrets through a `SecretProviderClass` using the CSI driver.
- Keep secret names in `config/config-dev-ci.yaml` when they are part of workload configuration.

Mounted Key Vault files can change when the underlying secret rotates. Applications still need a reload strategy if they read credentials only once at startup. For file-backed secrets, either implement hot reload in the process or restart the workload when config or secret content changes.

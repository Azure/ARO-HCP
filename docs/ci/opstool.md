# Opstool CI Platform

`opstool` is a standalone, dev-only AKS cluster that hosts operational tooling which does not fit into the main service or management cluster paths: the DEV CI telemetry exporter, CIHealth, and the supporting gateway and certificate management. It is rolled out with the same `templatize` and `pipeline.yaml` machinery as the rest of the repository, but as part of the standalone `dev-ci` topology (see [Dev-CI Topology](dev-ci-topology.md) for the rollout graph and the service groups that hang off it). For alert response and monitoring maintenance, see [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md).

| Item | Value |
| --- | --- |
| Environment | `dev` / `dev-ci`, region `westus3` |
| Configuration | `config/config-dev-ci.yaml` |
| Infra pipeline | `dev-infrastructure/dev-ci/cluster/opstool-aks-pipeline.yaml` |
| Reference workload | `tooling/tenant-quota` |

## How Opstool Differs From The Main Environments

The main environments share `config/config.yaml` and `topology.yaml` and inherit shared dev resources through the `Global` and `Region` service groups. `opstool` gets the same tooling but none of that implicit wiring: anything it needs from a shared resource has to be declared explicitly in `config/config-dev-ci.yaml` or in its pipelines.

The clearest example is the dev SVC container registry. `config/config-dev-ci.yaml` declares `svc.acr`, and `opstool-cluster.bicep` uses it to grant the cluster pull access. The cluster also has its own Azure Monitor Workspace rather than going through the regional AMW flow, and the shared dev Grafana integration that `Region` sets up is not applied to it. If `opstool` needs another shared global resource, hook it up explicitly.

## What The Infra Pipeline Provides

The infra pipeline creates the AKS cluster and the resources that workloads share:

- a workload Key Vault for application secrets;
- an Azure Monitor Workspace with the data collection resources that Prometheus remote-writes into;
- the shared `opstool-pagerduty` Action Group used for alert notifications;
- user-assigned managed identities for the cluster's own components and for workloads such as `tenant-quota`.

It then upgrades the node pools to the configured Kubernetes version, deploys the in-cluster Prometheus stack, and publishes the values that child workload pipelines consume (workspace ID, action group ID, Key Vault name, remote-write URL, identity IDs) through `dev-infrastructure/templates/output-opstool-cluster.bicep`. The Kusto outputs in that template are empty placeholders required by the pipeline schema for the Prometheus step; the grant that lets the collector write CI job outcomes to the DEV Kusto is a separate step in the same pipeline.

## Observability

Prometheus in the cluster remote-writes into the `opstool` Azure Monitor Workspace, and alerts are Azure Monitor Prometheus rule groups rather than in-cluster `PrometheusRule` objects. [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md) describes the full alert path. For a new workload this means exposing metrics through a `Service`, adding a `ServiceMonitor`, and putting any alert rules in the workload's own `alerting.bicep`. Target-health alerts are normally plain `up` or `absent` expressions.

## Adding A Workload

Follow the layout of `tooling/tenant-quota`:

```text
tooling/my-new-app/
├── deploy/             # Helm chart
├── pipeline.yaml
├── alerting.bicep      # optional
├── Makefile
└── source code
```

Add a child service group for the pipeline to `topology-dev-ci.yaml` and have the pipeline read the shared outputs from `output-opstool-cluster.bicep`. If the workload only needs Helm and the existing outputs, no change to the infra pipeline is required. If it needs a new Azure identity or a new cluster-level output, add it to both `opstool-cluster.bicep` and `output-opstool-cluster.bicep`.

### Identity And Secrets

Workloads that need Azure access get their own user-assigned managed identity, and the Kubernetes `ServiceAccount` is annotated with its client ID, matching the pattern in `tenant-quota`. Application secrets go in the workload Key Vault and are mounted through a `SecretProviderClass` using the CSI driver; secret names that are part of workload configuration belong in `config/config-dev-ci.yaml`.

Mounted Key Vault files change when the secret is rotated, but a process that reads its credentials only at startup will not notice. File-backed secrets need either hot reload in the application or a workload restart when the secret or configuration changes.

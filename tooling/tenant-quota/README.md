# DEV CI Telemetry Exporter (`tenant-quota`)

`tenant-quota` is the historical name of the extensible DEV CI telemetry exporter running on the standalone `opstool` AKS cluster. It began by collecting tenant and subscription quota data, but its scope is growing beyond quota-only telemetry.

Current examples include tenant capacity, subscription quota, E2E resource-group expiry, and Prow job outcomes and durations. The design supports additional CI telemetry sources without turning this README into a fixed collector or metric inventory.

For alert response, routing, and monitoring maintenance, use the canonical [DEV CI Monitoring and Alert Response](../../docs/ci/dev-ci-monitoring.md) runbook.

## Collection Sources of Truth

Use the implementation and deployment sources rather than maintaining a copied inventory here:

- [`main.go`](main.go) registers the collectors run by the process and defines its HTTP endpoints.
- [`pkg/`](pkg/) contains collector, metric, configuration, and credential behavior.
- [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml), under `opstool.tenantQuota`, is the source of truth for deployed configuration.

## Runtime Model

At startup the process loads the rendered runtime config, validates credentials, starts watching mounted secret files, resolves subscription IDs when needed, starts the registered collector loops, and serves its HTTP endpoints.

The detailed startup, configuration, credentials, and rendered deployment behavior is defined in:

- [`main.go`](main.go)
- [`pkg/config/config.go`](pkg/config/config.go)
- [`pkg/credentials/provider.go`](pkg/credentials/provider.go)
- [`deploy/config.yaml.tmpl`](deploy/config.yaml.tmpl)

The service listens on port `8080` and exposes `/healthz`, `/readyz`, `/version`, and `/metrics`.

## Deployment Layout

The rollout is owned by `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota` in [`pipeline.yaml`](pipeline.yaml).

The pipeline:

- reads shared outputs from [`dev-infrastructure/templates/output-opstool-cluster.bicep`](../../dev-infrastructure/templates/output-opstool-cluster.bicep)
- deploys the Helm chart using [`deploy/values.yaml.tmpl`](deploy/values.yaml.tmpl)
- deploys Azure Monitor rule groups from [`alerting.bicep`](alerting.bicep)

For cluster architecture, shared monitoring, identity, secret, and workload patterns, see [Opstool CI Platform](../../docs/ci/opstool.md).

## Configuration Source of Truth

The source of truth for deployed configuration is [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml), under `opstool.tenantQuota`.

Do not update tenant definitions in `deploy/values.yaml`. That file contains static chart defaults.

Subscription IDs are resolved at runtime from configured subscription display names rather than stored in the config.
Role assignment usage and limits are retrieved directly from Azure rather than configured per subscription.

## Secrets and Credential Reload

Client secrets live in the `opstool` workload Key Vault and are mounted into the pod with the CSI Secret Store driver.

Credential reload behavior is defined in [`pkg/credentials/provider.go`](pkg/credentials/provider.go). A Key Vault secret update can be picked up without restarting the pod when the CSI-mounted file refreshes and the process rereads the invalidated credential on its next use.

## Alerting

[`alerting.bicep`](alerting.bicep) is the source of truth for alert names, expressions, thresholds, durations, annotations, and routing. Rules are deployed into the `opstool` Azure Monitor Workspace and use the shared `opstool-pagerduty` Action Group supplied by the `DevCI.Unprivileged` rollout.

Do not duplicate the evolving alert catalog in this README. See [DEV CI Monitoring and Alert Response](../../docs/ci/dev-ci-monitoring.md) for response and maintenance procedures.

## Local Development

Run all commands from `tooling/tenant-quota`.

The [`Makefile`](Makefile) is the source of truth for supported local-development and image-workflow targets.

Example local workflow:

```bash
cd tooling/tenant-quota
make render-config
make fetch-secrets
make run
```

To run the containerized version locally:

```bash
cd tooling/tenant-quota
make run-image
```

## Managing Tenants

### Add or reconcile a tenant service principal

Use:

```bash
cd tooling/tenant-quota
./scripts/manage-service-principals.sh --tenant redhat
./scripts/manage-service-principals.sh --list
```

This script is the supported path for creating and reconciling the service principals, role assignments, and Key Vault secrets used by the exporter.

After adding or changing a tenant:

1. Update [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml).
2. Redeploy the `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint with `make dev-ci-local-run`. For a targeted redeploy, run only the `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota` service group.

### Renew a client secret

List current tenant credentials:

```bash
cd tooling/tenant-quota
./scripts/renew-sp-secret.sh --list
```

Renew one tenant:

```bash
cd tooling/tenant-quota
az login --tenant <azure-ad-tenant-id>
./scripts/renew-sp-secret.sh --tenant RedHat0
```

If needed, the script can also restart the deployment:

```bash
./scripts/renew-sp-secret.sh --tenant RedHat0 --restart
```

Because the runtime watches mounted secret files, a restart should normally be optional and is mainly a recovery step if the rotated secret does not propagate promptly.

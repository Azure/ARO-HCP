# tenant-quota

`tenant-quota` is the `opstool` workload that collects Azure quota data and exposes it as Prometheus metrics.

It currently covers two domains:

- Azure AD / Entra directory quota per tenant
- Azure subscription quota usage and limits for configured subscriptions and regions

The workload is deployed to the standalone `opstool` AKS cluster and ships its metrics through the shared `opstool` Prometheus stack into the `opstool` Azure Monitor Workspace.

## What It Collects

### Tenant directory quota

For each configured tenant with `directoryQuota: true`, the collector queries Microsoft Graph and exports:

- `tenant_quota_usage_percentage`
- `tenant_quota_total`
- `tenant_quota_used`
- `tenant_remaining_capacity`

These metrics are labeled with:

- `tenant_id`
- `tenant_name`

### Subscription quota

For each configured subscription, the collector resolves the subscription ID at runtime and exports:

- `azure_quota_usage`
- `azure_quota_limit`

These metrics are labeled with:

- `source`
- `subscription_id`
- `subscription_name`
- `region`
- `quota_name`
- `localized_name`

The current subscription quota sources are:

- `rbac` for role assignment count versus the configured role assignment limit
- `compute` for Azure Compute regional usage quotas
- `network` for Azure Network regional usage quotas

## Runtime Model

At startup the process loads the rendered runtime config, validates credentials, starts watching mounted secret files, resolves subscription IDs when needed, starts the collector loops, and serves the HTTP endpoints.

The source of truth for the current startup flow, HTTP handlers, and runtime defaults is:

- `main.go`
- `pkg/config/config.go`
- `pkg/credentials/provider.go`
- `deploy/config.yaml.tmpl`

## Deployment Layout

The rollout is owned by `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota` in `pipeline.yaml`.

The pipeline:

- reads shared outputs from `dev-infrastructure/templates/output-opstool-cluster.bicep`
- deploys the Helm chart using `deploy/values.yaml.tmpl`
- deploys Azure Monitor rule groups from `alerting.bicep`

## Configuration Source Of Truth

The source of truth for deployed configuration is `config/config-dev-ci.yaml`, under `opstool.tenantQuota`.

Do not update tenant definitions in `deploy/values.yaml`. That file is only static chart defaults.

Subscription IDs are resolved at runtime from the configured subscription display names rather than stored in the config.

## Secrets And Credential Reload

Client secrets live in the `opstool` workload Key Vault and are mounted into the pod with the CSI Secret Store driver.

The credential reload behavior is defined in `pkg/credentials/provider.go`.

In short, a Key Vault secret update can be picked up without restarting the pod, as long as the CSI-mounted file is refreshed and the process rereads the invalidated credential on its next use.

## Alerts

Alert rules are defined in `alerting.bicep` and deployed into the `opstool` Azure Monitor Workspace. Notifications use the shared `opstool-email-alerts` Action Group provided by the `DevCI.Infra` rollout.

## Local Development

Run all commands from `tooling/tenant-quota`.

The `Makefile` is the source of truth for the supported local development and image workflow targets.

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

This script is the supported path for creating and reconciling the service principals, role assignments, and Key Vault secrets used by the collector.

After adding or changing a tenant:

1. Update `config/config-dev-ci.yaml`.
2. Redeploy `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota` or just run the whole topology via `make dev-ci-local-run`

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

Because the runtime now watches mounted secret files, restart should normally be optional and mainly useful as a recovery step if the rotated secret does not propagate promptly.

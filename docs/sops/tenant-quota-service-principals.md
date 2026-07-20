# Tenant Quota Service Principals

This SOP covers creating and rotating credentials for the tenant-quota collector service principals.

## Background

The tenant-quota collector monitors Azure directory and subscription quota across multiple tenants. Each tenant has its own Entra service principal with Reader access on the monitored subscriptions and (for the RedHat0 tenant) Organization.Read.All Graph permission for directory quota.

Two scripts manage these SPs:

- [`manage-service-principals.sh`](../../tooling/tenant-quota/scripts/manage-service-principals.sh) -- creates the SP, assigns roles, grants Graph permissions, stores the secret in Key Vault
- [`renew-sp-secret.sh`](../../tooling/tenant-quota/scripts/renew-sp-secret.sh) -- rotates the client secret and updates Key Vault

These cannot be migrated to Bicep because they operate cross-tenant and require tenant-specific admin consent.

### Configured tenants

| Tenant | Tenant ID | KV Secret Name |
|--------|-----------|----------------|
| RedHat0 | `64dc69e4-d083-49fc-9569-ebece1dd1408` | `custom-metrics-collector-redhat0-client-secret` |
| TestTestARO | `93b21e64-4824-439a-b893-46c9b2a51082` | `custom-metrics-collector-testtestaro-client-secret` |

## Prerequisites

- `az` CLI logged into the **target tenant** (not the dev tenant): `az login --tenant <tenant-id>`
- Key Vault access on `opstool-kv-usw3` (in the dev tenant) for storing secrets
- Tenant admin role for admin consent (RedHat0 tenant only)

## Procedure: Initial Setup

For a new tenant (e.g. RedHat0):

```bash
cd tooling/tenant-quota/scripts
./manage-service-principals.sh --tenant redhat
```

If the Key Vault is in a different tenant than the SP, use the split workflow:

```bash
# Step 1: Create SP in target tenant (saves secret to a local file)
az login --tenant <target-tenant-id>
./manage-service-principals.sh --tenant redhat --skip-keyvault

# Step 2: Upload secret to Key Vault in dev tenant
az login --tenant 64dc69e4-d083-49fc-9569-ebece1dd1408
./manage-service-principals.sh --tenant redhat --keyvault-only --secret-file /tmp/sp-secret.json
```

After setup, update [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) under `opstool.tenantQuota.tenants` with the new SP's client ID and KV secret name, then redeploy the collector.

## Procedure: Secret Rotation

### 1. List current credential expiration

```bash
cd tooling/tenant-quota/scripts
./renew-sp-secret.sh --list
```

### 2. Rotate

```bash
az login --tenant <tenant-id>
./renew-sp-secret.sh --tenant RedHat0
```

To also restart the collector pod immediately:

```bash
./renew-sp-secret.sh --tenant RedHat0 --restart
```

Without `--restart`, the CSI driver picks up new KV secrets within ~2 minutes.

### 3. Verify

Check the collector logs for successful quota collection after rotation:

```bash
kubectl -n tenant-quota logs deploy/tenant-quota-collector --tail=20
```

## Key Locations

| What | Where |
|------|-------|
| Setup script | [`tooling/tenant-quota/scripts/manage-service-principals.sh`](../../tooling/tenant-quota/scripts/manage-service-principals.sh) |
| Rotation script | [`tooling/tenant-quota/scripts/renew-sp-secret.sh`](../../tooling/tenant-quota/scripts/renew-sp-secret.sh) |
| Key Vault | `opstool-kv-usw3` |
| Collector config | [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) under `opstool.tenantQuota.tenants` |
| Collector deployment | `tenant-quota` namespace on the opstool AKS cluster |

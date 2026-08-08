# CI Bot Secret Provisioning

This SOP describes how to manually provision or re-provision CI bot Entra application credentials in Azure Key Vault.

## Background

The [`ci-bot-ensure-secret.sh`](../../dev-infrastructure/scripts/ci-bot-ensure-secret.sh) script creates a client secret on an existing CI bot Entra application and stores it (along with the client ID and tenant ID) in Azure Key Vault. The pipeline runs this automatically for INT, STG, and PROD bots as part of the `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC` service group.

Manual runs are needed when:

- A Key Vault secret was accidentally deleted
- A new environment's bot was created but the pipeline hasn't run yet
- Troubleshooting credential issues in a specific environment

The script is idempotent: it skips secrets that already exist in Key Vault.

## Prerequisites

- `az` CLI logged into the DEV tenant (`64dc69e4-d083-49fc-9569-ebece1dd1408`)
- Key Vault write access on the target vault (e.g. `opstool-kv-usw3`)
- The Entra application must already exist (created by `ci-bot-identity.bicep`)

## Procedure

### 1. Run the script

```bash
cd dev-infrastructure/scripts

APP_NAME="OpenShift Release Bot - INT" \
ENV_NAME="int" \
KEY_VAULT_NAME="opstool-kv-usw3" \
./ci-bot-ensure-secret.sh
```

Replace `APP_NAME` and `ENV_NAME` with the target environment. Valid combinations are defined in [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) under `ci.<env>.bot.applicationName`.

### 2. Verify

```bash
az keyvault secret show --vault-name opstool-kv-usw3 --name ci-bot-int-client-secret --query '{name:name,created:attributes.created}' -o table
```

### 3. Admin consent (if warned)

The script attempts `az ad app permission admin-consent` as a best-effort step. If it warns about insufficient privileges, ask a tenant admin to grant consent manually via the Azure Portal (Entra ID > App registrations > the bot app > API permissions > Grant admin consent).

## Key Locations

| What | Where |
|------|-------|
| Script | [`dev-infrastructure/scripts/ci-bot-ensure-secret.sh`](../../dev-infrastructure/scripts/ci-bot-ensure-secret.sh) |
| Pipeline step | `ci-bot-secret-{env}` in [`dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`](../../dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml) |
| Key Vault secrets | `ci-bot-{env}-client-secret`, `ci-bot-{env}-client-id`, `ci-bot-{env}-tenant-id` |
| Bot app names | [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) under `ci.<env>.bot.applicationName` |

# OpenShift CI Credentials for ARO HCP E2E Tests

This directory contains scripts to manage Azure AD credentials for ARO HCP E2E tests in the Test Test Azure Red Hat OpenShift tenant.

## Base Image

The `Dockerfile` in this directory defines the base image used in our release configurations. It extends the OCP builder image with additional tools:

- OCP builder image (`registry.ci.openshift.org/ocp/builder`) as a base, which includes Go and runs on RHEL9
- Azure CLI with Bicep extension
- kubectl (latest stable) and kubelogin (latest release)
- OpenShift CLI (oc, latest stable)
- Promtool (pinned version)
- Required system tools (make, git, procps-ng)

### Version Management

- **Go / builder image**: Defined in `.ci-operator.yaml` at the repo root. The `build_root_image.tag` field specifies the OCP builder image tag (e.g., `rhel-9-golang-1.25-openshift-4.21`). This is the single source of truth for both CI (ci-operator reads it directly) and local builds (`versions.mk` extracts it via `yq`).
- **Promtool**: Pinned in `versions.mk` and as an `ARG` default in the `Dockerfile`.
- **kubectl, kubelogin, oc**: Always download the latest stable version — no pinning required.

When updating versions:

1. **Go bump**: Update `.ci-operator.yaml` first (the base-ci image must have the new Go version before `go.work` is updated). Then update `go.work` in a follow-up PR.
2. **Promtool bump**: Edit `versions.mk` and update the `ARG` default in the `Dockerfile`.
3. Run `make verify` to check that Go minor version in `go.work` matches the builder image tag, and that the promtool version is in sync.
4. Run `make test` to build the image and smoke-test all tools.

### CI Build Flow

The builder image tag is defined in `.ci-operator.yaml` at the repo root. ci-operator uses this as inrepo config to resolve the build root image for the base image build, replacing the previously centrally-defined configuration in https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__baseimage-generator.yaml. A Post Submit job builds this Docker image from the Dockerfile after any PR merges.

And in our Release Job(presubmit/periodic) we consume this prebuild images as build root https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml#L7 .

## Scripts Overview

| Script | Purpose |
|--------|---------|
| `create-openshift-release-bot-msft-test.sh` | Create Azure AD app + roles + permissions (calls `recycle-openshift-release-bot-creds.sh`) |
| `recycle-openshift-release-bot-creds.sh` | Rotate credentials and update `*-test-tenant` Vault secrets |
| `switch-vault-tenant.sh` | Switch active secrets between Test Test tenant and legacy tenant |

## Vault Secret Structure

```
selfservice/hcm-aro/
├── aro-hcp-stg              # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-stg-test-tenant  # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
├── aro-hcp-stg-legacy       # Original legacy tenant credentials (backup, no secretsync)
├── aro-hcp-prod             # Active secret (used by Prow jobs, with secretsync)
├── aro-hcp-prod-test-tenant # Test Test Azure Red Hat OpenShift tenant credentials (backup, no secretsync)
└── aro-hcp-prod-legacy      # Original legacy tenant credentials (backup, no secretsync)
```

## Prerequisites

- Enable Global Administrator via PIM (for app registration API Resource permission admin consent)
- Enable User Access Administrator / Owner role via PIM (for role assignments)
- az CLI installed and logged in: `az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082`
- HashiCorp Vault CLI installed
- jq installed

## Initial Setup (One-time)

```bash
# Create Azure AD app, assign roles, grant permissions, and store credentials
./create-openshift-release-bot-msft-test.sh

# Switch to Test Test tenant
./switch-vault-tenant.sh --to test-tenant
```

## Switching Tenants

```bash
# Check current tenant status
./switch-vault-tenant.sh --status

# Switch to Test Test Azure Red Hat OpenShift tenant
./switch-vault-tenant.sh --to test-tenant

# Rollback to legacy tenant
./switch-vault-tenant.sh --to legacy

# Switch only specific environment
./switch-vault-tenant.sh --to test-tenant --env stg
./switch-vault-tenant.sh --to test-tenant --env prod
```

## Credential Rotation

When credentials are expiring or need to be rotated:

```bash
# Rotate credentials (keeps old as backup)
./recycle-openshift-release-bot-creds.sh

# Rotate and delete old credentials
./recycle-openshift-release-bot-creds.sh --delete-old

# Rotate only specific environment
./recycle-openshift-release-bot-creds.sh --env stg

# Apply rotated credentials to active secrets
./switch-vault-tenant.sh --to test-tenant
```

## Verification

```bash
# Check current tenant status
./switch-vault-tenant.sh --status
```

## Troubleshooting

### Rollback to Legacy Tenant

If issues occur with Test Test tenant:

```bash
./switch-vault-tenant.sh --to legacy
```

### Check Prow Job Logs

Look for `Acquired 1 lease(s) for aro-hcp-test-tenant-quota-slice` in the build logs to confirm Test Test tenant is being used.

## Documentation

For detailed documentation, see:
- [Test Test Azure Red Hat OpenShift Tenant Access SOP](../../docs/sops/test-test-tenant-access.md)

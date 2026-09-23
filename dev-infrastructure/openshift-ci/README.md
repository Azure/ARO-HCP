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
| `recycle-openshift-release-bot-creds.sh` | Rotate credentials and patch the active `aro-hcp-prod` Vault profile |

## Vault Secret Structure

The rotation script updates `kv/selfservice/hcm-aro/aro-hcp-prod`. It patches
only `client-id`, `client-secret`, and `tenant`, preserving the customer shard
inventory and secret-sync metadata.

The supported profile shape is documented in
[Cluster Profile Secret Contract](../../docs/ci/cluster-profile-secret-contract.md).

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
```

## Credential Rotation

When credentials are expiring or need to be rotated:

```bash
# Rotate credentials (keeps old as backup)
./recycle-openshift-release-bot-creds.sh

# Rotate and delete old credentials
./recycle-openshift-release-bot-creds.sh --delete-old
```

## Verification

```bash
# Verify the active profile identity without printing the credential.
vault kv get -field=tenant kv/selfservice/hcm-aro/aro-hcp-prod
vault kv get -field=client-id kv/selfservice/hcm-aro/aro-hcp-prod
```

## Troubleshooting

Credential rotation no longer copies whole profiles or switches tenants. If a
rotation must be rolled back, create a new credential for the intended
application and patch the three identity fields again. Do not restore legacy
`subscription-id`, `subscription-name`, or `infra-subscription-id` fields.

### Check Prow Job Logs

Check the credential-selection step and confirm the expected cluster profile
directory was selected. Slot-managed jobs publish
`SELECTED_CLUSTER_PROFILE_DIR` in `${SHARED_DIR}/aro-hcp-slot.env`.

## Documentation

For detailed documentation, see:
- [Test Test Azure Red Hat OpenShift Tenant Access SOP](../../docs/sops/test-test-tenant-access.md)

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

- **Go / builder image**: Defined in `.ci-operator.yaml` at the repo root. The `build_root_image.tag` field specifies the OCP builder image tag (e.g., `rhel-9-golang-1.26-openshift-4.22`). This is the single source of truth for both CI (ci-operator reads it directly) and local builds (`versions.mk` extracts it via `yq`).
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

## Nested Podman Base Image

`Dockerfile.nested-podman` defines a separate, source-free toolchain for
integration and mega-linter jobs. It extends the existing OpenShift CI
`ci/nested-podman:latest` image, preserving its entrypoint and rootless Podman
setup. It installs the tools currently added by the job-local
`nested-podman-src` image: Git, Go, Make, pass, qrencode, net-tools, and tree.
EPEL 9 and CentOS Stream 9 use inline repository definitions with official
signing-key URLs and `gpgcheck=1`. EPEL uses Fedora's mirror service; no EPEL
release bootstrap RPM is downloaded or installed by this Dockerfile.
The tree package supplies a dependency of pass that is missing from UBI's
repositories. A dedicated CentOS Stream 9 BaseOS repository provides it,
restricted with `includepkgs=tree` so other UBI packages are not replaced.
Both repositories remain enabled, with CentOS limited to installing or updating tree.
DNF selects architecture-specific packages and verifies their signatures;
no direct package RPM URLs or per-architecture checksums are maintained.
Signature verification and package installation failures fail the build rather
than publishing an incomplete toolchain.

Local builds pull from `quay-proxy.ci.openshift.org` and require
[CI registry access](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/#how-do-i-gain-access-to-qci).
Use `NESTED_PODMAN_IMAGE=<pullspec>` to test a different upstream base.
Do not substitute a stale image from the legacy registry: the base must
initialize rootless Podman and forward commands through its entrypoint.

Build and smoke-test locally:

```bash
make build-nested-podman
make test-nested-podman
make test-nested-podman-runtime
```

Both tests use the image's default user without a runtime user override.
The tools test asserts UID 1000 and checks that Go can compile, test,
and install into `/opt/app-root`, the GOPATH used by integration jobs.
The runtime test also asserts that Podman is rootless, exercises the inherited
entrypoint, and checks the output of a nested container, so an entrypoint that
silently discards commands fails.
It uses host networking inside the outer container and does not validate
nested network isolation. It requires privileged container support; privilege
is restricted to that local test, not the image build. Use
`PLATFORM=linux/arm64` on an ARM host. These tests do not replace integration
and mega-linter jobs on the CI build farm.

Publication and consumption require companion changes in `openshift/release`.
The intended output is
`aro-hcp/aro-hcp-ci-images:aro-hcp-nested-podman-ci`, using the same
`baseimage-generator` promotion pattern as the general CI base. The image
definition alone does not switch existing jobs.
See [Nested Podman Toolchain](../../docs/ci/image-lifecycle.md#nested-podman-toolchain)
for the rollout order and producer/consumer configuration.

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

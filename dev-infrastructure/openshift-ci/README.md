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
- **Bicep**: Pinned in `versions.mk` and as an `ARG` default in the `Dockerfile`.
- **kubectl, kubelogin, oc**: Always download the latest stable version — no pinning required.

When updating versions:

1. **Go bump**: Update `.ci-operator.yaml` first (the base-ci image must have the new Go version before `go.work` is updated). Then update `go.work` in a follow-up PR.
2. **Bicep/promtool bump**: Run `make update-tool-versions` at the repo root to update both tools, or run `dev-infrastructure/openshift-ci/update-tool-versions.sh BICEP_VERSION` (or `PROMTOOL_VERSION`) to update one tool and its Dockerfile ARG.
3. Run `make verify` to check that Go minor version in `go.work` matches the builder image tag, and that the pinned tool versions are in sync.
4. Run `make test` to build the image and smoke-test all tools.

Inside an already-built CI image, run
`make -C dev-infrastructure/openshift-ci verify test IN_CONTAINER=true`.
This uses the same fail-closed `smoke-test.sh` as the local container test,
without requiring nested Docker or Podman. Use this mode only when the caller
already supplies the image under test. Missing or broken tools fail the job.
The baseimage-generator presubmit retains its non-root Go build/test check.

### Agentic Tool Updates

The [tool-version-updates GitHub Agentic Workflow](../../.github/workflows/tool-version-updates.md)
checks bicep, promtool and every Bingo-managed tool daily and after an owned
tool-update PR merges. It resolves stable
upstream versions and proposes one upstream family at a time. All five
`k8s.io/code-generator` tools update together; unrelated tools remain separate.
Native Bingo updates regenerate module/checksum files and the Make/shell helpers,
including their host-platform build settings.

Tool updates are serialized against existing agent-owned tool-update PRs.
Human PRs and unrelated automation do not block daily updates. Published PRs
use the existing CI/Tide auto-merge path.
The read-only `depctl inventory` preflight is the authority for
current/latest/status reporting; missing data is an error, and an outdated
family deferred by single-flight or checks is never current.

Checks use the proposed tool: bicep compiles the committed golden fixtures,
promtool runs the Prometheus rule generator's consumer tests, and Bingo updates build their tools
and run repository verification/lint. Dockerfile and `.bingo/` changes also
select the existing Prow baseimage-generator build. Current and single-flight
waiting states emit no-ops; missing data and blocked checks remain explicit
incomplete reports rather than claims that versions are current.
Real API, authentication and infrastructure failures remain failures.

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
The image also provides `/go` owned by UID 1000 and group 0 with mode `0775`,
so ci-operator can clone source into `/go/src` when it uses this image as a
build root. An image-build check runs as the default UID 1000 and creates a
temporary Git repository under `/go`; a clone-directory permission regression
fails the image build. This does not change the integration runtime GOPATH.
The runtime test also asserts that Podman is rootless, exercises the inherited
entrypoint, and checks the output of a nested container, so an entrypoint that
silently discards commands fails.
It uses host networking inside the outer container and does not validate
nested network isolation. It requires privileged container support; privilege
is restricted to that local test, not the image build. Use
`PLATFORM=linux/arm64` on an ARM host. These tests do not replace integration
and mega-linter jobs on the CI build farm.

The `baseimage-generator` variant in `openshift/release` builds and promotes
this image as `aro-hcp/aro-hcp-ci-images:aro-hcp-nested-podman-ci`, and the
main ci-operator config consumes it. See
[Nested Podman Toolchain](../../docs/ci/image-lifecycle.md#nested-podman-toolchain)
for how the producer and consumer fit together.

## OpenShift Release Bot MSFT Test

The "OpenShift Release Bot MSFT Test" app in the Test Test tenant is the CI identity for the jobs that use the `prod`
profile and for cleaning up expired app registrations in that tenant.

### CI Secret Storage (GSM)

CI credentials live in Google Secret Manager (GSM), collection `hcm-aro`. Each CI step mounts the groups it needs
(for example `/var/run/aro-hcp-prod`) and picks one with `VAULT_SECRET_PROFILE`. The step definitions are in
[openshift/release](https://github.com/openshift/release/tree/main/ci-operator/step-registry/aro-hcp).

| GSM group | Fields | Used by |
|-----------|--------|---------|
| `aro-hcp-prod` | `client-id`, `client-secret`, `tenant`, `customer-shard0-subscription-id`, `customer-shard0-subscription-name`, plus the transitional `subscription-id` and `subscription-name` | Jobs with `VAULT_SECRET_PROFILE=prod`: `prod-e2e-parallel`, `production` (CAPZ), the PROD cleanup periodics |
| `aro-hcp-msft-test-tenant` | `client-id`, `client-secret`, `tenant` | Expired app registration cleanup in the Test Test tenant. It holds the same app as `aro-hcp-prod`; once that cleanup reads `aro-hcp-prod` this group is deleted |

The supported field vocabulary, the scopes a consumer may read, and the
transitional keys being retired are defined in the
[Cluster Profile Secret Contract](../../docs/ci/cluster-profile-secret-contract.md).
Do not add fields outside that contract.

GSM values cannot be read back; `sm describe` shows a secret's metadata only. See the
[OpenShift CI documentation](https://docs.ci.openshift.org/how-tos/adding-a-new-secret-to-ci-gsm/).

### Credential Rotation and App Recreation

Follow [Rotating the CI Client Secret](../../docs/sops/test-test-tenant-access.md#rotating-the-ci-client-secret)
and, only if the app was deleted, [Recreating the App](../../docs/sops/test-test-tenant-access.md#recreating-the-app).
The secret is created with `az` in the Test Test tenant and published with the Secret Manager CLI, which needs
Red Hat SSO, so the steps say which access each one needs.

### Check Prow Job Logs

Look for `Acquired 1 lease(s) for aro-hcp-test-tenant-quota-slice` in the build logs to confirm Test Test tenant is being used.

## Documentation

For detailed documentation, see:
- [Test Test Azure Red Hat OpenShift Tenant Access SOP](../../docs/sops/test-test-tenant-access.md)

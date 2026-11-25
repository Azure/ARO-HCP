# CI Image Lifecycle

ARO HCP CI works with several image lifecycles at once, and most confusion comes from mixing them up. An E2E job rarely just runs one prebuilt container. Depending on the job family, ci-operator imports a shared CI image that an earlier postsubmit built, builds job-local runner and service images for the exact source revision under test, deploys the service images into a temporary environment, promotes some of them for reuse inside OpenShift CI, and, after merge, mirrors the service images into the ARO service ACR for real deployments.

This page explains how those pieces fit together. For the job families themselves, start with [CI Execution](execution.md). For registry topology and image distribution outside CI, see [ACRs and Images](../acrs-and-images.md).

## Lifecycle Overview

```mermaid
flowchart LR
  baseDocker["CI base Dockerfile"] --> baseGen["baseimage-generator"]
  baseGen --> baseImage["aro-hcp-e2e-base-ci"]
  baseImage --> pipelineRoot["pipeline:root"]
  pipelineRoot --> pipelineSrc["pipeline:src"]
  pipelineSrc --> runnerImages["runner images"]
  pipelineSrc --> serviceImages["service images"]
  runnerImages --> e2eJobs["E2E job steps"]
  serviceImages --> localE2E["local E2E provisioning"]
  serviceImages --> acrPush["images-push to ACR"]
```

Read the chart left to right. The shared build-root image is built and published by its own job. Each ci-operator run imports it into a temporary namespace as `pipeline:root`, builds a job-local source image (`pipeline:src`) containing the revision under test (for a PR, usually a synthetic merge of the base branch and the PR), and fans out the images the requested test target needs. Runner images execute the CI steps; service images are either deployed into a DEV local E2E environment or mirrored to ACR after merge.

Put differently, there are three layers: persistent shared CI images built by dedicated jobs, job-local `pipeline:*` images that live only in one run's namespace, and the runtime distribution of selected service images to ACR. A few names appear in more than one layer. `aro-hcp-e2e-tests` is built job-locally in many runs and promoted for reuse by selected postsubmits, and service images such as `aro-hcp-backend` exist first as job-local outputs and later as ACR content.

## Shared CI Images

### Shared CI Build Root

Most jobs do not rebuild their toolchain. The ci-operator configs in `openshift/release` import an already-published build root, the `aro-hcp-e2e-base-ci` tag of the `aro-hcp/aro-hcp-ci-images` ImageStream, through `build_root.image_stream_tag`. The `baseimage-generator` variant of the same config produces it: it builds [`dev-infrastructure/openshift-ci/Dockerfile`](../../dev-infrastructure/openshift-ci/Dockerfile) on the builder image selected by [`.ci-operator.yaml`](../../.ci-operator.yaml) in the repo root, and the postsubmit promotes the result into the ImageStream. The generator's jobs also run on PRs that touch the Dockerfile or the tool-version files, so a base image change is validated before merge.

This is why ci-operator logs of later jobs contain a line like:

```text
Tagging aro-hcp/aro-hcp-ci-images:aro-hcp-e2e-base-ci into pipeline:root
```

It means the shared build root is being imported into this job's namespace.

#### Tool Versions In The Build Root

The image carries the toolchain CI needs: the OCP builder base (Go on RHEL 9), Azure CLI with Bicep, `kubectl`, `kubelogin`, `oc`, `promtool` and the usual system tools. Some of those are pinned in more than one place, and the copies have to agree:

- **Builder image and Go version** live in `.ci-operator.yaml` at the repository root, in `build_root_image.tag`. That is the single source of truth for ci-operator, which reads it directly, and for local builds, where `versions.mk` extracts it with `yq`.
- **Bicep and `promtool`** are pinned in `versions.mk` and as `ARG` defaults in the Dockerfile.
- `kubectl`, `kubelogin` and `oc` are downloaded at their latest stable release and are not pinned.

To change them:

1. **Go bump.** Update `.ci-operator.yaml` first, because the shared base image has to carry the new Go version before `go.work` refers to it. Update `go.work` in a follow-up PR.
2. **Bicep or `promtool` bump.** Run `make update-tool-versions`, or `dev-infrastructure/openshift-ci/update-tool-versions.sh BICEP_VERSION` (or `PROMTOOL_VERSION`) to move one tool; both update the matching Dockerfile `ARG`.
3. Run `make verify`, which fails when the Go minor version in `go.work` disagrees with the builder tag or a pinned tool is out of sync.
4. Run `make -C dev-infrastructure/openshift-ci verify test`, which builds the image and smoke-tests every tool.

Inside an already-built CI image, `make -C dev-infrastructure/openshift-ci verify test IN_CONTAINER=true` runs the same fail-closed `smoke-test.sh` against the caller's image, without nested Docker or Podman. It tests the image it is given and fails on a missing or broken tool. The `baseimage-generator` presubmit keeps its own non-root Go build and test check.

#### Automated Tool Updates

The [tool-version-updates GitHub Agentic Workflow](../../.github/workflows/tool-version-updates.md) checks `bicep`, `promtool` and every Bingo-managed tool daily and after an owned tool-update PR merges. It proposes one upstream family at a time: all five `k8s.io/code-generator` tools move together and unrelated tools stay separate. Native Bingo updates also regenerate module and checksum files and the Make and shell helpers, including their host-platform build settings. Runs are serialized against existing agent-owned tool-update PRs so two of them cannot fight over the same files, and published PRs use the normal CI and Tide auto-merge path.

Each candidate is checked with the proposed tool rather than the old one: `bicep` compiles the committed golden fixtures, `promtool` runs the Prometheus rule generator's consumer tests, and a Bingo update builds its tool and runs repository verification and lint. Dockerfile and `.bingo/` changes also select the `baseimage-generator` Prow build. Current and single-flight-waiting states emit no-ops; missing data or a blocked check reports the run as incomplete instead of claiming the tools are current, and real API, authentication or infrastructure failures stay failures. The read-only `depctl inventory` preflight is the authority for current and latest versions.

### Nested Podman Toolchain

Integration tests and mega-linter run in nested-Podman jobs. Their toolchain follows the same producer/consumer pattern as the build root:

- [`Dockerfile.nested-podman`](../../dev-infrastructure/openshift-ci/Dockerfile.nested-podman) extends the OpenShift CI `ci/nested-podman` image with the ARO HCP tools (Go, Git, Make, and so on). The `baseimage-generator` variant builds it from that image rather than from `src`, because building from `src` would replace the nested Podman base, and promotes it as `aro-hcp-ci-images:aro-hcp-nested-podman-ci`.
- The main ci-operator config consumes that tag as its `nested-podman` base image. `nested-podman-src` is a small job-local layer on top of it that copies in the source under test. It stays job-local so a PR test never runs against source left over from an earlier postsubmit.

Package downloads therefore happen when the toolchain is published rather than in every job. Two consequences are worth knowing. A failed toolchain build does not replace the last promoted image, so consumers keep working. The generator only rebuilds when one of its trigger paths changes, so upstream base-image and package updates reach CI only after such a change or a deliberate rebuild. The checkout in `nested-podman-src` is owned by UID 1000, the toolchain's default user, so jobs can create repository-local build outputs such as `bin/envtest` without becoming root.

Local build and smoke-test commands are in the `dev-infrastructure/openshift-ci` Makefile:

```bash
make -C dev-infrastructure/openshift-ci build-nested-podman
make -C dev-infrastructure/openshift-ci test-nested-podman
make -C dev-infrastructure/openshift-ci test-nested-podman-runtime
```

Local builds pull from `quay-proxy.ci.openshift.org` and need
[CI registry access](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/#how-do-i-gain-access-to-qci).
Set `NESTED_PODMAN_IMAGE=<pullspec>` to test a different upstream base. Never
substitute an image from the legacy registry: the base has to initialize
rootless Podman and forward commands through its entrypoint.

The tests run the image as its default user, without a runtime user override.
The tools test asserts UID 1000 and that Go can compile, test and install into
`/opt/app-root`, the GOPATH integration jobs use. The image also provides `/go`
owned by UID 1000 and group 0 with mode `0775`, so ci-operator can clone source
into `/go/src` when it uses this image as a build root; an image-build check
clones into it, so a permission regression fails the build rather than a job.
The runtime test asserts that Podman is rootless, exercises the inherited
entrypoint and checks the output of a nested container, so an entrypoint that
silently discards commands fails. It uses host networking inside the outer
container and therefore does not validate nested network isolation, and it needs
privileged container support — privilege is scoped to that local test, not to
the image build. On an ARM host, pass `PLATFORM=linux/arm64`. None of these
tests replace the integration and mega-linter jobs on the CI build farm.

Package sources are pinned deliberately. EPEL 9 and CentOS Stream 9 use inline
repository definitions with official signing-key URLs and `gpgcheck=1`, EPEL
comes from Fedora's mirror service with no release bootstrap RPM, and the one
package UBI lacks (a dependency of `pass`) comes from a CentOS Stream 9 BaseOS
repository restricted with `includepkgs=tree` so it cannot replace UBI
packages. DNF picks architecture-specific packages and verifies their
signatures, and a verification or installation failure fails the build instead
of publishing an incomplete toolchain.

### Shared CI Test Runner Image

`aro-hcp-e2e-tests` is built from [`test/Containerfile.e2e`](../../test/Containerfile.e2e), which layers the ARO HCP test and tooling binaries (`hcpctl`, `templatize`, and the `test/` tools) on top of the shared base image. PR jobs usually build it job-locally. The main config also promotes it, together with `aro-hcp-e2e-tools`, into the `aro-hcp` namespace with a `latest` tag so that later jobs can import it instead of rebuilding. The periodic E2E variant does exactly that and takes `aro-hcp-e2e-tests:latest` as a `base_images` entry.

The promoted image can be pulled from the build farm registry as `quay-proxy.ci.openshift.org/aro-hcp/aro-hcp-e2e-tests:latest`. For the RBAC on app.ci and the login, follow [How do I gain access to QCI?](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/#how-do-i-gain-access-to-qci) in the OpenShift CI docs.

## Job-Local Images

### Runner Images Vs Service Images

Job-local images fall into two groups.

**Runner images** are the containers that execute step-registry scripts. The two main ones are `aro-hcp-e2e-tests`, used for config rendering, the test run, and most gather steps, and `aro-hcp-e2e-tools`, used by provisioning, deprovisioning, and the image push. A step selects its runner with `from:` in its ref. A few more specialized runners exist for individual targets, such as `nested-podman-src` for nested-Podman jobs, `aro-hcp-api-tools` for API validation, and a tooling image for the image-updater periodics.

**Service images** are the product components the job is testing, such as `aro-hcp-backend`, `aro-hcp-frontend`, and `aro-hcp-admin-api`; the main config lists the full set. They are either deployed into a temporary DEV environment or mirrored to ACR after merge. The provisioning step receives them through ci-operator `dependencies:` in its ref, which resolve to immutable pullspecs in environment variables. The provision script writes those pullspecs into the temporary environment's configuration, so the environment runs exactly the images built from the revision under test.

### Local E2E Versus Persistent E2E

How many images a job builds follows from what it targets, because ci-operator only builds what the requested target depends on.

A local E2E job (the `aro-hcp-local-e2e` workflow, used by `e2e-parallel`) provisions a fresh DEV environment. It needs the runner images for rendering configuration, provisioning, testing, and gathering, and the service images that provisioning deploys. This is why its ci-operator graph builds many images before any test step starts. Which environment it gets, and the identities and slots it leases, is covered by [slot-manager](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md); the lease step runs at the start of the workflow's `pre` phase.

A persistent-environment E2E job (the `aro-hcp-persistent-e2e` workflow) targets an environment that is already deployed, so it needs only the runner image. The `__e2e` variant that EV2 triggers (see [CI EV2 Integration](ev2-integration.md)) is the extreme case: it defines nothing but `aro-hcp-e2e-tests`. Periodic variants can go further and skip even that build by importing the promoted `latest` image.

## Promotion Vs Push

These two terms are easily blurred, but they refer to separate mechanisms with separate audiences.

Promotion is a ci-operator feature: it publishes selected CI-built images into a persistent ImageStream on the build farm so that later jobs can import them. For ARO HCP that covers the base CI image (promoted by `baseimage-generator`), the nested Podman toolchain, and the `aro-hcp-e2e-tests` and `aro-hcp-e2e-tools` runner images (promoted by the main config). Promoted images are reused inside OpenShift CI only.

Push is how service images leave CI. The `images-push-postsubmit` job runs the `aro-hcp-images-push` step from `openshift/release`, which receives the CI-built service images as dependencies and mirrors them into the service ACR. A sibling job pushes the tooling images that are deployed rather than tested (`aro-hcp-tenant-quota`). From the ACR, environment deployments and cross-environment mirroring take over, which [ACRs and Images](../acrs-and-images.md) describes.

## How To Read Ci-Operator Logs

The startup output of a ci-operator run reads best as a dependency graph. The lines worth recognizing:

- `Resolved source ... merging: #...` is the exact revision under test, for a PR the synthetic merge of the base branch and the PR.
- `Using namespace .../ci-op-...` is the temporary namespace. All `pipeline:*` images live only there.
- `Tagging ... aro-hcp-e2e-base-ci into pipeline:root` imports the shared build root, and `Building src` creates `pipeline:src` from the checkout.
- `Building aro-hcp-e2e-tools`, `Building aro-hcp-frontend`, and similar lines are the downstream image builds the target requires. A build line does not mean the corresponding test step has started.
- `Image .../pipeline:aro-hcp-frontend created digest=...` means the image now exists at an immutable digest for downstream steps or later mirroring.
- `Running [input:root], lease-proxy-server, src, ..., e2e-parallel` is ci-operator's node graph, not the start of the tests. `input:root` is the imported build root, `src` the source image, `aro-hcp-*` the image builds, and `e2e-parallel` the target, which runs once its prerequisites are ready. `lease-proxy-server` is ci-operator infrastructure, not an ARO HCP image.
- `Found existing build ...` means ci-operator reused an existing Build object for that node. What matters is still the final digest the downstream steps consume.

## Where To Look

The ci-operator configs and step registry live in `openshift/release` under `ci-operator/config/Azure/ARO-HCP/` and `ci-operator/step-registry/aro-hcp/`.

- `Azure-ARO-HCP-main.yaml`: service and runner image definitions, promotion, and the `images-push-postsubmit` job.
- `Azure-ARO-HCP-main__baseimage-generator.yaml`: the shared base and nested Podman images.
- `Azure-ARO-HCP-main__e2e.yaml` and `Azure-ARO-HCP-main__periodic.yaml`: the runner-only EV2 and periodic variants.
- `step-registry/aro-hcp/local-e2e/`, `provision/environment/`, and `images-push/`: the local E2E workflow, how service images reach provisioning, and the ACR push.
- In this repository: [`dev-infrastructure/openshift-ci/`](../../dev-infrastructure/openshift-ci/) for the base images and [`test/Containerfile.e2e`](../../test/Containerfile.e2e) for the test runner.

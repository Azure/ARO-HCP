# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the main repository for Red Hat OpenShift on Azure (ARO) using the Hosted Control Planes (HCP) architecture. It contains some of the code for the required microservices along with most of the configuration and pipeline definition necessary to deploy it.

## Common Commands

### Build, Test, Lint (whole repo)
```bash
make test              # Run all unit tests across the Go workspace (requires envtest setup, done automatically)
make lint              # Run golangci-lint across all modules
make lint-fix          # Run lint with --fix
make fmt               # Run goimports across all modules
make verify            # Run all verification checks (deepcopy, json-format, yamlfmt, materialize, gomega, schema) — this is what CI runs
make generate          # Regenerate deepcopy, mocks, format, tidy
make tidy              # Run go mod tidy across all modules + go work sync
make install-tools     # Install all dev tools via bingo
```

### Running a single test
There is no workspace-wide single-test shortcut. `cd` into the module directory and run:
```bash
cd frontend && go test -run TestMyFunction -v ./...
cd internal && go test -run TestSomething -v ./path/to/package/...
```

For integration tests (require Cosmos emulator):
```bash
cd test-integration && make test    # Runs all integration tests with Cosmos emulator
go test ./test-integration/frontend/...  # Frontend integration tests only
go test ./test-integration/backend/...   # Backend integration tests only
```

### E2E tests (local)
```bash
make e2e/local                                  # Full local E2E suite
make e2e-local/run-test TEST_NAME="TestName"    # Single E2E test
```

### Building individual services
```bash
make -C frontend build   # Build frontend binary
make -C backend build    # Build backend binary
make build-services      # Build all in-repo service images in parallel
```

### Config changes
After modifying `config/config*.yaml` or schema files:
```bash
cd config && make materialize   # Re-render configs and update helm fixtures
```
Rendered config changes under `config/rendered/` must be committed with your PR. Run `make verify-materialize` to check.

### Personal dev environment
```bash
make personal-dev-env   # Build images, deploy infrastructure (DEPLOY_ENV=pers required)
```

### Other useful commands
```bash
make rebase            # Rebase on upstream and re-materialize config (hack/rebase-n-materialize.sh)
make envtest-setup     # Download kubebuilder binaries (etcd, kube-apiserver) for controller tests
```
Note: `make test` runs `envtest-setup` automatically, but if you run `go test` directly in a module with controller tests (e.g. `kube-applier`), you need `KUBEBUILDER_ASSETS` set. Use: `export KUBEBUILDER_ASSETS=$(make -s envtest-setup)`

## Target Environments

There's a multi-layered build and deploy system supporting multiple target environments with configs under `config/`):
- **Personal dev**
  - cloud: "dev"; environment: "pers" (`DEPLOY_ENV=pers`)
  - Local development using `make` and properly-configured `az` command
  - Changes must be manually applied by the user by running proper `make` commands
- **CSPR environment** (Clusters Service PR check)
  - cloud: "dev"; environment: "cspr" (`DEPLOY_ENV=cspr`)
  - Uses Prow for CI/CD (jobs defined in [openshift/release](https://github.com/openshift/release/tree/master/ci-operator/jobs/Azure/ARO-HCP))
  - Changes get automatically applied after a PR merge
  - Note: The integrated/shared dev environment (`DEPLOY_ENV=dev`) has been decommissioned. Only global infrastructure (shared ACRs, DNS zones) is still deployed to the dev environment via the `global-pipeline-postsubmit` Prow job.
- **Production systems**
  - cloud: "public"; environments: "int", "stg", "prod"
  - Deployed via Microsoft ADO and EV2 into Microsoft Int, Stage and Prod environments (hosted in Microsoft Azure tenants)
  - For changes to be applied, a PR must be created for `sdp-pipelines` repo and updated pipelines run manually
  - After making changes, remind user about this and point at aka.ms/arohcp-pipelines for next steps


### Access

* "Dev/*" environments can be accessed with a @redhat.com account and are largely read/write. It's likely that `az` is currently logged into it.
* "Public/Int" environment can be accessed with a @microsoft account and is mostly read-only. It's possible to `az login` into, but you need to ask the user explicitly to do that.
* "Public/Stage" and "Public/Prod" require SRE-level access.

## Deployments

Loosely defined categories of deployed objects:
- Native Azure objects (using bicep files in `dev-infrastructure/`).
  - This includes databases, roles, access definitions, automations, monitoring stack "plumbing", etc. (e.g. global from `dev-infrastructure/global-pipeline.yaml`, regional from `dev-infrastructure/region-pipeline.yaml`, etc.)
  - But also two types of AKS clusters: Service Clusters and Management Clusters (`dev-infrastructure/mgmt-pipeline.yaml`)
- Service Clusters
  – One per supported Azure region
  - Hosts the Resource Provider (frontend and backend) and Cluster Service
  - `services_svc_pipelines` in `Makefile` has a list of deployed services (on top of what's in `dev-infrastructure/svc-pipeline.yaml`)
- Management Clusters
  - Multiple per Azure region (number depends on how many HCPs are running in that region)
  - Run all the things required to actually run HCPs
  - `services_mgmt_pipelines` in `Makefile` has a list of deployed services (on top of what's in `dev-infrastructure/mgmt-pipeline.yaml`)

## Components

Loose categorization:
- Deployment pipelines (dirs have `*pipeline.yaml` files present)
  - `dev-infrastructure/` has all the bicep files
  - The rest deploy services to management and service clusters. Most of these do not host the code of the service, just reference already-released images.
- ARO-HCP-specific microservices, like the Resource Provider's `frontend/` and `backend/`:
  - These contain Go code in addition to pipelines configs.
- Additional/helper (`test/`, `hack/`, `tooling/`, etc.)

Incomplete list:
- **Frontend**: ARM REST API endpoint (`frontend/`) - Go service handling Azure ARM API calls
- **Backend**: Internal processing service (`backend/`) - Go service for async operations
- **Cluster Service**: Core cluster management (`cluster-service/`) - Manages HCP cluster lifecycle
- **Maestro**: Multi-cluster orchestration (`maestro/`) - Handles communication between service and management clusters
- **Kube-Applier**: Kubernetes resource applier (`kube-applier/`) - Reconciles desired-state manifests on management clusters
- **Fleet**: Fleet controller (`fleet/`) - Service cluster controller for fleet-level operations
- **Mgmt-Agent**: Management cluster agent (`mgmt-agent/`) - Agent running on each management cluster
- **SessionGate**: Session management (`sessiongate/`) - Handles session gating for ARM requests
- **Admin**: Admin API (`admin/`) - Internal admin interface (split into `admin/client` and `admin/server` modules)
- **Infrastructure**: Azure infrastructure as code (`dev-infrastructure/`) - Bicep templates for all Azure resources
- **Internal**: Shared libraries (`internal/`) - Common APIs, database, OCM client, tracing utilities
- **demo**: helper scripts to quickly spin up and tear down an HCP cluster

The github.com/Azure/ARO-Tools repo is also a dependency and changes can be suggested for it.

## Additional Build, Configuration and Deployment Info

### Go Workspace
The project uses Go 1.25 workspaces with ~35 modules defined in `go.work`:
- Services: `admin/client`, `admin/server`, `backend`, `frontend`, `fleet`, `kube-applier`, `mgmt-agent`, `sessiongate`
- Libraries: `internal`, `test`, `test-integration`
- Tooling: `tooling/hcpctl`, `tooling/templatize`, `tooling/secret-sync`, etc.
- Infrastructure scripts: `dev-infrastructure/scripts/`

After adding a module or changing cross-module deps, run `make tidy` (which includes `go work sync`).

### Environment Configuration
- Main config: `config/config.yaml` and overlays
- Environment-specific settings in `dev-infrastructure/configurations/`
- Service configs use Helm charts in each `deploy/` directory

### Pipeline System
Uses a custom templatize system (`tooling/templatize/`) that processes:
- `pipeline.yaml` files define deployment pipelines
- Bicep templates for Azure resource deployment
- Helm charts for Kubernetes service deployment

### Deployment Topology
`topology.yaml` defines the entire deployment graph as a tree of service groups, each referencing a `pipeline.yaml`. Entrypoints (Global, Region, Monitoring) are the top-level deployment targets. Run `make entrypoint/Region` or `make pipeline/Frontend` to execute specific parts of the tree locally.

### Configuration System
- `config/config.yaml` is the main config with Go template variables (`{{ .ctx.region }}`, etc.)
- `config/config.schema.json` validates the config — update the schema when adding new parameters
- `config/config.msft.clouds-overlay.yaml` provides overrides for Microsoft cloud environments
- After any config change: `cd config && make materialize` to re-render, then commit `config/rendered/` changes
- See `docs/configuration.md` for full details

## Code Organization

### Service Structure
Each service follows consistent patterns:
- `Makefile` - Build, deploy, and run targets
- `deploy/` - Helm charts and Kubernetes manifests
- `pipeline.yaml` - Deployment pipeline definition
- Go services include standard `main.go`, `go.mod`

### Build Tags
- Lint tags: `LINT_GOTAGS='${GOTAGS},E2Etests'`

### Go conventions

- Every `go func(...)` spawned in non-test code must `defer utilruntime.HandleCrash()` (alias `k8s.io/apimachinery/pkg/util/runtime`) as its first deferred call, so an unhandled panic in the goroutine respects `utilruntime.ReallyCrash` and the binary's crash-on-panic policy instead of silently taking down the process. Goroutines whose body is passed to a kube wait helper that already wraps the call (e.g. `wait.Until`, `wait.UntilWithContext`, `wait.JitterUntil`) do not need it — those helpers call `HandleCrash` internally. When the goroutine invokes a named function via `go fn(...)`, put the `defer utilruntime.HandleCrash()` at the top of `fn` rather than wrapping the call site in a closure. Test code (`*_test.go`, `test/`, `test-integration/`, generated SDK fakes) is exempt.

- **Import aliases are enforced by linter.** The `.golangci.yml` `importas` config requires specific aliases. Key ones:
  - `arohcpv1alpha1` for `github.com/openshift-online/ocm-sdk-go/aro_hcp/v1alpha1`
  - `cmv1` for `github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1`
  - `azcorearm` for `github.com/Azure/azure-sdk-for-go/sdk/azcore/arm`
  - `hcpsdk20240610preview` for `github.com/Azure/ARO-HCP/test/sdk/v20240610preview/...`
  - `azureclient` for `github.com/Azure/ARO-HCP/backend/pkg/azure/client`
  - OpenShift API packages use `{group}{version}` pattern (e.g. `configv1`)

- **Import ordering** is enforced by `gci`: standard, blank, dot, default, `k8s.io`, `sigs.k8s.io`, `github.com/Azure`, `github.com/openshift`, `github.com/Azure/ARO-HCP`. Run `make fmt` to fix.

- **Gomega assertions must have descriptive annotations.** CI runs `verify-gomega-assertions` on `test/e2e/` and `test/util/`, which rejects bare `Expect().To()`/`NotTo()` calls. Always add a context string:
  - `Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")` — not `Expect(err).NotTo(HaveOccurred())`
  - `Expect(resp.Properties).NotTo(BeNil(), "cluster response Properties was nil")` — not `Expect(resp.Properties).NotTo(BeNil())`

### API Versioning
ARM API versions live under `internal/api/v<YYYYMMDD>preview/` (e.g. `v20240610preview`, `v20251223preview`). Each version directory has a `generated/` subdirectory with auto-generated types and a `register.go` that wires the version into the API registry. Conversion between API versions and internal types happens in the `*_methods.go` files. The internal (versionless) types live in `internal/api/`.

### E2E Tests
E2E tests live in `test/e2e/` and use Ginkgo/Gomega. Key conventions:
- E2E test files must **not** use the `_test.go` suffix (except `e2e_test.go` entry point) — the Ginkgo OpenShift extension excludes `_test.go` files during imports.
- Every `It()` block must accept `context.Context` as its first parameter.
- Every test must have labels: environment (`labels.RequireNothing` or `labels.RequireHappyPathInfra`), importance (`labels.Critical`/`High`/`Medium`/`Low`), and positivity (`labels.Positive`/`labels.Negative`).
- Framework helpers use explicit API version suffixes: `CreateHCPClusterFromParam20240610`, `GetHCPCluster20251223`, etc.
- See `test/AGENTS.md` for the full E2E design guide and code review standards.

### Integration Tests
The `test-integration/` directory uses a **declarative artifact-driven** test framework. Tests are defined as numbered step directories (`00-load-initial-state/`, `01-httpCreate-resource/`, etc.) under `artifacts/` trees — no Go code changes needed to add a new test case. See `test-integration/claude.md` for the full step type reference.

### Helm Fixture Testing
Services have `zz_fixture_TestHelmTemplate_*.yaml` files that are auto-generated by `cd config && make materialize`. These are test fixtures for Helm chart validation — they must be committed with your PR. If you change a service's Helm chart or config values, re-run materialize to update them.

### License Headers
All `.go` files require Apache 2.0 license headers. Run `make licenses` to add them via the `addlicense` tool. CI will fail if headers are missing.

### Infrastructure as Code
- Bicep templates in `dev-infrastructure/templates/`
- Reusable modules in `dev-infrastructure/modules/`
- Parameter files: `*.bicepparam` (with `.tmpl.bicepparam` templates)

## PR Standards

- PR titles must use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) format: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, etc.
- PR descriptions must explain the **"Why"**, not just the "What", and link to a Jira/GitHub issue.
- Tide is a merge-automation bot — its status is **not** a CI check; ignore it when evaluating CI.
- See `CONTRIBUTING.md` for the full PR standards.

## Tooling

Key development tools (installed via `make install-tools`):
- `golangci-lint` - Go linting
- `mockgen` - Mock generation
- `addlicense` - License header management
- `yamlfmt` - YAML formatting
- `oras` - OCI registry interaction

Custom tools in `tooling/`:
- `hcpctl` - CLI for HCP management and access
- `templatize` - Pipeline template processing
- `secret-sync` - Secret management utilities
- `prometheus-rules` - Monitoring rule generation

## Subdirectory Guidance

Several subdirectories have their own AI guidance files with domain-specific detail:
- `test/AGENTS.md` — E2E test design principles, review standards, label reference, and anti-patterns
- `test-integration/claude.md` — Step type reference for the declarative integration test framework
- `config/CLAUDE.md` — Config rendering workflow and example commits

## Documentation

Key documentation files:
- [Architecture Overview](docs/high-level-architecture.md)
- [Configuration Management](docs/configuration.md)
- [Personal Dev Setup](docs/personal-dev.md)
- [Service Components](docs/service-components.md)
- [Environment Details](docs/environments.md)
- [Deployment Concepts](docs/service-deployment-concept.md)

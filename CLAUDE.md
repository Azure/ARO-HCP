## Project Overview

This is the main repository for Red Hat OpenShift on Azure (ARO) using the Hosted Control Planes (HCP) architecture. It contains code some of the code for the required microservices along with most of the configuration and pipeline definiton necessary to deploy it.

## Target Environments

There's a multi-layered build and deploy system supporting multiple target environments with configs under `config/`):
- **Personal dev**
  - cloud: "dev"; environment: "pers" (`DEPLOY_ENV=pers`)
  - Local development using `make` and properly-configured `az` command
  - Changes must be manually applied by the user by running proper `make` commands
- **Shared/integrated/cloud dev environments**
  - cloud: "dev"; environments: "cspr", "ntly", etc. (list in `config/config.yaml` under `clouds.dev.environments`)
  - Use GitHub Actions for CI/CD (`.github/`)
  - Changes get automatically applied after a PR merge
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
  - But also two types of EKS clusters: Service Clusters and Management Clusters (`dev-infrastructure/mgmt-pipeline.yaml`)
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
- **Infrastructure**: Azure infrastructure as code (`dev-infrastructure/`) - Bicep templates for all Azure resources
- **Internal**: Shared libraries (`internal/`) - Common APIs, database, OCM client, tracing utilities
- **demo**: helper scripts to quickly spin up and tear down an HCP cluster

The github.com/Azure/ARO-Tools repo is also a dependency and changes can be suggested for it.

## Additional Build, Configuration and Deployment Info

### Go Workspace
The project uses Go workspaces. All Go modules are defined in `go.work`:
- Main services: `admin`, `backend`, `frontend`, `internal`, `test`
- Tooling: `tooling/hcpctl`, `tooling/templatize`, etc.

### Environment Configuration
- Main config: `config/config.yaml` and overlays
- Environment-specific settings in `dev-infrastructure/configurations/`
- Service configs use Helm charts in each `deploy/` directory

### Pipeline System
Uses a custom templatize system (`tooling/templatize/`) that processes:
- `pipeline.yaml` files define deployment pipelines
- Bicep templates for Azure resource deployment
- Helm charts for Kubernetes service deployment

## Code Organization

### Service Structure
Each service follows consistent patterns:
- `Makefile` - Build, deploy, and run targets
- `deploy/` - Helm charts and Kubernetes manifests
- `pipeline.yaml` - Deployment pipeline definition
- Go services include standard `main.go`, `go.mod`

### Build Tags
- Lint tags: `LINT_GOTAGS='${GOTAGS},E2Etests'`

### Infrastructure as Code
- Bicep templates in `dev-infrastructure/templates/`
- Reusable modules in `dev-infrastructure/modules/`
- Parameter files: `*.bicepparam` (with `.tmpl.bicepparam` templates)

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

## Documentation

Key documentation files:
- [Architecture Overview](docs/high-level-architecture.md)
- [Personal Dev Setup](docs/personal-dev.md)
- [Service Components](docs/service-components.md)
- [Environment Details](docs/environments.md)
- [Deployment Concepts](docs/service-deployment-concept.md)

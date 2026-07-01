# ARO HCP Documentation

Welcome to the **ARO HCP** documentation. This guide provides an overview of the **architecture and deployment** of ARO HCP, primarily intended for **developers and SREs** working on the project.

## Table of Contents

### [High Level Architecture](high-level-architecture.md)

- System overview, scopes and key components
- Major services and how they interact

### Environments

- [Overview](environments.md)
  - Overview of different deployment environments and Azure tenants
  - Key differences and capabilities of each environment
  - Access requirements and limitations
  - Feature/Capability Matrix
- Red Hat development tenant deployment environments
  - [Personal ARO HCP environment](personal-dev.md)
  - [CS PR environment](cspr.md)
  - [Personal perfscale environment](perscale-deployment.md)
- MSFT deployment environments
  - [MSIT INT](environments.md#msit-corp-tenant-msft-int-tenant)

### Networking and DNS

- [Ingress and Egress Concept](ingress-egress.md)
  - Service and management cluster ingress and egress
  - IP service tags
- [Istio Networking](istio.md)
  - Installation and configuration
  - Mesh management
  - Upgrades
  - Traffic control
- [Network Security](network-security.md)
  - Private Links
  - Network Security Perimeter
- [DNS](dns.md)
  - Overview of the DNS hierarchies and how they are managed
  - SVC and CX zones

### [Configuration Management](configuration.md)

- Overview and override structures
- Configuration schema
- Guidelines and limitations

### Deployment Concept

- [Pipelines Concept](pipeline-concept.md)
- [Pipeline Topology](pipeline-topology.md)
- [Service Deployment Concept](service-deployment-concept.md)
- Deployment artifacts
  - [Azure infrastructure Bicep templates](bicep.md)
  - [Helm Charts](service-deployment-concept.md#helm-chart)
  - [ACRs and Container images](acrs-and-images.md)

### Deploying ARO HCP

- [Pipelines](pipelines.md)
  - documents existing pipelines
- [EV2 Deployment](ev2-deployment.md)
  - Deployment process from pipeline.yaml to EV2 deployment
  - Building and executing an ADO pipeline
- [Secret Syncronization](secret-sync.md)
  - documents tools/processes to sync secrets

### Testing and CI

- [CI Overview](ci/README.md)
  - Entry point for the CI documentation set
  - Explains which CI modes exist and where to go next
- [CI Execution](ci/execution.md)
  - How PR validation, EV2 gating, and periodic jobs actually work
  - Cross-tenant Azure flow for DEV, INT, STG, and PROD
- [Dev-CI Topology](ci/dev-ci-topology.md)
  - Standalone `dev-ci` rollout, persistent CI-supporting infrastructure, and service-group ownership
  - Current mixed-management boundary for the DEV MSI mock SP pool and the long-term declarative direction
- [E2E Subscription Onboarding](ci/e2e-subscription-onboarding.md)
  - Procedure for onboarding E2E customer subscriptions across all environments (DEV, INT, STG, PROD)
  - Covers slot catalog, Boskos, cluster-profile inventory, and `dev-ci` bootstrap RBAC updates
- [CI Image Lifecycle](ci/image-lifecycle.md)
  - Shared CI build root, job-local image graph, and local E2E image injection
  - CI promotion inside OpenShift CI vs service-image mirroring to ACR
- [CI Identity Leasing](ci/identity-leasing.md)
  - Managed identity container pool and MSI mock SP pool deep dive
  - Release-side lease contract, pool sizing, and troubleshooting
- [CI Quota Monitoring](ci/quota-monitoring.md)
  - How Azure quotas that constrain CI are monitored via `tenant-quota`
  - Azure dashboard for real-time quota usage
- [CI EV2 Integration](ci/ev2-integration.md)
  - How EV2 selects Prow jobs and authenticates through Gangway
  - Commit pinning, rollout metadata, and promotion gating
- [CI Cleanup](ci/cleanup.md)
  - Cleanup modes: strict per-test, targeted teardown, background hygiene
  - Design rationale and behavioral differences across environments
- [E2E Testing in CI](ci/e2e-testing.md)
  - How to trigger E2E jobs from PRs
  - How to narrow test selection safely
- [CI Operations](ci/operations.md)
  - How to trigger, inspect, troubleshoot, and change CI
  - Tiny source-of-truth appendix for job families

### Observability

- [Alerts](alerts.md)
  - How to write, test, and deploy Prometheus alerting rules
  - CorrelationID behavior, severity mapping, IcM integration
- [Grafana Dashboards](grafana-dashboards.md)
- [Logging](logging.md)
- [Prometheus Rules](prometheus-rules.md)

### Guides and Operations

- [Introduce a new Service to ARO HCP](introduce-new-services.md)
  - Guidance on how to introduce new services into the ARO HCP architecture and deployment concept
- [Bump Service Component Image Digests](ops/bump-image-digests.md)
  - How to bump service component image digests in RH and MSFT environments
- [High Level HCP Creation Flow](ops/hcp-cluster-creation-flow.md)
  - Walkthrough of an HCP cluster creation process through all the service layers of ARO HCP
- [Opstool Cluster Guide](ops/opstool-cluster-guide.md)
  - Standalone cluster architecture, rollout model, shared resource wiring, and workload patterns for `opstool`
- [Resource Creation Diagram](resource-creation.md)
  - Detailed diagram of the resource creation flow (frontend, backend, Cluster Service, Maestro)
  - Covers HCPOpenShiftCluster, NodePool, and ExternalAuth resource types
- [Postgres Breakglass](ops/postgres-breakglass.md)
  - How to access the Postgres database
- [Tenant Quota Collector](../tooling/tenant-quota/README.md)
  - Tool-local deployment, configuration, and troubleshooting reference for `tenant-quota`
  - For CI relevance, see [CI Quota Monitoring](ci/quota-monitoring.md)

### [Terminology](terminology.md)

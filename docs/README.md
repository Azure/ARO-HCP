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
  - [Integrated DEV environment](integrated-dev.md)
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

- [Prow](prow.md)
  - Overview of Prow-based CI infrastructure
  - Presubmit and periodic jobs
  - How to trigger and monitor tests
  - EV2 pipeline integration

### Observability

- [Grafana Dashboards](grafana-dashboards.md)
- [Prometheus Rules](prometheus-rules.md)

### Guides and Operations

- [Introduce a new Service to ARO HCP](introduce-new-services.md)
  - Guidance on how to introduce new services into the ARO HCP architecture and deployment concept
- [Bump Service Component Image Digests](ops/bump-image-digests.md)
  - How to bump service component image digests in RH and MSFT environments
- [High Level HCP Creation Flow](ops/hcp-cluster-creation-flow.md)
  - Walkthrough of an HCP cluster creation process through all the service layers of ARO HCP
- [Postgres Breakglass](ops/postgres-breakglass.md)
  - How to access the Postgres database

### [Terminology](terminology.md)

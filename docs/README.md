# ARO HCP Documentation

Welcome to the **ARO HCP** documentation. This guide provides an overview of the **architecture and deployment** of ARO HCP, primarily intended for **developers and SREs** working on the project.

## Table of Contents

### 1. [High Level Architecture](high-level-architecture.md)
   - System overview, scopes and key components
   - Major services and how they interact

### 2. Environments
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
     - [MSIT INT](msit-int.md)

### 3. [Configuration Management](configuration.md)
   - Overview and override structures
   - Configuration schema
   - Guidelines and limitations

### 4. Deployment Concept
   - [Pipelines concept](pipeline-concept.md)
   - [Service deployment concept](service-deployment-concept.md)
   - Deployment artifacts
     - [Azure infrastructure Bicep templates](bicep.md)
     - [Helm Charts](service-deployment-concept.md#helm-chart)
     - [Container images](images.md)

### 5. Deploying ARO HCP
   - [Pipelines](pipelines.md)
      - documents existing pipelines
   - [EV2 Deployment](ev2-deployment.md)
      - Deployment process from pipeline.yaml to EV2 deployment
      - Building and executing an ADO pipeline
   - [Secret Syncronization](secret-sync.md)
      - documents tools/processes to sync secrets

### 6. Observability
   - [Grafana Dashboards](grafana-dashboards.md)
   - [Prometheus Rules](prometheus-rules.md)

### 7. Operations
   - [High Level HCP Creation Flow](ops/hcp-cluster-creation-flow.md)

### 8. [Terminology](terminology.md)

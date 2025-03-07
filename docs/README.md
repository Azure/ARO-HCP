# ARO HCP Documentation

Welcome to the **ARO HCP** documentation. This guide provides an overview of the **architecture and deployment** of ARO HCP, primarily intended for **developers and SREs** working on the project.

## Table of Contents

1. **[High Level Architecture](high-level-architecture.md)**
   - System overview, scopes and key components
   - Major services and how they interact

1. **[Environments](environments.md)**
   - Overview of different deployment environments and Azure tenants
   - Key differences and capabilities of each environment
   - Access requirements and limitations
   - Feature/Capability Matrix

1. **[Configuration Management](configuration.md)**
   - Overview and override structures
   - Configuration schema
   - Guidelines and limitations

1. **Deployment Concept**
   - [Pipelines concept](pipeline-concept.md)
   - [Service deployment concept](service-deployment-concept.md)
   - Deployment artifacts
     - [Azure infrastructure Bicep templates](bicep.md)
     - [Helm Charts](service-deployment-concept.md#helm-chart)
     - [Container images](images.md)

1. **Deploying ARO HCP**
   - [Pipelines](pipelines.md)
      - documents existing pipelines
   - Red Hat development tenant deployments
     - [Personal ARO HCP environment](personal-dev.md)
     - [Integrated DEV environment](integrated-dev.md)
     - [CS PR environment](cspr.md)
     - [Personal perfscale environment](perscale-deployment.md)
   - MSFT deployments
     - [MSIT INT](msit-int.md)

1. **[Terminology](terminology.md)**

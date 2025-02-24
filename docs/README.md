# ARO HCP Documentation

Welcome to the **ARO HCP** documentation. This guide provides an overview of the **architecture and deployment** of ARO HCP, primarily intended for **developers and SREs** working on the project.

## Table of Contents

1. **[High Level Architecture](high-level-architecture.md)**
   - System overview, scopes and key components
   - Major services and how they interact

1. **[Environments](environments.md)**
   - Overview of different environment (development, integration, staging, production)
   - Key differences and capabilities of each environment

1. **[Configuration Management](configuration.md)**
   - Overview and override structures
   - Configuration schema
   - Guidelines and limitations

1. **Deployment Concept**
   - [Pipelines concept](pipeline.md)
   - Deployment artifacts
     - [Azure infrastructure Bicep templates](bicep.md)
     - [Helm Charts](helm.md)
     - [Container images](images.md)
   - [Deploying to environments](deployment.md)

1. **[Terminology](terminology.md)**

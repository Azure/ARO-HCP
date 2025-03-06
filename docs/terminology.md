# ARO HCP Terminology

- **Definition**:
- **Abbreviation**:
- **References**:

## Azure Terminology

### Azure Resource Manager

- **Definition**: Deployment and management service for Azure
- **Abbreviation**: ARM
- **References**:
  - [Docs](https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/overview)

### Resource Provider

- **Definition**: Azure terminology for an ARM API that implements a certain resource type
- **Abbreviation**: RP
- **References**:
  - [ARO HCP RP](../frontend/)

### Bicep Templates

- **Definition**: Domain-specific language for deploying Azure resources. Translates to ARM templates.
- **References**:
  - [ARM templates documentation](https://learn.microsoft.com/en-us/azure/azure-resource-manager/templates/overview)
  - [Bicep documentation](https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/overview)

## ARO HCP Terminology

### Hosted Control Plane

- **Definition:** OCP control plane running as Pods on a Kubernetes cluster. These Pods are managed by [Hypershift](#hosted-control-plane).
- **Abbreviation:** HCP
- **References**:
  - [Blog](https://www.redhat.com/en/blog/red-hat-openshift-service-aws-hosted-control-planes-now-available)

### Hypershift

- **Definition** Hypershift is a middleware to host OCP control planes on Kubernetes clusters.
- **Reference:**
  - [Documentation](https://hypershift-docs.netlify.app/how-to/)
  - [Source](https://github.com/openshift/hypershift)

### Service Cluster

- **Definition**: Hosts the regional entry point services like [RP](#resource-provider) and [CS](#service-cluster) for HCP creation and management
- **Abbreviation**: SC, SVC
- **References**:
  - [High Level Architecture](high-level-architecture.md#service-cluster)
  - [Source](../dev-infrastructure)

### Management Cluster

- **Definition**: Execution layer for [HCPs](#hosted-control-plane) within a region, leveraging [Hypershift](#hypershift)
- **Abbreviation**: MC, MGMT
- **References**:
  - [High Level Architecture](high-level-architecture.md#management-clusters)
  - [Source](../dev-infrastructure)

### Clusters Service

- **Definition:**: Service that orchestrates the creation and day two management of managed OpenShift clusters
- **Abbreviation**: CS
- **References**:
  - [Source](https://gitlab.cee.redhat.com/service/uhc-clusters-service/)

### Maestro

- **Definition**: Maestro is a system to leverage CloudEvents over MQTT to transport Kubernetes resources to the target clusters, and then transport the resource status back.
- **References**:
  - [Source](https://github.com/openshift-online/maestro)
  - [High Level Architecture](high-level-architecture.md#service-cluster)

### Advanced Cluster Manager

- **Definition**: Advanced Cluster Manager is a service that provides additional lifecycle and policy management for OCP clusters.
- **Abbreviation**: ACM
- **References**:
  - [Documentation](https://www.redhat.com/en/technologies/management/advanced-cluster-management)

## Microsoft Terminology

### Azure DevOps

- **Definition**: Microsoft's cloud-based service for collaborating on code development. Includes source code hosting, CI/CD, and more.
- **Abbreviation**: ADO
- **References**:
  - [Docs](https://learn.microsoft.com/en-us/azure/devops/)
  - [ARO HCP ADO Pipelines](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionScope=%5COneBranch%5Chcp)

### EV2

- **Definition**: Microsoft automated platform for deploying Azure resources accross clouds.
- **References**:
  - [ARO HCP EV2](https://ev2docs.azure.net/getting-started/overview.html)

### EV2 Stamp

- **Definition**: A stamp allows for partitioning a region into scaling units. Used to build multiple management clusters in a region.
- **References**:
  - [Docs](https://ev2docs.azure.net/features/rollout-orchestration/configure-stamps.html)

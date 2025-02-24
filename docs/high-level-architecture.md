# ARO HCP High Level Architecture

This document describes the high level ARO HCP architecture and its various scopes. The goal is to give the reader enough context to understand and work on the infrastructure and service deployment processes of the ARO HCP project.

ARO HCP is a cloud service deployed across multiple Azure regions, with a design that ensures both regional autonomy and global efficiency. Each region operates an independent instance of ARO HCP to maximize reliability, while global services provide supporting infrastructure without introducing availability risks. To structure this, the architecture is divided into two primary scopes:

- **Regional Scope** – Contains all the components required to operate ARO HCP within a region.
- **Global Scope** – Consists of shared services that support regional instances while maintaining built-in redundancy and resilience across multiple regions.

A high level architecture diagram can be found [here](https://link.excalidraw.com/l/1NnYvmogbSd/2I3z0Ishpo0).

## Regional Scope

Each regional instance of ARO HCP is self-contained and operates independently to ensure high availability. The regional scope consists of two subscopes:

- **Service Cluster** – Handles customer interactions, provisioning, and lifecycle orchestration.
- **Management Cluster** – Responsible for hosting the deployed ARO HCP clusters.

### Service Cluster

The Service Cluster hosts the primary control plane service components for the ARO HCP service in a given region, processing customer requests and managing ARO HCP provisioning workflows. It hosts regional entry point services and delegates cluster creation to the Management Clusters.

#### Core Components

- **Resource Provider (RP)**
  - The primary API entry point that integrates with Azure Resource Manager (ARM).
  - Handles customer requests for provisioning and managing ARO HCP clusters.
  - Validates requests and forwards them to the Cluster Service (CS).

- **Cluster Service (CS)**
  - The orchestration engine responsible for processing requests from RP.
  - Manages the baseline infrastructure needed for ARO HCP clusters (networking, compute, storage).
  - Delegates cluster creation to Hypershift to the Management Clusters, using Maestro as communication mechanism.

- **Maestro**
  - Bridges the Service Cluster and Management Cluster.
  - Transfers Kubernetes manifests to Maestro Agents running in Management Clusters.
  - Tracks the state of manifests and maintains a cached record in the Service Cluster.

#### Azure layout

The service cluster is deployed into a dedicated Azure subscription. This subscription contains all the resources required to run the service cluster, including an AKS cluster, regional DNS zones, Key Vaults, storage accounts, Postgres and Cosmos DBs, Eventgrid MQTT brokers as well as managed identities.

### **Management Clusters**

The Management Clusters are the execution layer of ARO HCP, responsible for hosting HCPs within a region. They process provisioning requests from the Service Cluster and manage the full lifecycle of ARO HCP clusters. While the Service Cluster handles orchestration and external API interactions, the Management Cluster ensures that ARO HCP clusters are created, maintained, and updated reliably as requested by the customer via the Service Cluster.

#### Core Components

- **Hypershift Operator**
  - The core controller responsible for provisioning and managing ARO HCP clusters.
  - Processes cluster provisioning requests from **Cluster Service (CS)** via Kubernetes Custom Resources (CRs).
  - Ensures clusters are created, updated, and deleted according to specifications.
  - Allocates and manages required compute, networking, and storage resources for each ARO HCP cluster.

- **Maestro Agent**
  - Runs on every Management Cluster.
  - Receives Kubernetes manifests from Maestro Server and applies them to the Management Cluster.
  - Reports resource status back to the Maestro Server.
  - Operates asynchronously, ensuring reliable state enforcement without direct dependency on the Service Cluster.

- **Advanced Cluster Management (ACM)**
  - Responsible for additional lifecycle and policy management of ARO HCP clusters.

#### Scaling with Multiple Management Clusters

A single Management Cluster has a limit on the number of HCPs it can run due to resource constraints. To scale the service within a region, multiple Management Clusters can be deployed. These clusters operate independently while still being orchestrated by the Service Cluster to distribute HCPs efficiently. This horizontal scaling model ensures that ARO HCP can support a high number of HCPs in a region.

#### Azure Layout

Each Management Cluster is deployed into a dedicated Azure subscription. This subscription contains all the resources required to run the Management Cluster, including an AKS cluster, Key Vaults as well as managed identities.

## Global Scope

ARO HCP also relies on global Azure resources to provide **efficient, scalable** operations across all regions. Their **built-in redundancy and failover mechanisms** prevent them from becoming single points of failure.

### Key Global Services

- **Azure Container Registry (ACR) with Regional Replication**
  - Stores all container images required by ARO HCP.
  - **SVC ACR**: Contains all images for ARO HCP services.
  - **OCP ACR**: Mirrors OpenShift images from quay.io
  - Images only need to be mirrored once and are automatically replicated across required regions (see [here](images.md) for information about image mirroring).

- **Azure Front Door Global Deployment**
  - Provides global traffic distribution and resilience for OIDC artifacts in ARO HCP clusters.
  - Ensures low-latency access to authentication artifacts across multiple regions.

### Azure layout

The global resources are deployed into a dedicated Azure subscription. This subscription contains all the resources required to run the global services, including Azure Front Door, Azure Container Registry, container image mirroring and the parent DNS zones for the ARO HCP service.

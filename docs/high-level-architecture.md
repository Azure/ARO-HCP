# ARO HCP High Level Architecture

This document describes the high level ARO HCP architecture and its various scopes. The goal is to give the reader enough context to understand and work on the infrastructure and service deployment processes of the ARO HCP project.

ARO HCP is a cloud service deployed across multiple Azure regions, with a design that ensures both regional autonomy and global efficiency. Each region operates an independent instance of ARO HCP to maximize reliability, while global services provide supporting infrastructure without introducing availability risks. To structure this, the architecture is divided into three primary scopes:

- **Regional Scope** – Contains all the components required to operate ARO HCP within a region.
- **Geography Scope** – Contains all the components shared between regions in the same geography.
- **Global Scope** – Consists of shared services that support regional instances while maintaining built-in redundancy and resilience across multiple regions.
- **Monitoring Scope** – Covers the monitoring stack deployed per region, managed as an independent deployment entrypoint.

A high level architecture diagram can be found [here](https://link.excalidraw.com/l/1NnYvmogbSd/2I3z0Ishpo0).

## Regional Scope

Each regional instance of ARO HCP is self-contained and operates independently to ensure high availability. The regional scope consists of two subscopes:

- **Service Cluster** – Handles customer interactions, provisioning, and lifecycle orchestration.
- **Management Cluster** – Responsible for hosting the deployed ARO HCP clusters.

### Service Cluster

The Service Cluster hosts the primary control plane service components for the ARO HCP service in a given region, processing customer requests and managing ARO HCP provisioning workflows. It hosts regional entry point services and delegates cluster creation to the Management Clusters.

#### Core Components

- **RP Frontend**
  - Serves the ARM resource API, including API-version conversion, validation and admission.
  - Persists accepted resource intent and asynchronous operation records in Cosmos DB; serves resource and operation state from that database.
  - Uses provider state published by the backend when admission needs information about management clusters. It does not call CS or access management-cluster APIs.

- **RP Backend**
  - Reconciles resource intent from Cosmos DB to drive cluster and node-pool creation, updates and deletion.
  - Calls CS APIs and manages Azure resources and service-provider configuration through its controllers.
  - Retrieves manifests from CS, combines them with backend-owned Kubernetes resources and settings, and writes ApplyDesire documents for kube-applier.
  - Consumes observed Kubernetes state through ReadDesire documents, updates resource and provider state in Cosmos DB, and determines asynchronous operation completion.

- **Clusters Service (CS)**
  - Maintains cluster and node-pool lifecycle state in PostgreSQL and runs provisioning, update, upgrade and deletion workers.
  - Performs Azure networking, DNS and identity work and exposes generated Kubernetes manifests for the backend to retrieve.
  - Creates and consumes its own HostedCluster and NodePool ReadDesires through Cosmos DB to track observed state. See [service components](service-components.md) for the responsibilities shared with the backend.

- **Fleet**
  - Reconciles management-cluster registration.
  - Publishes observed capacity and scaling limits in management-cluster scheduling documents for placement decisions.
  - Reconciles management-cluster AKS node pools when configured and reports shared-ingress state through kube-applier observations.

Cosmos DB connects the ARM request path to backend reconciliation and carries the ApplyDesire/ReadDesire exchange between the backend, CS and kube-applier. Hypershift reconciles the resulting HostedCluster and NodePool resources on management clusters.

#### Azure layout

The service cluster is deployed into a dedicated Azure subscription. This subscription contains all the resources required to run the service cluster, including an AKS cluster, regional DNS zones, Key Vaults, storage accounts, Postgres and Cosmos DBs, Eventgrid MQTT brokers as well as managed identities.

### **Management Clusters**

The Management Clusters are the execution layer of ARO HCP, responsible for hosting HCPs within a region. They process provisioning requests from the Service Cluster and manage the full lifecycle of ARO HCP clusters. While the Service Cluster handles orchestration and external API interactions, the Management Cluster ensures that ARO HCP clusters are created, maintained, and updated reliably as requested by the customer via the Service Cluster.

#### Core Components

- **Hypershift Operator**
  - The core controller responsible for provisioning and managing ARO HCP clusters.
  - Reconciles HostedCluster and NodePool resources applied by kube-applier under the backend’s orchestration.
  - Ensures clusters are created, updated, and deleted according to specifications.
  - Allocates and manages required compute, networking, and storage resources for each ARO HCP cluster.

- **kube-applier**
  - Runs on every Management Cluster.
  - Executes server-side apply and deletion requests described by ApplyDesire documents against the local Kubernetes API, and records their outcomes.
  - Watches resources requested by ReadDesire documents and mirrors their observed content and status into Cosmos DB for the backend and CS.
  - Handles Kubernetes resource reconciliation within its management cluster; ARM operation completion and cluster lifecycle decisions belong to the backend.

- **mgmt-agent**
  - Runs on every Management Cluster alongside Hypershift and kube-applier.
  - Bridges management-cluster concerns (for example SWIFT NIC extended resources on nodes).
  - Emits structured **resource** and **pod** lifecycle **event logs** to Kusto via `mgmt-agent-controller` container logs — see [mgmt-agent event logs](../tooling/hcpctl/pkg/agent/prompts/exemplars/mgmt-agent-event-logs.md) for ad-hoc query patterns. These are intentionally **not** included in the default snapshot dump because output volume is unbounded.

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
  - **OCP ACR**: Mirrors OpenShift images from quay.io using a cache rule.
  - Images only need to be mirrored once and are automatically replicated across required regions (see [here](acrs-and-images.md) for information about image mirroring).

- **Azure Front Door Global Deployment**
  - Provides global traffic distribution and resilience for OIDC artifacts in ARO HCP clusters.
  - Ensures low-latency access to authentication artifacts across multiple regions.

### Azure layout

The global resources are deployed into a dedicated Azure subscription. This subscription contains all the resources required to run the global services, including Azure Front Door, Azure Container Registry, container image mirroring and the parent DNS zones for the ARO HCP service.

## Geography Scope

ARO HCP also relies on geography-scoped Kusto clusters according to the Geos defined in the Ev2 configuration: https://github.com/Azure/ARO-Tools/blob/main/config/ev2config/config.yaml

The first region added in a given geography is the region that will deploy the Kusto cluster associated with the geography. For example, the eastus2 rollout creates the Kusto cluster for the "United States" geography. 

### Key Geo Services

- **Kusto**
  - Stores logs from all clusters in a given geography (see [here](logging.md) for more information)

### Azure layout

The geography-level resources are deployed into the same dedicated global Azure subscription used for global services, with logical separation by geography (for example, one Kusto deployment per geography).

## Monitoring Scope

Monitoring resources are deployed as part of the regular regional rollout and follow the same lifecycle as other regional components. In addition, monitoring is defined as a separate deployment entrypoint (`Microsoft.Azure.ARO.HCP.Monitoring`) to provide flexibility and speed when iterating on the monitoring stack. This allows changes to alerting rules, metrics collection, or monitoring infrastructure to be pushed independently without waiting for or triggering a full regional rollout — supporting a gradual on-call ramp-up.

While the monitoring resources are regional in nature and operate within the regional subscription, the dedicated entrypoint ensures that monitoring can evolve on its own schedule when needed.

### Key Monitoring Services

- **Azure Monitor Metrics**
  - Provides metrics collection for both ARO HCP services running on the Service Cluster and hosted control planes running on Management Clusters.

- **Prometheus Alerting Rules & Action Groups**
  - Defines alerting rules and notification targets for operational monitoring of the ARO HCP service.

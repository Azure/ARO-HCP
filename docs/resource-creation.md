# ARO-HCP Resource Creation Flow

This document describes how resources are created in the ARO-HCP architecture, from API request through async completion. Detailed creation flow diagrams are provided for HCPOpenShiftCluster and NodePool. ExternalAuth follows the same general pattern (frontend validation, Cluster Service POST, CosmosDB transaction, backend polling).

## Overview

```mermaid
flowchart TD
    CLIENT["Client (ARM / az CLI)"] --> ARM[ARM routing]
    ARM --> FE["RP Frontend<br/>(service cluster)"]
    FE --> CS["Cluster Service<br/>(service cluster)"]
    FE --> COSMOS["CosmosDB<br/>(operation + resource docs)"]
    FE -->|"201 Created + operation headers"| CLIENT

    BE["RP Backend<br/>(service cluster)"] -->|polls| COSMOS
    BE -->|polls| CS
    BE -->|"updates operation status"| COSMOS
    BE -->|"creates billing doc on success"| COSMOS
    CS -->|"orchestrates via Maestro"| MAESTRO["Maestro<br/>(service cluster)"]
    MAESTRO -->|"resource bundles"| MC["Management Cluster<br/>(runs HCP workloads)"]

    style FE fill:#fff3e0
    style BE fill:#e8f5e9
    style CS fill:#e1f5fe
    style MAESTRO fill:#f3e5f5
```

## Resource Types

```mermaid
flowchart LR
    HCP["HCPOpenShiftCluster<br/>(parent resource)"] --> NP["NodePool<br/>(child of cluster)"]
    HCP --> EA["ExternalAuth<br/>(child of cluster)"]

    style HCP fill:#e1f5fe
    style NP fill:#e8f5e9
    style EA fill:#fff3e0
```

- **HCPOpenShiftCluster** - The hosted control plane cluster. Parent resource.
- **NodePool** - Worker node pools. Child of a cluster.
- **ExternalAuth** - External authentication configurations (OIDC providers). Child of a cluster.

## Frontend Middleware Chain

All requests pass through pre-mux middleware. Post-mux middleware varies by endpoint type.

```mermaid
flowchart TD
    REQ[Incoming HTTP Request] --> PRE[Pre-mux middleware - all requests]
    PRE --> P1[MiddlewarePanic - recover from panics]
    P1 --> P2[MiddlewareReferer - ensure Referer header]
    P2 --> P3[Metrics - request metrics collection]
    P3 --> P4[MiddlewareCorrelationData - ARM correlation tracking]
    P4 --> P5[Audit - audit logging]
    P5 --> P6[MiddlewareTracing - distributed tracing]
    P6 --> P7[MiddlewareLowercase - lowercase URL for routing]
    P7 --> P8[MiddlewareLogging - request logging]
    P8 --> P9[MiddlewarePanic - second recovery after tracing]
    P9 --> P10[MiddlewareBody - read and cache request body]
    P10 --> P11[MiddlewareSystemData - ARM SystemData extraction]
    P11 --> MUX[ServeMux pattern matching]

    MUX --> ROUTE{Endpoint type?}

    ROUTE -->|"List endpoints"| LIST["MiddlewareLoggingPostMux<br/>ValidateAPIVersion<br/>ValidateSubscriptionState"]
    ROUTE -->|"Read endpoints"| READ["MiddlewareResourceID<br/>MiddlewareLoggingPostMux<br/>ValidateAPIVersion<br/>ValidateSubscriptionState"]
    ROUTE -->|"Create/Update/Delete"| MUT["MiddlewareResourceID<br/>MiddlewareLoggingPostMux<br/>ValidateAPIVersion<br/>LockSubscription<br/>ValidateSubscriptionState"]
    ROUTE -->|"Operation endpoints"| OPS["MiddlewareResourceID<br/>MiddlewareLoggingPostMux<br/>ValidateAPIVersion<br/>ValidateSubscriptionState"]
    ROUTE -->|"Subscription mgmt"| SUB_MW["MiddlewareResourceID<br/>MiddlewareLoggingPostMux<br/>(PUT only: LockSubscription)"]

    LIST --> HANDLER[Route handler]
    READ --> HANDLER
    MUT --> HANDLER
    OPS --> HANDLER
    SUB_MW --> HANDLER
```

## HCPOpenShiftCluster Creation

```mermaid
flowchart TD
    PUT["PUT .../hcpOpenShiftClusters/{name}"] --> HANDLER["CreateOrUpdateHCPCluster<br/>(frontend/pkg/frontend/cluster.go)"]

    HANDLER --> CHECK{Resource exists<br/>in CosmosDB?}
    CHECK -->|No - Create| CREATE["createHCPCluster()"]
    CHECK -->|"Yes - Update (PUT)"| UPDATE["updateHCPCluster()"]
    CHECK -->|"Yes - Update (PATCH)"| PATCH["patchHCPCluster()"]

    CREATE --> SUB[Get subscription from CosmosDB]
    SUB --> DECODE["decodeDesiredClusterCreate()<br/>- unmarshal request body<br/>- set default values<br/>- convert to internal type<br/>- set TrackedResource fields<br/>- set SystemData<br/>- set MSI identity URL from header"]
    DECODE --> MUT["MutateCluster()<br/>(admission mutations)"]
    MUT --> VAL["ValidateCluster()<br/>(static validation)"]
    VAL --> ADMIT["AdmitClusterOnCreate()<br/>(admission checks)"]
    ADMIT --> CSBUILD["BuildCSCluster()<br/>(build Cluster Service request)"]
    CSBUILD --> CSPOST["clusterServiceClient.PostCluster()<br/>(create in Cluster Service)"]
    CSPOST --> TX[Create CosmosDB transaction]

    TX --> OP["Create operation document<br/>(OperationRequestCreate)"]
    OP --> RES["Create cluster document<br/>(ProvisioningState = Accepted)"]
    RES --> EXEC["Execute transaction atomically"]
    EXEC --> MERGE["Merge Cluster Service response<br/>with CosmosDB document"]
    MERGE --> RESP["Return 201 Created<br/>+ Azure-AsyncOperation header<br/>+ Location header"]
```

## NodePool Creation

```mermaid
flowchart TD
    PUT["PUT .../hcpOpenShiftClusters/{clusterName}/nodePools/{name}"] --> HANDLER["CreateOrUpdateNodePool<br/>(frontend/pkg/frontend/node_pool.go)"]

    HANDLER --> CHECK{Resource exists<br/>in CosmosDB?}
    CHECK -->|No - Create| CREATE["createNodePool()"]
    CHECK -->|"Yes - Update"| UPDATE["updateNodePool() / patchNodePool()"]

    CREATE --> DECODE["decodeDesiredNodePoolCreate()<br/>- unmarshal request body<br/>- set default values<br/>- convert to internal type<br/>- set TrackedResource and SystemData"]
    DECODE --> PARENT["getInternalClusterFromStorage()<br/>(fetch parent cluster)"]
    PARENT --> VAL["ValidateNodePoolCreate()<br/>(static validation)"]
    VAL --> ADMIT["AdmitNodePool()<br/>(admission checks against parent cluster)"]
    ADMIT --> CONFLICT["checkForProvisioningStateConflict()"]
    CONFLICT --> CSBUILD["BuildCSNodePool()<br/>(build Cluster Service request)"]
    CSBUILD --> CSPOST["clusterServiceClient.PostNodePool()<br/>(create in Cluster Service)"]
    CSPOST --> TX[Create CosmosDB transaction]

    TX --> OP["Create operation document<br/>(OperationRequestCreate)"]
    OP --> RES["Create node pool document<br/>(ProvisioningState = Accepted)"]
    RES --> EXEC[Execute transaction atomically]
    EXEC --> MERGE["Merge Cluster Service response<br/>with CosmosDB document"]
    MERGE --> RESP["Return 201 Created<br/>+ Azure-AsyncOperation header<br/>+ Location header"]
```

## Backend: Async Operation Processing

The backend runs as a set of Kubernetes-style controllers on the service cluster. Controllers use SharedInformers backed by CosmosDB via periodic relists (expiring watchers that trigger 410 Gone to force relist), not a native change feed.

```mermaid
flowchart TD
    INFORMER["SharedInformer<br/>(periodic CosmosDB relist<br/>via expiring watcher)"] --> FILTER{"ShouldProcess()?<br/>- not terminal<br/>- matching request type<br/>- matching resource type"}
    FILTER -->|Yes| COOLDOWN["Cooldown check<br/>(10s between syncs)"]
    COOLDOWN --> QUEUE[Rate-limited work queue]
    FILTER -->|No| SKIP[Skip]

    QUEUE --> SYNC["SynchronizeOperation()"]

    SYNC --> POLL["Poll Cluster Service status<br/>- GetClusterStatus() for clusters<br/>- GetNodePoolStatus() for node pools"]
    POLL --> CONVERT["Convert CS state to ARM ProvisioningState"]

    CONVERT --> STATES{Cluster Service state?}
    STATES -->|installing| PROV[ProvisioningState: Provisioning]
    STATES -->|updating| UPD[ProvisioningState: Updating]
    STATES -->|"ready (non-delete)"| SUCC[ProvisioningState: Succeeded]
    STATES -->|"ready (delete)"| NOOP["No state change<br/>(delete success is 404 from CS)"]
    STATES -->|error| FAIL["ProvisioningState: Failed<br/>(with ProvisionErrorCode)"]
    STATES -->|uninstalling| DEL[ProvisioningState: Deleting]
    STATES -->|"pending / validating"| TOLERATE["Tolerated only when already Accepted<br/>(no active transition)"]

    SUCC --> BILLING{"Create operation?"}
    BILLING -->|"Yes (cluster only)"| BILL["Create billing document<br/>in CosmosDB"]
    BILL --> UPDATEOP
    BILLING -->|No| UPDATEOP

    FAIL --> UPDATEOP
    TOLERATE --> REQUEUE[Requeue for next poll]
    PROV --> REQUEUE
    UPD --> REQUEUE
    DEL --> REQUEUE
    NOOP --> REQUEUE

    UPDATEOP["UpdateOperationStatus()<br/>- update operation doc<br/>- update resource doc<br/>- atomic CosmosDB transaction<br/>- POST async notification to ARM"]
```

## Backend Controllers

```mermaid
flowchart LR
    subgraph "Operation Controllers (poll CS status)"
        OCC[Cluster Create]
        OCU[Cluster Update]
        OCD[Cluster Delete]
        ONPC[NodePool Create]
        ONPU[NodePool Update]
        ONPD[NodePool Delete]
        OEAC[ExternalAuth Create]
        OEAU[ExternalAuth Update]
        OEAD[ExternalAuth Delete]
        ORC[Request Credential]
        OREV[Revoke Credentials]
    end

    subgraph "Validation Controllers (async checks)"
        VRG["Resource Group Existence"]
        VREG["RP Registration"]
        VMIS["Managed Identity Existence"]
    end

    subgraph "Other Controllers"
        MISMATCH["Mismatch Controllers<br/>(reconcile Cosmos vs CS)"]
        UPGRADE["Upgrade Controllers<br/>(trigger CP upgrades)"]
        PROPS["Cluster Properties Sync"]
        MAESTRO_CTRL["Maestro Bundle Controllers"]
    end
```

## Cluster Service and Maestro Role

Cluster Service is the core orchestrator for HCP lifecycle. When the frontend POSTs a new cluster or node pool to Cluster Service:

```mermaid
flowchart TD
    CS["Cluster Service<br/>(receives POST from frontend)"] --> VALIDATE["Validate cluster/nodepool spec"]
    VALIDATE --> PERSIST["Persist to CS database"]
    PERSIST --> PROVISION["Begin provisioning"]

    PROVISION --> MAESTRO["Send resource bundles via Maestro"]
    MAESTRO --> MC["Management Cluster"]
    MC --> HCP["Deploy Hosted Control Plane<br/>(etcd, API server, controllers)"]

    CS -->|"status: pending"| STATUS1[pending]
    STATUS1 -->|"status: validating"| STATUS2[validating]
    STATUS2 -->|"status: installing"| STATUS3[installing]
    STATUS3 -->|"status: ready"| STATUS4[ready]
    STATUS3 -->|"status: error"| STATUS5[error]

    subgraph "Management Cluster"
        HCP --> ETCD[etcd]
        HCP --> APISERVER[API Server]
        HCP --> CONTROLLERS[Controllers]
        HCP --> NODEPOOLS[Node Pool machines in customer VNet]
    end
```

## Async Operation Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Accepted : Frontend creates operation doc
    Accepted --> Provisioning : CS begins installing
    Accepted --> Failed : CS reports error

    Provisioning --> Succeeded : CS reports ready
    Provisioning --> Failed : CS reports error

    Succeeded --> [*] : Terminal
    Failed --> [*] : Terminal

    note right of Accepted
        Client polls via
        Azure-AsyncOperation URL
    end note
```

## Key Differences from Classic ARO-RP

| Aspect | Classic ARO-RP | ARO-HCP |
|--------|---------------|---------|
| Control plane | Runs on dedicated VMs in customer VNet | Hosted on management cluster (HCP) |
| Backend | Directly installs cluster (phases, bootstrap VM) | Delegates to Cluster Service, polls for status |
| Install orchestration | RP backend runs install steps (Hive/Podman) | Cluster Service + Maestro + management cluster |
| Database | CosmosDB (single document per cluster) | CosmosDB (separate operation + resource docs, transactions) |
| Operation model | Provisioning state on cluster doc, polled via async op record | Dedicated operation documents, periodic relist via expiring watchers triggers controllers |
| Node pools | Managed by machine-api operator after bootstrap | First-class ARM resource with own lifecycle |
| Resource types | OpenShiftCluster only | HCPOpenShiftCluster, NodePool, ExternalAuth |

# Service Components

## Frontend (`arohcpfrontend`)

The ARM-facing resource provider. Receives customer HTTP requests (PUT, POST, PATCH, DELETE) for
clusters and node pools.

**Responsibilities:**
- Request validation and authentication
- Creating async operations (returns `Azure-AsyncOperation` header with operation URL)
- Proxying GET requests

**Failure modes:** Validation errors (4xx), timeout creating async operation, panic/crash.

## Backend (`arohcpbackend`)

Processes async operations created by the Frontend. Runs as a set of controllers.

**Responsibilities:**
- Polling async operation status
- Translating ARM operations into Clusters Service API calls
- Dumping resource state periodically (the `datadump` and `csstatedump` controllers)

**Failure modes:** CS API errors, stale state, controller crash loops, provisioning state stuck.

## Clusters Service (`aro-hcp-clusters-service`)

Red Hat-hosted service that manages the full cluster and node pool lifecycle.

**Responsibilities:**
- Maintains its own state machine for cluster/nodepool provisioning
- Interacts with Azure to provision infrastructure required for a cluster
- Produces Maestro resource bundles describing desired management-cluster state

**Failure modes:** State machine stuck, bundle creation failure, internal errors, dependency on
external services (e.g. DNS, certificate provisioning).

## Maestro

Resource delivery system. The **server** runs on the service cluster; the **agent** runs on each
management cluster.

**Responsibilities:**
- Server: accepts resource bundles from CS, persists them, signals agents
- Agent: watches for bundles, applies them as ManifestWork objects on the management cluster

**Key identifiers:**
- Bundle IDs (from CS logs)
- ManifestWork names (applied on management cluster)

**Failure modes:** Agent not connected, bundle apply failure, ManifestWork rejected, stale bundles.

**Key Notes:** When reviewing Maestro transitions from logs, expect to see the spec and status
sides equal - that is, if the server sees N client pings on spec, we should see the server send
N specs to the broker, and the agent should get N specs from the broker and apply all of them to
the cluster. If the agent sees X status updates from the cluster, we should see X status events
to the broker, X status events processed by the server and at least X notifications to subscribers. 

## HyperShift

Operator running on management clusters that reconciles HostedCluster and NodePool custom resources
into actual control plane infrastructure.

**Responsibilities:**
- Creating hosted control plane pods (kube-apiserver, etcd, etc.)
- Managing NodePool machine sets
- Reporting conditions on HostedCluster and NodePool objects

**Key objects:**
- `HostedCluster` in a namespace on the management cluster
- `HostedControlPlane` in `<namespace>-<name>` namespace
- `NodePool` in the same namespace as the HostedCluster

**Failure modes:** Condition degraded/progressing stuck, pod crash loops, etcd issues, RBAC errors,
resource quota exceeded, image pull failures.

# ARO-HCP Architecture

ARO-HCP (Azure Red Hat OpenShift — Hosted Control Planes) is a managed OpenShift service where customer
cluster control planes run on shared management clusters rather than on the customer's own infrastructure.

## Request Flow

A customer operation (e.g. create cluster) flows through the following components in order:

```
Customer → ARM → Frontend → Backend (async) → Clusters Service → Maestro → HyperShift
                                    └→ kube-applier (desires) ────────────────┘
```

1. **ARM** delivers the request to the **Frontend** (RP), which validates it and creates an async operation.
2. The **Backend** picks up the async operation, translates it into Clusters Service API calls, and may also
   write kube-applier desires to Cosmos for direct management-cluster changes. Over time, the backend will take
   over from CS; check the codebase for clear distinction of responsibilities.
3. **Clusters Service** (CS) manages the cluster/nodepool lifecycle via its own state machine, producing
   Maestro resource bundles that describe the desired state on the management cluster.
4. **Maestro server** delivers those bundles to the management cluster as ManifestWork objects.
5. **Maestro agent** applies the objects to the management cluster and pushes status back to the server.
6. **Kube Applier** (on each management cluster) reconciles backend `ApplyDesire` / `DeleteDesire` / `ReadDesire`
   documents from Cosmos against the local kube-apiserver.
7. **HyperShift** reconciles HostedCluster and NodePool custom resources on the management cluster,
   creating the actual control plane pods in a hosted-control-plane namespace.

## Topology

- **Management clusters**: Run HyperShift, Maestro agent, kube-applier, and hosted control planes for many customers.
- **Service clusters**: Run the Frontend, Backend, Clusters Service, and Maestro server.
- Each environment has its own Kusto cluster with two databases:
  - **Service database**: Frontend, Backend, and Clusters Service logs.
  - **HCP database**: HyperShift, control-plane-operator, and per-cluster control plane logs.

## Resource Types

| ARM Resource Type | Description |
|---|---|
| `microsoft.redhatopenshift/hcpopenshiftclusters` | Top-level cluster resource |
| `microsoft.redhatopenshift/hcpopenshiftclusters/nodepools` | Node pool (child of cluster) |
| `microsoft.redhatopenshift/hcpopenshiftclusters/requestadmincredential` | Admin credential request |

Each cluster resource maps to downstream objects:
- A CS cluster ID (`cid`) (opaque string like `2iig1flm0pfjr9h8kkg6ggbjig1p3fpa`)
- Maestro bundle IDs and ManifestWork names
- A HyperShift HostedCluster in a namespace on the management cluster
- A HyperShift HostedControlPlane in its own namespace (`<hc-namespace>-<hc-name>`)

## Key Repositories

| Repository | Contains |
|---|---|
| `ARO-HCP` | Frontend, Backend, Admin API, tooling |
| `aro-hcp-clusters-service` | Clusters Service (private, mirrored from GitLab) |
| `maestro` | Maestro server and agent |
| `hypershift` | HyperShift operator |

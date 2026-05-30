# Phase 2 Fleet Controllers Implementation Plan

**Jira:** [ARO-26926](https://redhat.atlassian.net/browse/ARO-26926) — Controller-driven downstream reconciliation

## Context

Phase 2 of the management cluster fleet management enhancement transitions from the Phase 1 CS-mirror approach to a controller-driven, CosmosDB-as-source-of-truth model. Three new controllers reconcile downstream systems (ClustersService, Maestro) from CosmosDB documents. The existing `ManagementClusterMigrationController` (Phase 1) is retired.

Enhancement doc: `/Users/goberlec/dev/aro-enhancements/enhancements/cluster-lifecycle/mgmt-cluster-fleet-management-phase2.md`

## Design Decisions (from grilling session)

| Decision | Choice |
|----------|--------|
| Runtime model | Single binary, `fleet controller` Cobra subcommand |
| Controller base | Kube-applier pattern (own workqueue + event handlers + Run/SyncOnce), shared `internal/controllerutils` |
| Package structure | `fleet/pkg/controllers/{csregistration,maestroregistration,lifecycle}/` |
| Stamp lookup | Informer cache for parent Stamp |
| Cross-enqueue | Custom event handler on Stamp informer derives MC RID (`{stamp}/managementClusters/default`) |
| Maestro consumer client | Use `maestroopenapi.DefaultAPIService` directly; thin interface at controller boundary as test seam |
| CS shard identification | Match by AKS resource ID via ListProvisionShards; `ClusterServiceProvisionShardID` is controller-owned, not CLI-ingested |
| Deployment | Service cluster, Kubernetes lease-based leader election |
| Worker threads | 4 (CS reg) / 4 (Maestro reg) / 1 (lifecycle) |
| Cleanup | Remove `ManagementClusterMigrationController` from backend |

## Implementation Steps

### 1. Add `fleet controller` Cobra subcommand

**Files:**
- `fleet/cmd/controller/cmd.go` — Cobra command, flag definitions
- `fleet/cmd/controller/options.go` — RawControllerOptions → ValidatedControllerOptions → ControllerOptions

**Pattern:** Follow the established Raw → Validated → Completed options pattern from `fleet/cmd/register/options.go`:
- `RawControllerOptions` — string flags from CLI
- `Validate()` → `ValidatedControllerOptions` — parsed/validated values
- `Complete()` → `ControllerOptions` — constructed clients (FleetDBClient, CS client, Maestro client, leader election lock)

`ControllerOptions` holds:
- `FleetDBClient` (CosmosDB)
- `ClusterServiceClient` (`ocm.ClusterServiceClientSpec`)
- Maestro REST API endpoint (for `maestroopenapi`)
- Leader election lock (Kubernetes lease)
- Healthz/metrics listen addresses

Register in `fleet/main.go` alongside the existing `register` subcommand.

### 2. Controller startup and wiring (`fleet/pkg/app/`)

**Files:**
- `fleet/pkg/app/fleet_controller.go` — `Run()` method: leader election → informers → cache sync → controllers
- `fleet/pkg/app/cosmos_wiring.go` — FleetDBClient factory (can reuse from register)
- `fleet/pkg/app/cs_wiring.go` — ClustersService client factory

**Pattern:** Follow `kube-applier/pkg/app/kube_applier.go`:
1. Start healthz + metrics servers
2. `runControllersUnderLeaderElection()`:
   - Create Stamp + ManagementCluster informers from `FleetDBClient.GlobalListers()`
   - Create 3 controllers, passing informers + clients
   - Leader election callback: start informers → `WaitForCacheSync` → start controllers

### 3. ClustersServiceRegistrationController

**Package:** `fleet/pkg/controllers/csregistration/`

**Files:**
- `controller.go` — Controller struct, workqueue, event handlers, Run/SyncOnce
- `controller_test.go` — Table-driven tests using `ocm.MockClusterServiceClientSpec`

**Watches:** ManagementCluster informer (primary), Stamp informer (cross-enqueue via custom handler)

**SyncOnce logic:**
1. Fetch ManagementCluster from DB (live read, not cache — need fresh ETag)
2. Look up parent Stamp from Stamp informer cache. If `Approved` condition is not True → skip, set `ClustersServiceRegistered=False` with reason `StampNotApproved`
3. Find existing provision shard:
   - If `Status.ClusterServiceProvisionShardID` is set → `GetProvisionShard`. If 404 → fall through to search
   - `ListProvisionShards` → match by `AzureShard.AksManagementClusterResourceId` == MC's `Status.AKSResourceID`
   - If found → `UpdateProvisionShard`
   - If not found → `PostProvisionShard`
4. Store returned shard ID in `Status.ClusterServiceProvisionShardID`
5. Map `Spec.SchedulingPolicy` to shard status: `Schedulable` → `active`, `Unschedulable` → `maintenance`
6. Set `ClustersServiceRegistered=True` condition
7. Write MC back to CosmosDB (with ETag concurrency)

**Provision shard builder fields from ManagementCluster:**
- `AzureShard.AksManagementClusterResourceId` ← `Status.AKSResourceID`
- `AzureShard.PublicDnsZoneResourceId` ← `Status.PublicDNSZoneResourceID`
- `AzureShard.CxSecretsKeyVaultUrl` ← `Status.HostedClustersSecretsKeyVaultURL`
- `AzureShard.CxManagedIdentitiesKeyVaultUrl` ← `Status.HostedClustersManagedIdentitiesKeyVaultURL`
- `AzureShard.CxSecretsKeyVaultManagedIdentityClientId` ← `Status.HostedClustersSecretsKeyVaultManagedIdentityClientID`
- `MaestroConfig.ConsumerName` ← `Status.MaestroConsumerName`
- `MaestroConfig.RestApiConfig.Url` ← `Status.MaestroRESTAPIURL`
- `MaestroConfig.GrpcApiConfig.Url` ← `Status.MaestroGRPCTarget`
- `Status` ← mapped from `Spec.SchedulingPolicy`

### 4. MaestroRegistrationController

**Package:** `fleet/pkg/controllers/maestroregistration/`

**Files:**
- `controller.go` — Controller struct, workqueue, event handlers, Run/SyncOnce
- `consumer_client.go` — `MaestroConsumerClient` interface (test seam)
- `controller_test.go` — Table-driven tests with fake consumer client

**Interface (test seam):**
```go
type MaestroConsumerClient interface {
    GetConsumer(ctx context.Context, consumerName string) (*maestroopenapi.Consumer, error)
    CreateConsumer(ctx context.Context, consumer maestroopenapi.Consumer) (*maestroopenapi.Consumer, error)
    PatchConsumer(ctx context.Context, consumerID string, patch maestroopenapi.ConsumerPatchRequest) (*maestroopenapi.Consumer, error)
}
```
Production impl wraps `maestroopenapi.DefaultAPIService`.

**Watches:** ManagementCluster informer (primary), Stamp informer (cross-enqueue)

**SyncOnce logic:**
1. Fetch ManagementCluster from DB (live read)
2. Look up parent Stamp from informer cache. If `Approved` not True → skip, set `MaestroRegistered=False`
3. Get or create Maestro consumer by `Status.MaestroConsumerName`
4. Set `MaestroRegistered=True` condition
5. Write MC back to CosmosDB

### 5. ManagementClusterLifecycleController

**Package:** `fleet/pkg/controllers/lifecycle/`

**Files:**
- `controller.go` — Controller struct, workqueue, event handlers, Run/SyncOnce
- `controller_test.go` — Table-driven tests

**Watches:** ManagementCluster informer only (no Stamp cross-enqueue needed)

**SyncOnce logic:**
1. Fetch ManagementCluster from DB (live read)
2. Check if both `ClustersServiceRegistered` and `MaestroRegistered` conditions exist
3. If either is absent → do not touch `Ready` (preserve current value for Phase 1→2 migration safety)
4. If both present → set `Ready=True` if both are `True`, `Ready=False` otherwise
5. Write MC back to CosmosDB

### 6. Cross-enqueue handler for Stamp changes

Shared helper used by CS and Maestro registration controllers. When a Stamp changes in the Stamp informer, derive the child MC resource ID and enqueue it:

```go
func stampToMCEnqueueHandler(queue workqueue.TypedRateLimitingInterface[MCKey]) cache.ResourceEventHandlerFuncs {
    enqueue := func(obj any) {
        stamp, ok := obj.(*fleet.Stamp)
        if !ok { return }
        mcRID := deriveManagementClusterRID(stamp.ResourceID) // append /managementClusters/default
        queue.Add(MCKeyFromResourceID(mcRID))
    }
    return cache.ResourceEventHandlerFuncs{
        AddFunc:    enqueue,
        UpdateFunc: func(old, new any) { enqueue(new) },
    }
}
```

### 7. Remove provision shard ID from `fleet register`

The `ClusterServiceProvisionShardID` field is owned by the CS registration controller, not the CLI. Remove it from the register command.

**File:** `fleet/cmd/register/options.go`
- Delete `provisionShardNamespaceUUID` constant (line 35)
- Remove `provisionShardID` from `validatedRegisterOptions` (line 100) and `registerOptions` (line 164)
- Remove UUID v5 generation in `Validate()` (lines 133-137)
- Remove `ClusterServiceProvisionShardID: o.provisionShardID` from `buildManagementCluster()` (line 178)
- Remove `github.com/google/uuid` import

### 8. Remove ManagementClusterMigrationController

**Files to modify/delete:**
- `backend/pkg/controllers/managementclustercontrollers/management_cluster_migration.go` — delete
- `backend/pkg/app/backend.go` — remove commented-out controller instantiation
- Any associated test files

### 9. Update `fleet/go.mod` dependencies

Add:
- `github.com/openshift-online/ocm-sdk-go` (for CS client)
- `github.com/openshift-online/maestro` (for maestroopenapi consumer types)
- `k8s.io/client-go` (for workqueue, cache, leader election)

### 10. Helm chart updates for fleet controller Deployment

**Files:**
- `fleet/deploy/templates/deployment.yaml` — new Deployment for `fleet controller`
- `fleet/deploy/templates/lease-rbac.yaml` — RBAC for leader election lease
- `fleet/values.yaml` — configuration values (CS URL, Maestro URL, CosmosDB, replicas)

## Key Files Reference

| File | Role |
|------|------|
| `fleet/main.go` | CLI entry point |
| `fleet/cmd/controller/cmd.go` | `fleet controller` subcommand |
| `fleet/pkg/app/fleet_controller.go` | Controller manager lifecycle |
| `fleet/pkg/controllers/csregistration/controller.go` | CS provision shard sync |
| `fleet/pkg/controllers/maestroregistration/controller.go` | Maestro consumer sync |
| `fleet/pkg/controllers/lifecycle/controller.go` | Ready condition aggregation |
| `internal/controllerutils/cooldown.go` | Shared cooldown gate |
| `internal/database/informers/fleet_informers.go` | Stamp + MC informer factories |
| `internal/database/fleet_client.go` | FleetDBClient interface |
| `internal/ocm/client.go` | ClusterServiceClientSpec (provision shard CRUD) |
| `kube-applier/pkg/controllers/apply_desire/controller.go` | Reference pattern for controller structure |

## Verification

1. **Unit tests**: Each controller has tabular `SyncOnce` tests covering:
   - Happy path (approved stamp, successful registration)
   - Unapproved stamp (skip with condition reason)
   - CS/Maestro API errors (condition set to False, requeue)
   - Missing provision shard recovery (list + match by AKS ID)
   - Lifecycle aggregation (both present, one missing, both True, mixed)
2. **Integration**: Run `fleet controller` against a dev environment CosmosDB with pre-registered stamps/MCs
3. **Migration safety**: Verify existing MCs with Phase 1 `Ready=True` retain their `Ready` condition until both sub-conditions are set

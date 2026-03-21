# HCP Recovery Controller Logic

## Overview

The controller reconciles `HCPRecovery` custom resources to orchestrate disaster recovery of Hosted Control Planes. It follows a **sequential step pipeline** pattern where each reconciliation loop executes **at most one mutating action**. Completed steps are skipped on subsequent reconciliations via status condition checks.

## Recovery Pipeline

| Step | Condition | Action |
|------|-----------|--------|
| 1. `validateBackup` | `BackupValidated` | Verifies Velero Backup exists, belongs to the right cluster, and is `Completed` |
| 2. `pauseHostedCluster` | `HostedClusterPaused` | Patches the HostedCluster's `spec.pausedUntil=true` to stop HyperShift operator interference |
| 3. `deleteHcpNamespace` | `HCPNamespaceDeleted` | Deletes the HCP namespace (`{hc.namespace}-{hc.name}`) for a clean restore |
| 4. `removeCloudResourcesFinalizers` | `CloudFinalizersRemoved` | Strips finalizers from cloud resources (CAPI/CAPZ types) to unblock namespace termination |
| 5. `removeDeploymentResourceFinalizers` | `DeploymentFinalizersRemoved` | Strips finalizers from deployment resources (cluster-api, capi-provider) after replicas reach zero |
| 6. `waitForNamespaceDeletion` | `NamespaceFullyRemoved` | Polls until the namespace is fully gone |
| 7. `createVeleroRestore` | `VeleroRestoreCompleted` | Creates a Velero Restore CR and monitors it to completion |

### Not Yet Implemented

The CRD defines additional conditions for future steps:

| Condition | Purpose |
|-----------|---------|
| `CAPIMachinesBackedUp` | Capture CAPI Machine state before namespace deletion |
| `CAPIMachinesReconciled` | Reconcile machines after Velero restore completes |
| `ManagedByReconciled` | Reconcile managed-by labels and ownership references |
| `HostedClusterUnpaused` | Unpause the HostedCluster after successful restore |
| `HealthChecked` | Post-restore health checks confirming HCP is operational |

## Error Handling

Three strategies are used depending on the nature of the error:

| Strategy | Behavior | Example |
|----------|----------|---------|
| **Permanent** | Sets a `False` condition, no active requeue (waits for relist) | Backup not found, cluster mismatch |
| **Retryable** | Sets a `False` condition if changed, otherwise requeues with rate limiting | API errors, backup not yet completed |
| **Transient** | Requeues with rate limiting immediately (no condition update) | Unexpected API failures |

## Reconciliation Flow Diagram

```mermaid
flowchart TD
    Start([HCPRecovery CR Created/Updated]) --> Informer[Informer detects change]
    Informer --> Enqueue[Enqueue to workqueue]
    Enqueue --> Process[processNextWorkItem]

    Process --> NotFound{CR still exists?}
    NotFound -->|No| Done([Done - no requeue])
    NotFound -->|Yes| Sync[syncRecovery]

    Sync --> Steps[process - sequential step pipeline]

    Steps --> S1[Step 1: validateBackup]

    S1 --> S1_Get{Get Velero Backup<br/>by spec.backupId}
    S1_Get -->|Not Found| S1_Perm[Permanent Error:<br/>BackupNotFound]
    S1_Get -->|API Error| S1_Retry[Retryable Error:<br/>BackupRetrievalError]
    S1_Get -->|Found| S1_Cluster{Label api.openshift.com/id<br/>matches spec.clusterId?}
    S1_Cluster -->|No| S1_Mismatch[Permanent Error:<br/>BackupClusterMismatch]
    S1_Cluster -->|Yes| S1_Phase{Backup phase<br/>== Completed?}
    S1_Phase -->|No| S1_NotReady[Retryable Error:<br/>BackupNotCompleted]
    S1_Phase -->|Yes| S1_Done[Set condition:<br/>BackupValidated=True]

    S1_Done --> S2[Step 2: pauseHostedCluster]

    S2 --> S2_Already{Condition<br/>HostedClusterPaused<br/>== True?}
    S2_Already -->|Yes| S3
    S2_Already -->|No| S2_GetHC{Get HostedCluster<br/>by clusterId label}
    S2_GetHC -->|API Error| S2_Retry[Retryable Error]
    S2_GetHC -->|Not Found| S2_Perm[Permanent Error:<br/>HostedClusterNotFound]
    S2_GetHC -->|Found| S2_Paused{HC already paused?<br/>spec.pausedUntil == true}
    S2_Paused -->|Yes| S2_Cond[Set condition:<br/>HostedClusterPaused=True]
    S2_Paused -->|No| S2_Patch[Patch HC:<br/>set pausedUntil=true<br/>add hcp-recovery annotations]

    S2_Cond --> S3
    S2_Patch --> Requeue([Requeue - action executed])

    S3[Step 3: deleteHcpNamespace]

    S3 --> S3_Skip{Condition<br/>NamespaceFullyRemoved=True<br/>OR HCPNamespaceDeleted=True?}
    S3_Skip -->|Yes| S4
    S3_Skip -->|No| S3_GetHC{Get HostedCluster}
    S3_GetHC -->|API Error| S3_Retry[Retryable Error]
    S3_GetHC -->|Not Found| S3_SkipCond[Set condition:<br/>HCPNamespaceDeleted=True]
    S3_GetHC -->|Found| S3_NS{Get namespace<br/>HC.namespace-HC.name}
    S3_NS -->|Not Found| S3_SkipCond
    S3_NS -->|API Error| S3_RetryNS[Retryable Error]
    S3_NS -->|Terminating| S3_SkipCond
    S3_NS -->|Active| S3_Del[Delete namespace]

    S3_SkipCond --> S4
    S3_Del --> Requeue

    S4[Step 4: removeCloudResourcesFinalizers]

    S4 --> S4_Skip{Condition<br/>CloudFinalizersRemoved<br/>== True?}
    S4_Skip -->|Yes| S4b
    S4_Skip -->|No| S4_GetHC{Get HostedCluster}
    S4_GetHC -->|Not Found| S4_SkipCond[Set condition:<br/>CloudFinalizersRemoved=True]
    S4_GetHC -->|API Error| S4_Retry[Retryable Error]
    S4_GetHC -->|Found| S4_NS{Get namespace}
    S4_NS -->|Not Found| S4_SkipCond
    S4_NS -->|Not Terminating| S4_Perm[Permanent Error:<br/>NamespaceNotTerminating]
    S4_NS -->|Terminating| S4_Collect[Collect finalizers from:<br/>AzureMachines, Machines,<br/>MachineSets, MachineDeployments,<br/>HostedControlPlanes, Clusters]
    S4_Collect --> S4_Any{Finalizers found?}
    S4_Any -->|Yes| S4_Remove[Patch: remove cloud finalizers]
    S4_Any -->|No| S4_SkipCond

    S4b[Step 5: removeDeploymentResourceFinalizers]

    S4b --> S4b_Skip{Condition<br/>DeploymentFinalizersRemoved<br/>== True?}
    S4b_Skip -->|Yes| S5
    S4b_Skip -->|No| S4b_GetHC{Get HostedCluster}
    S4b_GetHC -->|Not Found| S4b_SkipCond[Set condition:<br/>DeploymentFinalizersRemoved=True]
    S4b_GetHC -->|API Error| S4b_Retry[Retryable Error]
    S4b_GetHC -->|Found| S4b_NS{Get namespace}
    S4b_NS -->|Not Found| S4b_SkipCond
    S4b_NS -->|Not Terminating| S4b_Perm[Permanent Error:<br/>NamespaceNotTerminating]
    S4b_NS -->|Terminating| S4b_Collect[Collect finalizers from:<br/>cluster-api, capi-provider<br/>deployments (replicas==0)]
    S4b_Collect --> S4b_Any{Finalizers found?}
    S4b_Any -->|Yes| S4b_Remove[Patch: remove deployment finalizers]
    S4b_Any -->|No| S4b_SkipCond

    S4_SkipCond --> S5
    S4_Remove --> Requeue

    S5[Step 5: waitForNamespaceDeletion]

    S5 --> S5_GetHC{Get HostedCluster}
    S5_GetHC -->|Not Found| S5_Done[Set condition:<br/>NamespaceFullyRemoved=True]
    S5_GetHC -->|API Error| S5_Retry[Retryable Error]
    S5_GetHC -->|Found| S5_NS{Namespace still exists?}
    S5_NS -->|Not Found| S5_Done
    S5_NS -->|Exists| S5_Wait[Retryable Error:<br/>NamespaceStillExists<br/>- requeue and wait]

    S5_Done --> S6

    S6[Step 6: createVeleroRestore]

    S6 --> S6_Get{Get existing Velero Restore<br/>restore-RECOVERY_NAME}
    S6_Get -->|API Error| S6_Transient[Transient Error - requeue]
    S6_Get -->|Exists| S6_Phase{Restore phase?}
    S6_Phase -->|Completed| S6_Done[Set condition:<br/>VeleroRestoreCompleted=True]
    S6_Phase -->|Failed/PartiallyFailed/<br/>FailedValidation| S6_Perm[Permanent Error:<br/>RestoreFailed]
    S6_Phase -->|In Progress| S6_Wait[Retryable Error:<br/>RestoreInProgress]
    S6_Get -->|Not Found| S6_Create[Create Velero Restore<br/>from spec.backupId<br/>with PV restore + excluded resources]

    S6_Done --> Complete([Recovery Complete])
    S6_Create --> Requeue

    S1_Perm --> Requeue
    S1_Retry --> Requeue
    S1_Mismatch --> Requeue
    S1_NotReady --> Requeue
    S2_Retry --> Requeue
    S2_Perm --> Requeue
    S3_Retry --> Requeue
    S3_RetryNS --> Requeue
    S4_Retry --> Requeue
    S4_Perm --> Requeue
    S5_Retry --> Requeue
    S5_Wait --> Requeue
    S6_Transient --> Requeue
    S6_Perm --> Requeue
    S6_Wait --> Requeue

    style Start fill:#4CAF50,color:white
    style Complete fill:#4CAF50,color:white
    style Requeue fill:#FF9800,color:white
    style S1_Perm fill:#f44336,color:white
    style S1_Mismatch fill:#f44336,color:white
    style S2_Perm fill:#f44336,color:white
    style S4_Perm fill:#f44336,color:white
    style S6_Perm fill:#f44336,color:white
```

## Action Model

Each reconciliation produces at most one `actions` struct. The `validate()` method panics if more than one action field is set. Supported action types:

| Action | Description |
|--------|-------------|
| `StatusUpdate` | Server-side apply to the HCPRecovery status subresource |
| `PatchHostedCluster` | Merge-patch on a HostedCluster (used for pausing) |
| `DeleteHcpNamespace` | Delete a namespace by name |
| `RemoveFinalizers` | Batch merge-patch to strip finalizers from multiple resources |
| `CreateVeleroRestore` | Create a new Velero Restore CR |

## Velero Restore Configuration

The Velero Restore is created with:
- **Name**: `restore-{recoveryName}`
- **Namespace**: `velero`
- **Backup**: from `spec.backupId`
- **RestorePVs**: `true`
- **ExistingResourcePolicy**: `update`
- **ItemOperationTimeout**: `4h`
- **Excluded resources**: `nodes`, `events`, `events.events.k8s.io`, `backups.velero.io`, `restores.velero.io`, `resticrepositories.velero.io`

## Finalizer Removal Targets

When the HCP namespace is terminating, finalizers are stripped from:
- `Deployments`
- `AzureMachines`
- `Machines`
- `MachineSets`
- `MachineDeployments`
- `HostedControlPlanes`
- `Clusters`
- Named deployments `cluster-api` and `capi-provider` (only when replicas == 0)
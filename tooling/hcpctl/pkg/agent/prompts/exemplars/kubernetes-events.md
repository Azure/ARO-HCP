This exemplar demonstrates how to use Kubernetes API Event logs when investigating control plane failures. Snapshot
output already includes a summarized `controlPlaneEvents` view; write ad-hoc queries when you need to filter to one
component or reason.

**Note:** For a complete end-to-end RCA workflow combining snapshot conditions, Kubernetes events, and mgmt-agent
snapshots, see [cluster-creation-timeout-z-stream-upgrade.md](cluster-creation-timeout-z-stream-upgrade.md). That
exemplar shows the full investigative flow from test timeout through multiple "Why" layers to root cause.

---

# Root Cause

An ARO HCP cluster create may stall while a control plane pod (for example `etcd-0`) cannot mount storage or schedule.
`hostedClusterConditions` and operator logs show symptoms; Kubernetes Events in Kusto often carry the direct
`reason`/`message` (for example `FailedMount`).

## Summary

The `kube-events` collector on each service and management cluster ingests Kubernetes **Event** objects into
**`ServiceLogs.kubernetesEvents`**. Scope with `cluster`, `eventNamespace` (hosted control plane namespace on the
management cluster), `objectKind`, `objectName`, `reason`, and `message`. Snapshot **`controlPlaneEvents`** runs the
same table with aggregation — use it first, then narrow with ad-hoc KQL. These events are not mgmt-agent logs; use
[mgmt-agent-snapshots.md](mgmt-agent-snapshots.md) for container waiting/termination timelines when Events are
inconclusive.

## Recursive 'Why' Chain

### Why is an etcd pod not becoming ready?

Start from snapshot `controlPlaneEvents` or `hostedClusterConditions` `Degraded.message`. If the summary points at etcd
but lacks detail, filter events to that pod in the hosted control plane namespace.

#### Proof 1: Log Snippet

We can match the snapshot `controlPlaneEvents` shape and filter to one pod:

```kql
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
| where timestamp between (datetime(2026-05-19T20:52:10Z) .. datetime(2026-05-19T22:10:46Z))
| where cluster == 'int-uksouth-mgmt-1'
| where eventNamespace == 'ocm-arohcpint-<cid>-<hosted-cluster-name>'
    and objectName startswith 'etcd-'
| extend firstSeen = coalesce(firstSeen, todatetime(log.event_time)), lastSeen = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, firstSeen, lastSeen) by objectKind, objectName, reason, message
| project objectKind, objectName, reason, message, firstSeen, lastSeen, count
| order by firstSeen asc
```

| objectKind | objectName | reason                 | message                                                                                                      | firstSeen                | lastSeen                 | count |
|------------|------------|------------------------|--------------------------------------------------------------------------------------------------------------|--------------------------|--------------------------|-------|
| Pod        | etcd-0     | FailedMount            | MountVolume.MountDevice failed for volume "pvc-…" : rpc error: code = Internal desc = failed to find disk … | 2026-05-19T21:01:33.000Z | 2026-05-19T21:22:32.000Z | 18    |
| Pod        | etcd-1     | SuccessfulAttachVolume | AttachVolume.Attach succeeded for volume "pvc-…"                                                             | 2026-05-19T21:01:31.000Z | 2026-05-19T21:01:31.000Z | 1     |

High `count` on the same `reason`/`message` indicates a sustained failure. Volume- or mount-related filters
(`reason contains 'Volume' or message contains 'Volume'`) help when several replicas share a namespace.

#### Proof 2: Log Snippet

For infrastructure tied to a specific node, filter by `objectKind == 'Node'` and correlate with pod scheduling events on
the same node name embedded in the `Scheduled` message.

Replace `<cid>`, `<hosted-cluster-name>`, time bounds, and `cluster` with values from the snapshot `manifest.json`
(`hosted_control_plane_namespace`). A full end-to-end RCA using these events is in `cluster-installation-azure-disk-failure.md`.

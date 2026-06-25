This document shows how to investigate a watched custom resource using mgmt-agent **resource event** logs when snapshot
conditions and Kubernetes events are not enough — using `PodNetworkInstance` (PNI) for Swift networking as the example.

# Root Cause

An ARO HCP cluster create with Swift networking may stall while `PodNetworkInstance` never reaches `Ready=True`, leaving
kube-apiserver pods waiting for network attachment. Snapshot conditions and `controlPlaneEvents` show symptoms; mgmt-agent
ResourceWatcher logs carry the PNI object's condition timeline — not pre-canned in the snapshot because output volume is
unbounded.

## Summary

mgmt-agent **ResourceWatcher** logs `resource event` with the full CR in `log.object` for API groups including
`multitenancy.acn.azure.com`. Query `ServiceLogs.containerLogs` where `container_name == 'mgmt-agent-controller'` and
`tostring(log.msg) == 'resource event'`, filtered by `log.gvr`, `log.namespace`, and `log.name`. Scope `log.namespace` to
`hosted_control_plane_namespace` from snapshot `manifest.json` — the same namespace `controlPlaneOperatorLogs` uses
(`HostedControlPlaneLogs.containerLogs`, `namespace_name`).

mgmt-agent also runs **PodWatcher** (`log.msg == 'pod event'`) for pod lifecycle; filter `log.name` to a control plane pod
prefix when container waiting/termination detail is needed. PodWatcher only logs when a container's state *type* changes.

After snapshot **conditions** and **events** (see [kubernetes-events.md](kubernetes-events.md)), write scoped ad-hoc queries.

## Recursive 'Why' Chain

### Why is Swift networking not ready?

PNI lives in the hosted control plane namespace alongside control plane pods. Filter ResourceWatcher events to
`podnetworkinstances` in that namespace.

#### Proof 1: Log Snippet

We can trace PNI `Ready` condition transitions over time:

```kql
let hcpNamespace = 'ocm-arohcpint-<cid>-<hosted-cluster-name>'; // manifest hosted_control_plane_namespace
cluster('https://hcp-int-uk.uksouth.kusto.windows.net').database('ServiceLogs').table('containerLogs')
| where timestamp between (datetime(2026-06-24T14:00:00Z) .. datetime(2026-06-24T15:00:00Z))
| where cluster == 'int-uksouth-mgmt-1'
| where container_name == 'mgmt-agent-controller'
| where tostring(log.msg) == 'resource event'
| where tostring(log.gvr) has 'podnetworkinstances'
| where tostring(log.namespace) == hcpNamespace
| project timestamp, event=tostring(log.event), name=tostring(log.name),
    ready=tostring(log.object.status.conditions[?type == 'Ready'][0].status),
    reason=tostring(log.object.status.conditions[?type == 'Ready'][0].reason),
    message=tostring(log.object.status.conditions[?type == 'Ready'][0].message)
| order by timestamp asc
```

| timestamp                | event  | name        | ready | reason    | message                        |
|--------------------------|--------|-------------|-------|-----------|--------------------------------|
| 2026-06-24T14:05:00.000Z | Add    | cluster-pni | False | Pending   | Waiting for network attachment |
| 2026-06-24T14:12:30.000Z | Update | cluster-pni | True  | AsExpected|                                |

Replace time bounds, `cluster`, and `hcpNamespace` with values from snapshot `manifest.json` (`hosted_control_plane_namespace`).
For other watched CRs, keep the same query shape and change the `log.gvr` filter (for example `hostedclusters` under
`hypershift.openshift.io`).

This document shows a proof chain for an HCP cluster creation timeout during a z-stream upgrade test.

# Root Cause

The test timed out because the RP backend would not complete cluster creation while ARO-HCP had already triggered a
z-stream upgrade from 4.22.0 to 4.22.2 and `controlPlaneVersion.history` still had no `Completed` entry. Clusters Service
reached `ready` at **17:55:16Z** and cluster-service operation status flipped to `Succeeded` at **17:55:56Z**, but the
backend kept reporting *"hosted cluster has no installed version"* through the test's 20-minute create wait, which expired at **18:04:41.993Z**.
New kube-apiserver ReplicaSet `54b7f655f6` pods logged `etcd-client` dial failures while etcd quorum stayed healthy
(`EtcdAvailable=True`).

## Summary

[Prow job `pull-ci-Azure-ARO-HCP-main-e2e-parallel` #2069828891979026432](https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/5771/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2069828891979026432).
The failing spec was **Service Provider should upgrade the control plane z-stream automatically on behalf of the customer for 4.22**
(`test/e2e/control_plane_automated_z_stream_upgrade.go`). Cluster `rg-zstream-upgrade-4-22-d84tcz` / `cluster-zstream-4-22-js5rwv` (CS id `2r4sj32f75bfmfa9m6rkupe7o982hqst`,
HostedCluster `p2d8v9o7y9g8x5o` in `ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst`). The test started HCP cluster creation at
**17:44:41.986Z** (version 4.22.0, candidate channel) and failed when `CreateHCPClusterFromParam20240610` hit its 20-minute
budget at **18:04:41.993Z**. The RP backend `operationclustercreate` reconcile had been in `Provisioning` since **17:45:13Z**.

Early backend picks reported missing cached state (`.api.url is empty`, `ReadDesire has no kube content`), then
`ComponentsNotAvailable` while the control plane rolled out. From **17:54:07Z** the blocking message became *"hosted cluster
has no installed version"* — after CS `ready` but before any `Completed` version entry. `force-upgrade-to` 4.22.2 appeared at
**17:55:20Z** while 4.22.0 was still `Partial`; dual-`Partial` history (4.22.0 + 4.22.2) persisted through teardown.

## Recursive 'Why' Chain

### Why did the test time out?

The **Service Provider should upgrade the control plane z-stream automatically on behalf of the customer for 4.22** spec
never finished cluster creation; the 20-minute create wait expired while the backend was still `Provisioning`:

#### Proof 1: Test Log

```
"ts"="2026-06-24 17:44:41.986256" "msg"="Starting HCP cluster creation" "clusterName"="cluster-zstream-4-22-js5rwv" "resourceGroup"="rg-zstream-upgrade-4-22-d84tcz" "version"="4.22.0" "channelGroup"="candidate"
  [FAILED] in [It] - .../test/e2e/control_plane_automated_z_stream_upgrade.go:102 @ 06/24/26 18:04:41.993
"ts"="2026-06-24 18:04:41.993645" "msg"="===== TEST CASE ENDED: FAILURE ====="
```

#### Proof 2: Log Snippet

```kql
// manifest.json: resource_group
let resourceGroup = 'rg-zstream-upgrade-4-22-d84tcz';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('backendLogs')
| where resource_group == resourceGroup
| where log.controller_name == 'operationclustercreate'
| where tostring(log.msg) == 'picked cluster create operation status'
| summarize firstSeen = min(timestamp), lastSeen = max(timestamp), samples = count()
    by message = tostring(log.message), provisioningState = tostring(log.provisioningState)
| order by firstSeen asc
```

| firstSeen                | lastSeen                 | samples | provisioningState | message (abbrev.)                                                                                    |
|--------------------------|--------------------------|---------|-------------------|------------------------------------------------------------------------------------------------------|
| 2026-06-24T17:45:13.156Z | 2026-06-24T17:45:23.149Z | 2       | Provisioning      | cluster state not cached yet; hosted cluster state not cached yet                                    |
| 2026-06-24T17:45:54.110Z | 2026-06-24T17:46:58.398Z | 4       | Provisioning      | .api.url is empty; hosted cluster state not cached yet                                               |
| 2026-06-24T17:47:56.279Z | 2026-06-24T17:50:01.056Z | 6       | Provisioning      | .api.url is empty; ReadDesire has no kube content                                                   |
| 2026-06-24T17:50:47.812Z | 2026-06-24T17:50:57.825Z | 2       | Provisioning      | hosted cluster is not available: KubeconfigWaitingForCreate … capi-provider has 1 unavailable replicas |
| 2026-06-24T17:51:42.360Z | 2026-06-24T17:52:40.821Z | 4       | Provisioning      | hosted cluster is not available: ComponentsNotAvailable … (30+ operators); kas/capi unavailable replicas |
| 2026-06-24T17:53:13.384Z | 2026-06-24T17:53:23.385Z | 2       | Provisioning      | hosted cluster is not available: ComponentsNotAvailable … (12 operators); 14 deployments unavailable |
| 2026-06-24T17:54:07.996Z | 2026-06-24T17:54:18.027Z | 2       | Provisioning      | hosted cluster has no installed version; kube-controller-manager has 1 unavailable replicas          |
| 2026-06-24T17:54:54.717Z | 2026-06-24T17:59:25.773Z | 23      | Provisioning      | hosted cluster has no installed version                                                            |
| 2026-06-24T17:59:56.809Z | 2026-06-24T18:04:21.070Z | 13      | Provisioning      | hosted cluster has no installed version; kube-apiserver has 1 unavailable replicas                   |

### Why was there no installed version, and why did kube-apiserver have 1 unavailable replica?

Clusters Service reached `ready` at **17:55:16Z** while the ARM create was still in flight, so ARO-HCP's automated z-stream upgrade to 4.22.2 was triggered at **17:55:20Z**. A `Completed` entry in `controlPlaneVersion.history` requires every control plane component to be ready; during the automated z-stream upgrade rollout none reached that state, so the backend kept reporting *hosted cluster has no installed version* (from **17:54:07Z**). From **17:59:56Z** that message was paired with *kube-apiserver deployment has 1 unavailable replicas* — kube-apiserver on the new ReplicaSet never became ready before the create timeout:

#### Proof 1: Log Snippet

```kql
// manifest.json: hosted_cluster_namespace
let hcNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst';
// manifest.json: hosted_control_plane_namespace (name suffix)
let hcName = 'p2d8v9o7y9g8x5o';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('containerLogs')
// manifest.json: time_window.start .. time_window.end
| where timestamp between (datetime(2026-06-24T17:43:34Z) .. datetime(2026-06-24T18:49:43Z))
| where container_name == 'mgmt-agent-controller'
| where tostring(log.msg) == 'resource event'
| where tostring(log.object.kind) == 'HostedCluster'
| where tostring(log.namespace) == hcNamespace
| where tostring(log.name) == hcName
| extend desired = tostring(log.object.spec.release.image),
         versionHistory = tostring(log.object.status.controlPlaneVersion.history),
         forceUpgradeTo = tostring(log.object.metadata.annotations['hypershift.openshift.io/force-upgrade-to'])
| extend desired = replace_regex(desired, @'sha256:[a-f0-9]{64}', 'sha256:…'),
         forceUpgradeTo = replace_regex(forceUpgradeTo, @'sha256:[a-f0-9]{64}', 'sha256:…'),
         versionHistory = replace_regex(versionHistory, @'sha256:[a-f0-9]{64}', 'sha256:…')
| summarize first_occurrence = min(timestamp), last_occurrence = max(timestamp),
            event = arg_min(timestamp, tostring(log.event)),
            desired = arg_min(timestamp, desired),
            forceUpgradeTo = max(forceUpgradeTo)
    by versionHistory
| project first_occurrence, last_occurrence, event, desired, forceUpgradeTo, versionHistory
| order by first_occurrence asc
```

| first_occurrence         | last_occurrence          | event | desired | forceUpgradeTo                                     | versionHistory |
|--------------------------|--------------------------|-------|---------|----------------------------------------------------|----------------|
| 2026-06-24T17:49:05.714Z | 2026-06-24T17:49:10.292Z |       |         |                                                    |                |
| 2026-06-24T17:49:21.097Z | 2026-06-24T17:49:53.026Z |       |         |                                                    | [{"state":"Partial","version":"4.22.0","image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","startedTime":"2026-06-24T17:49:20.0000000Z"}] |
| 2026-06-24T17:50:36.094Z | 2026-06-24T17:53:43.409Z |       |         |                                                    | [{"image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","state":"Partial","version":"4.22.0","startedTime":"2026-06-24T17:49:20.0000000Z"}] |
| 2026-06-24T17:54:13.288Z | 2026-06-24T17:54:13.288Z |       |         |                                                    | [{"version":"4.22.0","image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","state":"Partial","startedTime":"2026-06-24T17:49:20.0000000Z"}] |
| 2026-06-24T17:55:20.312Z | 2026-06-24T17:55:20.642Z |       |         | quay.io/openshift-release-dev/ocp-release@sha256:… | [{"version":"4.22.0","image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","startedTime":"2026-06-24T17:49:20.0000000Z","state":"Partial"}] |
| 2026-06-24T17:56:15.160Z | 2026-06-24T17:58:59.295Z |       |         | quay.io/openshift-release-dev/ocp-release@sha256:… | [{"version":"4.22.2","image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","startedTime":"2026-06-24T17:56:15.0000000Z","state":"Partial"},{"version":"4.22.0","image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","startedTime":"2026-06-24T17:49:20.0000000Z","state":"Partial","completionTime":"2026-06-24T17:56:15.0000000Z"}] |
| 2026-06-24T18:04:19.743Z | 2026-06-24T18:05:11.198Z |       |         | quay.io/openshift-release-dev/ocp-release@sha256:… | [{"image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","version":"4.22.2","startedTime":"2026-06-24T17:56:15.0000000Z","state":"Partial"},{"image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","version":"4.22.0","startedTime":"2026-06-24T17:49:20.0000000Z","state":"Partial","completionTime":"2026-06-24T17:56:15.0000000Z"}] |
| 2026-06-24T18:06:14.719Z | 2026-06-24T18:09:17.779Z |       |         | quay.io/openshift-release-dev/ocp-release@sha256:… | [{"state":"Partial","version":"4.22.2","image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","startedTime":"2026-06-24T17:56:15.0000000Z"},{"state":"Partial","version":"4.22.0","image":"arohcpocpdev.azurecr.io/openshift-release-dev/ocp-release@sha256:…","startedTime":"2026-06-24T17:49:20.0000000Z","completionTime":"2026-06-24T17:56:15.0000000Z"}] |

### Why did the upgrade leave kube-apiserver with 1 unavailable replica?

The new kube-apiserver ReplicaSet never became fully ready during the forced-upgrade rollout:

#### Proof 1: Log Snippet

```kql
// clustersService/phases gathered output: CS cluster reached ready
let csReadyAt = datetime(2026-06-24T17:55:16Z);
// manifest.json: time_window subset through test failure
let probeEnd = datetime(2026-06-24T18:05:00Z);
// manifest.json: hosted_cluster_namespace
let hcNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst';
// manifest.json: hosted_control_plane_namespace (name suffix)
let hcName = 'p2d8v9o7y9g8x5o';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('containerLogs')
// manifest.json: time_window subset (CS ready .. test failure)
| where timestamp between (csReadyAt .. probeEnd)
| where container_name == 'mgmt-agent-controller'
| where tostring(log.msg) == 'resource event'
| where tostring(log.object.kind) == 'HostedCluster'
| where tostring(log.namespace) == hcNamespace
| where tostring(log.name) == hcName
| extend conditions = log.object.status.conditions
| mv-expand condition = conditions
| extend type = tostring(condition.type),
         status = tostring(condition.status),
         reason = tostring(condition.reason),
         message = tostring(condition.message)
| where type in ('Available', 'Degraded', 'EtcdAvailable', 'KubeAPIServerAvailable')
| summarize first_occurrence = min(timestamp), last_occurrence = max(timestamp), samples = count()
    by type, status, reason, message
| order by type asc, first_occurrence asc
```

| type                   | status | reason              | message (abbrev.)                                                                                  | first_occurrence         | last_occurrence          | samples |
|------------------------|--------|---------------------|----------------------------------------------------------------------------------------------------|--------------------------|--------------------------|---------|
| Available              | True   | AsExpected          | The hosted control plane is available                                                              | 2026-06-24T17:55:20.312Z | 2026-06-24T18:04:36.087Z | 11      |
| Degraded               | False  | AsExpected          | The hosted cluster is not degraded                                                                 | 2026-06-24T17:55:20.312Z | 2026-06-24T18:04:19.743Z | 6       |
| Degraded               | True   | UnavailableReplicas | kube-apiserver deployment has 1 unavailable replicas                                               | 2026-06-24T17:58:59.295Z | 2026-06-24T17:58:59.295Z | 1       |
| Degraded               | True   | UnavailableReplicas | [catalog-operator, ignition-server, konnectivity-agent, kube-controller-manager, kube-scheduler, machine-approver, olm-operator, openshift-apiserver, packageserver] — 1 unavailable replica each | 2026-06-24T18:04:21.289Z | 2026-06-24T18:04:21.289Z | 1       |
| Degraded               | True   | UnavailableReplicas | [azure-cloud-controller-manager, catalog-operator, control-plane-pki-operator, hosted-cluster-config-operator, ignition-server, konnectivity-agent, kube-controller-manager, kube-scheduler, machine-approver, olm-operator, openshift-apiserver, packageserver] — 1 each | 2026-06-24T18:04:22.552Z | 2026-06-24T18:04:22.552Z | 1       |
| Degraded               | True   | UnavailableReplicas | [konnectivity-agent, openshift-apiserver, packageserver] — 1 unavailable replica each                | 2026-06-24T18:04:33.614Z | 2026-06-24T18:04:33.614Z | 1       |
| Degraded               | True   | UnavailableReplicas | [ignition-server-proxy, konnectivity-agent, openshift-apiserver, packageserver] — 1 each           | 2026-06-24T18:04:36.087Z | 2026-06-24T18:04:36.087Z | 1       |
| EtcdAvailable          | True   | QuorumAvailable     |                                                                                                    | 2026-06-24T17:55:20.312Z | 2026-06-24T18:04:36.087Z | 11      |
| KubeAPIServerAvailable | True   | AsExpected          | Kube APIServer deployment is available                                                             | 2026-06-24T17:55:20.312Z | 2026-06-24T18:04:36.087Z | 11      |

### Why was kube-apiserver unavailable?

kube-apiserver pods on the new ReplicaSet `54b7f655f6` cold-started while old RS `dd7d46b5d` pods drained; the `kube-apiserver` container entered `running` but stayed `ready=false` for the rest of the window—**~4m 4s** on the first pod (`tn2dc`, **18:00:38Z** through test failure **18:04:41Z**; `etcd-client` dial errors on that pod continued to **18:04:52Z**). Later new-RS pods (`qmpf7`, `8kgd8`) also never flipped `ready=true` before teardown:

#### Proof 1: Log Snippet

```kql
// manifest.json: hosted_control_plane_namespace
let hcpNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('containerLogs')
// manifest.json: time_window subset (upgrade rollout window)
| where timestamp between (datetime(2026-06-24T17:55:00Z) .. datetime(2026-06-24T18:05:00Z))
| where container_name == 'mgmt-agent-controller'
| where tostring(log.msg) == 'pod event'
| where tostring(log.namespace) == hcpNamespace
| where tostring(log.name) startswith 'kube-apiserver-'
| extend containers = log.object.status.containerStatuses
| mv-expand container = containers
| project timestamp,
    event=tostring(log.event),
    podName=tostring(log.name),
    containerName=tostring(container.name),
    ready=tobool(container.ready),
    reason=coalesce(
        tostring(container.state.waiting.reason),
        tostring(container.state.terminated.reason),
        iff(isnotempty(container.state.running), 'Running', '')
    )
| where containerName == 'kube-apiserver' or (event in ('Add', 'Delete') and isempty(containerName))
| summarize firstSeen = min(timestamp), lastSeen = max(timestamp), count = count()
    by event, podName, containerName, ready, reason
| order by firstSeen asc
```

| firstSeen                | lastSeen                 | count | event  | podName                        | containerName  | ready | reason           |
|--------------------------|--------------------------|-------|--------|--------------------------------|----------------|-------|------------------|
| 2026-06-24T17:58:48.375Z | 2026-06-24T17:58:48.375Z | 1     | Add    | kube-apiserver-54b7f655f6-tn2dc |                |       |                  |
| 2026-06-24T18:00:25.672Z | 2026-06-24T18:00:25.672Z | 1     | Update | kube-apiserver-dd7d46b5d-2pl4k | kube-apiserver | false | Completed        |
| 2026-06-24T18:00:25.807Z | 2026-06-24T18:00:33.891Z | 5     | Update | kube-apiserver-54b7f655f6-tn2dc | kube-apiserver | false | PodInitializing  |
| 2026-06-24T18:00:26.005Z | 2026-06-24T18:00:26.005Z | 1     | Delete | kube-apiserver-dd7d46b5d-2pl4k | kube-apiserver | false | Completed        |
| 2026-06-24T18:00:38.033Z | 2026-06-24T18:00:38.033Z | 1     | Update | kube-apiserver-54b7f655f6-tn2dc | kube-apiserver | false | Running          |
| 2026-06-24T18:00:47.823Z | 2026-06-24T18:00:47.823Z | 1     | Add    | kube-apiserver-54b7f655f6-qmpf7 |                |       |                  |
| 2026-06-24T18:02:00.778Z | 2026-06-24T18:02:00.778Z | 1     | Update | kube-apiserver-dd7d46b5d-dd96n | kube-apiserver | false | Completed        |
| 2026-06-24T18:02:00.900Z | 2026-06-24T18:02:16.601Z | 7     | Update | kube-apiserver-54b7f655f6-qmpf7 | kube-apiserver | false | PodInitializing  |
| 2026-06-24T18:02:01.566Z | 2026-06-24T18:02:01.566Z | 1     | Delete | kube-apiserver-dd7d46b5d-dd96n | kube-apiserver | false | Completed        |
| 2026-06-24T18:02:23.751Z | 2026-06-24T18:02:23.751Z | 1     | Update | kube-apiserver-54b7f655f6-qmpf7 | kube-apiserver | false | Running          |
| 2026-06-24T18:02:32.411Z | 2026-06-24T18:02:32.411Z | 1     | Add    | kube-apiserver-54b7f655f6-8kgd8 |                |       |                  |
| 2026-06-24T18:03:50.322Z | 2026-06-24T18:03:50.322Z | 1     | Update | kube-apiserver-dd7d46b5d-hpfdr | kube-apiserver | false | Completed        |
| 2026-06-24T18:03:50.389Z | 2026-06-24T18:03:50.389Z | 1     | Delete | kube-apiserver-dd7d46b5d-hpfdr | kube-apiserver | false | Completed        |
| 2026-06-24T18:03:52.589Z | 2026-06-24T18:03:54.542Z | 3     | Update | kube-apiserver-54b7f655f6-8kgd8 | kube-apiserver | false | PodInitializing  |
| 2026-06-24T18:03:55.540Z | 2026-06-24T18:03:55.540Z | 1     | Update | kube-apiserver-54b7f655f6-8kgd8 | kube-apiserver | false | Running          |

#### Proof 2: Log Snippet

New-RS `54b7f655f6` pods logged sustained `etcd-client` dial errors; prior RS `dd7d46b5d` logged fewer:

```kql
// manifest.json: hosted_control_plane_namespace
let hcpNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('HostedControlPlaneLogs').table('containerLogs')
// manifest.json: time_window subset (upgrade rollout window)
| where timestamp between (datetime(2026-06-24T17:56:00Z) .. datetime(2026-06-24T18:05:00Z))
| where namespace_name == hcpNamespace
| where pod_name has 'kube-apiserver'
| where container_name == 'kube-apiserver'
| extend logLine = tostring(log)
| where logLine has 'etcd-client' and logLine has 'Error while dialing'
| extend rsHash = extract(@"kube-apiserver-([^-]+)-", 1, pod_name)
| summarize dialErrors = count(), first_occurrence = min(timestamp), last_occurrence = max(timestamp)
    by pod_name, rsHash
| order by first_occurrence asc
```

| pod_name                         | rsHash     | dialErrors | first_occurrence         | last_occurrence          |
|----------------------------------|------------|------------|--------------------------|--------------------------|
| kube-apiserver-dd7d46b5d-2pl4k   | dd7d46b5d  | 11         | 2026-06-24T17:56:07.709Z | 2026-06-24T17:58:37.712Z |
| kube-apiserver-dd7d46b5d-dd96n   | dd7d46b5d  | 19         | 2026-06-24T17:56:16.845Z | 2026-06-24T18:00:46.839Z |
| kube-apiserver-dd7d46b5d-hpfdr   | dd7d46b5d  | 27         | 2026-06-24T17:56:26.071Z | 2026-06-24T18:02:27.235Z |
| kube-apiserver-54b7f655f6-tn2dc  | 54b7f655f6 | 185        | 2026-06-24T18:00:34.364Z | 2026-06-24T18:04:52.129Z |
| kube-apiserver-54b7f655f6-qmpf7  | 54b7f655f6 | 204        | 2026-06-24T18:02:17.320Z | 2026-06-24T18:04:57.173Z |
| kube-apiserver-54b7f655f6-8kgd8  | 54b7f655f6 | 109        | 2026-06-24T18:03:55.187Z | 2026-06-24T18:04:57.526Z |

#### Proof 3: Log Snippet

Kubernetes Events on the first new-RS pod show **~97s** of `FailedScheduling` before assignment (**17:58:48Z**–**18:00:25Z**), then readiness probe HTTP **403** failures after containers started:

```kql
// manifest.json: hosted_control_plane_namespace
let hcpNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o';
// pod event query (Proof 1): first new-RS kube-apiserver pod
let failingPod = 'kube-apiserver-54b7f655f6-tn2dc';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
// manifest.json: time_window subset (upgrade rollout window)
| where timestamp between (datetime(2026-06-24T17:55:00Z) .. datetime(2026-06-24T18:05:00Z))
| where eventNamespace == hcpNamespace
| where objectName == failingPod
| extend first_occurrence = coalesce(firstSeen, todatetime(log.event_time)),
         last_occurrence = coalesce(lastSeen, todatetime(log.event_time))
| summarize count = sum(count), first_occurrence = min(first_occurrence), last_occurrence = max(last_occurrence)
    by objectKind, reason, message
| order by first_occurrence asc
```

| objectKind | reason              | message (abbrev.)                                                                                         | count | first_occurrence         | last_occurrence          |
|------------|---------------------|-----------------------------------------------------------------------------------------------------------|-------|--------------------------|--------------------------|
| Pod        | FailedScheduling    | 0/24 nodes: anti-affinity / Too many pods / untolerated taints; preemptionPolicy=Never                    | 300   | 2026-06-24T17:58:48.000Z | 2026-06-24T17:59:03.000Z |
| Pod        | NotTriggerScaleUp   | pod didn't trigger scale-up: taints, anti-affinity                                                        | 1     | 2026-06-24T17:58:48.000Z | 2026-06-24T17:58:48.000Z |
| Pod        | FailedScheduling    | 0/24 nodes: Too many pods / anti-affinity / untolerated taints; preemptionPolicy=Never                    | 1     | 2026-06-24T17:58:49.000Z | 2026-06-24T17:58:49.000Z |
| Pod        | Scheduled           | Successfully assigned …/kube-apiserver-54b7f655f6-tn2dc to aks-userswft1-40825394-vmss000000              | 1     | 2026-06-24T18:00:25.000Z | 2026-06-24T18:00:25.000Z |
| Pod        | Pulling             | Pulling image …ocp-v4.0-art-dev@sha256:ea1f2137…                                                         | 1     | 2026-06-24T18:00:26.000Z | 2026-06-24T18:00:26.000Z |
| Pod        | Pulled              | Pulled …ea1f2137… in 3.098s (216559587 bytes)                                                           | 1     | 2026-06-24T18:00:29.000Z | 2026-06-24T18:00:29.000Z |
| Pod        | Created             | Container created                                                                                         | 7     | 2026-06-24T18:00:29.000Z | 2026-06-24T18:00:35.000Z |
| Pod        | Started             | Container started                                                                                         | 7     | 2026-06-24T18:00:29.000Z | 2026-06-24T18:00:35.000Z |
| Pod        | Pulling             | Pulling image …ocp-v4.0-art-dev@sha256:f4cac01f…                                                         | 1     | 2026-06-24T18:00:30.000Z | 2026-06-24T18:00:30.000Z |
| Pod        | Pulled              | Pulled …f4cac01f… in 668ms (95548069 bytes)                                                             | 1     | 2026-06-24T18:00:31.000Z | 2026-06-24T18:00:31.000Z |
| Pod        | Pulling             | Pulling image …ocp-v4.0-art-dev@sha256:74c3ddcb…                                                         | 1     | 2026-06-24T18:00:32.000Z | 2026-06-24T18:00:32.000Z |
| Pod        | Pulled              | Pulled …74c3ddcb… in 81ms (150965123 bytes)                                                             | 1     | 2026-06-24T18:00:32.000Z | 2026-06-24T18:00:32.000Z |
| Pod        | Pulled              | Image …ea1f2137… already present on machine                                                               | 1     | 2026-06-24T18:00:33.000Z | 2026-06-24T18:00:33.000Z |
| Pod        | Pulled              | Image …62f7b845… already present on machine                                                               | 1     | 2026-06-24T18:00:33.000Z | 2026-06-24T18:00:33.000Z |
| Pod        | Pulling             | Pulling image …ocp-v4.0-art-dev@sha256:42074680…                                                         | 1     | 2026-06-24T18:00:34.000Z | 2026-06-24T18:00:34.000Z |
| Pod        | Pulled              | Pulled …42074680… in 922ms (111342340 bytes)                                                            | 1     | 2026-06-24T18:00:35.000Z | 2026-06-24T18:00:35.000Z |
| Pod        | Pulled              | Image …74c3ddcb… already present on machine                                                               | 1     | 2026-06-24T18:00:35.000Z | 2026-06-24T18:00:35.000Z |
| Pod        | Unhealthy           | Readiness probe failed: HTTP probe failed with statuscode: 403                                            | 3     | 2026-06-24T18:00:42.000Z | 2026-06-24T18:00:42.000Z |

### Why was `/readyz` not passing?

kube-apiserver container logs during 17:56:00Z–18:05:00Z are dominated by `etcd-client` dial failures:

#### Proof 1: Log Snippet

```kql
// manifest.json: hosted_control_plane_namespace
let hcpNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o';
// pod event query (Proof 1): first new-RS kube-apiserver pod
let failingPod = 'kube-apiserver-54b7f655f6-tn2dc';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('HostedControlPlaneLogs').table('containerLogs')
// manifest.json: time_window subset (upgrade rollout window)
| where timestamp between (datetime(2026-06-24T17:56:00Z) .. datetime(2026-06-24T18:05:00Z))
| where namespace_name == hcpNamespace
| where pod_name == failingPod
| where container_name == 'kube-apiserver'
| extend logLine = tostring(log)
| where logLine !has 'FLAG:'
| extend failureSnippet = coalesce(
    extract(@'Error while dialing: ([^"\\]+)', 1, logLine),
    extract(@'Error while dialing: ([^"]+)"', 1, logLine),
    extract(@'"error":"((?:[^"\\]|\\.){0,240})', 1, logLine),
    iff(logLine has 'addrConn.createTransport' and logLine has 'failed to connect',
        'grpc: addrConn.createTransport failed to connect',
        ''),
    iff(logLine has 'retrying of unary invoker failed', 'etcd-client: retrying unary invoker failed', ''))
| where isnotempty(failureSnippet)
| summarize samples = count(), first_occurrence = min(timestamp), last_occurrence = max(timestamp)
    by failureSnippet
| order by samples desc
| take 15
```

| failureSnippet                                                                                                                                              | samples | first_occurrence         | last_occurrence          |
|-------------------------------------------------------------------------------------------------------------------------------------------------------------|---------|--------------------------|--------------------------|
| dial tcp: lookup etcd-client.ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o.svc: operation was canceled                                  | 185     | 2026-06-24T18:00:34.364Z | 2026-06-24T18:04:52.129Z |
| dial unix /opt/azurekmsactive.socket: connect: no such file or directory                                                                                    | 3       | 2026-06-24T18:00:34.364Z | 2026-06-24T18:00:36.670Z |

#### Proof 2: Log Snippet

etcd StatefulSet rolled during the forced upgrade:

```kql
// manifest.json: hosted_control_plane_namespace
let hcpNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
// manifest.json: time_window subset (upgrade rollout window)
| where timestamp between (datetime(2026-06-24T17:55:00Z) .. datetime(2026-06-24T18:05:00Z))
| where eventNamespace == hcpNamespace
| where objectName in ('etcd-0', 'etcd-1', 'etcd-2')
| where reason in ('Killing', 'Scheduled', 'Started')
| extend first_occurrence = coalesce(firstSeen, todatetime(log.event_time)),
         last_occurrence = coalesce(lastSeen, todatetime(log.event_time))
| where reason != 'Killing' or message has 'Stopping container etcd'
| summarize first_occurrence = min(first_occurrence), last_occurrence = max(last_occurrence), count = sum(count)
    by objectName, reason, message
| order by first_occurrence asc
```

| objectName | reason    | message (abbrev.)                                                              | first_occurrence         | last_occurrence          | count |
|------------|-----------|--------------------------------------------------------------------------------|--------------------------|--------------------------|-------|
| etcd-2     | Killing   | Stopping container etcd                                                        | 2026-06-24T17:56:15.000Z | 2026-06-24T17:56:15.000Z | 1     |
| etcd-2     | Killing   | Stopping container etcd-defrag                                                 | 2026-06-24T17:56:15.000Z | 2026-06-24T17:56:15.000Z | 1     |
| etcd-2     | Killing   | Stopping container etcd-metrics                                                | 2026-06-24T17:56:15.000Z | 2026-06-24T17:56:15.000Z | 1     |
| etcd-2     | Scheduled | Successfully assigned …/etcd-2 to aks-userswft1-40825394-vmss000000              | 2026-06-24T17:56:45.000Z | 2026-06-24T17:56:45.000Z | 1     |
| etcd-2     | Started   | Container started                                                              | 2026-06-24T17:56:55.000Z | 2026-06-24T17:57:02.000Z | 6     |
| etcd-1     | Killing   | Stopping container etcd                                                        | 2026-06-24T17:57:05.000Z | 2026-06-24T17:57:05.000Z | 1     |
| etcd-1     | Killing   | Stopping container etcd-defrag                                                 | 2026-06-24T17:57:05.000Z | 2026-06-24T17:57:05.000Z | 1     |
| etcd-1     | Killing   | Stopping container etcd-metrics                                                | 2026-06-24T17:57:05.000Z | 2026-06-24T17:57:05.000Z | 1     |
| etcd-1     | Scheduled | Successfully assigned …/etcd-1 to aks-userswft3-40628154-vmss000003              | 2026-06-24T17:57:35.000Z | 2026-06-24T17:57:35.000Z | 1     |
| etcd-1     | Started   | Container started                                                              | 2026-06-24T17:57:45.000Z | 2026-06-24T17:57:49.000Z | 6     |
| etcd-0     | Killing   | Stopping container etcd                                                        | 2026-06-24T17:57:54.000Z | 2026-06-24T17:57:54.000Z | 1     |
| etcd-0     | Killing   | Stopping container etcd-defrag                                                 | 2026-06-24T17:57:54.000Z | 2026-06-24T17:57:54.000Z | 1     |
| etcd-0     | Killing   | Stopping container etcd-metrics                                                | 2026-06-24T17:57:54.000Z | 2026-06-24T17:57:54.000Z | 1     |
| etcd-0     | Scheduled | Successfully assigned …/etcd-0 to aks-userswft2-13369368-vmss000000              | 2026-06-24T17:58:23.000Z | 2026-06-24T17:58:23.000Z | 1     |
| etcd-0     | Started   | Container started                                                              | 2026-06-24T17:58:36.000Z | 2026-06-24T17:58:45.000Z | 6     |

### Why couldn't kube-apiserver reach etcd?

The dial failures fit **upgrade churn** from the in-flight z-stream bump to 4.22.2: etcd restarted member-by-member (**17:56:15Z**–**17:58:45Z**, Proof 2 above) while the new kube-apiserver ReplicaSet rolled in (**from 17:58:48Z**), so kas logged sustained `etcd-client` lookup/dial errors even though etcd quorum stayed healthy (`EtcdAvailable=True`). Using the queries below, we did not see any `etcd-client` Service or Endpoints Events in Kusto for this window:

#### Proof 1: Log Snippet

`etcd-client` Service:

```kql
// manifest.json: hosted_control_plane_namespace
let hcpNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
// manifest.json: time_window subset (upgrade rollout window)
| where timestamp between (datetime(2026-06-24T17:55:00Z) .. datetime(2026-06-24T18:05:00Z))
| where eventNamespace == hcpNamespace
| where objectKind == 'Service' and objectName == 'etcd-client'
| extend first_occurrence = coalesce(firstSeen, todatetime(log.event_time)),
         last_occurrence = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, first_occurrence, last_occurrence, message) by objectKind, objectName, reason
| project objectKind, objectName, reason, message, first_occurrence, last_occurrence, count
| order by first_occurrence asc
```

No rows — no Service Events for `etcd-client` in this window.

#### Proof 2: Log Snippet

`etcd-client` Endpoints:

```kql
// manifest.json: hosted_control_plane_namespace
let hcpNamespace = 'ocm-arohcpci01-2r4sj32f75bfmfa9m6rkupe7o982hqst-p2d8v9o7y9g8x5o';
// manifest.json: kusto_cluster
cluster('https://hcp-dev-us-2.eastus2.kusto.windows.net').database('ServiceLogs').table('kubernetesEvents')
// manifest.json: time_window subset (upgrade rollout window)
| where timestamp between (datetime(2026-06-24T17:55:00Z) .. datetime(2026-06-24T18:05:00Z))
| where eventNamespace == hcpNamespace
| where objectKind == 'Endpoints' and objectName == 'etcd-client'
| extend first_occurrence = coalesce(firstSeen, todatetime(log.event_time)),
         last_occurrence = coalesce(lastSeen, todatetime(log.event_time))
| summarize arg_max(count, first_occurrence, last_occurrence, message) by objectKind, objectName, reason
| project objectKind, objectName, reason, message, first_occurrence, last_occurrence, count
| order by first_occurrence asc
```

No rows — no Endpoints Events for `etcd-client` in this window.

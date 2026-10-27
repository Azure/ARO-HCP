# SWIFT Recorder Deployment

The recorder defaults to enabled in **every environment**, including public,
shared, and personal environments, and always runs continuous router checks.
`swiftRecorder.enabled` is the only deployment gate, independent of source
registry; there is no separate router-check switch or startup-only deployment.
Startup `captureMode` remains `slow` outside ephemeral `ci00`/`ci01`, which use
`all`. Source-image selection and pins are unchanged. Defaults do not deploy
anything live: deployments must supply repo-built images through overrides.

This guide owns images, permissions, and rollout. See the [README](../swift-recorder/README.md)
for build/test commands and [log reference](../swift-recorder/LOG_RECORDS.md)
for runtime behavior, emitted schemas, probe roles, TLS identities, and cadence.

## Images

`swift-recorder/Dockerfile` builds a dedicated `swift-recorder` image from
repository-root context. The DaemonSet executes `/swift-recorder controller`.
Override `swiftRecorder.image` registry/repository/digest per environment.
An image override selects an image, not enablement. Baseline validation permits
an empty digest until deployment supplies the override; Helm requires the
effective selected digest.

The [pipeline](../swift-recorder/pipeline.yaml) retains explicit dependencies:
`management/deploy` depends on `global/mirror-image` (ImageMirror), uses the
`global/output` identity, and obtains its telemetry endpoint from
`kusto/kusto-lookup`. When disabled, mirroring, Kusto lookup, and Helm deployment
are skipped; only the shared global identity lookup remains, and the chart
renders no resources. In [topology](../topology.yaml), recorder diagnostics follow
mgmt-agent and do not gate Fleet registration.

Public INT, STG, and PROD additionally require an `sdp-pipelines` PR and EV2
rollout; see <https://aka.ms/arohcp-pipelines>. Include service group
`Microsoft.Azure.ARO.HCP.SwiftRecorder` when packaging the updated topology.

## Runtime Router Checks

The enabled chart includes the following access for continuous checks in every
environment. The [runtime contract](../swift-recorder/LOG_RECORDS.md#runtime-router-check-records)
defines discovery, namespace/Pod identity, DNS provenance, and health interpretation.
Changing startup `captureMode` does not change this access or gate router checks.

### Kubernetes Permissions

The [ClusterRole](../swift-recorder/deploy/templates/rbac.yaml) grants:

| Resources | Verbs / restriction |
|---|---|
| Pods | `get/list/watch` |
| Services, EndpointSlices, Routes, MultiTenantPodNetworkConfigs, Machines, AzureMachines | `list/watch` |
| Secret `ignition-server-ca-cert`, ConfigMap `root-ca` | `get/list/watch`, exact `resourceNames` |

There is no wildcard Secret, Node, `pods/exec`, lease, write, or Azure identity
access. Inventory uses cluster-wide informers and namespace-indexed listers.
The ClusterRoleBinding grants access across all namespaces, including dynamic
HCP namespaces; namespace selection in code is **not an authorization boundary**.
Trust informers use matching
`metadata.name=ignition-server-ca-cert` and `metadata.name=root-ca` selectors,
required for list/watch authorization under the named-resource rules.

**CA Secret access includes its signing private key.** Kubernetes cannot
authorize individual data keys; the ordinary informer cache retains the whole
Secret across all HCP namespaces. Only public `tls.crt` enters discovery inputs
and helper requests. ConfigMap `root-ca` supplies the separate internal root.
Structured probe records omit key/certificate payloads and request bundles.
The recorder uses normal low-verbosity controller/client-go logging, not a
secret-redacting logger: avoid high-verbosity request/object dumps that could
expose Secrets. Restrict chart, image, and service-account modification rights;
removing chart resources removes both trust-resource RBAC rules.

### Host Access

The Pod runs as root with host networking, `ClusterFirstWithHostNet` DNS,
`RuntimeDefault` seccomp, a read-only root filesystem, and privilege escalation
disabled. It drops all capabilities and adds only `SYS_ADMIN` for helper `setns`.
No hostPID, privileged container, `NET_ADMIN`, or `NET_RAW` is used.
**`SYS_ADMIN` remains powerful; read-only mounts do not make this unprivileged.**

| Read-only host mount | Container path / purpose |
|---|---|
| `/var/log` | `/host/var/log`; CNI input `/host/var/log/azure-vnet.log` |
| `/var/run/netns` | Same path, `HostToContainer` propagation for newly created namespaces |
| `/proc/sys/kernel/random/boot_id` | `/host/boot-id` for correlation |
| `/run/containerd` | Same path; `--runtime-endpoint=unix:///run/containerd/containerd.sock` (also CLI default) |

The netns host directory uses `DirectoryOrCreate` so kubelet can initialize it on
nodes that have not hosted a pod network namespace yet. Other host directories
must already exist. Namespace
mounts propagate into the Pod, never back to the host. Mounting containerd's
parent directory, not its socket inode, keeps replacement sockets visible after
runtime restarts. It exposes other runtime-directory contents/sockets, but not
all of `/run`; no host resolver file is mounted.

**Read-only filesystem mounting does not restrict socket API operations.**
Runtime-directory access grants broad node-level power beyond Kubernetes RBAC.
The intended discovery/status-only client is not a security boundary. Production
security review must explicitly accept runtime access and CA signing-key access,
restrict modification rights, and verify removal before expanding scope.

## Rollout Checks

Verify OS/runtime support for `setns` under root, `SYS_ADMIN`, and `RuntimeDefault`,
admission approval for host networking/hostPaths, and namespace mount propagation.
Do not add capabilities or privileged/hostPID access as a workaround. Required
discovery APIs must be served, including CAPI/CAPZ `v1beta1`; missing APIs can
block initial cache synchronization, router workers, and recorder readiness.

The [DaemonSet](../swift-recorder/deploy/templates/daemonset.yaml) selects only
SWIFT-enabled Linux nodes, uses `service-lifecycle-critical` priority, and updates
at most one node at a time. Requests are 20m CPU / 64Mi memory, with a 256Mi limit.
Node name comes from the downward API; cluster name, region, and environment come
from config. Startup bounds are:

| Flag | Value |
|---|---|
| `--startup-dwell` | `30s` |
| `--post-success-capture` | `10s` |
| `--sample-interval` | `1s` |
| `--capture-timeout` | `500ms` |
| `--max-pods` | `32` |
| `--max-buffer-bytes` | `16777216` |
| `--max-record-bytes` | `65536` |
| `--episode-timeout` | `15m` |

```sh
kubectl get nodes -l kubernetes.azure.com/podnetwork-swiftv2-enabled=true
kubectl -n swift-recorder rollout status daemonset/swift-recorder
kubectl -n swift-recorder get pods -o wide
kubectl -n swift-recorder get service,servicemonitor
kubectl -n swift-recorder logs daemonset/swift-recorder --tail=50
```

Check arguments, mounts, RBAC, absence on non-SWIFT nodes, and `/healthz`, `/readyz`,
`/metrics` plus ServiceMonitor discovery on port 8091 (`--health-address=:8091`).
Host networking exposes this port on each selected node: rule out conflicts and
limit access to intended monitoring. Readiness waits for inputs/caches, not probe
success. Check structured records in service `containerLogs` using the
[summary and coverage rules](../swift-recorder/LOG_RECORDS.md#runtime-router-check-records);
a healthy subset does not establish complete coverage. No new ingestion rule,
table, or identity is required.

On a scoped management-cluster canary, measure watch counts, relist load, memory
per active HCP namespace against the limit, and ingestion volume before expanding.
Keep startup capture `slow` outside ephemeral CI; scope any temporary `all`
diagnostic rollout and return it to `slow` afterward.

To stop all diagnostics and remove runtime/trust access:

```sh
helm uninstall swift-recorder -n swift-recorder
```

Run on the intended management cluster and persist `swiftRecorder.enabled: false`
to prevent redeployment. Alternatively apply the chart with `enabled=false` to
remove its resources. **Skipping a subsequent pipeline does not uninstall an
existing release.**

### Personal Validation

Use the actual personal environment, not renamed/simulated CI:

```sh
make -C swift-recorder deploy DEPLOY_ENV=pers
```

This builds/pushes, records the actual digest in an environment-scoped override,
and explicitly enables that rollout. Keep resolved `environmentName: pers` and
`--environment=pers`, never `ci00`/`ci01`. No extra permission flag is needed;
image/security prerequisites still apply and startup capture remains `slow`.
For an existing dedicated image, overlay
`clouds.dev.environments.pers.defaults.swiftRecorder.image` with its actual
`registry`, `repository`, and `digest`.

`record-override` and `record-latest-override` only select images. Root
`build-services` / `record-services-override` include locally built recorder
images; `latest-services-override` does not require this unpublished image, so
merge a recorder-specific latest override when using it. After disposable
validation, uninstall and persist `enabled: false` if access should be removed.

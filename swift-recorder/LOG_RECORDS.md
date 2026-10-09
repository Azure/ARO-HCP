# SWIFT Diagnostic Log Records

The recorder emits independent startup, root-node, and runtime router-check
streams. This is their runtime and emitted-schema reference; see the
[deployment guide](../docs/swift-recorder-deployment.md) for images, permissions,
and rollout, and the [README](README.md) for build/test quickstart.

## Parsing And Lifecycle

Arobit decodes the `swift-recorder` container's JSON stdout in place under `log`,
retaining outer Kubernetes/cluster routing metadata. The existing `$.log.log`
mapping delivers an object to service `containerLogs`: query `log.record.event`
or `log.record.snapshot.state` directly. Plain text or malformed JSON remains a
string, not an empty record. Routing uses container name, not `controller_name`;
other containers' parsing is unchanged. No new table or ingestion identity is
needed. Distinguish streams before interpreting `log.record`; their schemas
are not interchangeable, and ordinary controller errors are not probe records.

Startup follows mgmt-agent's Cobra validation/completion, structured slog/klog,
and component-base metrics patterns. Each node has its own client-go workqueues,
without leader election. Readiness waits for local-Pod/CNI initialization and
router discovery caches/handler synchronization, not successful probes. Signal
cancellation stops and joins controllers, input, informers, and the HTTP server.

## Startup Captures

The recorder watches node-local Pods and polls new Azure VNet `Processing ADD
command` records every 100 ms for Pod UID, sandbox ID, and namespace path, never
publishing raw CNI arguments. Existing log contents are skipped at startup;
rotations are followed and incomplete/oversized input is bounded. Namespace
discovery is best-effort, not a synchronous pre-CNI hook: missing namespaces
retry within the episode budget rather than implying networking failure.

Each capture invokes a short-lived helper that validates a namespace FD, enters
it, dumps bounded rtnetlink state, and exits. Controllers/buffers retain no
namespace FDs or netlink sockets. In `slow` mode, bounded per-Pod rings publish
only after unresolved startup exceeds the dwell; fast-startup buffers are
discarded. `all` publishes every observed eligible episode without relaxing
budgets, including newly successful sandboxes in the post-success window.
Published slow episodes also sample briefly after success. Missing/unknown
sandbox conditions remain unknown, not diagnosed SWIFT failures. Errors and
budget drops are explicit, not successful snapshots. State is in memory and
can be lost on exit. One startup helper runs at a time, so the sample interval
is a minimum, not a per-Pod rate guarantee under load.

[`pkg/recorder/recorder.go`](pkg/recorder/recorder.go) emits `"SWIFT startup record"`
with `controller_name: swift-startup-recorder`. Its `record` contains:

| Fields | Meaning |
|---|---|
| `event`, `at`, `episode_started_at`, `pod_uid`, `network_condition` | Event (`open`, `snapshot`, `error`, `recovery`, `close`), timestamps, and Pod evidence |
| `sandbox_id`, `netns_path`, `add_observed_at`, `add_source_time` | Optional CNI attempt identity/timestamps |
| `reason` | Optional error/closure reason |
| `snapshot.started_at`, `snapshot.finished_at`, `snapshot.state` | Capture timing and helper output, only for snapshots |

Helper output contains namespace device/inode and sections for links, addresses,
routes, rules, and neighbors. Links include MAC, master/parent indices,
up/running/carrier state, and RX/TX/error/drop counters. These do not prove a
selected netvsc datapath, physical transmission, or host receipt. There is no
packet capture, ethtool-specific VF counter collection, host netlink event ring,
or remediation. See [root-to-pod correlation](#root-to-pod-correlation) for joins.

`workqueue_*` metrics use queue name `swift-startup-recorder`; REST-client metrics
share the component-base registry. Counters `swift_recorder_episodes_total`,
`swift_recorder_captures_total`, and `swift_recorder_overflows_total` use bounded
outcome/limit labels, not Pod IDs. Informer caches and Go process overhead are
outside the encoded-record byte budget. See [deployment bounds](../docs/swift-recorder-deployment.md#rollout-checks)
and the [integration test](INTEGRATION.md).

## Root Node-State Records

The root-namespace monitor (`pkg/netwatch`) logs a structured `record` under
`"SWIFT node netlink record"`. It tracks vmbus/pci-backed links, including
hv_netvsc synthetic NICs and their MANA/mlx5_core VFs. It does not track
addresses, ordinary Kubernetes veths/bridges, routes, rules, neighbors, or traffic
counters. It stays in the root namespace, needing only an ordinary netlink socket
and no additional mounts, capabilities, RBAC, or Helm values. Collection uses
subscribe-then-dump-then-replay; recoverable failures retry internally without
stopping the independent startup recorder.

`pkg/netwatch/record.go`, `state.go`, and `observe.go` define the emitted schema.
The [interaction diagram](pkg/netwatch/INTERACTION.md) describes collection and
recovery.

## Identity and ordering

- A root-namespace **ifindex** identifies a link within the current generation.
  Names can change. A MANA PCI address is shared by multiple VFs and does not
  identify an individual VF. Departures remove current state; a reused ifindex
  starts fresh. Reconnection also rebuilds the entire generation.
- **`session_id`** is a UUID created for one `Run` lifetime. **`sequence`** starts
  at 1 and increases for every emitted record, including unavailable records.
  Both persist across generation rebuilds.
- **`last_applied_notification`** is generation-local. It counts processed
  notifications, including ignored input and establishment replay, not just
  changes. It resets for a new generation; unavailable records report zero.
  It is not a kernel sequence number or a session-restart indicator.
- **`observed_at`** is the notification receive time for a change, or the time
  a state/status record was requested. **`emitted_at`** is when that record was
  built. Neither establishes an atomic kernel snapshot or cross-stream cause.

## Envelope

`LogTo` emits through `logr`, with `controller_name` and `boot_id` supplied by
`cmd/cmd.go`. Node and cluster identity come from log-ingestion metadata, not
from fields invented inside `record`. Correlation requires that metadata and
boot identity in addition to device evidence.

## Record

| Field | Presence | Meaning |
|---|---|---|
| `reason` | Always | `change` or `periodic`, as described below. |
| `session_id` | Always | UUID identifying this `Run` lifetime. |
| `sequence` | Always | 1-based emission order in this session. |
| `observed_at` | Always | RFC 3339 notification receipt or state-record request time. |
| `emitted_at` | Always | RFC 3339 record construction time. |
| `last_applied_notification` | Always | Generation-local processed-notification count. |
| `status` | Always | Availability of the selected link state. |
| `state` | Nonempty available state, unless omitted for size | Full resulting selected link inventory, not a diff. |
| `observations` | Changes, unless omitted for size | Structured changes caused by an applied notification. |
| `incomplete` | Content could not be emitted within the record budget | Explains why state and observations were omitted. |

### Reasons

- **`change`**: an applied notification produced at least one observation.
- **`periodic`**: a state/status record. This includes the two-minute heartbeat,
  successful initial establishment, successful rebuilding, and unavailable
  status after failure. The name does not imply that a fresh dump occurred.

There are no separate initial, recovery, availability, or unreconciled reason
values. Consumers inspect `status` and record ordering instead.

### Status

`status.available` is always present. When false, `status.reason` explains the
failure and `state` is omitted, rather than exposing stale state as current.
When true with no selected links, `state` is also omitted because it is empty.
Check availability and `incomplete`, not just the presence of `state`.

Emitted failure reasons are:

- `subscription_open_failed`: the notification socket could not be opened.
- `dump_deadline_exceeded`, `dump_interrupted`, `selected_state_truncated`, or
  `dump_failed`: the baseline could not be established.
- `kernel_notification_overflow`: notifications may have been lost: the kernel
  reported loss, or more than 4096 notifications queued while the baseline was
  being established.
- `malformed_notification`: a relevant event could not be applied safely.
- `subscription_lost` or `subscription_closed`: the notification source failed.

Every failed generation is discarded. Subscription and collection retry with
bounded exponential backoff. The two-minute ticker is checked between receives
and during retry backoff; baseline collection and notification replay run
synchronously, without an independent heartbeat goroutine.

### LinkState

| Field | Type / presence | Meaning |
|---|---|---|
| `ifindex` | Integer, always | Root-namespace interface index. |
| `name` | String, always | Current interface name. |
| `mac` | String, when nonempty | Hardware address from `IFLA_ADDRESS`. |
| `parent_bus` | String, when nonempty | Backing bus, normally `vmbus` or `pci`. |
| `parent_device` | String, when nonempty | VMBus GUID or shared PCI address. |
| `master_index` | Integer, when nonzero | Current master from `IFLA_MASTER`; omission means no current master. |
| `parent_index` | Integer, when nonzero | Kernel-reported parent link index. |
| `mtu` | Integer, always | Current MTU. |
| `up` | Boolean, always | Administrative `IFF_UP` flag. |
| `running` | Boolean, always | `IFF_RUNNING` flag. |
| `lower_up` | Boolean, always | `IFF_LOWER_UP` flag. |
| `operstate` | Integer, always | Decoded `IFLA_OPERSTATE` value. |
| `carrier` | Boolean, always | Decoded `IFLA_CARRIER` value. |

`operstate` and `carrier` are scalar values; missing decoded attributes become
zero and false respectively. They do not carry a separate unknown/presence bit.
Addresses and counters are not part of this link-state schema.

### Observation

Every observation has a `kind` and `ifindex`. One notification can produce
several observations, each containing only the fields in its category.

| Kind | Meaning |
|---|---|
| `link_appeared` | A selected interface was not previously tracked. |
| `link_disappeared` | A tracked interface's `DELLINK` was observed. |
| `link_renamed` | `name` changed. |
| `link_pairing_changed` | `master_index` or `parent_index` changed. |
| `link_state_changed` | `up`, `running`, `lower_up`, `operstate`, or `carrier` changed. |
| `link_configuration_changed` | `mtu`, `mac`, `parent_bus`, or `parent_device` changed. |

`fields` contains `{field, from, to}` entries for field-level differences and
is omitted for appearances/departures. Counter-only changes produce no
observation. `before` preserves the previous LinkState on departure and pairing
changes; it is historical evidence, not part of the resulting inventory.

The optional `trigger` carries:

| Field | Presence / meaning |
|---|---|
| `notification` | `DELLINK` for departure or `NEWLINK` for pairing changes. |
| `new_netns_id` | When supplied on departure: a destination ID local to the root namespace's peer-netns table, not a global namespace identity. Zero remains present. |
| `new_ifindex` | When supplied: the interface index in the destination namespace. |
| `last_known_master_index` | A previously observed nonzero master, retained through unpairing for this interface incarnation. |
| `last_known_synthetic_guid` | That master's observed VMBus identity, retained even if the synthetic departed first. Never inferred from PCI address or name. |

A departure does not by itself prove destruction versus movement into a
particular pod. No destination namespace lookup or cross-lifetime VF tracking
is performed.

## Size budget

The emitter marshals each complete Record before logging and enforces a fixed
64 KiB budget. On oversize or marshal failure, it preserves bookkeeping and
status, sets `incomplete`, and omits both `state` and `observations`. Consumers
must not interpret this fallback as an available-empty inventory. The root
budget is not configurable through the per-pod `--max-record-bytes` flag.

## Root-to-pod correlation

A VMBus GUID identifies the synthetic NIC, not a VF or pod. Match it only within
the same cluster, node, and boot:

- Current root synthetics carry the GUID in `state[].parent_device`; departure
  observations preserve it in `before`.
- A root VF's current `master_index` can resolve to a synthetic in the same
  inventory. Pairing/departure triggers retain a previously observed synthetic
  GUID after unpairing or the synthetic's earlier departure.
- In pod captures, the matching synthetic is in
  `record.snapshot.state.state.links.entries[]`, with `parentBus == "vmbus"`
  and the same `parentDevice`. The pod UID and namespace evidence identify that
  capture; the GUID relates observations, not an inferred VF lifetime.

A shared MANA PCI address, interface name, or namespace-local ifindex cannot
uniquely join root and pod VFs. If pairing or cluster/node/boot evidence is
missing, leave the association unknown. Reused indices and reconstructed
root generations do not authorize carrying old associations forward.

## Examples

### Available-empty state after establishment

```json
{
  "reason": "periodic",
  "session_id": "11111111-1111-1111-1111-111111111111",
  "sequence": 1,
  "observed_at": "2026-09-22T10:00:00Z",
  "emitted_at": "2026-09-22T10:00:00Z",
  "last_applied_notification": 0,
  "status": { "available": true }
}
```

The same shape is used for an available-empty heartbeat or rebuilt generation.
`sequence` orders emissions; it is not a generation identifier.

### Subscription unavailable

```json
{
  "reason": "periodic",
  "session_id": "11111111-1111-1111-1111-111111111111",
  "sequence": 2,
  "observed_at": "2026-09-22T10:00:01Z",
  "emitted_at": "2026-09-22T10:00:01Z",
  "last_applied_notification": 0,
  "status": { "available": false, "reason": "subscription_open_failed" }
}
```

The inventory is unknown, not empty. Further failed attempts and heartbeat
records can repeat unavailable status. Successful rebuilding emits `periodic`
with available status and its reconstructed inventory, without inventing
change observations across the gap.

The tested full change examples are the [pairing golden fixture](pkg/netwatch/testdata/record_change.json)
and [departure golden fixture](pkg/netwatch/testdata/record_departure.json).
They include complete LinkState, field changes, and retained pairing evidence.

## No atomicity or lossless history

Each record is a userspace reconstruction from a dump plus applied
notifications, not a synchronized kernel snapshot. Overflow and other detected
failures make state unavailable, but ordering counters do not prove absence of
loss. Replayed establishment notifications update the baseline without emitting
change observations. A reused ifindex or rebuilt generation starts fresh; no
cross-lifetime identity or movement is inferred.

## Runtime Router-Check Records

[`pkg/routercheck/controller.go`](pkg/routercheck/controller.go) emits `"SWIFT router check"`
with `controller_name: swift-router-check`. [Discovery](pkg/routercheck/discovery.go)
and [probe types](pkg/probe/probe.go) define its payloads. This stream always runs
with the recorder in every environment, independently of startup `captureMode`.
Records retain the actual
deployment environment, including `pers`, `int`, `stg`, and `prod`.

The envelope includes `node_name`, `cluster_name`, `region`, `environment`,
`boot_id`, `pod_namespace`, `pod_name`, and `pod_uid`. Each pass adds a fresh
`pass_id`, `pod_ready`, `pod_deleting`, and `pni`. After runtime discovery,
records also carry `sandbox_id`, `netns_path`, `netns_device`, and `netns_inode`.
Join on pass and pod identity within the same cluster/node/boot, not pod name or
namespace inode alone. A pass that cannot resolve its runtime need not have
sandbox fields. Readiness metadata is evidence, not a prerequisite for a
health check, and successful recorder readiness does not imply successful probes.

[CRI discovery](pkg/routercheck/runtime.go) requires a unique ready sandbox
matching the current Pod namespace/name/UID, a valid namespace path beneath the
configured netns directory, and current router-container identity. Namespace
device/inode and sandbox identity are revalidated around probing; do not join
across replacement sandboxes. Runtime failures expose fixed reasons rather than
raw CRI configuration. DNS provenance is described below.

Every pass emits one compact `field: summary` record, including successful,
advisory, partial, failed and canceled outcomes. Healthy and advisory-only passes
do not emit detailed discovery, target inventories, worker identities, DNS
configuration, raw probe results, or namespace snapshots. Detailed evidence is
published only for a failed, non-canceled pass. There are no per-pass `start` or
`finish` records. Summary fields are:

| Field | Meaning |
|---|---|
| `outcome` | `healthy`, `advisory`, `partial`, `failed`, or `canceled`; a partial pass is not complete coverage. |
| `unavailable` | Optional fixed reason for an unavailable pass. |
| `partial`, `canceled` | Coverage and lifecycle indicators independent of probe success counts. |
| `age_seconds` | Pod age from its creation timestamp, or null if unknown. |
| `cadence_seconds` | Unjittered base delay, 30 or 300 seconds; not the actual next-pass timestamp. |
| `elapsed_ms` | Whole-pass duration. |
| `discovered_targets`, `submitted_targets`, `reported_targets`, `omitted_targets` | Counts of planned, submitted, observed and missing targets. |
| `roles` | Discovered target counts by role, without per-target addresses or identities. |
| `http_success`, `http_failed` | HTTP health outcomes. |
| `tcp_connected`, `tcp_refused`, `tcp_timeout`, `tcp_failed` | Connection-only worker control outcomes. Refusal is reachability evidence, not a successful SSH check. |
| `retired_failed` | HTTP/TCP failures on explicitly retiring targets. A shared worker target is retiring only when all associated workers are deleting. These remain in protocol failure counts but do not alone trigger a diagnostic dump. |
| `dns_success`, `dns_failed`, `dns_reported`, `dns_expected` | DNS outcome and coverage counts. |
| `issues` | Counts of summary issues, such as `creation_timestamp_missing`. |
| `truncated`, `evidence_errors` | Counts of incomplete or unavailable diagnostic evidence. |

If only deleting targets fail, the outcome is `advisory` unless the pass is also
partial or canceled. Readiness alone does not excuse a target failure. Active
target failures, DNS failures, and other non-advisory failures still trigger
failure details. Sandbox replacement produces a canceled summary with
`unavailable: sandbox_changed`, not a diagnostic dump for the retired sandbox.

For failure details, `field` identifies the payload in `record`:

| Field | Record |
|---|---|
| `unavailable` | A reason such as `runtime_discovery_failed`, `discovery_failed`, `runtime_revalidation_failed`, `probe_execution_failed`, or `probe_result_invalid`. |
| `swift_interface` | `ip`, optional `cidr`/`mac`, and `source` from MTPNC discovery. |
| `target` | A `target` plus available `pod_name`, `pod_uid`, `node`, `ready`, `deleting`, `endpoint_slice`, `target_ref`, `selector_matches`, `target_port_matches`, and `workers` context. |
| `plan` | `discovered_targets`, `submitted_targets`, and `omitted_targets` describe the bounded plan submitted to the helper. |
| `omitted_targets` | Batches of target records excluded from the submitted plan, retaining their identity context. These probes did not run. |
| `coverage` | `discovered_targets`, `submitted_targets`, `reported_targets`, `omitted_targets`, and `partial` compare discovery with returned observations; not a health verdict. |
| `dns_config` | `servers`, `searches`, `options`, and `source`, either `cri_sandbox_config_router_mount_verified` or `unavailable`. |
| `result` | The helper's structured result described below. |

MTPNC interface sources distinguish `interfaceInfos` from the shipped legacy
`deprecated_primaryIP` fallback. The DNS source is the sandbox's CRI `DNSConfig`,
usable only when runtime evidence confirms that the current router container's
resolver mount matches the sandbox resolver mount. Missing, mismatched, or
unverifiable mount evidence aborts runtime discovery. This is not a read of
the resolver file's current contents, and `source: cri_sandbox_config_router_mount_verified` alone is
not proof of the pod's effective resolver configuration. The helper's
`/etc/resolv.conf` is never a fallback. Search suffixes and options are evidence;
DNS questions use the discovered names verbatim without search expansion.

### Probe Results

The result carries `namespaceDevice`, `namespaceInode`, `startedAt`, `finishedAt`,
`targets`, `dnsConfig`, `dns`, `before`, `after`, optional `counterDeltas`, and
optional `truncated`. Every target has an `id`, `role`, literal `address`, `port`,
`sourceIP`, `serverName`, `trustBundle`, `path`, `plainHTTP`, `tcpOnly`, and
`livenessOnFailure`.
`trustBundle` identifies the selected public trust bundle (`root` for direct
ignition server and all KAS probes, `ignition` for ignition router/proxy probes);
it is not certificate data.
The helper Request carries public certificate bundles separately in `trustBundles`,
and those bundles are omitted from structured probe records and results. An empty
`sourceIP` delegates source selection to the kernel. `serverName` supplies TLS SNI and HTTP Host,
not an implicit DNS lookup. Direct `ignition-server` probes use
`ignition-server.<namespace>.svc` and `ca.crt` from ConfigMap `root-ca`. Router and
`ignition-server-proxy` probes use the `ignition` Route host and public `tls.crt`
from Secret `ignition-server-ca-cert`. Missing required Routes or Route hosts
abort discovery for the pass before any probes are submitted.

### Role Matrix And TLS Identities

The normal bounded plan includes every discovered peer, service endpoint, and
correlated worker, not representatives:

| Roles | Destinations | Protocol / identity / trust |
|---|---|---|
| `haproxy-ready` | `127.0.0.1:9444` | HTTP `/haproxy_ready`, no TLS |
| `router-loopback`, `router-management`, `router-swift`, `peer-management`, `peer-swift` | Loopback, own and peer management/SWIFT IPs, port 8443 | HTTPS `/healthz`, ignition Route host, `ignition` |
| `ignition-server-service`, `ignition-server-endpoint` | Service IPs / EndpointSlice addresses and discovered ports | HTTPS `/healthz`, `ignition-server.<namespace>.svc`, `root` |
| `ignition-server-proxy-service`, `ignition-server-proxy-endpoint` | Service IPs / EndpointSlice addresses and discovered ports | HTTPS `/healthz`, ignition Route host, `ignition` |
| `kas-router-loopback`, `kas-router-management`, `kas-router-swift`, `kas-peer-management`, `kas-peer-swift` | Same router paths on port 8443 | HTTPS `/readyz`, `api.<hcp-name>.hypershift.local`, `root` |
| `kas-service`, `kas-endpoint` | `kube-apiserver` Service / EndpointSlice destinations | HTTPS `/readyz`, same built-in KAS identity, `root` |
| `worker-outbound` | Worker InternalIPs, port 22 | TCP-only; no TLS, HTTP, SSH handshake, or application data |

SWIFT peer and worker targets bind each same-family local SWIFT source; worker
targets set `tcpOnly: true`, empty `serverName`/`path`, and `plainHTTP: false`.
Not-ready and terminating endpoint context is retained, not treated as healthy.
No worker-to-router ingress or load-balancer probes run; outbound reachability
does not validate those paths or SSH readiness. KAS never uses customer hostnames
or customer certificate configuration. Its Service serving port selects `client`
(normally 6443); matching slice ports supply actual replica ports. Unnamed Service
ports match unnamed slice ports, not arbitrary listeners. Unready replicas remain
in the plan. Three routers and three KAS replicas normally add eleven KAS targets
per local router.

The cached Service must have a named, nonempty-UID `HostedControlPlane` owner in
API group `hypershift.openshift.io`. An ignition Route HCP owner, when present,
must match. The HCP name is never inferred from a namespace suffix or customer
hostname. Routed checks require a cached Route named `kube-apiserver-internal`,
`kube-apiserver`, or `kube-apiserver-private` in the same namespace, with matching
HCP name/UID, label `hypershift.openshift.io/hosted-control-plane: <namespace>`,
and passthrough TLS to the KAS Service. The internal Route host must exactly match
the built-in identity; external Route hosts never replace it. Direct KAS checks
require the same complete discovery input as routed checks. Missing or invalid
required Routes, Service-owner mismatches, and ignition-owner mismatches abort
discovery for the whole pass with `unavailable: discovery_failed`. The controller
logs the returned error and retries through workqueue backoff.
No HCP API reads or additional Secret access are required.

Worker discovery reads informer-cached management-cluster `cluster.x-k8s.io/v1beta1` Machines and
`infrastructure.cluster.x-k8s.io/v1beta1` AzureMachines in the router's HCP namespace.
It requires a single consistent CAPI cluster identity across those inventories,
matching Machine `spec.clusterName` and cluster-name labels, a same-namespace
AzureMachine infrastructure reference in the CAPZ API group, matching AzureMachine
cluster labels, and matching Machine owner UID if that owner is present.
The namespace suffix is not a CAPI cluster name, and multiple cluster identities
return a discovery error. This correlation does not require
additional Cluster/HCP reads or a registered guest Node.

Destinations come only from AzureMachine `status.addresses` entries of type
`InternalIP`. Each target's `workers` array retains `cluster_name`, `machine_name`,
`machine_uid`, `azure_machine_name`, `azure_machine_uid`, optional `provider_id`
(AzureMachine spec) and `machine_provider_id`, `address_source:
AzureMachine.status.addresses.InternalIP`, optional `machine_address_matches`,
`machine_deleting`, and `azure_machine_deleting`. The optional match boolean
compares against valid Machine InternalIPs; absence means there is no corroborating
Machine address. Machine addresses and provider IDs are corroborating evidence;
they do not replace CAPZ's observed destination. Deleting resources are included, with
target `deleting: true` if any correlated resource is deleting. Duplicate
destination/source pairs share a target while retaining all worker identities.

`Gather` selects cluster-labeled Machines and resolves each AzureMachine through
its Machine infrastructure reference. Missing workers, required lookup failures,
reference/identity/owner mismatches, missing or invalid pre-boot InternalIPs, and
absent same-family SWIFT sources abort discovery for the whole pass. The summary
reports `unavailable: discovery_failed`, the controller logs the returned error,
and the workqueue retries with backoff. Machine addresses remain corroboration.

### Target Observations

Each target observation reports `startedAt`, `durationMs`, `stage`, optional
`error`, actual `source`/`destination`, `timingsMs`, `tls`, `tlsVerify`, `tlsVerified`,
`httpStatus`, `expectedStatus`, `expected200`, `bodyBytesDiscarded`,
`bodyTruncated`, and optional `route`/`routeError`. Stages identify where execution
reached: namespace entry, trust setup, connect, TLS, HTTP write/headers/body, or completion.
HTTPS uses `tlsVerify: true` (`TLSVerify`), meaning certificate verification is
enabled, not that it succeeded. Only `tlsVerified: true` (`TLSVerified`) means
the verified TLS handshake completed; HTTP can still fail afterward. Missing or
invalid trust fails closed at `stage: trust` with the fixed error
`TLS trust unavailable or invalid`, before connecting. There is no
certificate-verification bypass, system-root fallback, or fallback to
another bundle. Chain, hostname, and expiry failures do not produce a verified
result. Missing or oversized bundles are rejected earlier during discovery.
Plain HTTP readiness and TCP-only controls do not perform TLS verification.
Bodies are discarded with bounds; structured probe records omit response bodies,
headers, certificates, credentials, and raw CRI configuration.
`bodyBytesDiscarded` is capped at 32 KiB. The helper reads at most one additional
decoded byte to distinguish an exactly-sized body from `decodedBodyLimit`
truncation. A separate 64 KiB body wire budget includes chunk framing and can
report `bodyTruncationReason: wireLimit` before the decoded limit is reached.

Only KAS `/readyz` targets set `livenessOnFailure: true`. A completed, verified
readiness response with status 500 or above triggers a separate connection to
`/livez?exclude=etcd` using the same address, source, server name and root trust.
The optional nested `liveness` health result records its own status, stage,
timings, TLS verification, and bounded body-discard evidence. It is diagnostic:
the top-level readiness failure and HTTP failure count remain unchanged even
when liveness succeeds. No follow-up occurs for 401/403, connection/TLS failures,
or incomplete HTTP responses. This is a fixed KAS health query, not arbitrary
query-string support. The follow-up has its own five-second target budget,
subject to the existing helper and pass deadlines.

DNS observations cover A and AAAA questions over UDP and TCP to the sandbox DNS
servers for `ignition-server`, `ignition-server-proxy`, and `kube-apiserver` Service
FQDNs when the cluster domain is known, never Route hosts.
They report `name`, `server`, `protocol`, `type`, `startedAt`,
`durationMs`, `stage`, optional `error`, `source`/`destination`, `rcode`,
`truncated`, and address `answers`. A truncated UDP response does not trigger an
implicit TCP retry; TCP is a separate observation.

`before` and `after` hold bounded namespace evidence: `at`, `state`, `listeners`,
`rpFilter`, `counters`, `errors`, and `truncated`. Counter deltas are
namespace-wide and cannot attribute traffic exclusively to these checks. Neither
route evidence nor successful health checks prove a particular VF datapath.
There is no packet capture or remediation.

### Bounds And Interpretation

Targets have five-second budgets; the helper has a 15-second deadline and the
whole pass a 30-second deadline. Successful passes are requeued after completion
with positive 20% jitter: 30-36 seconds while the Pod is younger than ten minutes, then 5-6
minutes. Missing creation timestamps use the slower delay and a summary issue.
The shared controller cooldown utility enforces this per-Pod delay across queue
events. Errors return to the rate-limited workqueue for backoff and retry.
The cooldown is keyed by Pod namespace, name and UID. Existing
healthy local routers are included at controller startup. Sandbox replacement
or pod lifecycle changes can cancel a pass rather than produce a stale result.

API discovery uses ordinary cluster-wide client-go informers and namespace-indexed
listers. All discovery caches, the local-Pod cache and its handler synchronize
before workers start. `Gather` produces typed `DiscoveryInput` from listers;
`BuildDiscovery` is a pure transformation into a probe plan. Required lookup
failures and missing or inconsistent input abort the pass with an error rather
than produce a partial discovery plan. Informer state is eventually consistent,
not an atomic cross-resource snapshot or a freshness guarantee. Cache contents
follow list/watch updates; there is no separate data-cache TTL.

The node-filtered Pod informer identifies local routers; discovery Pod listers
supply peers and Service-selected Pods. Named trust informers provide only public
certificates to discovery/helper inputs, but cache whole Secrets. See the
[permissions and logging cautions](../docs/swift-recorder-deployment.md#kubernetes-permissions)
for exact-name selectors, signing-key access, and high-verbosity risks.

Inspect `truncated`, evidence `errors`, discovery failures, and absent
results before interpreting coverage. The normal bounded plan includes all
discovered peers and workers, not a single representative peer or worker. Target/input/output bounds
can omit evidence, with truncation flags identifying incomplete coverage beyond
that plan; a planned target record alone does not prove the probe ran.

The emitter reserves half of `min(--max-record-bytes, 64 KiB)` for payload and
splits oversized structured objects/arrays into independent records, extending
`field` with paths such as `result.targets[0]`. An oversized scalar becomes
`record_too_large`. Consumers must include these split fields, not filter only
for `field == "result"`, and must not assume map-field emission order. This
stream has no root-monitor `session_id`/`sequence` semantics or lossless-history
guarantee; `pass_id` correlates a pass without proving complete delivery.

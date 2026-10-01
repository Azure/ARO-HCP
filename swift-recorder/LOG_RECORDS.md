# Root Node-State Log Records

The root-namespace monitor (`pkg/netwatch`) logs a structured `record` under
`"SWIFT node netlink record"`. It tracks vmbus/pci-backed links, including
hv_netvsc synthetic NICs and their MANA/mlx5_core VFs. It does not track
addresses, routes, rules, neighbors, or traffic counters. Per-pod capture
records are unchanged; see the [runtime contract](README.md#runtime-contract)
and [integration test](INTEGRATION.md).

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

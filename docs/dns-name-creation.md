# DNS name creation and reservation

This document describes how ARO-HCP reserves a unique kube-apiserver DNS name for
each cluster, how collisions are prevented, and how names are eventually returned
to the pool for reuse.

The design is implemented by two backend controllers in
[`backend/pkg/controllers/dnsreservation`](../backend/pkg/controllers/dnsreservation):

- **`DNSReservationController`** — watches `HCPOpenShiftCluster`s and reserves a
  name for every cluster that declares a base domain prefix.
- **`DNSReservationCleanupController`** — watches `DNSReservation`s and reaps
  orphaned, expired, or superseded reservations. It is the *eventual reaper* of
  every reservation; the creation controller never hard-deletes anything.

The persisted resource is the `DNSReservation` type in
[`internal/api/coreapi/types_dnsreservation.go`](../internal/api/coreapi/types_dnsreservation.go).

## DNS name format

A reserved name has the form:

```
<baseDomainPrefix>.<random>
```

- `baseDomainPrefix` comes from the customer-supplied
  `HCPOpenShiftCluster.CustomerProperties.DNS.BaseDomainPrefix`.
- `<random>` is a **4-character** suffix.

Example: a cluster with base domain prefix `mycluster` might get the reservation
name `mycluster.a1b2`.

### Rules for name creation

| Rule | Value |
|------|-------|
| Random suffix length | 4 characters |
| Charset | `a`–`z` and `0`–`9` (lowercase letters and digits only) |
| Offensive-word filter | Each candidate suffix is checked with [`go-clean-lang`](https://github.com/tzvatot/go-clean-lang); non-clean candidates are discarded and a new one is rolled |

Only lowercase letters and digits are used so the suffix is always a valid DNS
label component. The offensive-word filter runs in a loop: the controller keeps
generating random suffixes until one passes `cleanlang.IsClean`, guaranteeing no
customer-visible DNS name contains an accidental slur.

The full resource ID of a reservation is subscription-scoped:

```
/subscriptions/<subscriptionID>/providers/Microsoft.RedHatOpenShift/dnsReservations/<baseDomainPrefix>.<random>
```

It lives directly under the subscription (not under the cluster) so that a
reservation can outlive the cluster that owned it — which is essential for the
reuse cooldown described below.

## How we ensure names don't collide (within a subscription)

Uniqueness is enforced by **CosmosDB**, not by a read-then-write check.

- Every document's CosmosDB `id` is derived deterministically from its resource
  ID, and the `Resources` container is partitioned by subscription ID.
- Therefore two reservations with the same `<baseDomainPrefix>.<random>` name in
  the same subscription would map to the same document `id` in the same
  partition, and CosmosDB rejects the second `Create` with **HTTP 409 Conflict**.

The `DNSReservationController` never pre-checks whether a name is free. It simply
tries to `Create` the reservation:

1. Generate a random suffix and build the candidate reservation.
2. `Create` it in CosmosDB.
   - **Success** → the name was free and is now ours.
   - **Conflict (409) or any transient error** → return the error.

On a returned error, the cluster-watching controller's **rate-limited workqueue**
requeues the cluster. On the next attempt the controller generates a **brand-new
random suffix**, so retries naturally walk away from a contended name until one
sticks. This is why no name is ever special, predictable, or hand-picked:
collisions self-heal by re-rolling.

## Lifecycle of a reservation

```
Pending ──(cluster binds it)──▶ Bound ──(cluster gone / moved)──▶ PendingDeletion ──(1 week)──▶ deleted
```

`BindingState` (on the `DNSReservation`) tracks the state, and
`ServiceProviderCluster.Status.KubeAPIServerDNSReservation` is the authoritative
pointer recording "this live cluster wants this name".

1. **Pending** — the name has been uniquely claimed in CosmosDB, but no
   `ServiceProviderCluster` yet points at it. The creation controller sets
   `MustBindByTime = now + 61 minutes` at this point.
2. **Bound** — the creation controller has written the reservation's resource ID
   onto `ServiceProviderCluster.Status.KubeAPIServerDNSReservation` and flipped
   `BindingState` to `Bound` (clearing `MustBindByTime`). The name is in active
   use. (If the best-effort flip to `Bound` fails, the cleanup controller repairs
   it — see case 6 below.)
3. **PendingDeletion** — the owning cluster has gone away (or moved to a
   different name). The cleanup controller sets `CleanupTime = now + 1 week`.
   The name is now in its reuse cooldown.
4. **deleted** — once `CleanupTime` passes, the cleanup controller deletes the
   document and the name returns to the pool.

## Expiry timers

There are two independent timers, serving two different purposes:

### `MustBindByTime` — 61 minutes (unbound-reservation expiry)

Set to `now + 61m` when a reservation is created in the `Pending` state. It bounds
how long a reservation may sit **unclaimed**. If a cluster never binds the
reservation by this deadline, the cleanup controller deletes it and the name is
freed immediately (no cooldown, because the name was never in active use). This is
what cleans up the *losers* of a create-time conflict retry: when the creation
controller reserves a name, fails to record the pointer, and then reserves a
different name on retry, the first reservation is left orphaned in `Pending` and
is reaped once its 61-minute window expires.

### `CleanupTime` — 1 week (name-reuse cooldown)

Set to `now + 1 week` when a **bound** reservation's owning cluster is deleted or
moves to a different name. The name is **not** returned to the pool immediately;
it is held in `PendingDeletion` for a full week. This prevents a freshly-created
cluster from re-acquiring a DNS name that resolvers and customers may still cache
and associate with the old cluster. Only after the week elapses does the cleanup
controller delete the reservation and free the name.

## What eventually reaps reusable names

The **`DNSReservationCleanupController`** is the sole reaper. It watches all
`DNSReservation`s (via a shared informer) and, on each reconcile, evaluates the
reservation against ten cases and drives it toward the correct state. The ten
cases are, in evaluation order:

| # | Condition | Action |
|---|-----------|--------|
| 1 | `CleanupTime` is set and in the past | Delete the reservation (cooldown elapsed → name reusable) |
| 2 | `CleanupTime` is set and in the future | Wait (still cooling down) |
| 3 | Owning cluster gone **and** `Bound` | Mark `PendingDeletion`, `CleanupTime = now + 1 week` |
| 4 | Owning cluster gone **and** `Pending` | Delete immediately (never bound, no cooldown) |
| 5 | Cluster points to this reservation **and** `Bound` | Steady state — no-op |
| 6 | Cluster points to this reservation **and** not `Bound` | Fix state to `Bound` (repairs a failed best-effort bind) |
| 7 | Cluster has no reservation, this is `Pending`, `MustBindByTime` not expired | Wait (may still bind) |
| 8 | Cluster has no reservation, this is `Pending`, `MustBindByTime` expired | Delete immediately (never bound) |
| 9 | Cluster points to a **different** reservation **and** this is `Pending` | Delete this extra reservation |
| 10 | Cluster points elsewhere / has none **and** this is `Bound` | Mark `PendingDeletion`, `CleanupTime = now + 1 week` (cluster likely deleted & recreated) |

Re-enqueues of the same reservation are throttled by a one-hour time-based
cooldown so the coarse informer resync does not hot-loop; errors still requeue
immediately with rate-limited backoff.

## How cluster deletion works

When a cluster that owns a bound reservation is deleted, its
`ServiceProviderCluster` disappears. The cleanup controller detects this:

- **Case 3** (owning cluster gone, reservation `Bound`): the reservation is moved
  to `PendingDeletion` with `CleanupTime = now + 1 week`.
- **Case 1** (one week later): the reservation is deleted and the DNS name is
  returned to the pool for reuse.

The same one-week cooldown applies via **case 10** when a cluster is deleted and
recreated: the recreated cluster reserves a *new* name, and the old, now-detached
`Bound` reservation is moved to `PendingDeletion` for a week before its name can
be reused.

Reservations that were never bound (e.g. conflict-retry losers, or a cluster
deleted before it ever bound a name) skip the cooldown entirely and are deleted
immediately via **cases 4 and 8**, since a name that was never in active use
carries no risk of stale DNS caching.

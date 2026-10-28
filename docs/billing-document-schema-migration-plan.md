# Billing document schema migration

## Goal and order

Migrate ARO-HCP and Billing together from today's flat billing document to a nested `properties` payload with metadata suitable for generic CRUD. Complete the schema migration first; adopt generic CRUD afterward. A container migration is an optional separate follow-up.

## Steps

1. **Define the target schema:** use `GenericDocument[T]` and `CosmosMetadata` as references; agree on the billing payload under `properties`, metadata, and field ownership. Keep billing `deletionTime` distinct from generic soft-delete `deletionTimestamp`.
2. **Implement shared compatibility support:** define old/new representations and conversions in the public module; integrate both repositories with atomic, ETag-protected dual writes and old-authoritative reads.
3. **Build and validate migration support:** implement direct Billing-container backfill, verification, and cleanup; test concurrency, mixed versions, restart recovery, and real billing processing before rollout.
4. **Deploy dual writers:** update both services and verify every writer in the migration scope supports both representations before relying on new data.
5. **Backfill and verify:** reconcile new data from old and confirm complete coverage and semantic agreement; resolve failures before switching authority.
6. **Switch reads:** stop or fence old-to-new backfill, deploy new-authoritative readers and queries in both services, and continue dual writing through validation.
7. **Stop old writes:** deploy new-only writers after all readers use new data. Treat old fields as stale and retire old-reading rollback targets.
8. **Clean up:** remove only obsolete legacy fields, preserving required routing metadata; verify completion and remove compatibility code. Address generic CRUD adoption and optional container migration separately afterward.

## Stage gates

| Stage | Read authority | Writes | Gate before advancing |
|---|---|---|---|
| 1. Expand | Old | Old and new | All writers in both services are dual-write capable; old-only instances are gone. |
| 2. Backfill | Old | Old and new | Migrate all billing records from old to new; verify complete coverage, valid metadata, and semantic agreement. |
| 3. Switch reads | New | Old and new | Both services, including queries and informers, use new data; validate real billing processing. |
| 4. Stop legacy writes | New | New only | All consumers use new data and all writers have stopped updating old fields. |
| 5. Cleanup | New | New only | Remove obsolete old fields, verify completion, then remove compatibility code. |

## Required guarantees

- **Preserve identity and behavior:** keep existing document IDs, partition values, cluster references, event IDs, billing timestamps, watermark semantics, and retention policy.
- **Keep routing metadata:** the current Billing container uses `/subscriptionId`. That top-level field must remain even after legacy payload cleanup. Adding `partitionKey` does not change the container's partition-key path.
- **Atomic, conditional writes:** update both representations in one document operation using verified ETag enforcement. Preserve other writers' and unknown fields. After conflicts, re-read and recompute rather than retry stale decisions with fresh ETags. This protection is required before dual writing, even though generic CRUD adoption comes later.
- **Explicit authority:** select one complete representation per stage; do not fall back field-by-field based on nil or zero values. Backfill old → new only while old is authoritative. Stop or fence incompatible migration workers before switching authority.
- **Complete, restart-safe migration:** scan the Billing container directly, including deleted, orphaned, and unlinked records. Reconcile stale new copies as well as missing ones. Make updates idempotent and progress reconstructible or durable; failures and incomplete scans must block promotion.
- **Rollback boundary:** after new-only writes begin, old data is stale. Rolling back to old-reading code requires rebuilding the old representation from new first.

## Decisions before implementation

1. Exact new JSON layout, metadata, field ownership, and `InstanceVersion` rules across both services; keep customer cluster identity separate from any future billing-record resource path.
2. Shared-module publication/adoption order and whether it shares only types/conversions or also conditional-storage primitives.
3. Verified write mechanism, handling of malformed records or missing new data, and exact legacy-field cleanup list.
4. Release/configuration controls, migration scope, promotion evidence, rollback procedure, and stale-worker fencing.
5. Extend the existing migration controller or add a Billing-specific controller. The current controller does not scan the Billing container; its in-memory completion tracking is insufficient as a promotion gate.

## Validation

Test old-only, dual, and new-only records; null/malformed/unknown fields; mixed-version writers; stale ETag rejection; concurrent cutoffs, candidacy, and watermarks; interrupted/repeated migration; and phase transitions. Verify unchanged billing payloads and event identities, including submission followed by checkpoint failure and replay. Require real test-environment billing processing before rollout, not just serialization tests or document counts.

## Later work

- **Generic CRUD:** support Billing's existing IDs consistently across storage operations and review deletion semantics. Decide whether billing records need their own internal resource paths; this need not change their Cosmos IDs or other document families' identities.
- **Container migration:** optionally move to `/partitionKey` in a new container. Design a separate handoff covering concurrent updates/deletes, new ETags, rollback, and one authoritative billing processor; cross-container writes are not atomic.

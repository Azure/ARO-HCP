# Billing document schema migration plan

Status: agreed migration sequence; detailed schema and execution controls require
the decisions listed below. Recorded October 24, 2026.

## Objective and sequence

Migrate the persisted billing document from its current flat representation to a
representation compatible with ARO-HCP's generic Cosmos CRUD conventions, while
ARO-HCP and HCP Billing continue operating on the same records.

The work is sequenced as follows:

1. Define the target schema and transitional shared contract.
2. Complete the coordinated, in-place old/new schema migration.
3. Adopt generic CRUD and resolve its document-identity assumptions separately.
4. Consider a separate container migration afterward.

The schema migration preserves existing billing behavior and document identity.
It does not change billing start/stop policy, watermark semantics, usage-event
identity, retention policy, or resolve historical duplicate billing records.

## Terminology

- **Old representation:** BillingDocument as stored today, with billing fields
  at the document's top level.
- **New representation:** the agreed nested billing payload and storage metadata
  needed for eventual generic CRUD integration. The current generic serializer
  uses the JSON field `properties`; its Go field is named `Content`.
- **Dual write:** one atomic update of both representations in the same Cosmos
  document, derived from one proposed logical state.
- **Authoritative representation:** the source used for processing decisions,
  queries, and reconciliation when representations are missing or disagree.
- **Migration scope:** an explicitly identified set of Cosmos accounts,
  databases, containers, and service deployments whose writers participate in
  one promotion decision. Scope boundaries must be verified before execution.

## Invariants throughout the schema migration

### Preserve storage addresses and billing identity

- Keep the existing Billing container and every document's current `id` and
  partition-key value. Do not derive replacement IDs from cluster ARM paths.
- Keep the customer cluster's `resourceId` meaning intact. It is a reference to
  the billed cluster, not necessarily a unique identity for a billing record.
- Preserve cluster-to-billing-document references and hosting event hash inputs.
- Preserve historical records for distinct cluster incarnations, even when they
  refer to the same customer ARM resource path.
- The existing Billing container is partitioned by `/subscriptionId`. Retain that
  top-level field permanently for this container, including after legacy payload
  cleanup. Adding `partitionKey` does not change the configured partition path.
- If the target envelope also contains `partitionKey`, define and validate its
  equality with the existing routing value. Do not normalize or relocate existing
  partition values as a side effect of this migration.

### Establish authority explicitly

- Each stage selects one authoritative representation for equivalent business
  fields. Do not merge individual fields using nonzero/non-null preference.
- Preserve meaningful absent/null states, particularly cutoffs and watermarks.
  A nil new cutoff must not resurrect an old cutoff through fallback.
- During old-authoritative stages, backfill flows old to new. During
  new-authoritative stages, any compatibility repair flows new to old.
- Treat disagreement as observable migration state. Repair only from the source
  authorized for the current stage; never select the largest watermark or newest
  timestamp as a generic conflict-resolution rule.
- Once new-only writes begin, old fields are stale and cannot authorize decisions.

### Make writes atomic and concurrency-protected

- Both services and the migrator must use a verified optimistic-concurrency
  mechanism against the ETag of the snapshot used to make each update decision.
- Persist both representations in a single atomic document operation. Two
  sequential requests, one per representation, are not a dual write.
- Preserve fields owned by the other service, unknown fields, and storage metadata
  as appropriate for the selected operation. A full replacement requires faithful
  preservation of the complete document, not just the fields currently modeled.
- A field-specific writer updates both copies of its owned fields. If it also
  initializes or repairs the complete new representation, derive that output from
  the complete authoritative snapshot protected by the same ETag.
- On a conflict, obtain refreshed state and recompute. Do not retry a stale
  proposal with a newly fetched ETag. Carry returned ETags into subsequent writes
  or deletion decisions.
- Verify actual service enforcement; the presence of a PATCH If-Match SDK option
  or a mock assertion is insufficient evidence.
- Implement these guarantees in the existing storage adapters for the migration.
  Full generic CRUD adoption is not a prerequisite.

## Stage plan

| Stage | Read authority | Service writes | Migration action |
|---|---|---|---|
| 0. Prepare | Old | Old | Define contract, controls, and validation |
| 1. Expand | Old | Old and new | Deploy dual-write support |
| 2. Backfill | Old | Old and new | Reconcile new representation from old |
| 3. Switch reads | New | Old and new | Switch both services and query paths |
| 4. Stop legacy writes | New | New only | Retire old-field updates |
| 5. Contract | New | New only | Remove obsolete legacy fields |

### Stage 0: prepare the contract and implementation

- Specify exact JSON names, nesting, required metadata, field ownership,
  absent/null behavior, and version initialization/increment rules.
- Distinguish any schema/migration version from `InstanceVersion`, which records
  document updates, and from Cosmos ETags, which enforce write concurrency.
- Keep the shared contract externally importable by Billing. Do not introduce an
  import of ARO-HCP's Go `internal` packages into the public module.
- Inventory every reader and writer: creation, lifecycle cutoffs, orphan
  candidacy, managed-RG repair, hosting/compute checkpoints, cleanup, queries,
  informers, diagnostic tools, migrations, and manual repair tools.
- Inventory legacy documents, malformed fields, duplicate records, and any
  document TTLs. Define an explicit disposition for records that cannot migrate.
- Implement and test the compatibility transformations and atomic write path in
  both repositories. Agree on release/configuration controls and rollback support.

Exit gate: target schema, writer coverage, phase controls, malformed-record policy,
and test approach are agreed; concurrency behavior is demonstrated against Cosmos
or an appropriate emulator before progressing to operational rollout.

### Stage 1: expand with old-authoritative reads and dual writes

- Deploy versions of ARO-HCP and Billing that read old data and atomically write
  both representations for their operations. New documents contain both.
- Keep old data authoritative throughout the mixed-version rollout. Old-only
  writers can leave new fields stale even if a document was previously backfilled.
- Ensure serialization preserves fields needed by older and newer participants.

Exit gate: every writer in the migration scope is confirmed dual-write capable;
old-only instances, jobs, and repair paths cannot continue writing unnoticed.
Deployment completion alone is not evidence that all documents are synchronized.

### Stage 2: backfill and verify

- Sweep the Billing container directly, including active, deleted, orphaned, and
  unlinked documents. Do not rely on existing cluster or subscription documents
  to discover the full billing population.
- Convert from the authoritative old representation. Reconcile existing new
  payloads too: checking only whether `properties` exists misses stale dual data.
- Use conditional, idempotent updates; re-read and recompute after conflicts.
  Skip already-correct records to avoid unnecessary ETag/version/TTL churn.
- Check all pages and late iterator failures. Report incomplete scans, failed
  records, and unresolved mismatches explicitly.
- Make progress and completion reconstructible from durable data. Persist any
  checkpoint or obligation that cannot safely be reconstructed by another scan.
- Account for concurrent creation, updates, and deletion without recreating a
  deleted record. Respect per-document TTL semantics; migration writes change
  Cosmos `_ts` and can extend expiration.

Exit gate: a complete verification of the defined scope establishes valid new
payloads/metadata, semantic agreement, and no unresolved missing, malformed, or
divergent records. All writers maintain that invariant. An observed marker alone
does not prove completeness, and completion evidence must identify the applicable
migration phase and scope.

### Stage 3: switch to new-authoritative reads, retaining dual writes

- Disable or retire old-to-new backfill before new authority takes effect.
- Switch processing, query predicates, projections, and informer decoding to the
  new representation. Continue writing both representations atomically.
- Mixed read versions are acceptable only while both representations retain
  equivalent meaning and all writers preserve their agreement.
- Define explicit handling for missing/invalid new data. Recommended policy:
  hold the affected document and report it rather than silently fall back to old
  data. This policy remains an open decision.
- Validate real hosting and compute processing, including checkpoint conflicts,
  retries, and final-cutoff behavior, against the new representation.

Exit gate: all consumers in scope use new-authoritative reads, real processing is
validated, and compatibility copies remain synchronized.

### Stage 4: stop writing legacy payload fields

- Deploy new-only writers after every consumer uses the new representation.
- Retain the old fields temporarily; they may now become stale. They must not be
  read or used as a source for repair or backfill.
- Preserve required top-level routing and envelope metadata.

Exit gate: every writer is new-only and every reader is new-authoritative; no
remaining service, job, migration, or rollback target depends on current old data.

### Stage 5: remove legacy payload fields

- Run a conditional, idempotent cleanup sweep that removes only the agreed list
  of obsolete fields. Preserve `id`, `/subscriptionId` routing, Cosmos metadata,
  the new envelope, and unrelated/unknown fields.
- Verify complete cleanup without altering billing values or event identities.
- Remove temporary old-schema decoding, dual-write logic, and migration controls
  after all participants and operational tooling support the contracted schema.

Exit gate: all retained documents and deployed participants satisfy the final
contract; cleanup failures and exceptions are resolved explicitly.

## Migrator choice

The current backend `CosmosMigration` controller walks Resources and kube-applier
documents; it does not directly enumerate the Billing container. Its per-process
completed-subscription map is not a durable migration-completion gate.

Proposed approach: a Billing-specific reconciler using reusable storage/retry
machinery, with direct container coverage and phase-aware transformations. Decide
between extending the existing controller and introducing a dedicated controller
after checking discovery, restart, rate-limiting, and observability requirements.

## Rollback and coordination

- Gate promotions using verified deployment state for both services and complete
  data validation for the same scope. Do not infer readiness from timing alone.
- While dual writing maintains equivalence, old-reading versions remain potential
  rollback targets. Returning to old-only writers requires another synchronization
  gate before a later attempt to promote new reads.
- From Stage 4 onward, rollback to an old-reading version requires an explicit
  recovery procedure that restores old data from new; a binary rollback alone is
  unsafe. Retaining stale old fields does not extend the rollback window.
- A phase-specific backfill or cleanup must not continue across an incompatible
  authority transition. Define how stale workers are stopped or fenced.
- The user owns deployment execution and phase promotion. This plan is not
  authorization to mutate live infrastructure or billing documents.

## Validation required

- Fixed-fixture round trips for old-only, dual, and new-only documents; missing,
  null, malformed, unknown, and conflicting fields; identity and partition values.
- Every writer preserves the other service's fields and writes both copies
  atomically in the dual-write phases.
- Real stale-version rejection between RP, Billing, and migration workers,
  including candidate cancellation, cutoff recording, and watermark advancement.
- Restart and partial-failure cases: interrupted pages, late query failures,
  throttling, conflicts, concurrently created/deleted records, and repeated sweeps.
- Mixed-version readers/writers and phase-transition fencing; stage-appropriate
  rollback behavior and direct enumeration of records without live clusters.
- Billing event equivalence and replay: identical subjects, quantities, timestamps,
  and event IDs, including submission success followed by checkpoint conflict.
  Cosmos concurrency does not make AUB submission and checkpointing atomic.
- End-to-end test-environment processing before canary/production. Compilation,
  document counts, and no-op runs do not establish real billing correctness.

## Work after the schema migration

### Generic CRUD adoption and identity

Evaluate using explicit existing Cosmos IDs versus assigning billing records their
own internal resource paths. A customer cluster reference and a billing-record
identity must remain distinct. Preserve existing IDs unless a separate identity
migration is explicitly agreed.

Review all generic creation, serialization, lookup, replacement, transaction, and
deletion paths. Existing internal resources use resource-shaped paths and generally
derive Cosmos IDs from those paths; some helpers bypass a document-specific
`GetCosmosUID()` by calling the embedded metadata implementation. Also review
generic soft-delete behavior against Billing's cleanup requirements.

Adding a resource path does not by itself require changing current Cosmos IDs.
Preserve other document families' existing behavior and verify their regressions.

### Optional container migration

A later container can use `/partitionKey`, retaining subscription-based values or
adopting a separately justified partitioning strategy. Changing only the path name
does not change how records are grouped.

Design that move separately: cross-container writes are not atomic, destination
ETags differ, updates and deletes must be propagated, and Billing must process
only the authoritative copy. Agree on a write-pause or durable online handoff,
catch-up verification, rollback, and retirement before provisioning it.

## Decisions still required before implementation

1. Exact new payload/envelope and metadata layout, including handling of required
   routing fields and separation of a future record path from the cluster ARM ID.
2. Whether to publish/adopt the flat shared module first or introduce migration
   support before its initial adoption in Billing.
3. Shared module scope: wire types/conversions only, or independently reusable
   conditional-storage primitives as well.
4. Verified write mechanism for the migration and complete-document preservation
   if replacement is selected.
5. `InstanceVersion` ownership, initialization, and atomic increments by every
   participating writer; retain ETag-based informer versions until a coordinated
   alternative is agreed and verified.
6. Release-based versus configuration-based stage control, migration scopes,
   stale-worker fencing, promotion evidence, and rollback procedures.
7. Missing/invalid new-data policy and disposition of malformed legacy records.
8. Migrator placement, scan/checkpoint design, observability, operational limits,
   and exact cleanup field list.

## Repository references

- Shared schema: `billingapi/types.go`.
- RP wrapper: `internal/database/cosmosstorage/billingcosmosstorage/types_billingdocument.go`.
- Generic envelope: `internal/database/cosmosstorage/cosmosstorageutils/types_generic.go`
  and `types_typeddocument.go` in the same directory.
- Metadata contract: `internal/api/coreapi/arm_types_cosmosdata.go`.
- Generic conversion and CRUD: `internal/database/cosmosstorage/cosmosstorageutils/`.
- Existing migration controller: `backend/pkg/controllers/cosmosmigration/cosmos_migration.go`.
- Container partition configuration: `dev-infrastructure/modules/rp-cosmos.bicep`.
- Billing consumer: `/home/abbyduke/Billing/pkg/database/` and its processing callers.

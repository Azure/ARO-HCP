# Cosmos Resource Deletion via Soft-Delete

## Problem

Cosmos DB's "latest version" change feed mode does not surface hard deletions.
When a resource is deleted via `DeleteItem`, the change feed never sees that event, which means
controllers watching through `ChangeFeedListWatcher` cannot react to deletions until the next
full relist (up to 30 minutes later). This breaks correctness guarantees for controllers that
need to act promptly on resource removal.

## Solution: DeletionTimestamp + TTL

Instead of hard-deleting documents with `DeleteItem`, we implement a two-phase soft-delete:

1. **Mark phase** (immediate): Set `DeletionTimestamp` on the document and set the Cosmos TTL
   to 30 seconds. This is a Replace operation, which the change feed **does** surface.
2. **Cleanup phase** (automatic): After the TTL expires, Cosmos DB automatically removes the
   document from storage.

### Document Schema Change

`TypedDocument` gains a new field:

```go
type TypedDocument struct {
    BaseDocument
    PartitionKey      string                `json:"partitionKey"`
    ResourceID        *azcorearm.ResourceID `json:"resourceID"`
    ResourceType      string                `json:"resourceType"`
    DeletionTimestamp *metav1.Time          `json:"deletionTimestamp,omitempty"`
    Properties        json.RawMessage       `json:"properties"`
}
```

When `DeletionTimestamp` is nil (or absent from the JSON), the document is live. When set, the
document is logically deleted and awaiting Cosmos TTL cleanup.

### CRUD Behavior Changes

#### Delete

When `Delete` is called, the CRUD helper:

1. Reads the document from Cosmos (`ReadItem`).
2. Sets `deletionTimestamp` to the current time.
3. Sets `ttl` to 30 (seconds after the write timestamp `_ts`).
4. Increments `properties.cosmosMetadata.instanceVersion` so the change feed recognizes the update.
5. Replaces the document with an etag precondition (optimistic concurrency).

If the document is already not found, the delete is a no-op (idempotent).

#### Get / GetByID

After reading a document, the CRUD layer checks for `DeletionTimestamp`. If it is non-nil,
the method returns a 404 Not Found error as if the document does not exist. This ensures
callers never see a logically-deleted document through the typed CRUD layer.

#### List (including global listers)

All list queries gain an additional WHERE clause:

```sql
AND (NOT IS_DEFINED(c.deletionTimestamp))
```

Since `deletionTimestamp` uses `omitempty` in its JSON tag, live documents have no
`deletionTimestamp` field in their JSON at all. The `IS_DEFINED` check is both correct
and index-friendly. This filter applies to:

- Per-partition scoped lists (`nestedCosmosResourceCRUD.List`)
- Cross-partition global listers (`cosmosGlobalLister.List`)
- Active operations lister (`cosmosActiveOperationsGlobalLister.List`)

### Change Feed Behavior

The `processDocument` method in `ChangeFeedWatcher` checks `DeletionTimestamp` on every
document surfaced by the change feed:

- **DeletionTimestamp is non-nil AND the resource was previously seen** (in the list snapshot
  or a prior change feed event): Deliver a `watch.Deleted` event with the current document
  content.
- **DeletionTimestamp is non-nil AND the resource was NOT previously seen**: Skip silently.
  This handles the case where a resource was created and deleted between relists.

This means controllers and informers receive timely Delete notifications without waiting for
a relist cycle.

### Mock Implementation

The in-memory mock (`MockResourcesDBClient`) mirrors production behavior:

- `Delete` performs a read-modify-write that sets `DeletionTimestamp` and `TTL`, then calls
  `StoreDocument` (which records to the mock change feed).
- `GetDocument` returns the raw document regardless of deletion status (the CRUD layer does
  the filtering).
- `ListDocuments` filters out documents with `DeletionTimestamp` set.
- The mock change feed sees the soft-delete as a normal mutation (since it goes through
  `StoreDocument`), so changefeed-based tests work identically.

### Timeline

```
T+0s   Delete() called
       ├─ ReadItem (get current doc + etag)
       ├─ Set deletionTimestamp = now, ttl = 30
       ├─ Increment instanceVersion
       └─ ReplaceItem (conditional on etag)

T+0-1s Change feed surfaces the updated document
       └─ ChangeFeedWatcher delivers watch.Deleted event

T+30s  Cosmos TTL expires
       └─ Document physically removed from storage
```

## Integration Tests

The following test scenarios verify the end-to-end behavior:

1. **Changefeed delivers Delete event**: Create a cluster, start list/watch, delete the
   cluster, verify a `watch.Deleted` event arrives with the document content.

2. **Changefeed + lister reports not-found**: Create a cluster, start an informer, delete
   the cluster, verify the informer delivers a Delete event and the lister returns not-found.

3. **Changefeed + relist has no extra records**: Create a cluster, delete it, then perform a
   fresh list/watch cycle. Verify no events are delivered for the deleted cluster (it should
   not appear in either the list result or as a change feed event).

4. **Cosmos TTL cleanup**: Create a cluster, delete it (sets 30s TTL), wait for TTL to expire,
   then verify the document is physically absent from Cosmos. This test runs only against the
   real Cosmos emulator.

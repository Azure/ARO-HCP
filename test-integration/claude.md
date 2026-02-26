# test-integration

Artifact-driven integration tests for frontend, backend, and admin services. Tests run against a mock Cosmos DB by default.

## Directory Layout

```
test-integration/
├── admin/                          # Admin API integration tests
├── backend/                        # Backend controller tests
├── frontend/                       # Frontend service tests
├── utils/
│   ├── databasemutationhelpers/   # Step framework, assertions, step_*.go implementations
│   ├── integrationutils/          # Test infrastructure (mock cosmos, cluster service mock, HTTP servers)
│   └── controllertesthelpers/     # Controller test framework
```

## How Tests Work

Tests are **declarative**: you define a sequence of numbered step directories under an artifacts tree. The framework discovers them automatically via `//go:embed artifacts` and `fs.ReadDir()`.

### Artifact tree structure

```
artifacts/<SuiteName>/<ResourceType>/<TestCase>/
  00-load-initial-state/
  01-httpCreate-resource/
  02-httpGet-resource/
  ...
  99-cosmosCompare-end-state/
```

- Each test case is a directory containing numbered step subdirectories
- Steps execute sequentially, sorted by their numeric prefix (`NN-`)
- Step type is parsed from the directory name: `NN-<stepType>-<description>/`

### Adding a new test

1. Create a new directory under the appropriate `artifacts/<Suite>/<ResourceType>/` path
2. Add step directories with the `NN-<stepType>-<description>/` naming convention
3. Put JSON files in each step directory (resource documents, expected results, keys)
4. The test framework discovers it automatically -- no Go code changes needed

## Step Types

### Loading steps

| Step type | Purpose |
|-----------|---------|
| `load` / `loadCosmos` | Load raw JSON documents directly into cosmos |
| `loadClusterService` | Load mock cluster service state (files suffixed `-cluster.json`, `-nodepool.json`, `-externalauth.json`, `-autoscaler.json`, `-hypershiftdetails.json`, `-provisionshard.json`) |
| `kubernetesLoad` | Create Kubernetes resources via fake clientsets |
| `kubernetesApply` | Update existing K8s resources from JSON/YAML files. Can be partial files. |
| `migrateCosmos` | Trigger database schema migrations |

### HTTP API steps (ARM REST calls)

| Step type | Purpose | Compares response? |
|-----------|---------|--------------------|
| `httpGet` | GET a single resource | Yes, via `ResourceInstanceEquals` |
| `httpList` | LIST resources | Yes, each item compared |
| `httpCreate` / `httpReplace` | PUT a resource | No (checks error only) |
| `httpPost` | POST a resource | No (checks error only) |
| `httpPatch` | PATCH a resource | No (checks error only) |
| `httpDelete` | DELETE a resource | No (checks error only) |

### Database CRUD steps (direct DB operations)

| Step type | Purpose |
|-----------|---------|
| `create` | Create resource via DB CRUD API |
| `get` | Get resource by parsed ResourceID |
| `getByID` | Get by cosmos ID |
| `list` | List resources in container |
| `replace` | Replace entire resource |
| `replaceWithETag` | Replace with optimistic concurrency |
| `delete` | Delete resource |
| `listActiveOperations` | List in-progress operations for a resource |

### Untyped database steps

| Step type | Purpose |
|-----------|---------|
| `untypedGet` | Get untyped document |
| `untypedList` | List untyped documents |
| `untypedListRecursive` | Recursively list untyped descendants |
| `untypedDelete` | Delete untyped document |

### Assertion / control steps

| Step type | Purpose |
|-----------|---------|
| `cosmosCompare` | Assert entire cosmos state matches expected JSON documents |
| `kubernetesCompare` | Assert K8s resource state matches expected JSON files (uses `ResourceInstanceEquals`) |
| `completeOperation` | Mark an async operation as succeeded |

## Step Directory Contents

Most steps contain:
- `00-key.json` -- identifies the target resource (format varies by step type)
- One or more `*.json` files -- resource documents or expected results
- `expected-error.txt` (optional) -- expected error substring for error-checking steps

### Key file formats

**HTTP steps:**
```json
{"resourceID": "/subscriptions/.../providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/name"}
```

**Typed CRUD steps:**
```json
{
  "parentResourceId": "/subscriptions/.../providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/name",
  "resourceType": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/hcpOpenShiftControllers"
}
```

**Untyped recursive steps:**
```json
{
  "parentResourceId": "/subscriptions/.../...",
  "resourceType": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
  "descendents": [
    {"resourceType": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools", "resourceName": "pool-name"}
  ]
}
```

**Kubernetes steps** (`kubernetesLoad`, `kubernetesApply`, `kubernetesCompare`):

- No key file needed
- Place standard Kubernetes resource JSON/YAML files directly in the step directory
- Resources are identified by their `apiVersion`, `kind`, `metadata.name`, and optionally `metadata.namespace`

## Cosmos Document ID Format

The `.id` field in cosmos documents is a UUID V5 derived from the resource's ARM resource ID:
- Lowercase the entire resource ID
- Generate a UUID V5 using a fixed namespace UUID (`bf1ee0a1-0147-41ed-a083-d3cbbf7bea99`) and the lowercased resource ID as the name

Example: `/subscriptions/AAA/resourceGroups/BBB/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/CCC`
becomes: `<uuid-v5-hash>` (e.g., `a1b2c3d4-e5f6-5789-abcd-ef0123456789`)

The UUID V5 format provides:
- Deterministic IDs (same resource ID always produces the same UUID)
- Compliance with Cosmos DB key length limits
- URL-safe characters (no `/` or `|` delimiters)

Implementation: `internal/api/arm/types_cosmosdata.go:ResourceIDStringToCosmosID()`

## ResourceInstanceEquals (comparison logic)

`per_resource_comparer.go` -- the core assertion function used by all comparison steps.

**Always stripped from both expected and actual before comparison:**
- Cosmos internals: `_rid`, `_self`, `_etag`, `_attachments`, `_ts`
- `cosmosMetadata.etag`
- `endTime` (for operations)
- Timestamps: `startTime`, `lastTransitionTime`, `operationId`
- Internal tracking: `activeOperationId`, `internalId` (including under `intermediateResourceDoc`)
- Controller condition `lastTransitionTime` entries

**Stripped only for Operation resources** (detected via `resourceType` field or `operationId` presence):
- `id`, `resourceId`, `cosmosMetadata` -- these are UUID-based for operations

**Not stripped (now compared) for non-operation resources:**
- `id` -- must match the pipe-delimited cosmos ID for cosmos documents, or the ARM resource ID for HTTP responses

## Running Tests

```bash
go test ./test-integration/frontend/...
go test ./test-integration/backend/...
go test ./test-integration/admin/...
```

Tests run against mock infrastructure by default. Set `FRONTEND_SIMULATION_TESTING=true` to also run against real Cosmos DB.

## Key Source Files

- `utils/databasemutationhelpers/resource_crud_test_util.go` -- test orchestration, step discovery
- `utils/databasemutationhelpers/per_resource_comparer.go` -- assertion/comparison logic
- `utils/databasemutationhelpers/step_*.go` -- individual step implementations
- `utils/integrationutils/utils.go` -- test infrastructure setup (mock cosmos, HTTP servers)
- `utils/integrationutils/kube_mock.go` -- fake Kubernetes clientsets with informer support
- `utils/integrationutils/cluster_service_mock.go` -- OCM cluster service mock
- `internal/databasetesting/mock_dbclient.go` -- in-memory cosmos mock

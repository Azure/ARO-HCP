# Regenerate the field-level writer mapping

Work from the current checkout of ARO-HCP. Regenerate
`docs/generated/written-by-mapping.json`.
The mapping tracks which controllers and frontend actions write each Cosmos
struct field. This prompt is maintained separately from the generated output.

## Establish scope and evidence

1. Read repository and applicable directory guidance.
2. Inventory **every concrete controller** that writes to Cosmos struct fields.
   Follow constructors through application startup to actual `Run` calls.
   Include each controller registered via `controllerregistry.Registration` in
   `registration.go` files. Cover backend, fleet, kube-applier, mgmt-agent,
   and sessiongate.
3. Inventory every mutating frontend endpoint in `frontend/pkg/frontend/` and
   admin actions in `admin/server/`.
4. For each controller and frontend action, trace through the implementation to
   identify every Cosmos struct field it writes. Follow delegated helpers,
   desire builders, storage calls, and inline assignments. Read implementations
   and delegated helpers, not just comments or names. Cross-check each write
   against the actual field assignment, storage call, or patch builder.
5. Only include fields in Go files under `arohcp-cosmos-client/api/`
   subdirectories that have `json:` struct tags. Exclude embedded structs
   that serve purely as containers (e.g. `CosmosMetadata`).

Useful entry points (discover new ones rather than treating this as a closed
list):

- `frontend/pkg/frontend/`, `admin/server/`
- `backend/pkg/app/backend.go`, `backend/pkg/controllers/`,
  `backend/pkg/azure/cachedreader/`, `backend/pkg/utils/validationutils/`
- `fleet/pkg/manager/manager.go`, `fleet/pkg/controllers/`
- `kube-applier/pkg/app/`, `kube-applier/pkg/controllers/`
- `mgmt-agent/cmd/`, `mgmt-agent/pkg/controller/`
- `sessiongate/pkg/controller/` and its startup wiring
- `internal/controllerutils/`, `backend/pkg/utils/controllerutils/`,
  `internal/database/unioninformers/`
- `arohcp-cosmos-client/api/{coreapi,fleetapi,kubeapplierapi,metadataapi}/`,
  `arohcp-cosmos-client/apihelpers/`,
  `arohcp-cosmos-client/storage/`, `internal/database/{informers,listers}/`
- Azure client interfaces, OCM/Cluster Service adapters, desire builders,
  validation implementations, and lifecycle tests called by these controllers.

## Writer naming conventions

Use these canonical forms for writer names in the JSON output. Consistency is
critical because the codegen tool (`hack/writtenby/`) applies these names
directly into source comments.

- **Controller names**: Use the string value of the `*ControllerName` constant
  defined in the controller package (e.g. `ClusterDNSAllocation`,
  `ControlPlaneActiveVersions`, `PlacementController`). Do not abbreviate or
  paraphrase. Do not add trailing periods.
- **Frontend actions**: `Frontend {METHOD} {Resource}` with optional
  parenthetical qualifier for create-only or update-only paths.
  Examples: `Frontend PUT/PATCH Cluster`, `Frontend PUT Cluster (Create)`,
  `Frontend DELETE NodePool`.
- **Admin API actions**: `Admin API {HandlerName}` with optional parenthetical
  qualifier. Example: `Admin API RolloutVersionPinHandler (set/clear)`.
- **System/infrastructure writers**: Use a descriptive label when no controller
  name constant exists. Example: `Cosmos storage (persistence metadata)`.
- **No wildcards**: Expand wildcard references like `ClusterDeletion*
  controllers` into the individual controller names.
- **No trailing periods**: Writer names must not end with a period.
- **Parenthetical qualifiers**: When a writer only writes a field under
  specific conditions, add a parenthetical qualifier. Keep commas inside
  parentheses (they will not be split).
  Example: `Frontend PUT Cluster (Create)`.

## JSON output format

Produce a single JSON file at `docs/generated/written-by-mapping.json`. The
schema is:

```json
{
  "file-relative-to-api-dir": {
    "StructTypeName": {
      "FieldName": ["WriterA", "WriterB"]
    }
  }
}
```

Concrete rules:

- **Top-level keys**: File paths relative to `arohcp-cosmos-client/api/`
  (e.g. `coreapi/types_cluster.go`).
- **Second-level keys**: Go struct type names as declared in the source
  (e.g. `ClusterCustomerProperties`).
- **Third-level keys**: Go field names (not json tag names).
- **Values**: Arrays of writer name strings. Writers within each array must be
  sorted alphabetically (Go default string ordering). This ensures
  deterministic output and avoids churn.
- **Coverage**: Include every leaf struct field that any controller or frontend
  action writes. If a struct field has no known writers, omit it from the
  mapping (do not include an empty array).
- **Sort keys**: Sort all JSON object keys alphabetically at every level for
  deterministic output.

Example:

```json
{
  "coreapi/types_cluster.go": {
    "Cluster": {
      "CustomerProperties": [
        "ClusterBaseDomainPrefixSync",
        "ClusterDNSAllocation",
        "Frontend PUT/PATCH Cluster"
      ],
      "Identity": [
        "ClusterIdentitySync",
        "Frontend PUT/PATCH Cluster (Create/Update)"
      ]
    },
    "ClusterCustomerProperties": {
      "Version": [
        "Frontend PUT/PATCH Cluster"
      ]
    }
  }
}
```

## Applying annotations

After generating the JSON mapping, run the codegen tool to update the source
annotations:

```bash
go run ./hack/writtenby/
```

The tool reads `docs/generated/written-by-mapping.json` and updates
`// Written by:` comments in `arohcp-cosmos-client/api/`. Writers are sorted
alphabetically. The tool is idempotent: running it twice produces no diff.

## Verification and delivery

- Cross-check the generated mapping against existing `// Written by:`
  annotations in the source. Report any discrepancies (missing writers,
  extra writers, or fields with annotations not in the mapping) as a
  summary table in the PR description.
- Verify that every controller name in the mapping corresponds to an actual
  `*ControllerName` constant or frontend handler in the source.
- Verify that every field path in the mapping corresponds to an actual struct
  field in the source file.
- After running the codegen tool, verify that `go build ./...` still succeeds.
- Regeneration is a documentation task. Do not change runtime behavior or
  contact live Azure/Kubernetes resources to manufacture evidence.
- Keep this prompt in `docs/prompts/written-by-annotations.md`.

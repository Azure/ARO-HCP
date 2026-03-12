# Query Factory Refactoring Report

## Problem

The `QueryFactory` in `tooling/hcpctl/pkg/kusto/` had several issues:

1. **Half-finished safe/unsafe templating** -- the two-pass template rendering was stubbed but buggy (inverted parameter binding logic, debug output left in).
2. **KQL templates used wrong delimiters** -- all templates used `{{`/`}}` uniformly, but parameterizable fields (timestamps, cluster names) needed `<<`/`>>` delimiters for the first pass so they could be replaced with KQL parameter tag names in safe mode.
3. **Query templates scattered across modules** -- `test/cmd/aro-hcp-tests/custom-link-tools/` had its own KQL templates that duplicated logic already in the kusto package.
4. **Project fields not configurable** -- queries had hardcoded project lines that couldn't be extended, preventing reuse across must-gather and custom-link-tools contexts.
5. **Repetitive factory methods** -- every query method was 4-6 lines of copy-paste boilerplate (`templateData()` + set fields + `buildQuery()`), making additions error-prone.

## Changes Made

### Phase 1: Fix safe/unsafe templating

**Files:** `tooling/hcpctl/pkg/kusto/query.go`, `query_test.go`

- Fixed inverted `PreprocessParameterBindings` call: changed `PreprocessParameterBindings(f.UnsafeTemplating)` to `PreprocessParameterBindings(!f.UnsafeTemplating)`.
- Removed debug `fmt.Println` left in `buildQuery`.
- Fixed all `NewQueryFactory` call sites across the codebase to include the required `unsafeTemplating bool` parameter.
- Added tests for safe mode (KQL parameter tags in output, non-nil `*kql.Parameters`) and unsafe mode (literal values in output, nil parameters).

### Phase 2: Fix template delimiters

**Files:** 8 existing `.kql.gotmpl` templates under `tooling/hcpctl/pkg/kusto/templates/`

Changed `<<`/`>>` delimiters to only wrap fields that are addressable via KQL parameters:
- `TimestampMin`, `TimestampMax` (datetime values)
- `ClusterName` (infra cluster identifier)
- `SubResourceGroupId` (subscription/RG path)
- `ClusterId` (HCP cluster ID)
- `ResourceGroupName`

Fields like `Table`, `Limit`, `NoTruncation`, `HasClusterIds`, `ClusterIds`, `HCPNamespacePrefix` remain as `{{`/`}}` since they are structural (table names, boolean flags, array literals) rather than user-supplied values.

### Phase 3: Consolidate query templates

**Files:**
- Created 10 new templates in `tooling/hcpctl/pkg/kusto/templates/components/`
- Added 10 factory methods to `query.go` (BackendLogs, FrontendLogs, ClustersServiceLogs, ClustersServicePhases, MaestroLogs, HypershiftLogs, ACMLogs, BackendControllerConditions, HostedControlPlane, DetailedServiceLogs)
- Deleted 10 `.kql.tmpl` files from `test/cmd/aro-hcp-tests/custom-link-tools/artifacts/`
- Updated `test/cmd/aro-hcp-tests/custom-link-tools/options.go` to import and use `kusto.QueryFactory`

All component templates use `<<>>` for parameterizable fields and `{{.ProjectFields}}` for the project line (except `backend_controller_conditions` which has a fundamentally different output schema).

### Phase 4: Configurable project field merging

**Files:** `tooling/hcpctl/pkg/kusto/query.go`

- Added `StandardProjectFields` (`timestamp, log, cluster, namespace_name, container_name`)
- Added `MergeStandardProject` toggle on `QueryFactory`
- Added `mergeProjectFields()` helper: when enabled, starts with standard fields and appends any extras from the query-specific base set not already present
- Each `QueryDefinition` declares its base `ProjectFields`; the toggle controls whether standard fields get merged in

This allows must-gather to use the same query templates with compact project lines, while custom-link-tools gets the full standard set.

### Phase 5: Declarative QueryDefinition registry

**Files:** `tooling/hcpctl/pkg/kusto/query.go`, `templates.go`, `query_test.go`, `test/cmd/aro-hcp-tests/custom-link-tools/options.go`

Introduced `QueryDefinition` struct:

```go
type QueryDefinition struct {
    Name          string
    Table         string
    Database      string
    TemplatePath  string
    ProjectFields []string // nil = no project field substitution
}
```

- **19 package-level `QueryDefinition` vars** declare all queries (components, infra, kubernetes-events, services, HCP, discovery).
- **`Build(def QueryDefinition) (Query, error)`** is the generic constructor on `QueryFactory` -- handles `templateData`, project field merging, and `buildQuery` in one call.
- **`buildForTables(tables, base)`** helper builds one query per table from a base definition (used by `InfraServiceLogs` and `ServiceLogs`).
- **All simple factory methods** became one-liner wrappers: `func (f *QueryFactory) BackendLogs() (Query, error) { return f.Build(BackendLogsQuery) }`.
- **Special-case methods** (`HostedControlPlaneLogs`, `KubernetesEventsMgmt`, `ClusterNamesQueries`) retain their control flow but reference `QueryDefinition` vars instead of hardcoded strings.
- **`AllQueryDefinitions` slice** enumerates every registered definition for test validation.
- **`ListTemplatePaths()`** walks the embedded template filesystem to return all `.kql.gotmpl` paths.
- **custom-link-tools** now uses `QueryDefinition` vars directly: `svcFactory.Build(kusto.BackendLogsQuery)` instead of function references.

### Phase 6: Integrity tests

**File:** `tooling/hcpctl/pkg/kusto/query_test.go`

Three new tests ensure the registry stays correct:

| Test | What it catches |
|------|----------------|
| `TestQueryDefinitions_TemplatePathsExist` | A definition with a wrong/missing template path |
| `TestQueryDefinitions_NoDanglingTemplates` | A template file not referenced by any definition |
| `TestQueryDefinitions_NoDuplicateTemplatePaths` | A definition missing from `AllQueryDefinitions` (count mismatch) |

## Files Modified

| File | Change |
|------|--------|
| `tooling/hcpctl/pkg/kusto/query.go` | QueryDefinition, Build(), buildForTables(), AllQueryDefinitions, refactored all factory methods |
| `tooling/hcpctl/pkg/kusto/query_test.go` | Safe/unsafe tests, component tests, 3 integrity tests |
| `tooling/hcpctl/pkg/kusto/templates.go` | Added ListTemplatePaths() |
| `tooling/hcpctl/pkg/kusto/templates/components/*.kql.gotmpl` | 10 new component templates |
| `tooling/hcpctl/pkg/kusto/templates/{infra,hcp,services,kubernetes-events,discovery}/*.kql.gotmpl` | Delimiter fixes on 8 existing templates |
| `tooling/hcpctl/pkg/mustgather/gather.go` | Fixed NewQueryFactory calls (added unsafeTemplating param) |
| `tooling/hcpctl/pkg/mustgather/queries_test.go` | Fixed NewQueryFactory calls |
| `test/cmd/aro-hcp-tests/custom-link-tools/options.go` | Switched to kusto package queries, uses Build() with QueryDefinition vars |
| `test/cmd/aro-hcp-tests/custom-link-tools/artifacts/*.kql.tmpl` | 10 files deleted |

## Template Inventory (19 templates, 19 definitions)

```
templates/components/acm_logs.kql.gotmpl                    -> ACMLogsQuery
templates/components/backend_controller_conditions.kql.gotmpl -> BackendControllerConditionsQuery
templates/components/backend_logs.kql.gotmpl                 -> BackendLogsQuery
templates/components/clusters_service_logs.kql.gotmpl        -> ClustersServiceLogsQuery
templates/components/clusters_service_phases.kql.gotmpl      -> ClustersServicePhasesQuery
templates/components/detailed_service_logs.kql.gotmpl        -> DetailedServiceLogsQuery
templates/components/frontend_logs.kql.gotmpl                -> FrontendLogsQuery
templates/components/hosted_controlplane.kql.gotmpl          -> HostedControlPlaneQuery
templates/components/hypershift_logs.kql.gotmpl              -> HypershiftLogsQuery
templates/components/maestro_logs.kql.gotmpl                 -> MaestroLogsQuery
templates/discovery/cluster_id.kql.gotmpl                    -> ClusterIdQueryDef
templates/discovery/cluster_names.kql.gotmpl                 -> ClusterNamesQueryDef
templates/hcp/hcp_logs.kql.gotmpl                            -> HostedControlPlaneLogsQuery
templates/infra/kubernetes_events.kql.gotmpl                 -> InfraKubernetesEventsQuery
templates/infra/service_logs.kql.gotmpl                      -> InfraServiceLogsQuery
templates/infra/systemd_logs.kql.gotmpl                      -> InfraSystemdLogsQuery
templates/kubernetes-events/mgmt.kql.gotmpl                  -> KubernetesEventsMgmtQuery
templates/kubernetes-events/svc.kql.gotmpl                   -> KubernetesEventsSvcQuery
templates/services/service_logs.kql.gotmpl                   -> ServiceLogsQueryDef
```

## How to Add a New Query

1. Create a `.kql.gotmpl` template in the appropriate subdirectory under `templates/`.
2. Add a `QueryDefinition` var in `query.go`.
3. Add it to the `AllQueryDefinitions` slice.
4. Optionally add a typed wrapper method for IDE discoverability.
5. Run `go test ./pkg/kusto/...` -- the integrity tests will catch missing registrations or dangling templates.

## Verification

All tests pass across all three affected packages:

```
go test ./pkg/kusto/...                              # 35 tests
go test ./pkg/mustgather/...                         # 7 tests
go test ./cmd/aro-hcp-tests/custom-link-tools/...    # 2 tests
```

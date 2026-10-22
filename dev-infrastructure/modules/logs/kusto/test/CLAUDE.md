# Kusto Function Integration Tests

Declarative, artifact-driven integration tests for KQL stored functions. Runs a [Kusto emulator](https://learn.microsoft.com/en-us/azure/data-explorer/kusto-emulator-overview) (`kustainer-linux`) and validates function outputs against the same table schemas and function definitions deployed to production.

## Directory Layout

```
test/                                    # this directory (Go module: kustotest)
├── artifacts/                           # embedded test fixtures (//go:embed)
│   └── <Suite>/                         # e.g. ServiceLogs, HostedControlPlaneLogs, Catalog
│       └── <FunctionName>/              # function under test
│           └── <test-case>/             # one test case
│               ├── 00-ingestRows-<table>/
│               │   └── rows.json
│               ├── 01-invokeFunction-<name>/
│               │   ├── 00-key.json
│               │   └── expected-result.json
│               └── ...
├── kusto_test.go            # Test entry point: TestKustoFunctions (auto-discovers suites)
├── runner.go                # Walks artifact tree, wires emulator lifecycle
├── emulator.go              # Kusto emulator client (createDatabase, loadKQLFile)
├── embed.go                 # //go:embed artifacts/*
├── step.go                  # Step interface + NN-<type>-<name> discovery
├── step_ingest_rows.go      # Reads table schema via .show cslschema, builds datatable(), ingests
├── step_invoke_function.go  # Calls stored function, compares results
├── step_invoke_query.go     # Runs raw KQL, compares results
├── results.go               # Converts query.Dataset to []map[string]any
├── Makefile                 # test, fmt, verify-fmt targets
├── go.mod                   # standalone module
└── README.md                # human-facing documentation
```

Sibling directories consumed at runtime (via `KUSTO_KQL_ROOT`):

```
../tables/       # .kql files defining table schemas + inline transform functions
../functions/    # .kql files defining stored functions (organized by subdirectory)
```

## How Tests Work

1. The Makefile starts a `kustainer-linux` container (Kusto emulator)
2. `RunFunctionTests` computes database/table/function topology in-process (no manifest file; see Database Topology below), creates all databases, then all tables, then all functions
3. The runner walks `artifacts/<Suite>/<FunctionName>/<TestCase>/` discovering step directories
4. Steps execute sequentially within each test case against the database specified in each step's `00-key.json`
5. Between test cases, all tables across all databases are cleared for isolation

Tables are loaded before functions so that `.create-or-alter function` statements validate against real schemas. This is the same ordering `kustoctl validate` (`verify-kql`) uses.

### Database Topology

No manifest file. Topology is derived fresh on every run:

- **Functions**: every `.kql` file under `functions/`, grouped by database from its `{Database}_` filename prefix, ordered within each database via `tooling/kustoctl/pkg/kqlfunc`'s `// @depends:` parsing (same mechanism `kustoctl generate kql-bicep` uses).
- **Tables**: every `{"database": ..., "table": ...}` pair declared across all `00-ingestRows-*/00-key.json` files in `artifacts/`, regardless of which suite is running.

Filter and enrichment functions operate on a generic piped `T` parameter and never name a table; only `ingestRows` steps declare one.

### Debug Output

Set `KUSTO_DEBUG=1` for step-level logging (ingestion counts, KQL expressions, row match details). Without it, only pass/fail and errors are shown.

### Environment variables

| Variable | Purpose | Set by |
|----------|---------|--------|
| `KUSTO_ENDPOINT` | Emulator HTTP endpoint (e.g. `http://localhost:8082`) | Makefile |
| `KUSTO_KQL_ROOT` | Path to the parent `kusto/` directory containing `tables/` and `functions/` | Makefile |

Tests skip when either variable is unset.

## Step Types

Steps follow the `NN-<type>-<name>` naming convention.

### `ingestRows`

Seeds a table with test data.

**Directory contents:**
- `00-key.json`: `{"database": "DatabaseName", "table": "tableName"}` (required; declares which database and table to ingest into)
- `rows.json` (or any `.json` file except `00-key.json`): array of row objects. Keys match column names; missing columns get type-appropriate KQL defaults.

The step queries `.show table <name> cslschema` to discover the schema, then builds a `.set-or-append <table> <| datatable(...)` expression.

### `invokeFunction`

Calls a stored function and compares the result.

**Directory contents:**
- `00-key.json`: `{"database": "DatabaseName", "function": "FuncName", "args": ["arg1", "arg2"]}`
- `expected-result.json` (optional): array of expected row objects (column-by-column string comparison)
- `expected-error.txt` (optional): expected error substring

### `invokeQuery`

Runs raw KQL and compares the result.

**Directory contents:**
- `00-key.json`: `{"database": "DatabaseName", "query": "KQL expression"}`
- `expected-result.json` (optional): array of expected row objects
- `expected-error.txt` (optional): expected error substring

### `invokeMgmt`

Runs a management command and compares the result. Not expected to be used frequently; it exists for reflection cases where you need to validate metadata about the database itself (e.g. function names, folders, docstrings via `.show functions`) rather than query results. The `Catalog/ShowFunctions` tests use this step type.

**Directory contents:**
- `00-key.json`: `{"database": "DatabaseName", "command": ".show functions | project Name, Folder"}`
- `expected-result.json` (optional): array of expected row objects
- `expected-error.txt` (optional): expected error substring

Management commands return `v1.Dataset` where tables are not marked as primary results, so all tables are iterated.

For all invoke types, omitting both `expected-result.json` and `expected-error.txt` creates a smoke test (validates execution succeeds).

## Function Conventions

- Filenames use the pattern `{Database}_{FunctionName}.kql` (e.g. `ServiceLogs_GetClustersServiceIDForARMResourceID.kql`). The database prefix disambiguates functions that share the same name but target different databases. Filenames must be globally unique across the entire `functions/` tree.
- Function names use `PascalCase` with verb prefixes: `Get` (single row), `List` (multi-row), `Filter` (narrows piped input via `invoke`), `Enrich` (adds columns to piped input via `invoke`)
- Parameters use `p_PascalCase` (e.g. `p_ResourceId`, `p_StartTime`); the `p_` prefix prevents KQL parameter shadowing from hiding table columns
- Time parameters should prefer a range (`p_StartTime`, `p_EndTime`) over a single cutoff
- `// @depends: Name, Name2` lists exact function names this one invokes (same database only; omit/blank if none; unresolved names, including table names, error). `main.bicep` (from `main.bicep.tmpl`) is generated from these headers by `tooling/kustoctl/pkg/kqlfunc`; the test manifest reads them directly at test time (see Database Topology above). Run `make generate-kusto` after changing dependencies and commit the result; `make verify-generate` catches drift.

## Function Docstring Format

The `Catalog/ShowFunctions` suite validates that every function's Kusto `docstring` follows a structured, pipe-delimited format. The test runs `.show functions` and extracts three sections:

```
Description | Returns: what the function returns | Example: FunctionName("arg1", "arg2")
```

All three sections are required. The extraction logic:
- **Description**: everything before the first `|`, trimmed
- **Returns**: text after `| Returns:` up to the next `|`, trimmed
- **Example**: text after `| Example:` to end of string, trimmed

When writing a new function's `docstring`, match this format exactly. The Catalog test's `expected-result.json` is the source of truth for expected values. If the docstring doesn't parse correctly, the Catalog test will fail with a column mismatch on `Description`, `Returns`, or `Example`.

Example docstring in a `.kql` file:

```kql
docstring = 'Fetches logs for a resource within a time window | Returns: timestamped log rows | Example: MyFunction("uksouth", "/subscriptions/.../myResource", ago(10m), now())',
```

## Adding a Test Case

No Go code changes required.

1. Create `artifacts/<Database>/<FunctionName>/<test-case>/`
2. Add numbered step directories: `00-ingestRows-<tableName>/`, `01-invokeFunction-<label>/`, etc.
3. Place JSON files in each step directory per the step type spec above
4. Run `make fmt` from this directory to normalize JSON formatting
5. Run `make test` (or top-level `make test-integration-kusto`)

New suites are auto-discovered from subdirectories under `artifacts/`; no Go code changes needed.

## Result Comparison

All values are compared as strings via `fmt.Sprintf("%v", val)`. The `datasetToRows` function filters `query.Dataset` tables to primary results only (`IsPrimaryResult()`), skipping metadata tables the emulator returns.

Expected rows are compared column-by-column. Only columns present in `expected-result.json` are checked; extra columns in the actual result are ignored. Row count must match exactly.

## Running Tests

```bash
# From this directory
make test

# From repo root
make test-integration-kusto

# As part of all integration suites
make test-integration
```

### Formatting

```bash
make fmt           # jq round-trip all JSON fixtures
make verify-fmt    # fail if any fixture isn't formatted (CI check)
```

## Relationship to verify-kql

`make verify-kql` (implemented in `tooling/kustoctl/cmd/validate/kql.go`) also runs the Kusto emulator and loads tables before functions for schema validation. It catches schema mismatches (e.g. referencing a nonexistent column) at the syntax level.

This integration suite goes further: it seeds data, invokes functions, and asserts outputs. Use `verify-kql` for fast CI schema validation; use this suite for behavioral correctness of function logic.

## Key Implementation Details

- `emulatorClient.loadKQLFile()` uses `.execute database script` to run a `.kql` file, then inspects the result for `Failed` rows
- `ingestRowsStep` builds KQL `datatable()` expressions from JSON using type-aware literal formatting (`kqlLiteral`)
- KQL functions use lazy binding (table references resolved at query time), so functions can reference tables loaded earlier in the same session
- The emulator's `.show table <name> cslschema` returns comma-separated `col:type` pairs with no spaces
- `query.Dataset` (from `Query()`) vs `v1.Dataset` (from `Mgmt()`): both satisfy `query.Dataset`, but `datasetToRows` uses `query.Dataset` for portability

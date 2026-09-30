# Kusto Function Integration Tests

Declarative, artifact-driven integration tests for KQL stored functions. Runs a [Kusto emulator](https://learn.microsoft.com/en-us/azure/data-explorer/kusto-emulator-overview) (`kustainer-linux`) and validates function outputs against the same table schemas and function definitions deployed to production.

## Directory Layout

```
test/                                    # this directory (Go module: kustotest)
├── artifacts/                           # embedded test fixtures (//go:embed)
│   ├── manifest.json                    # single manifest declaring all database topology
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
2. `RunFunctionTests` reads `artifacts/manifest.json`, creates each database listed, and loads its tables then functions
3. The runner walks `artifacts/<Suite>/<FunctionName>/<TestCase>/` discovering step directories
4. Steps execute sequentially within each test case against the primary database
5. Between test cases, all tables across all databases are cleared for isolation

Tables are loaded before functions so that `.create-or-alter function` statements validate against real schemas. This is the same ordering `kustoctl validate` (`verify-kql`) uses.

### Database Manifest

A single `artifacts/manifest.json` declares the database topology shared by all suites. Databases are explicit buckets; each lists its own tables and functions.

```json
{
  "databases": {
    "HostedControlPlaneLogs": {
      "tables": ["containerLogs"],
      "functions": ["hcpComponentLogs"]
    }
  }
}
```

For cross-database functions, add the referenced database as another key:

```json
{
  "databases": {
    "ServiceLogs": {
      "tables": ["cosmosResourceSnapshots"],
      "functions": ["crossDbResolver"]
    },
    "HostedControlPlaneLogs": {
      "tables": ["containerLogs"]
    }
  }
}
```

- `databases`: map of database name to its spec. Databases are created in manifest key order, so list referenced databases before databases whose functions depend on them.
- `tables`: table names matching filenames in `tables/` (without `.kql` extension). Only list tables that test cases ingest into or that functions under test depend on.
- `functions`: function names matched by filename anywhere under `functions/` (directory structure is irrelevant; duplicate filenames are a test failure).

Tests fail if `manifest.json` is missing.

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

New suites are auto-discovered from subdirectories under `artifacts/`; no Go code changes needed. Update `artifacts/manifest.json` if the new suite needs databases, tables, or functions not already listed.

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

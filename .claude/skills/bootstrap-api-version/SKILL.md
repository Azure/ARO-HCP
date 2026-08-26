---
name: bootstrap-api-version
description: Bootstrap a new ARM API version by copying the latest version, wiring it into the frontend, SDK, tests, and integration fixtures. Invoke with the new version string (e.g. "2027-03-15-preview").
---

# Bootstrap a New API Version

Scaffold a new ARM API version as an identical copy of the latest existing version, then wire it into every registration point, test suite, and build target. The result is a branch with two commits: one for the API scaffold, one for the E2E test.

## Inputs

The user provides:
- **NEW_VERSION**: the API version string, e.g. `2027-03-15-preview`

Derived values:
- **NEW_PKG**: version string without hyphens, prefixed with `v` (e.g. `v20270315preview`)
- **PREV_PKG**: the most recent existing version — discover by listing directories matching `internal/api/v*preview/` and picking the latest alphabetically
- **PREV_VERSION**: the dash-separated form of PREV_PKG (e.g. `2026-06-30-preview`)
- **TIMEBOMB_DATE**: one calendar month after the version date, in RFC3339 UTC format

## Discovery

Before making any changes, read the codebase to find current registration points. The patterns below describe WHAT to look for and WHERE, not exact paths — paths may drift as the repo evolves.

- **TypeSpec version enum**: Search for `enum Versions` in `api/` `.tsp` files
- **API build config**: Find `VERSION ?=` in `api/Makefile`; find autorest tag blocks in `api/readme.md` and `api/testsdk.md`
- **API spec readme**: Find the spec-level `readme.md` under `api/redhatopenshift/.../hcpopenshiftclusters/` — it has `package-YYYY-MM-DD-preview` tag blocks used by `make -C api lint-openapi`
- **Language-specific SDK configs**: Search for `readme.go.md` and `readme.python.md` alongside the main `readme.md` in the API spec directory — these have per-version tag blocks and (for Go) a `batch` list
- **Frontend registration**: Search for `RegisterVersion` calls and blank imports of previous API version packages in `frontend/`
- **Integration test registry**: Search for `AllAPIVersions` in `test-integration/`
- **Cross-version test constants**: Search for the previous version's dash-separated string in `test-integration/` test files
- **Version compliance fixtures**: Find `artifacts/VersionCompliance/` under `test-integration/` and list its `expected/` directories
- **Defaults consistency test**: Search for `TestEnsureDefaultsConsistency` and find where previous versions are tested
- **OpenAPI spec path in defaults test**: Search for `openapi.json` in `internal/api/defaults_test.go` or similar
- **E2E framework helpers**: Search for version-suffixed functions like `CreateHCPClusterFromParam` + PREV_PKG in `test/util/framework/`
- **E2E test files**: Find `cluster_create_` + PREV_PKG files in `test/e2e/`
- **Import alias rules**: Check `.golangci.yml` for `importas` rules referencing SDK packages

## Step-by-step checklist

Split into two commits (see Commit Strategy below).

---

### Commit 1: API scaffold

#### 1. TypeSpec version enum

Add a new enum variant to the `Versions` enum in the main `.tsp` file, following the pattern of existing entries. Then regenerate: `cd api && npm run compile && npm run format`. This produces the OpenAPI spec, example files, and preview directory for the new version.

#### 2. API build configuration

- Update `VERSION ?=` default in `api/Makefile` to NEW_PKG
- Add a new autorest tag section for NEW_PKG in `api/readme.md` (before the previous version's tag — newest first), pointing to the new version's `openapi.json`
- Add a matching tag section in `api/testsdk.md` with the SDK module name
- Update any example commands in `api/docs/` that reference the previous version's tag

#### 2a. API spec readme

Update the spec-level `readme.md` at `api/redhatopenshift/.../hcpopenshiftclusters/readme.md`:
- Update the default `tag:` value at the top to `package-NEW_VERSION`
- Add a new `Tag: package-NEW_VERSION` block (before the previous version's tag — newest first) pointing to the new version's `openapi.json`

This file uses dash-separated version format (`package-YYYY-MM-DD-preview`) and is required by `make -C api lint-openapi`.

#### 2b. Language-specific SDK configs

Find `readme.go.md` and `readme.python.md` in the same directory as the main API `readme.md`. These have per-version tag blocks for autorest code generation:

- **`readme.go.md`**: Add NEW_VERSION to the `batch` list under `$(go) && $(multiapi)`, and add a new `Tag: package-NEW_VERSION and go` section (before the previous version's tag) with the output folder path.
- **`readme.python.md`**: Add a new `Tag: package-NEW_VERSION and python` section (before the previous version's tag) with namespace and output folder using underscore-separated version (e.g. `v2027_03_15_preview`).

#### 3. Internal API version package

Generate models: `cd api && make models VERSION=NEW_PKG`

Then copy every non-generated `.go` file from `internal/api/PREV_PKG/` to `internal/api/NEW_PKG/`. In each copied file:
- Change the `package` declaration from PREV_PKG to NEW_PKG
- Update the copyright year to the current year
- In `register.go`: update the version string returned by `String()` to the new version

The files to copy are all `.go` files in the previous version's directory that are NOT inside `generated/`. This typically includes a `register.go`, several `*_methods.go` files, and `*_test.go` files.

#### 4. Frontend registration

Find where previous versions are registered in the frontend:
- A blank-import file (imports version packages for their `init()` side effects) — add a new blank import
- The file containing `RegisterVersion` calls — add import + call for the new package

#### 5. Test SDK generation

```bash
cd api && make testsdk VERSION=NEW_PKG
```

Then wire the new SDK module into `test/go.mod` with a `require` + local `replace` directive, following the pattern of the previous version's entry.

#### 6. Integration test infrastructure

- Find `AllAPIVersions()` and add a `RegisterVersion` call for the new version (with its import)
- Find cross-version test constants (version strings like `"2025-12-23-preview"`) and add one for the new version

#### 7. Version compliance test fixtures

For each scenario directory under the `VersionCompliance` artifacts tree, copy the previous version's expected response files to the new version:
- `expected/get/PREV_VERSION.json` → `expected/get/NEW_VERSION.json`
- `expected/list/PREV_VERSION.json` → `expected/list/NEW_VERSION.json`

Since the new version is initially identical, these are exact copies.

#### 8. Defaults consistency test

Find the defaults consistency test(s) that test each API version's defaults against internal canonical defaults. Add test cases for the new version following the pattern of the previous version's cases. This requires importing the new version package.

#### 9. Defaults test OpenAPI path

Find the test that reads the OpenAPI spec to verify defaults (searches for `openapi.json` path). Update the path to point to the new version's spec.

#### 10. Tidy and verify

```bash
make tidy                       # go mod tidy + go work sync
make fmt                        # goimports
make licenses                   # add Apache 2.0 headers to new files
make lint                       # check for issues
make -C api validate-examples   # validate OpenAPI example files against the spec
make -C api lint-openapi        # lint specs against Azure ARM rules
make test                       # run unit tests
make verify                     # full CI verification (deepcopy, json-format, materialize, etc.)
```

---

### Commit 2: E2E test with timebomb

#### 11. E2E test framework helpers

Find the previous version's helpers in the test framework directory. Four areas need version-suffixed additions — copy each function/type, replacing the version suffix:

- **Deployment params**: Cluster/NodePool param structs, factory functions, and infra-population helpers
- **HCP helpers**: Build, create-and-wait, get, and update functions for clusters and node pools
- **Deployment helpers**: High-level orchestration functions (create cluster from params, create customer resources)
- **Per-test framework**: Client factory methods that create SDK clients for the new version

Import the new SDK package with the alias `hcpsdk<YYYYMMDD>preview` (e.g. `hcpsdk20270315preview` — note: no `v` prefix, matching the `importas` lint rules in `.golangci.yml`).

Also, in the `checkOperationResult` function in `hcp_helper.go`, add a `cmpopts.IgnoreFields` call for each resource type in the new API version. Use the existing list of ignored fields as a reference.

#### 12. E2E test file

Create a new E2E test file following the pattern of the previous version's cluster create test. Key elements:

- **Labels**: `RequireNothing`, `Critical`, `Positive`, `AroRpApiCompatible`
- **Timebomb pattern**: In non-development environments, query ARM to check if the API version is registered for the resource provider. If not available before TIMEBOMB_DATE, skip gracefully. If not available after TIMEBOMB_DATE, hard-fail. Development environments always run.
- **Test flow**: create resource group → create customer resources → create HCP cluster → get credentials → verify cluster health
- Use the new version's framework helpers for creation. Credential retrieval can use any version's helper (it's version-independent).

#### 13. Record E2E test specs

```bash
make record-nonlocal-e2e
```

This updates test fixture files that track the E2E test suite contents.

#### 14. Final tidy

```bash
make tidy && make fmt && make lint
```

---

## Commit strategy

Split into exactly two commits:

1. **`api: scaffold new API version NEW_VERSION`**
   Body: "Add the NEW_VERSION API version as a copy of PREV_VERSION with no feature changes. This establishes the version entry point so new features can be added incrementally in follow-up PRs."
   Contains: steps 1-10 (including 2a and 2b)

2. **`test: add E2E test with timebomb for NEW_VERSION API version`**
   Contains: steps 11-14

## Common pitfalls

- **Forgetting `register.go` String() update**: The copied `register.go` must return the NEW version string, not the previous one. This is the one semantic change in an otherwise mechanical copy.
- **Import alias enforcement**: `.golangci.yml` may enforce import aliases for SDK packages. Check the `importas` config and use any required aliases.
- **License headers**: Run `make licenses` before committing — all new `.go` files need Apache 2.0 headers.
- **go.work sync**: The SDK module lives under `test/` which should already be in `go.work`. Run `make tidy` to ensure `go work sync` picks it up correctly.
- **Version compliance fixtures**: Initially identical copies. When features are later added to the new version, these fixtures must be updated or `TestVersionCompliance` will fail with a clear diff showing exactly what to fix.
- **E2E file naming**: E2E test files must NOT use the `_test.go` suffix (except the entry point). Follow the convention of existing test files.
- **Copilot instructions**: Per `CONTRIBUTING.md`, also add a condensed version of any new skill to `.github/copilot-instructions.md` for GitHub Copilot compatibility.

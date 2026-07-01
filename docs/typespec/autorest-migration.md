# Migrating Go Code Generation from AutoRest to TypeSpec SDK Emitter

## Background

AutoRest was officially deprecated on July 1, 2026 ([Azure/autorest#5175](https://github.com/Azure/autorest/issues/5175)). The replacement for Go SDK generation is the `@azure-tools/typespec-go` emitter, which generates Go code directly from TypeSpec sources, eliminating the intermediate AutoRest step.

This document describes the design and reasoning behind the migration of Go code generation in ARO-HCP from AutoRest to the TypeSpec Go SDK emitter.

### References

- [AutoRest deprecation notice](https://github.com/Azure/autorest/issues/5175)
- [TypeSpec documentation](https://typespec.io/docs)
- [TypeSpec Azure documentation](https://azure.github.io/typespec-azure/)
- [TypeSpec Go emitter reference](https://azure.github.io/typespec-azure/docs/emitters/clients/typespec-go/reference/)
- [TypeSpec Go emitter source (Azure/autorest.go)](https://github.com/Azure/autorest.go)
- [TypeSpec Go QuickStart (Azure SDK for Go Wiki)](https://github.com/Azure/azure-sdk-for-go/wiki/TypeSpec-Go-QuickStart)
- [tsp-client CLI (npm)](https://www.npmjs.com/package/@azure-tools/typespec-client-generator-cli)
- [Azure REST API specs repo](https://github.com/Azure/azure-rest-api-specs)
- ARO-HCP internal: [Configuration management](../configuration.md), [Service components](../service-components.md)

## Current Pipeline

The existing code generation pipeline has two stages:

```
TypeSpec (.tsp) → tsp compile → OpenAPI JSON (via @azure-tools/typespec-autorest)
                                      ↓
                              autorest → Go code (via @autorest/go@4.0.0-preview.74)
```

### Generation Targets

Two Makefile targets in `api/Makefile` invoke AutoRest:

1. **`make models`** — Server-side Go models
   - Configured by `api/readme.md` (AutoRest literate config)
   - Reads intermediate OpenAPI JSON specs as input
   - Outputs to `internal/api/<VERSION>/generated/`
   - Post-processing deletes client files (`*_client.go`, `client_factory.go`, `options.go`, `responses.go`), keeping only models, constants, and serialization code
   - Uses `containing-module` to avoid generating `go.mod`

2. **`make testsdk`** — Full client SDK for testing
   - Configured by `api/testsdk.md` (AutoRest literate config)
   - Outputs to `test/sdk/<VERSION>/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/`
   - Generates complete client SDK including fakes, operations clients, options, and responses
   - Three versioned SDKs: `v20240610preview`, `v20251223preview`, `v20260630preview`

3. **`make lint-openapi`** — OpenAPI spec validation
   - Uses AutoRest as an orchestrator for Azure OpenAPI Validator and Spectral linting
   - Validates all API version specs against ARM rules

### API Versions

Three API versions are defined in `main.tsp` via the `@versioned` decorator:

| TypeSpec Enum | Date Format | Makefile Tag |
|---|---|---|
| `v2024_06_10_preview` | `2024-06-10-preview` | `v20240610preview` |
| `v2025_12_23_preview` | `2025-12-23-preview` | `v20251223preview` |
| `v2026_06_30_preview` | `2026-06-30-preview` | `v20260630preview` |

Each version produces a separate Go package in its own directory. This per-version isolation is a deliberate design choice that preserves API stability for consumers.

**Important limitation:** The `@azure-tools/typespec-go` emitter always generates for the latest API version defined in the TypeSpec source. It does not support per-version projection from multi-versioned TypeSpec (unlike `@azure-tools/typespec-autorest` which emits per-version OpenAPI specs). As a result, only the latest API version (currently `v20260630preview`) is regenerated using typespec-go. Older versions' generated code stays committed as-is.

### Dependencies on AutoRest

| Artifact | Purpose | Replacement |
|---|---|---|
| `autorest` npm package (`devDependencies`) | CLI runner for code generation | `tsp compile --emit @azure-tools/typespec-go` |
| `@autorest/go@4.0.0-preview.74` (pinned in readme.md/testsdk.md) | Go code generator plugin | `@azure-tools/typespec-go` emitter |
| `api/readme.md` | AutoRest literate config for server models | CLI options or tspconfig overrides |
| `api/testsdk.md` | AutoRest literate config for test SDK | CLI options or tspconfig overrides |
| `npx autorest --azure-validator --spectral` in lint-openapi | Orchestrates OpenAPI validation | Direct `npx openapi-validator` + `npx spectral` |

## Target Pipeline

```
TypeSpec (.tsp) → tsp compile with @azure-tools/typespec-go    → Go code (directly)
TypeSpec (.tsp) → tsp compile with @azure-tools/typespec-autorest → OpenAPI JSON (kept for linting/validation)
```

The key change is that Go code generation happens directly from TypeSpec, removing the OpenAPI-as-intermediate-format step for Go. OpenAPI generation is preserved for validation, examples, and external consumers.

## Design Decisions

### Why `@azure-tools/typespec-go` (not alternatives)?

The `@azure-tools/typespec-go` emitter is the official Microsoft-maintained replacement for `@autorest/go`. It:

- Lives in the same repository (`Azure/autorest.go`) as the legacy generator
- Produces structurally equivalent output (same file layout: `constants.go`, `models.go`, `models_serde.go`, `time_rfc3339.go`, plus client files)
- Is actively maintained and the designated successor per the deprecation notice
- Is already referenced in the project's `tspconfig.yaml` (for external Azure SDK generation)
- Is used by the Azure SDK for Go itself (e.g., `armcompute` has already migrated)

### Why keep OpenAPI generation?

Even though Go code no longer needs the intermediate OpenAPI JSON, the specs serve other purposes:

- **Linting:** `make lint-openapi` validates specs against Azure ARM rules
- **Example validation:** `make validate-examples` checks example payloads against the spec
- **External consumers:** The specs follow the `azure-rest-api-specs` repository structure for eventual upstream submission
- **Documentation:** OpenAPI specs are committed to the repository as a reference

The `@azure-tools/typespec-autorest` emitter remains in the emit list in `tspconfig.yaml`.

### Why CLI options instead of separate tspconfig files?

The project needs to generate Go code for multiple API versions to separate output directories with different configurations (models-only vs. full SDK). There are three approaches:

1. **CLI `--option` overrides** — Pass all emitter options on the `tsp compile` command line
2. **Separate tspconfig files** — Create `tspconfig-models.yaml` and `tspconfig-testsdk.yaml`
3. **npm scripts wrapping tsp compile** — Encapsulate the CLI invocations

We use CLI options invoked from the Makefile (approach 1, optionally wrapped in npm scripts for readability). This keeps configuration co-located with the build target and avoids maintaining multiple tspconfig files that could drift. The existing `tspconfig.yaml` is left untouched since it serves external Azure SDK generation.

### Per-version generation limitation

The `@azure-tools/typespec-go` emitter always generates for the latest API version defined via the `@versioned` decorator. The TCGC `api-version` option controls the default API version header in generated client requests — it does **not** project the TypeSpec model to a specific version. Only `@azure-tools/typespec-autorest` performs per-version projection internally.

As a result, only the latest API version (currently `v20260630preview`) is regenerated. Older versions' generated code stays committed as-is.

### Models-only generation via post-processing

The `@azure-tools/typespec-go` emitter does not have a built-in "models-only" mode. It always generates the full SDK (clients, options, responses). Fake server generation is disabled via `generate-fakes=false` for both targets — set to `true` if fakes are needed in the future. The current approach of deleting client files after generation is preserved for server-side models:

```makefile
rm -f ${OUTPUT_DIR}/client_factory.go
rm -f ${OUTPUT_DIR}/*_client.go
rm -f ${OUTPUT_DIR}/options.go
rm -f ${OUTPUT_DIR}/responses.go
```

This is the same post-processing the project uses today with AutoRest and is a pragmatic solution.

### Legacy unversioned SDK removal

An unversioned SDK exists at `test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/` (referenced by `test/go.mod`). This is a legacy artifact from before the multi-version approach was introduced. It will be removed and its references in `test/go.mod` cleaned up.

### OpenAPI linting migration

The `lint-openapi` target replaces `npx autorest --azure-validator --spectral` with direct CLI calls to both validators:

1. **Azure OpenAPI Validator** (`@microsoft.azure/openapi-validator`) — Already in `package.json`. Enforces Azure ARM API design rules.
2. **Spectral** (`@stoplight/spectral-cli`) — Added as a new devDependency. General-purpose OpenAPI linting.

Both tools are invoked sequentially per API version spec, maintaining the same validation coverage.

## Migration Steps

### Step 1: Add `@azure-tools/typespec-go` dependency

Add to `api/package.json` `dependencies`:

```json
"@azure-tools/typespec-go": "^0.14.0"
```

Also ensure `@azure-tools/typespec-client-generator-core` is present (needed for `api-version` selection). Run `npm install`.

### Step 2: Replace `make models` target

Update `api/Makefile` to invoke `tsp compile` with the `@azure-tools/typespec-go` emitter:

```makefile
TSP_PROJECT = redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters

# Map VERSION tag to API version date format
API_VERSION_MAP_v20240610preview = 2024-06-10-preview
API_VERSION_MAP_v20251223preview = 2025-12-23-preview
API_VERSION_MAP_v20260630preview = 2026-06-30-preview
API_VERSION_DATE = $(API_VERSION_MAP_$(VERSION))

.PHONY: models
models: $(GOIMPORTS)
	tsp compile $(TSP_PROJECT) \
		--no-emit \
		--emit @azure-tools/typespec-go \
		--option "@azure-tools/typespec-go.emitter-output-dir=$(CURDIR)/../internal/api/$(VERSION)/generated" \
		--option "@azure-tools/typespec-go.containing-module=github.com/Azure/ARO-HCP" \
		--option "@azure-tools/typespec-go.disallow-unknown-fields=true" \
		--option "@azure-tools/typespec-go.generate-fakes=false" \
		--option "@azure-tools/typespec-go.inject-spans=false" \
		--option "@azure-tools/typespec-go.fix-const-stuttering=true" \
		--option "@azure-tools/typespec-go.flavor=azure" \
		--option "@azure-tools/typespec-client-generator-core.api-version=$(API_VERSION_DATE)"
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../internal
	# Remove client API files, keep only constants and models.
	rm $(RM_FORCE) $(AUTOREST_OUTPUT_DIR)/client_factory.go
	rm $(RM_FORCE) $(AUTOREST_OUTPUT_DIR)/*_client.go
	rm $(RM_FORCE) $(AUTOREST_OUTPUT_DIR)/options.go
	rm $(RM_FORCE) $(AUTOREST_OUTPUT_DIR)/responses.go
```

### Step 3: Replace `make testsdk` target

```makefile
TESTSDK_OUTPUT_DIR = ../test/sdk/$(VERSION)/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp

.PHONY: testsdk
testsdk: $(GOIMPORTS)
	tsp compile $(TSP_PROJECT) \
		--no-emit \
		--emit @azure-tools/typespec-go \
		--option "@azure-tools/typespec-go.emitter-output-dir=$(CURDIR)/$(TESTSDK_OUTPUT_DIR)" \
		--option "@azure-tools/typespec-go.module=github.com/Azure/ARO-HCP/test/sdk/$(VERSION)/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp" \
		--option "@azure-tools/typespec-go.generate-fakes=true" \
		--option "@azure-tools/typespec-go.fix-const-stuttering=true" \
		--option "@azure-tools/typespec-go.flavor=azure" \
		--option "@azure-tools/typespec-client-generator-core.api-version=$(API_VERSION_DATE)"
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../test/sdk
```

Remove the legacy unversioned SDK at `test/sdk/resourcemanager/` and clean up `test/go.mod` references.

### Step 4: Update `package.json` npm scripts

Remove the `"models"` and `"testsdk"` scripts (they reference `autorest`). Keep `"compile"`, `"format"`, and `"format-check"`.

### Step 5: Migrate `make lint-openapi`

Replace AutoRest-orchestrated validation with direct CLI calls:

```makefile
.PHONY: lint-openapi
lint-openapi:
	@echo "Linting OpenAPI specs for all API versions..."
	@for spec_path in $(API_SPEC_PATHS); do \
		version=$$(basename $$(dirname $$spec_path)); \
		echo ""; \
		echo "==> Linting $$version"; \
		npx openapi-validator $$spec_path --openapi-type=arm || exit 1; \
		npx spectral lint $$spec_path || exit 1; \
	done
```

Add `@stoplight/spectral-cli` to `devDependencies` in `package.json`.

### Step 6: Remove AutoRest artifacts

- Remove `autorest` from `devDependencies` in `api/package.json`
- Remove `api/readme.md` (AutoRest literate config for models)
- Remove `api/testsdk.md` (AutoRest literate config for test SDK)
- Run `npm install` to update the lockfile

### Step 7: Validate

1. Generate models and test SDK with the new pipeline
2. Diff output against the current generated code to identify any structural changes
3. Run `make lint` and `make test` from the repo root
4. Run `make lint-openapi` and `make validate-examples` in `api/`
5. Run integration tests: `cd test-integration && make test`
6. Run `make verify` to check all CI validations pass

## Key Behavioral Differences

### No `*Update` types
AutoRest generated separate `*Update` types (e.g. `HcpOpenShiftClusterUpdate`, `NodePoolUpdate`) for PATCH operations. The typespec-go emitter does not generate these — client `BeginUpdate` methods accept the main resource type (e.g. `HcpOpenShiftCluster`, `NodePool`) directly. Code that constructs update payloads must be adjusted.

### `time_rfc3339.go` replaced by `azcore/runtime/datetime`
AutoRest generated a local `time_rfc3339.go` helper file with `populateDateTimeRFC3339`/`unpopulateDateTimeRFC3339` functions. The typespec-go emitter uses `github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime/datetime` with generic `populateTime[datetime.RFC3339]`/`unpopulateTime[datetime.RFC3339]` functions instead. This requires `azcore >= v1.22.0`.

### Comment formatting
Doc comments on generated types have different line wrapping. This is cosmetic and has no functional impact.

## Upstream SDK Generation (azure-rest-api-specs)

### How tspconfig.yaml replaces readme.md for upstream SDKs

Previously, SDK generation for the official Azure SDKs (azure-sdk-for-go, azure-sdk-for-python, etc.) was configured via AutoRest readme files in the spec repo:

| Old (AutoRest) | New (TypeSpec) |
|---|---|
| `readme.md` with embedded YAML config blocks | `tspconfig.yaml` |
| Per-language `readme.go.md`, `readme.python.md`, etc. | Per-language emitter options in `tspconfig.yaml` |
| `autorest.md` in SDK repos pointing to readme.md | `tsp-location.yaml` in SDK repos pointing to tspconfig.yaml |
| `autorest` CLI | `tsp-client` CLI (`@azure-tools/typespec-client-generator-cli`) |

### tspconfig.yaml (spec repo side)

The `tspconfig.yaml` at `api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/tspconfig.yaml` already has emitter options configured for all five SDK languages (Go, Python, Java, C#, TypeScript). When this spec is submitted to `azure-rest-api-specs`, CI validates the TypeSpec and tspconfig, and on merge, automation triggers SDK generation for all configured languages.

Key interpolation variables used in emitter options:

| Variable | Resolves to |
|---|---|
| `{output-dir}` | The SDK repo root at generation time |
| `{service-dir}` | Value from `parameters.service-dir.default` |
| `{project-root}` | The directory containing tspconfig.yaml |

### tsp-location.yaml (SDK repo side)

A `tsp-location.yaml` file lives in each SDK repo's generated package directory (e.g., `azure-sdk-for-go/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/`). It points back to the spec via commit SHA:

```yaml
directory: specification/redhatopenshift/Microsoft.RedHatOpenShift/hcpopenshiftclusters
commit: <sha>
repo: Azure/azure-rest-api-specs
```

This file is created by `tsp-client init` and updated by bumping `commit` and running `tsp-client update`. It does not exist in our ARO-HCP repo — it is maintained in each language's SDK repo.

### Upstream workflow

1. Spec author submits TypeSpec + `tspconfig.yaml` to `azure-rest-api-specs`
2. CI validates TypeSpec, lints generated OpenAPI, checks tspconfig emitter options
3. On merge, automation creates PRs in each language SDK repo
4. Each SDK PR includes generated code and a `tsp-location.yaml` pointing back to the spec
5. SDK teams review and merge through their standard release process

### Legacy spec-level readme files

The following AutoRest readme files still exist in the TypeSpec project directory and can be removed once TypeSpec is the sole generation path for upstream SDKs:

- `hcpopenshiftclusters/readme.md` — AutoRest config for the spec repo
- `hcpopenshiftclusters/readme.go.md` — Go-specific AutoRest config
- `hcpopenshiftclusters/readme.python.md` — Python-specific AutoRest config

These are superseded by the emitter options in `tspconfig.yaml`.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Generated code has minor structural differences (field ordering, import paths, helper implementations) | High | Low | Diff carefully; differences in generated code are expected and acceptable if semantically equivalent. Update downstream code if needed. |
| `api-version` CLI option doesn't select the correct version | Medium | High | Fall back to separate tspconfig files per version, or investigate alternative version selection mechanisms. |
| `@azure-tools/typespec-go` version incompatible with `@typespec/compiler@^1.12.0` | Low | Medium | Check npm peer dependencies before installing; pin compatible version. |
| `time_rfc3339.go` changes to use `azcore/runtime/datetime` instead of local helper | Medium | Low | If import paths change, update `go.mod` dependencies. The existing `azcore` dependency should already cover this. |
| Spectral / Azure validator produce different results than AutoRest-orchestrated run | Low | Low | Compare validation output side-by-side; adjust ruleset configuration if needed. |

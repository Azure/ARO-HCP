# Add Group Field to Image Updater

## Context

The image updater config (`tooling/image-updater/config.yaml`) has ~30 image entries that are currently flat. When updating images, you either update all of them or cherry-pick individual components with `--components`. Adding a `group` field lets you update logically related images together (e.g. all prometheus images, all velero images) with a single `--groups` flag.

## Group Assignments

| Group | Components |
|---|---|
| `aro-rp` | arohcpfrontend, arohcpbackend, admin-api, sessiongate |
| `cs` | clusters-service |
| `aro-deps` | backplaneAPI, imageSync |
| `hypershift-stack` | hypershift, maestro, maestro-agent-sidecar, acm-operator, acm-mce |
| `pko` | pko-package, pko-manager, pko-remote-phase-manager |
| `prom-stack` | prometheus-operator, prometheus, prometheus-config-reloader, kube-state-metrics, kube-webhook-certgen |
| `obs-agents` | arobit-forwarder, arobit-mdsd, kubeEvents |
| `velero` | velero-server, velero-azure-plugin, velero-hypershift-plugin |
| `platform-utils` | aksCommandRuntime, acrPull, secretSyncController, secretSyncProvider |

## Changes

### 1. `tooling/image-updater/internal/config/config.go`

- Add `Group string` field to `ImageConfig` struct (yaml tag: `group`)
- Add validation in `Load()`: every image must have a non-empty `group`
- Add `FilterByGroups(groupNames []string) (*Config, error)` method:
  - Returns new Config with only images matching the given groups
  - Error if a specified group doesn't match any image
- Add `Groups() []string` helper that returns sorted list of distinct group names

### 2. `tooling/image-updater/internal/options/options.go`

- Add `Groups string` field to `RawUpdateOptions`
- Bind `--groups` flag in `BindUpdateOptions()` (comma-separated, e.g. `--groups hypershift-stack,velero`)
- Update `Validate()` filtering logic:
  - If `--components` or `--groups` is specified: build inclusion set as union of individual components + all components from specified groups
  - Then apply `--exclude-components` to remove from that set
  - If neither `--components` nor `--groups`: apply `--exclude-components` against all (current behavior preserved)

### 3. `tooling/image-updater/config.yaml`

Add `group:` field to every image entry.

### 4. `tooling/image-updater/Makefile`

- Add `GROUPS ?=` variable
- Add `GROUP_FLAGS := $(if $(GROUPS),--groups $(GROUPS))`
- Add `$(GROUP_FLAGS)` to the `update` target command
- Update `help` target with `GROUPS` variable documentation and example

### 5. `tooling/image-updater/internal/config/config_test.go`

- Add `TestFilterByGroups` - filtering by single/multiple groups, empty list, non-existent group
- Add `TestGroups` - sorted distinct group names
- Add test case for missing group field in `TestConfigLoad`
- Update all existing test configs to include `group` field

### 6. `tooling/image-updater/internal/options/options_test.go`

- Add test cases to `TestRawUpdateOptions_Validate_ComponentFiltering` for:
  - `--groups` alone (single and multiple)
  - `--groups` combined with `--components` (union)
  - `--groups` with `--exclude-components`
  - `--groups` and `--components` with `--exclude-components`
  - Non-existent group name
- Update `createTestConfigFile` to include `group` field
- Update all inline YAML test configs to include `group` field

### 7. `tooling/image-updater/README.md`

- Add "Groups" to Table of Contents
- Add "Groups" section under Common Usage Patterns with examples
- Add `--groups` to Command Reference flags table
- Add `group` to Configuration Reference (new "Image Fields" table)
- Update Quick Start with group example

## Verification

```bash
cd tooling/image-updater
go test ./...     # all tests pass
go build -o image-updater .  # binary builds
image-updater update --help  # --groups flag visible
```

## Modifications Applied During Execution

1. **Behavior change for `--components` + `--exclude-components`**: The original code had `--components` take full precedence over `--exclude-components` (exclude was silently ignored when components were specified). The new implementation applies `--exclude-components` *after* the inclusion set is built from `--components`/`--groups`. This is a deliberate change - it makes exclude always meaningful and enables patterns like `--groups hypershift-stack --exclude-components maestro-agent-sidecar`. Two existing test cases were updated to reflect this new behavior.

2. **All existing test YAML configs updated**: Every inline YAML config in both `config_test.go` and `options_test.go` needed a `group` field added, since the `Load()` validation now requires it. This affected `TestConfigLoad_WithUseAuth`, `TestConfigLoad_WithKeyVault`, `TestComplete_AuthenticationRequirements`, and `TestKeyVaultDeduplication` tests.

3. **Added `TestGroups` test**: In addition to the planned `TestFilterByGroups`, added a `TestGroups` test function to verify the `Groups()` helper returns correctly sorted, deduplicated group names.

4. **`sort` import added to `config.go`**: Required for the `Groups()` method. Note: `sets.NewString().List()` already returns sorted results, so the explicit `sort.Strings()` call is redundant but makes intent clear.

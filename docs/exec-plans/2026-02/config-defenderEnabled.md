# Plan: Add `defenderEnabled` Config Flag for Conditional MDSD Mount Path

## Context

PR 4164 changed the frontend MDSD audit socket mount path from `/var/run/mdsd/asa` to `/var/run/mdsd`
(parent directory) to prevent breakage when Microsoft Defender for Cloud crashes and changes the inode
of the `asa` subdirectory. PR 4160 introduced the same socket mounting pattern for the Admin API.

This path change is only safe/needed when Defender for Cloud is active in the environment (MSFT-managed
environments). In environments without Defender (dev, cspr, etc.), the narrower `/var/run/mdsd/asa` mount
is preferable. The fix: a new `defenderEnabled` config flag that drives path selection.

**Scope**: only two places mount this socket:
- `frontend/deploy/templates/frontend.deployment.yaml`
- `admin/deploy/templates/admin.deployment.yaml`

(The `observability/arobit` daemonset mounts `/var/run/mdsd/` for a different purpose and is unaffected.)

---

## Changes

### 1. `config/config.schema.json`

Add `defenderEnabled` as a top-level boolean property in the `defaults` object definition (same level as
`automationDryRun`):

```json
"defenderEnabled": {
  "type": "boolean",
  "description": "Whether Microsoft Defender for Cloud is active in the environment"
}
```

Also add it to the `required` array for `defaults`.

### 2. `config/config.yaml`

Add in the top-level `defaults` section near the Automation Account block:

```yaml
  # Microsoft Defender for Cloud
  defenderEnabled: false
```

### 3. `config/config.msft.clouds-overlay.yaml`

Add `defenderEnabled: true` **only** under the `int` environment. Leave existing `frontend.audit.connectSocket`
entries untouched; do NOT add `adminApi.audit.connectSocket` (that remains false everywhere for now):

```yaml
clouds:
  public:
    environments:
      int:
        defaults:
          defenderEnabled: true
          frontend:
            audit:
              connectSocket: true   # already present, no change
```

`stg` and `prod` keep `defenderEnabled: false` (not added there).

### 4. `frontend/values.yaml`

Add to the `audit` section:

```yaml
audit:
  connectSocket: {{ .frontend.audit.connectSocket }}
  defenderEnabled: {{ .defenderEnabled }}
```

### 5. `admin/values.yaml`

Add to the `audit` section:

```yaml
audit:
  connectSocket: {{ .adminApi.audit.connectSocket }}
  defenderEnabled: {{ .defenderEnabled }}
```

### 6. `frontend/deploy/templates/frontend.deployment.yaml`

Replace both the `volumeMounts` and `volumes` path with a conditional:

```yaml
{{- if .Values.audit.connectSocket }}
volumeMounts:
  - name: mdsd-asa-run-vol
    {{- if .Values.audit.defenderEnabled }}
    mountPath: /var/run/mdsd
    {{- else }}
    mountPath: /var/run/mdsd/asa
    {{- end }}
{{- end }}
...
{{- if .Values.audit.connectSocket }}
volumes:
  - name: mdsd-asa-run-vol
    hostPath:
      {{- if .Values.audit.defenderEnabled }}
      path: /var/run/mdsd
      {{- else }}
      path: /var/run/mdsd/asa
      {{- end }}
      type: Directory
{{- end }}
```

Note: placing `defenderEnabled` under `audit` in values (`.Values.audit.defenderEnabled`) keeps it
logically grouped.

### 7. `admin/deploy/templates/admin.deployment.yaml`

Same conditional pattern applied to the `volumeMounts` and `volumes` sections (identical logic as frontend).

### 8. Test fixtures — frontend

**`frontend/testdata/helmtest_connect_socket.yaml`** — add `defenderEnabled: false` override to make
the test explicit (tests use the rendered `dev/dev/westus3.yaml` config which will have `false`, but
being explicit is better):

```yaml
testData:
  frontend:
    audit:
      connectSocket: true
  defenderEnabled: false
```

Add new test file:
- `frontend/testdata/helmtest_connect_socket_defender.yaml` with `connectSocket: true` + `defenderEnabled: true`

The corresponding `zz_fixture_*` files are regenerated automatically by `make materialize` (see Execution below).

### 9. Test fixtures — admin

Add new test files:
- `admin/testdata/helmtest_connect_socket.yaml` with `connectSocket: true` + `defenderEnabled: false`
- `admin/testdata/helmtest_connect_socket_defender.yaml` with `connectSocket: true` + `defenderEnabled: true`

The corresponding `zz_fixture_*` files are regenerated automatically by `make materialize`.

---

## Execution

Make all code and config changes first, then run a single command that validates the schema, renders
all configs, and regenerates all helm test fixtures in one step:

```bash
cd config && make materialize
```

`make materialize` internally calls `make update-helm-fixtures` which runs
`UPDATE=true go test --count=1 ./...` in `tooling/helmtest`. **Do not run helmtest with `UPDATE=true`
separately** — it will fail for tests that rely on the base rendered config (e.g. `frontend-mise-enabled`,
`dev-westus3-svc-1-admin-api`) if `make materialize` hasn't been run first to populate `defenderEnabled`
in the rendered YAML.

To confirm fixtures are stable after generation:

```bash
cd tooling/helmtest && go test -run TestHelmTemplate -count=1 ./...
```

---

## Critical Files

| File | Change |
|------|--------|
| `config/config.schema.json` | Add `defenderEnabled: boolean` to defaults, add to `required` |
| `config/config.yaml` | Add `defenderEnabled: false` to defaults |
| `config/config.msft.clouds-overlay.yaml` | Add `defenderEnabled: true` for `int` only |
| `frontend/values.yaml` | Add `audit.defenderEnabled` |
| `admin/values.yaml` | Add `audit.defenderEnabled` |
| `frontend/deploy/templates/frontend.deployment.yaml` | Conditional path |
| `admin/deploy/templates/admin.deployment.yaml` | Conditional path |
| `frontend/testdata/helmtest_connect_socket.yaml` | Add `defenderEnabled: false` |
| `frontend/testdata/helmtest_connect_socket_defender.yaml` | New test case |
| `admin/testdata/helmtest_connect_socket.yaml` | New test case |
| `admin/testdata/helmtest_connect_socket_defender.yaml` | New test case |
| `config/rendered/**` | Re-rendered by `make materialize` |
| `frontend/testdata/zz_fixture_*` | Regenerated by `make materialize` |
| `admin/testdata/zz_fixture_*` | Regenerated by `make materialize` |

---

## Verification

1. Run `make materialize` — this validates the schema and renders configs. Check exit code is 0.

2. Run helm tests to confirm all fixtures are stable:
   ```bash
   cd tooling/helmtest && go test -run TestHelmTemplate -count=1 ./...
   ```

3. Spot-check rendered `config/rendered/dev/dev/westus3.yaml` has `defenderEnabled: false`.

4. Verify generated fixtures produce correct paths:
   - `frontend/testdata/zz_fixture_TestHelmTemplate_frontend_connect_socket.yaml`:
     `mountPath: /var/run/mdsd/asa` (non-defender default)
   - `frontend/testdata/zz_fixture_TestHelmTemplate_frontend_connect_socket_defender.yaml`:
     `mountPath: /var/run/mdsd` (defender path)
   - Same pattern in the two admin fixtures.

5. Verify MSFT environment values using `config/Makefile`'s `render-partial-config` target, which
   applies `config.msft.clouds-overlay.yaml` and works without MSFT credentials (uses
   `--skip-schema-validation` and `--ev2-cloud public` with placeholder ev2 values):

   ```bash
   for env in int stg prod; do
     make -C config render-partial-config \
       ARO_HCP_CLOUD=public \
       ARO_HCP_DEPLOY_ENV=$env \
       LOCATION=eastus \
       CONFIG_OUTPUT=/tmp/config-${env}.yaml
     echo -n "$env: "
     python3 -c "
   import yaml
   with open('/tmp/config-${env}.yaml') as f:
       d = yaml.safe_load(f)
   print('defenderEnabled={} connectSocket={}'.format(d['defenderEnabled'], d['frontend']['audit']['connectSocket']))
   "
   done
   ```

   Expected output:
   ```
   int: defenderEnabled=True connectSocket=True
   stg: defenderEnabled=False connectSocket=True
   prod: defenderEnabled=False connectSocket=True
   ```

   `stg` and `prod` have `connectSocket=true` but `defenderEnabled=false`, so they mount
   `/var/run/mdsd/asa`. They can be switched to `defenderEnabled=true` in a follow-up once `int`
   is confirmed stable.

---

## Notes on Original Plan vs Actual Implementation

The following corrections were discovered during execution:

- **Test update flag**: The original plan referenced `go test ./... -update`. The actual mechanism is
  an environment variable: `UPDATE=true go test ./...`. There is no `-update` CLI flag.
- **`make materialize` handles fixture regeneration**: `make materialize` already calls
  `make update-helm-fixtures` → `UPDATE=true go test`. Running helmtest with UPDATE separately is
  redundant and error-prone (it fails if done before `make materialize` because the base rendered
  config lacks `defenderEnabled`).
- **`public/int` rendered configs not available locally**: `config/rendered/` only contains `dev`
  configs. Use `make -C config render-partial-config` with `ARO_HCP_CLOUD=public` and
  `ARO_HCP_DEPLOY_ENV=int/stg/prod` to produce partial renders of MSFT environment configs locally
  without needing MSFT credentials. This is also the same mechanism used by the prow
  `aro-hcp-write-config` step in CI.

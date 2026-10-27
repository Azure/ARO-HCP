# Component updater

Shared CLI (`component-updater`) that discovers the next compatible version for
configured ARO-HCP components and optionally updates local source configuration.
Components are declared in `config.yaml` under this directory; each has its own
`provider`. The tool is **not** Istio-specific — Istio is only the first wired
component (`istio-mesh-revision` / `azure-istio`). AKS and Grafana providers are
not implemented yet and can be added without changing the CLI shape.

Adding this tool does not schedule jobs, deploy clusters, or open PRs.

Agent-oriented procedure: [AGENTS.md](AGENTS.md).

## Prerequisites (local)

- Go matching this repo (`go.work` / module `go` version). Prefer Homebrew Go on
  macOS (`brew install go`). Do **not** set `GOROOT=/usr/local/go` if that tree
  is stale or mixed with another toolchain.
- GNU Make 4.x (required for `make -C config materialize`). On macOS:
  `brew install make` and put `$(brew --prefix make)/libexec/gnubin` first on
  `PATH`, or invoke `gmake`.
- Azure CLI, logged into a tenant that can read mesh catalogs:
  ```bash
  az login --tenant 64dc69e4-d083-49fc-9569-ebece1dd1408   # Red Hat tenant
  ```
  Mesh discovery needs read access to Container Service / subscription location
  APIs. If the credential can see exactly one Enabled subscription, the tool
  uses it automatically. Pass `--subscription-id` (or `AZURE_SUBSCRIPTION_ID`)
  when the token can see more than one.

## Local usage

From `tooling/component-updater/`:

```bash
export AZURE_TOKEN_CREDENTIALS=dev
# Optional when az login can see multiple Enabled subscriptions:
# export AZURE_SUBSCRIPTION_ID=1d3378d3-5a3f-4712-85a1-2485495dfc4b

# Read-only discovery (no file writes)
go run . check --config config.yaml --component istio-mesh-revision --timeout 5m

# Apply a local bump and materialize (default): edits config/config.yaml, then
# runs `make -C <config-dir> materialize` to refresh rendered configs + Helm fixtures
go run . update --config config.yaml --component istio-mesh-revision --timeout 5m

# Same via Make (materialize still runs by default on update)
make update COMPONENT=istio-mesh-revision

# From repository root
make component-updater-check COMPONENT=istio-mesh-revision
make component-updater-update COMPONENT=istio-mesh-revision

# Inspect what changed
git diff -- ../../config/config.yaml ../../config/rendered
```

### `check` vs `update` vs materialize

| Command | Writes `config/config.yaml`? | Runs `make materialize`? |
| --- | --- | --- |
| `check` | no | no |
| `update` (default) | yes, if a bump is selected | **yes** (after a successful source edit) |
| `update --skip-materialize` | yes, if a bump is selected | no — prints `make -C … materialize` to run yourself |

Materialization needs GNU Make 4.x. It is skipped when discovery fails or no
source value changed. On success it prints a one-line confirmation and hides
`make materialize` logs (so Slack/CI digests stay short). On failure the error
includes the make output plus a `make -C … materialize` retry command; the
source edit is kept.

Neither command upgrades clusters, pushes commits, or opens a PR.

Version-bump PRs must include source configuration, generated configurations,
and Helm fixtures together. A tooling-only change does not require a version bump.

### Relation to `image-updater`

`tooling/image-updater` does **not** run materialize itself. After an image
digest bump you still run `make -C config materialize` (documented in
`tooling/image-updater/AGENTS.md`). This component-updater runs that step by
default on `update` so a local Istio bump produces a complete, reviewable diff
in one command. Use `--skip-materialize` only when you want to inspect the
source edit before rendering.

## Configuration

See `config.yaml` in this directory. Each map key under `components` is a
component name selectable with `--component`. Fields:

| Field | Required | Notes |
| --- | --- | --- |
| `provider` | yes | `azure-istio` today; future values register in `providers.New` |
| `targets` | yes | Exactly one `{jsonPath, filePath}` for now |
| `locations` | Istio | Use `[all]` for update; subsets are check-only |
| `kubernetesVersionPath` | Istio | Path to configured AKS version used for compatibility |
| `paused` | no | `update` skips discovery/writes; `check` still works |

## Selection and safety (Istio)

- **Configured** is the local source configuration value, not a live cluster
  inventory. The configured service-cluster AKS version is read from the same
  file for compatibility checking.
- Discovery reads Azure's `Microsoft.ContainerService` provider metadata for
  `locations/meshRevisionProfiles`, maps every advertised location to its canonical
  name using subscription location metadata, then queries all those mesh catalogs
  concurrently (at most eight regions in flight).
- **Why these protections exist:** `defaults.svc.istio.versions` is a **shared**
  default. There is no supported config-driven mesh downgrade (`docs/istio.md`):
  once a revision is the highest installed control plane, config cannot roll it
  back. Separately, `istio-upgrade` may **skip** an unavailable target for a
  cluster, while the Istio Helm chart still renders from config (e.g.
  `istio-shared-configmap-<revision>`) and can leave in-cluster objects for a
  revision that never installed. Cleaning that up means a validated
  current/forward config bump, re-run, and **manual** ConfigMap deletion — so
  the component-updater’s job is to refuse bumps that are not known-good
  **before** they land in shared config.
- **Why every advertised region (and not a hand-maintained list):** discovery
  uses Azure’s mesh-supported locations via `locations: [all]` and requires the
  candidate to be available and AKS-compatible under `KubernetesOfficial` in
  **every** one of them (fail-closed: empty catalogs, API errors, and missing
  mappings block). We deliberately do **not** maintain an explicit region list
  in updater config — that list goes stale when Azure adds mesh regions or when
  we forget to update it, and an outdated allow-list can authorize an unsafe
  shared bump. Explicit location lists are allowed for read-only `check`
  diagnostics only; `update` refuses anything without `all`.
- **If ConfigMap drift already happened** (typical: Helm wrote a ConfigMap for a
  revision `istio-upgrade` skipped): set `svc.istio.versions` to a validated
  revision that is **not lower than** the highest installed control-plane
  revision, materialize, re-run the service-cluster / Istio pipeline, then
  manually delete `istio-shared-configmap-<bad-revision>` in `aks-istio-system`.
  Helm will not remove that orphan when the version field moves on.
- Only newer revisions in the same major, at most two minors ahead (`n+2`), are
  considered; prefer `n+1` when eligible everywhere. Downgrades are never proposed.
- The selected revision must be an advertised upgrade from the configured revision
  and list the configured AKS major/minor as compatible under `KubernetesOfficial`
  in every discovery region (`AKSLongTermSupport` alone is not enough).

## Pausing automatic updates

If discovery/update looks wrong, or you need to hold the line while fixing
drift, set `paused: true` on the component in this tool’s `config.yaml`.
`update` then reports `PAUSED` and skips discovery/writes for that component;
`check` still works for read-only diagnostics. With the updater paused, edit
`config/config.yaml` (e.g. `svc.istio.versions`) by hand to a validated value,
run `make -C config materialize`, and ship that change through the normal
pipeline. Clear `paused` (or set `paused: false`) when automatic bumps should
resume. Regional overlays stay manual via the existing config system either way.

## Authentication and retries

Authentication uses `DefaultAzureCredential` (set `AZURE_TOKEN_CREDENTIALS=dev`
for local `az` login). When `--subscription-id` / `AZURE_SUBSCRIPTION_ID` is
unset, the tool lists subscriptions for the credential and uses the sole
Enabled one (fail closed if zero or many). Discovery allows up to three SDK
retries per request with a 20-second per-try timeout. `--timeout` bounds the
entire operation (default 2m). HTTP 400/401/403/404 are not retried.

## Extending to other components

The shared contract is intentionally neutral:

```go
Check(ctx, CheckRequest{Current, Locations, ConstraintVersion}) (Result, error)
```

Providers read only the fields they need. Istio uses `Locations` +
`ConstraintVersion` (configured AKS version). A future Grafana provider can
ignore both and key only off `Current`.

To add a component:

1. Implement `providers.VersionProvider` (and optionally `UpdateValidator`).
2. Register the name in `providers.New` and add provider-specific required fields
   in `config.validateComponent`.
3. Add a component entry to `config.yaml` with `provider: <name>` and targets.
4. Keep Istio's all-regions / AKS-compat rules inside the Istio provider only.

The updater resolves a provider **per component**, so Istio and a future AKS or
Grafana component can run in one config file.

## Development

```bash
go test ./...
go vet ./...
GOTOOLCHAIN=go1.26.0 golangci-lint run --build-tags=E2Etests ./...
```

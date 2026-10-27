Load ../../CLAUDE.md and README.md in this directory for full context and policy.

## What this tool is

`tooling/component-updater` (`component-updater`) is a **shared** CLI for
discovering and optionally bumping version fields in `config/config.yaml`.
It is **not** an Istio-only tool: components are entries in
`tooling/component-updater/config.yaml`, each with a `provider`, and the
updater resolves providers per component.

**Wired today:** only `istio-mesh-revision` (`provider: azure-istio`).
**Not wired yet:** AKS, Grafana, and other providers (add via `providers.New` +
config validation — see README “Extending to other components”).

The tool does not deploy clusters, push commits, or open PRs.

## Shared bump procedure (any component)

### Prerequisites

- Homebrew Go (avoid a broken `GOROOT=/usr/local/go`)
- GNU Make 4.x on `PATH` (macOS: `brew install make` + gnubin)
- `az login` to a tenant that can read mesh catalogs (when the provider needs
  Azure discovery)
- `export AZURE_TOKEN_CREDENTIALS=dev`
- Optional: `AZURE_SUBSCRIPTION_ID` / `--subscription-id` only when the
  credential can see more than one Enabled subscription (otherwise the tool
  auto-selects the sole one)

### Local commands

```bash
cd tooling/component-updater
export AZURE_TOKEN_CREDENTIALS=dev

# Replace <component> with a key from config.yaml (today: istio-mesh-revision)
go run . check --config config.yaml --component <component> --timeout 5m
go run . update --config config.yaml --component <component> --timeout 5m
```

`update` materializes by default after a successful source edit. Use
`--skip-materialize` only to inspect the source edit first.

Repo-root: `make component-updater-check` / `make component-updater-update`
with `COMPONENT` (and `SUBSCRIPTION_ID` only if auto-discovery is ambiguous).

### After a successful bump

Commit together: `config/config.yaml`, `config/rendered/**`, and Helm fixtures.
A tooling-only change (no version bump) does not need rendered config churn.

### Pause / manual override (any component)

1. Set `paused: true` on that component in `tooling/component-updater/config.yaml`.
2. Edit the target field in `config/config.yaml` by hand to a validated value.
3. `make -C config materialize` and ship via the normal pipeline.
4. Clear `paused` when automatic bumps should resume.

`check` still works while paused.

## Istio-only rules (`istio-mesh-revision`)

These apply to the Istio provider / mesh revision component, not to the shared
CLI design:

- Keep `locations: [all]` for `update`. Do not hand-maintain a region list —
  it goes stale and can authorize an unsafe **shared** `svc.istio.versions` bump.
- Do not treat a regional `check` as permission to bump the shared default.
- Do not config-downgrade below the highest revision already installed on a
  cluster control plane (`docs/istio.md`); there is no supported downgrade path.
- If Helm left `istio-shared-configmap-<bad-revision>` in `aks-istio-system`
  after `istio-upgrade` skipped that revision: converge config to a validated
  current/forward revision, re-run, then delete the orphaned ConfigMap manually.

## Extending providers

Register in `internal/providers/factory.go` (`providers.New`) and add
provider-specific required fields in `config.validateComponent`. Keep each
provider’s safety policy inside that provider. Shared API:
`Check(ctx, CheckRequest)` — providers ignore unused request fields.

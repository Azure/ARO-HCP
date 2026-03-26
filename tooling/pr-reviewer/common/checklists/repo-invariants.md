# Repo Invariants

These are the shared invariants the reviewer should apply before domain-specific heuristics.

## Config and rendered outputs

- Changes to `config/config.yaml`, overlays, or schema should be paired with rendered output changes under `config/rendered/` when materialization is expected.
- Config naming and placement must still respect environment / region / scope uniqueness.

## Generated file families

- API/TypeSpec changes should propagate to OpenAPI, generated types, deepcopy, SDK, and relevant test fixtures.
- Helm/value/template changes should update `zz_fixture_TestHelmTemplate_*` outputs.
- Interface or API-shape changes should update mock/deepcopy artifacts when applicable.

## Go workspace blast radius

- Touching `internal/` often impacts multiple modules in `go.work`.
- Shared-package changes should be reviewed for cross-module compatibility, not only local package tests.

## Pipelines and retries

- Broad `pipeline.yaml` retry edits deserve skepticism; targeted retries are acceptable, generic retries that hide product issues are not.
- Changes that affect multiple service or management deployments should be treated as high blast radius even when the patch is small.

## Helm / Kubernetes / Bicep

- RBAC changes require least-privilege scrutiny.
- Namespace or cluster-type gating changes must match the intended deployment scope.
- Bicep/parameter changes need to preserve scope placement and rollout order.

## Tests and evidence

- Live reviews with a local checkout should apply `common/validation/command-policy.md`; `make verify` and `make lint` are the baseline commands.
- Review whether the PR contains the right kind of evidence for the changed risk profile: unit, integration, fixture, E2E, or rollout note.
- Report validation blockers and command-induced drift explicitly; do not silently skip or clean them up.
- Do not assume every failing test is caused by the PR; do not hand-wave away failures either.

## Ownership

- Use local `OWNERS` plus root `OWNERS` to understand who should weigh in when the change is risky or cross-domain.

## Helper contracts

- When a diff touches controller, database, API, or test-helper code, also apply `common/checklists/high-risk-helpers.md`.
- Prefer existing shared ID, error, cooldown, condition, and test helpers over hand-rolled replacements unless the PR shows why the shared contract is wrong.

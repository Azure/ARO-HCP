# Validation Command Policy

Use repo-native command execution as review evidence for live ARO-HCP reviews.

## Scope

- Apply this policy when the review target maps to a local checkout, current branch diff, commit range, or PR branch that the reviewer can run commands against.
- For synthetic prompt-only evals or historical exercises with no matching live checkout, report validation as `not applicable` or `not run (no live checkout)` instead of inventing command results.

## Always run for live reviews

- `make verify`
- `make lint`

Record each command as `pass`, `fail`, or `blocked`.

If a command is blocked by environment or toolchain state, say why. Do not silently skip it.

## Execution order

Prefer read-only validation before mutating verify commands.

Suggested order for live reviews:

1. `make lint`
2. conditional read-only commands such as `make test-compile`, `make test-unit`, and `make -C test build`
3. mutating verify commands such as `make verify`, `make verify-generate`, and `make verify-yamlfmt`

Before validation, note any pre-existing worktree drift so later command-induced changes are obvious.

If a mutating verify command rewrites files or fails mid-generation:

- report the drift immediately
- treat later repo-wide validation as tainted unless it is rerun from a clean or disposable checkout
- do not silently clean the tree back up

## Add these commands when the change warrants them

### Go behavior changes

Run `make test-unit` when the review covers behavior-changing Go code, especially under paths such as:

- `backend/`
- `internal/`
- `admin/`
- `cluster-service/`
- `maestro/`
- shared Go modules or controllers

### Wide cross-module or API-shape changes

Run `make test-compile` when the change touches shared `internal/` packages, multiple Go modules, or broad API/model surfaces and you want an explicit cross-workspace compile signal.

This does not replace `make test-unit`; it supplements it when the blast radius is broad.

### Generated, API, deepcopy, mock, or fixture families

Run `make verify-generate` when the change touches generated or generator-driven surfaces, such as:

- `internal/api/`
- `internal/api/zz_generated.deepcopy.go`
- `mock_*.go`
- `go:generate` outputs
- `test-integration/frontend/artifacts/DatabaseCRUD/`
- other generated artifacts expected to stay in sync with source changes

### YAML, Helm, config, policy, and rendered outputs

Run `make verify-yamlfmt` when the change touches YAML-heavy or rendered surfaces, such as:

- `config/`
- `config/rendered/`
- `acm/`
- `observability/`
- `deploy/`
- `helm-charts/`
- repo YAML that must stay formatter-clean and structurally consistent

### Test harness and Bicep-backed test artifacts

Run `make -C test build` when the change touches:

- `test/`
- `demo/bicep/`
- `test/e2e-setup/bicep/`
- generated test-artifact build inputs

This gives a safe build signal for the test harness without running full E2E.

### Reviewer tooling changes

Run `make -C tooling/pr-reviewer validate` when the change touches `tooling/pr-reviewer/`, `.claude/commands/arohcp/`, `.github/copilot-instructions.md`, or `.github/instructions/arohcp-reviewer.instructions.md`.

That reviewer-local target covers:

- `make -C tooling/pr-reviewer pycheck`
- `make -C tooling/pr-reviewer jsoncheck`
- `make -C tooling/pr-reviewer inventorycheck`
- `make -C tooling/pr-reviewer historycheck`
- `make -C tooling/pr-reviewer commandcheck`
- `make -C tooling/pr-reviewer copilotcheck`
- `make -C tooling/pr-reviewer fixturecheck`
- `make -C tooling/pr-reviewer classify-test`
- `make -C tooling/pr-reviewer evalrunner-test`

Use it to validate the Python tools, machine-readable reviewer assets, manifest-to-inventory consistency, history corpus schema compliance, Claude command entrypoints, Copilot instruction entrypoints, historical fixture structure, routing goldens, eval-runner logic, and eval file references without relying on the main repo Makefile.

### Automated behavioral evals

Run `make -C tooling/pr-reviewer evalcheck SELECTION=mixed` when you need behavioral evidence that the reviewer still produces the right kind of review.

Use targeted selections such as `SELECTION=13` while iterating on a specific lesson, or `SELECTION=all` for a deeper sweep.

This target is intentionally separate from `make -C tooling/pr-reviewer validate` because it:

- executes the reviewer headlessly through the local `claude` CLI
- uses an automated judge to score the produced review
- is slower and model-dependent even when the package wiring is correct

## Focused fallback checks when repo-wide validation is blocked

If repo-wide commands are blocked by toolchain or environment issues, add focused non-mutating checks when they can recover useful signal for the touched paths.

Good fallback examples:

- `go test ./backend/pkg/controllers/operationcontrollers ./internal/database`
- `go test ./test-integration/utils/databasemutationhelpers`
- package-scoped `go test -c` or similarly narrow compile checks
- `make -C test build` for test-harness and Bicep-backed paths

These fallback checks do not replace the blocked repo-wide commands. Report them as supplemental evidence.

## Commands not to auto-run during review

- Do not run mutable update targets such as `make -C test update` unless the user explicitly asked for mutation during the review session.
- Do not automatically run live E2E jobs from review flow. If the change needs that level of proof, call out the missing evidence or escalate to the right owners.

## Reporting rules

- Report validation results in the review output, not only in scratch notes.
- If a verify command rewrites generated files or formatting, treat that drift as review signal and state which files moved.
- Use command failures as evidence when they sharpen a real concern; do not dump raw logs into the review.
- Distinguish `fail`, `blocked`, and `not applicable` clearly.
- If you used focused fallback checks because repo-wide validation was blocked, say that explicitly.

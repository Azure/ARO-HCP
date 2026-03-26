# Reviewer Manifest

This file is the index for the ARO-HCP PR reviewer. Treat the listed files as the authoritative system. The README only helps humans find things.

## Entry points

- `SKILL.md` — portable orchestrator workflow and trigger description.
- `CLAUDE.md` — repo-local entry point that points back to `SKILL.md`.
- `AGENTS.md` — minimal loader for environments that look for agent notes.
- `Makefile` — convenience targets for script validation, reviewer asset validation, asset inventory checks, history corpus validation, path classification, and history bootstrap.
- `common/tools/run_reviewer_evals.py` — shared automated eval runner used by the make-based eval harness and the Claude eval command.
- `.claude/commands/arohcp/review.md` — reusable Claude project command for reviewing a PR, commit range, or current diff.
- `.claude/commands/arohcp/eval.md` — reusable Claude project command for running reviewer evals, including the mixed-domain suite.
- `.github/copilot-instructions.md` — repo-wide Copilot entrypoint for invoking the in-repo reviewer on review requests.
- `.github/instructions/arohcp-reviewer.instructions.md` — path-scoped Copilot instructions for keeping reviewer packaging and validation changes aligned.

## Domain specialists

- `sub-reviewers/resource-provider-api.md`
- `sub-reviewers/backend-state.md`
- `sub-reviewers/cluster-service.md`
- `sub-reviewers/maestro.md`
- `sub-reviewers/lifecycle-operators.md`
- `sub-reviewers/azure-infra-bicep.md`
- `sub-reviewers/config-pipelines.md`
- `sub-reviewers/observability-testing-tooling.md`
- `sub-reviewers/cross-cutting.md`

## Shared authoritative assets

- Routing and ownership: `common/domain-routing/path-routing.json`, `common/owners/domain-owners.json`
- Repo invariants, helper hotspots, and validation: `common/checklists/repo-invariants.md`, `common/checklists/high-risk-helpers.md`, `common/validation/command-policy.md`
- Seed historical lessons: `common/learnings/seed-history-lessons.md`
- Triage and output: `common/triage/severity-confidence.md`, `common/output-contract/review-format.md`
- Evidence and style: `common/evidence/evidence-requirements.md`, `common/review-style/comment-style.md`
- Restraint and fallback: `common/scope-boundaries/non-goals.md`, `common/fallback-behavior/low-confidence.md`
- Risk and intent: `common/risk-model/blast-radius.md`, `common/change-intent/heuristics.md`
- Maintenance: `common/update-workflows/incremental-refresh.md`, `common/staleness/freshness-rules.md`, `common/coverage/coverage-model.md`, `common/versioning/policy.md`
- Safety and rollout: `common/human-escalation/rules.md`, `common/security-privacy/history-handling.md`, `common/acceptance-criteria/readiness.md`

## Machine-readable assets

- `common/schema/history-corpus.schema.json`
- `common/taxonomy/finding-types.json`
- `common/suppressions/suppressions.json`
- `common/staleness/seed-status.json`
- `common/coverage/seed-coverage.json`
- `common/versioning/version.json`
- `common/versioning/asset-inventory.json`
- `tests/history-corpus-smoke.json`

## Seed rationale and calibration

- `fixtures/historical-prs/*.md`
- `calibration/great-review-patterns.md`

## Evaluation

- `evals/evals.json`
- `tests/README.md`

## Rule for future edits

Do not bury new reviewer behavior in README prose. Add or update the authoritative asset, then add a fixture and an eval whenever the behavior is important enough to keep.

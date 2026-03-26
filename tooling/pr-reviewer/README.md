# ARO-HCP PR Reviewer

This directory contains the in-repo ARO-HCP PR reviewer: a high-signal review system that routes changed paths to domain specialists, reuses historical review lessons, runs repo-native validation, and works through both Claude and GitHub Copilot entrypoints.

The goal is to keep reviewer behavior in the same assets the agent actually uses: routing, sub-reviewers, fixtures, evals, and validators, instead of letting it drift into prose-only documentation.

## Start here

- `SKILL.md` is the main reviewer workflow.
- `MANIFEST.md` is the index of authoritative assets.
- `Makefile` provides easy entry points for script validation, reviewer asset validation, path classification, and history bootstrap.
- `common/validation/command-policy.md` defines the baseline and conditional repo-native commands for live reviews.
- `common/checklists/high-risk-helpers.md` captures shared helper families and recurring code patterns that carry review risk in controllers, IDs, errors, and test helpers.
- `sub-reviewers/` holds the domain specialists.
- `common/` holds shared rules, routing, evidence standards, and maintenance policy.
- `fixtures/` and `calibration/` hold seed historical rationale.
- `evals/evals.json` is the initial evaluation set.
- `.claude/commands/arohcp/review.md` packages the reviewer as a reusable Claude project command.
- `.claude/commands/arohcp/eval.md` packages the eval flow for Claude, including the mixed-domain suite.
- `.github/copilot-instructions.md` packages the same reviewer for Copilot as a repo-wide review entrypoint.
- `.github/instructions/arohcp-reviewer.instructions.md` keeps Copilot path-scoped reviewer packaging aligned when editing reviewer assets.

## How it works

At runtime, the reviewer follows a compact flow:

1. Claude commands or Copilot instructions point the agent at `tooling/pr-reviewer/`.
2. `SKILL.md` and `MANIFEST.md` define the authoritative workflow and asset set.
3. Changed paths are classified with `common/tools/classify_paths.py` and `common/domain-routing/path-routing.json`.
4. The reviewer loads `sub-reviewers/cross-cutting.md`, every matched domain specialist, and the router-selected `history_fixtures`.
5. Validation runs from `common/validation/command-policy.md`, including reviewer-local validation when the reviewer itself changes.
6. Output is shaped by the review format, evidence rules, severity triage, and the no-findings path.

## How to run the reviewer

Run from the repository root so the packaged entrypoints and reviewer assets are all in scope.

### Claude

The Claude packaging lives under `.claude/commands/arohcp/`.

- `cd /path/to/ARO-HCP`
- `claude`
- `/arohcp:review`

Common examples:

- Review the current branch diff: `/arohcp:review`
- Review a PR: `/arohcp:review 4457`
- Review a commit range: `/arohcp:review origin/main..HEAD`
- Review specific paths: `/arohcp:review backend/ internal/api/`
- Run the mixed eval suite: `/arohcp:eval mixed`
- Run specific eval ids: `/arohcp:eval 9,11`

### GitHub Copilot

The Copilot packaging lives under `.github/copilot-instructions.md` and `.github/instructions/arohcp-reviewer.instructions.md`.

Start Copilot in the repository root:

- `cd /path/to/ARO-HCP`
- `Copilot`
- optional sanity check: `/instructions`

Copilot does not use the Claude slash command entrypoints, so ask for the review in plain English and explicitly mention the in-repo reviewer.

Common examples:

- `Review my current branch diff using the in-repo ARO-HCP reviewer under tooling/pr-reviewer/.`
- `Review the diff against origin/main using the ARO-HCP reviewer.`
- `Review this commit range with the ARO-HCP reviewer: origin/main..HEAD`
- `Review these paths with the ARO-HCP reviewer: backend/ internal/api/`
- `Run the mixed ARO-HCP reviewer eval suite from tooling/pr-reviewer/evals/evals.json.`
- `make -C tooling/pr-reviewer evalcheck SELECTION=mixed`
- `make -C tooling/pr-reviewer evalcheck SELECTION=13`

`make -C tooling/pr-reviewer evalcheck` runs the shared automated eval runner. It executes the reviewer headlessly with the local `claude` CLI and scores the output with an automated judge, so it is intentionally separate from `make -C tooling/pr-reviewer validate`.

## How to contribute

Keep contribution changes operational and source-of-truth driven:

1. Start with `SKILL.md`, `MANIFEST.md`, and the relevant files under `common/` or `sub-reviewers/`.
2. Change reviewer behavior in authoritative assets, not just in `README.md`, `.claude/commands/`, or `.github/` instruction files.
3. Keep Claude and Copilot entrypoints thin; if packaging changes, update the matching validators instead of duplicating logic there.
4. If routing or ownership changes, update `common/domain-routing/path-routing.json`, `common/owners/domain-owners.json`, and the affected sub-reviewer scope.
5. If review behavior changes in a meaningful way, add or refresh the supporting fixture, calibration example, eval, or regression test.
6. Run `make -C tooling/pr-reviewer validate` before handing the change off.
7. Run `make -C tooling/pr-reviewer evalcheck SELECTION=<targeted ids or mixed>` when you change reviewer behavior or eval assets and want behavioral evidence, not just packaging checks.
8. Rerun a smoke test when you change entrypoints, routing, validation policy, or classifier behavior.
9. Teach the reviewer something new only when it reflects a recurring pattern with real correctness, rollout, security, persistence, generated-artifact, or operational risk.
10. Do not encode style rules, generic utilities, churn-heavy internals, or checks already enforced reliably by tooling.
11. Prefer escalation or a caveat over a hard new rule when the evidence is incomplete or the behavior depends on missing rollout context.
12. Promote a real review lesson into a fixture, calibration note, or eval when you expect the same mistake to recur and the reviewer should catch it again automatically.

## Source of truth rule

If a reviewer behavior changes, update:

1. the authoritative rule asset under `common/` or `sub-reviewers/`
2. at least one fixture or calibration artifact showing the why
3. the eval/test set when behavior should be exercised automatically

## What this is not

This is not a long-form design doc set. The markdown here is operational: routing, review rules, evidence format, and maintenance instructions.

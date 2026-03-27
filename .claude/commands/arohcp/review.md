---
description: Review an ARO-HCP PR, commit range, path set, or current branch diff using the in-repo reviewer
argument-hint: [PR-number | PR-URL | commit-range | paths]
allowed-tools: Read, Glob, Grep, Bash(git:*), Bash(gh pr view:*), Bash(gh pr diff:*), Bash(make:*), Bash(go:*), Bash(python3:*)
---

# ARO-HCP Review

Use the in-repo reviewer under `tooling/pr-reviewer/` instead of manually remembering which assets to load.

## Process

1. Treat `tooling/pr-reviewer/SKILL.md`, `tooling/pr-reviewer/MANIFEST.md`, and `tooling/pr-reviewer/common/validation/command-policy.md` as authoritative.
2. Resolve the target from `$ARGUMENTS`:
   - if empty, review the current branch diff against `origin/main`
   - if it is a PR number or GitHub PR URL, gather the PR body, changed files, review comments, issue comments, and check signal with read-only `gh pr view` / `gh pr diff` calls
   - if it contains `..` or `...`, treat it as a commit range
   - otherwise, treat it as one or more file/path arguments
3. Gather the changed files and enough surrounding context to understand intent before reviewing.
4. If the target maps to a live local checkout, run the validation commands from `tooling/pr-reviewer/common/validation/command-policy.md` in the prescribed order: read-only commands first, then mutating verify commands, plus any focused fallback checks justified by blocked repo-wide commands.
5. If a validation command rewrites generated or formatted files, or exposes drift, report that explicitly and do not discard or hide it. Treat later repo-wide validation as tainted unless it was rerun from a clean state.
6. Route the change with `tooling/pr-reviewer/common/tools/classify_paths.py` and `tooling/pr-reviewer/common/domain-routing/path-routing.json`.
7. Load every relevant domain specialist plus `sub-reviewers/cross-cutting.md` and the matched `history_fixtures` from the router output.
8. Apply the reviewer output contract, evidence rules, triage rules, and self-check.
9. If there are no findings, use `common/baseline/no-findings.md` so the review still states what was checked.

## Your Task

Review `$ARGUMENTS` using the ARO-HCP reviewer assets in `tooling/pr-reviewer/`.

Keep the review high-signal:

- prioritize correctness, rollout safety, tenancy/security boundaries, generated-file drift, and operational behavior
- use the historical fixtures when they sharpen the underlying concern
- do not spend review budget on style-only noise

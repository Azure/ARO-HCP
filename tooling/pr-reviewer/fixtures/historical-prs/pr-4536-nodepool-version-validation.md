# PR #4536 — Nodepool version validation tightening

- **Title:** `fix: add nodepool validation for restrict X.Y.Z version format`
- **Merged:** 2026-03-20
- **Touched areas:** `frontend/`, `internal/admission/`, `internal/validation/`, `test-integration/frontend/`

## Why it mattered

The change tightened nodepool version validation. Reviewers were not satisfied with a loose helper that still accepted `X.Y` when product behavior required `X.Y.Z`, while also allowing prerelease/nightly values in the AFEC path.

## High-signal review moments

- A reviewer suggested using `semver.Parse` specifically because the code needed exact validation instead of a must/loose parser.
- Reviewers asked whether `X.Y` was still allowed in another validation path and pushed to make the rule consistent.
- Reviewers explicitly asked for tests covering both the rejected case (`X.Y`) and allowed prerelease/nightly cases.
- Reviewers raised compatibility concerns around old vs new values so unrelated updates would not break.

## Reusable lesson

For validation-tightening PRs, the reviewer should check both semantics and compatibility:

- exact format correctness
- allowance for old stored data where required
- actionable errors
- tests that prove both refusal and acceptance paths

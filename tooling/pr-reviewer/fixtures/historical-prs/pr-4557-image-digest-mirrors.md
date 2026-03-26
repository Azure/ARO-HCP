# PR #4557 — ImageDigestMirrors plumbing

- **Title:** `frontend: Plumb ImageDigestMirrors field to Cluster Service#4305`
- **Merged:** 2026-03-20
- **Touched areas:** TypeSpec, OpenAPI, internal API, deepcopy, validation, OCM conversion, SDK, E2E setup, verifiers, and test fixtures

## Why it mattered

This was a classic cross-layer propagation change. The correctness question was not just whether one code path compiled, but whether every hand-written and generated layer stayed aligned.

## Review/issue context

- Review comments were light, but issue comments show the author differentiated a unit flake from the product change and pushed the flake chase into follow-up work.
- The PR touched enough layers that drift between source-of-truth and generated artifacts was the primary review risk.

## Reusable lesson

For API/model plumbing PRs, the reviewer should verify end-to-end propagation across:

- source definition
- generated OpenAPI/SDK/deepcopy artifacts
- validation
- conversion/adapters
- E2E setup and verifiers

And when tests fail, separate product risk from likely flake with evidence.

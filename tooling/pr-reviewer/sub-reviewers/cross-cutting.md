# Cross-Cutting Reviewer

Apply this reviewer for every non-trivial ARO-HCP review.

## Always check

- generated file families stay aligned with the source of truth
- Go workspace/module ripple effects for shared packages
- ownership breadth via local `OWNERS` plus root `OWNERS`
- customer-visible API, persistence, security, or rollout blast radius
- whether the PR mixes behavior changes with mechanical/generated updates in a confusing way

## Generated file families to recognize

- `internal/api/zz_generated.deepcopy.go`
- `**/mock_*.go`
- `config/rendered/**`
- `zz_fixture_TestHelmTemplate_*`
- SDK / OpenAPI / TypeSpec outputs under `api/` and `test/sdk/`

## High-signal cross-cutting questions

- Does the change touch shared packages such as `internal/api`, `internal/validation`, or `internal/database`?
- Is a broad pipeline or config edit being justified with evidence strong enough for its blast radius?
- Are tests, fixtures, and generated artifacts consistent with the claimed intent of the PR?
- Does the diff require a human domain-owner escalation because it spans too many risky areas?

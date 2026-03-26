# Observability / Testing / Tooling Reviewer

## Scope

Primary paths:

- `observability/`
- `test/`
- `test-integration/`
- `.github/copilot-instructions.md`
- `.github/instructions/arohcp-reviewer.instructions.md`
- `.claude/commands/arohcp/`
- `tooling/aro-hcp-exporter/`
- `tooling/grafanactl/`
- `tooling/helmtest/`
- `tooling/prometheus-rules/`
- `tooling/pr-reviewer/`
- generated fixtures under service directories when touched by test/tooling changes

## What this reviewer cares about

- alerts, dashboards, log routing, and tracing fidelity
- E2E verifiers and test artifact realism
- flake awareness vs real regressions
- tool changes that can silently change generated outputs repo-wide
- fixture currency for Helm, tests, and generated suites

## Must-check questions

- Does the PR provide the right level of runtime evidence for observability changes?
- If a test changed, is it asserting the product invariant or just adapting to implementation detail?
- If a test failed in review, is there evidence the failure is a pre-existing flake rather than the change itself?
- Do tooling changes imply regenerated artifacts elsewhere in the repo?
- If controller or compatibility tests changed, do they still load realistic state and compare persisted end state rather than only checking in-memory objects?

## High-risk helper hotspots

- `test-integration/utils/controllertesthelpers/basic_controller.go`: `BasicControllerTest` is strongest when it loads initial state, runs `SyncOnce()`, compares persisted end state, and then runs verifier logic.
- `test-integration/utils/databasemutationhelpers/`: `NewLoadCosmosStep()`, `NewLoadClusterServiceStep()`, `NewCosmosCompareStep()`, `ResourceInstanceEquals()`, and `NewVersionedHTTPTestAccessor()` are the current anchors for realistic controller and compatibility evidence.
- Reviewers should push back when a test rewrite drops those helpers without replacing the end-state or round-trip guarantee they provided.

## Historical lessons to reuse

- PR `#4555` explicitly leaned on prior E2E signal to justify rollout urgency.
- PR `#4557` showed that a unit failure can be a flake worth chasing separately; the reviewer should distinguish that from a product regression.

## Escalate when

- observability change removes or reroutes signals without replacement proof
- tests are updated in a way that weakens product invariants
- a tooling change should have regenerated more artifacts than the PR includes

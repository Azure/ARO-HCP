# Great Review Patterns (Seed Calibration)

Good ARO-HCP review comments from the seed corpus share a few traits.

## 1. They are specific and actionable

Example pattern from PR `#4536`:

- name the exact parser or behavior that is too loose
- say what input shape should fail or pass
- ask for the targeted test coverage that proves it

## 2. They reason about compatibility, not just local correctness

Example pattern from PR `#4536`:

- reviewers asked how stricter validation would interact with existing stored data and unrelated updates

## 3. They scale their scrutiny to blast radius

Example pattern from PR `#4318`:

- a tiny textual change across many pipeline files was still treated as important because it changed rollout behavior repo-wide

## 4. They ask for rollout evidence when operational risk is real

Example pattern from PR `#4555`:

- rollout confidence came from fixture changes plus prior E2E signal, not from optimism alone

## 5. They keep flakes and product regressions separate

Example pattern from PR `#4557`:

- a unit flake was acknowledged and quarantined into follow-up work instead of being confused with the functional change itself

## 6. They force persisted-state semantics to be explicit

Example pattern from PR `#1766`:

- reviewers asked what the real primary key was
- they pushed for clearer field naming when billing identity semantics mattered
- they asked what happened if the query returned more than one row instead of letting duplicate-state behavior stay implicit

## 7. They cross-check observability prose, math, and generated outputs

Example pattern from PR `#2252`:

- reviewers caught mismatches between the written SLO definition, the PromQL, and the threshold math
- they verified that tested rules, generated Bicep outputs, and dashboards all moved together

## 8. They simplify runtime observability plumbing

Example pattern from PR `#1229`:

- reviewers preferred direct image references, `stderr` logging, and fewer volumes/files when adding maestro metrics
- they treated unnecessary operational indirection as review debt, not harmless implementation detail

## 9. They scrutinize Helm/operator migrations for privilege and ownership drift

Example pattern from PR `#1073`:

- reviewers questioned custom image sourcing
- they flagged obsolete build glue left behind during the Helm move
- they pushed RBAC toward least privilege because the runtime service account inherits what the chart grants

## 10. They force controller logic to follow the real state graph

Example pattern from PR `#3954`:

- reviewers challenged shortcuts around upgrade-path selection
- they pushed the code to reason from the actual version and available upgrade path, not duplicated helper state
- they preferred explicit resolver-style decomposition when controller decision trees got too dense
- they asked what stale `active_versions` or `desired_version` state would do on the next reconcile, rather than treating a one-pass happy path as sufficient evidence
- they distinguished "seed version missing from the next-minor graph" from "next minor channel missing" before accepting fallback version selection

## 11. They treat admin security surfaces as schema and identity problems

Example pattern from PR `#3679`:

- reviewers pushed for required and immutable fields
- they preferred deriving cluster identity from the HCP resource ID over accepting user-supplied identifiers
- they asked for tighter field docs because unclear semantics are a security and operability risk on admin APIs

## 12. They review broad observability rollouts as operational systems

Example pattern from PR `#1633`:

- reviewers asked how the new Prometheus instance would itself be monitored
- they pushed on persistence, anti-affinity, and monitor usefulness rather than assuming “more metrics” was automatically correct
- they treated docs and deployment references as part of the rollout contract

## 13. They lean on helper contracts when the codebase already encodes the safety rule

Example pattern from current ARO-HCP helpers:

- reviewers push back on hand-built resource IDs when `ToClusterResourceID*`, `ToNodePoolResourceID*`, or `ToOperationResourceIDString()` already define the supported identity shape
- they treat `404`/`409`/`412` classification, `CloudError` construction, and active-operation-aware cooldowns as behavior contracts, not replaceable plumbing
- they ask whether controller tests still prove persisted end state using `BasicControllerTest` and `databasemutationhelpers`, instead of accepting a lighter but weaker test path

## Anti-patterns to avoid

- generic “add tests” comments with no missing behavior identified
- style-only notes in high-risk PRs
- comments that ignore generated artifacts or rendered outputs already present in the diff
- broad architectural claims unsupported by the change under review
- accepting SLO text, query logic, and threshold math that disagree with each other
- treating Helm/operator migrations as packaging-only changes while skipping RBAC, runtime identity, or obsolete-pipeline review
- duplicating upgrade or identity state when the real source of truth already exists nearby
- treating a repo-wide observability rollout as harmless YAML churn

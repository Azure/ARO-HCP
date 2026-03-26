# PR #3954 — Control plane upgrade controller flow

- **Title:** `feat: add control plane upgrade controller flow`
- **Merged:** 2026-02-16
- **Touched areas:** `backend/pkg/controllers/upgradecontrollers/`, `internal/api/`, `internal/cincinatti/`, `internal/ocm/`, generated deepcopy output, integration/frontend artifacts

## Why it mattered

This PR introduced a new control plane upgrade controller flow. The hard part was not simply adding a controller, but ensuring the desired-version logic followed the real upgrade graph and state model instead of baking in shortcuts that would later mis-pick versions or duplicate state already available elsewhere.

## High-signal review moments

- Reviewers pushed on whether the logic considered the actual current version, available upgrades, and next-minor behavior together rather than assuming a simplified path.
- Reviewers explicitly asked not to duplicate fields from customer or persisted state when those values could already be read from the right source of truth.
- Strong comments also steered the implementation toward clearer resolver-style decomposition so the controller decision tree stayed understandable and testable.
- Review signal stayed strongest when it asked what happened after one reconcile completed: did temporary `active_versions` and `desired_version` state get pruned or cleared, or could stale upgrade intent leak into the next controller pass?
- Good review comments also distinguished "the next minor channel does not exist yet" from "this specific seed version is not present in the next-minor graph." Treating those as the same thing can silently pick a non-gateway patch and break the preserved next-minor path.

## Reusable lesson

For upgrade-controller PRs, the reviewer should check:

- whether desired-version logic follows the real upgrade graph instead of a local shortcut
- whether state or version fields are duplicated unnecessarily
- whether the controller decomposition keeps version resolution, graph lookup, and state reads understandable
- whether controller and integration evidence are strong enough for a control-plane behavior change
- whether persisted or derived upgrade state is lifecycle-safe: stale `active_versions` should not bias later graph intersections, and stale `desired_version` should not keep retriggering obsolete work once the cluster catches up
- whether graph fallback logic distinguishes "missing channel" from "missing seed version in that channel" before choosing the newest candidate
- whether tests cover controller-to-controller lifecycle behavior instead of only isolated resolver helpers

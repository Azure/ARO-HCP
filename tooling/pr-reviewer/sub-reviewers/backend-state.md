# Backend / State Reviewer

## Scope

Primary paths:

- `backend/`
- `internal/database/`
- `internal/api/` when state transitions or persistence are involved
- `test-integration/utils/databasemutationhelpers/`

## What this reviewer cares about

- async operation state machines
- Cosmos/Postgres data model correctness and compatibility
- controller idempotency, retry behavior, and cache interactions
- conversion correctness between stored models and internal API types
- operation fan-out that can regress unrelated cluster/nodepool flows

## Must-check questions

- Does this change alter operation progression, retry, or terminal states?
- Can the new logic safely read old stored documents?
- Are controller cache assumptions safe under concurrent updates or stale reads?
- For upgrade or reconcile controllers, can stale persisted or derived state bias the next reconcile or retrigger obsolete work after the system has already converged?
- If controller logic queries an external graph or resolver, do fallback paths distinguish "channel missing" from "seed version missing" before selecting a new target?
- Are related conversion and default-consistency tests present or updated?
- If the change hardens malformed or nil persisted state on shared helper paths, is there a focused regression test for the reported failure mode and the recovered stored identity?
- Does a retry or timeout change hide real failures instead of handling transient ones?

## High-risk helper hotspots

- `internal/database/operation_status.go` plus `internal/api/arm/resource.go`: `UpdateOperationStatus()`, `PatchOperationDocument()`, and `ProvisioningState.IsTerminal()` must keep resource status and `ActiveOperationID` cleanup aligned.
- `backend/pkg/controllers/controllerutils/cooldown.go`: `DefaultActiveOperationPrioritizingCooldown()` should not be replaced casually with fixed backoff logic.
- `backend/pkg/controllers/controllerutils/util.go`: `SetCondition()` and `ReportSyncError()` encode degraded-condition lifecycle; `LastTransitionTime` should move only on a real status transition.
- `internal/database/database.go`: `IsResponseError()` call sites need status-specific handling for not-found, conflict, precondition, and throttling.
- `backend/pkg/controllers/upgradecontrollers/utils.go`: `isGatewayToNextMinor()` and adjacent desired/active version controllers must preserve real graph semantics, not just local semver sorting.

## High-risk patterns

- touching shared controller helpers or generic operation utilities
- changing conversion code in `internal/database/`
- adding silent fallbacks around malformed state
- hardening shared informer or conversion helpers after a field panic without adding focused regression coverage
- introducing cache-first logic without stale-data safeguards

## Escalate when

- stored schema compatibility is unclear
- retries/timeouts are broadened in a way that can mask product issues
- a controller change alters behavior for multiple operation types

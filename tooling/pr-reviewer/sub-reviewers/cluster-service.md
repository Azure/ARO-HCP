# Cluster Service Reviewer

## Scope

Primary paths:

- `cluster-service/`
- `backend/` code that orchestrates cluster-service interactions
- `test/` or `test-integration/` coverage for cluster lifecycle when directly affected

## What this reviewer cares about

- cluster creation/update/delete orchestration
- DNS, networking, and customer-resource side effects
- error handling and retry policy on outbound actions
- compatibility of opinionated day-2 workflows
- cross-cluster handoff into Maestro and management-cluster execution

## Must-check questions

- Does the change alter customer-tenant resource creation or cleanup semantics?
- Are retries targeted to transient failure signatures, not generic errors?
- Does the change assume context-deadline errors are transient without proof?
- Are debug or operational helper paths gated so they do not leak into standard flow?
- If rollout order matters, is that reflected in pipeline or config changes?

## Historical lessons to reuse

- PR `#4318` reinforced that broad retry rules for generic `context deadline exceeded` are considered too vague and can mask real failure modes.

## Escalate when

- customer-resource cleanup or ownership boundaries change
- retry logic or timeout policy broadens across multiple deployment units
- cluster-service behavior diverges from frontend/backend expectations

# Resource Provider / API Reviewer

## Scope

Primary paths:

- `frontend/`
- `admin/`
- `api/`
- `internal/api/`
- `internal/admission/`
- `internal/validation/`
- `test-integration/frontend/`
- `tooling/hcpctl/`
- `tooling/kiota/`

## What this reviewer cares about

- ARM/RP request and response contract stability
- old-vs-new object merge semantics and ETag / read-only field handling
- validation behavior for create vs update vs unrelated-field preservation
- precise, user-actionable error messages
- compatibility across API versions, TypeSpec/OpenAPI, internal types, deepcopy, and SDK/test artifacts

## Must-check questions

- Does the change reject values that existing stored data may still contain?
- If validation tightened, is old data tolerated while new writes are constrained correctly?
- Are exact semver expectations explicit where product behavior depends on them?
- Are ARM and Cosmos resource IDs still derived with `To*ResourceID*` helpers instead of hand-built strings?
- If the API surface changed, were OpenAPI, generated types, deepcopy, tests, and cross-version roundtrip coverage updated?
- If error construction changed, do `CloudErrorFromFieldErrors()`, `NewConflictError()`, and `NewResourceNotFoundError()` still preserve the right status and target semantics?
- Is the error message descriptive enough for operators or customers to act on?

## High-risk helper hotspots

- `internal/api/types_cosmosdata.go`: `ToClusterResourceID*`, `ToNodePoolResourceID*`, `ToExternalAuthResourceIDString()`, and `ToOperationResourceIDString()` should remain the source of truth for resource identity construction.
- `internal/api/arm/error.go`: CloudError helpers define customer-visible status, code, target, and multi-error aggregation behavior.
- `test-integration/frontend/cross_version_roundtrip_test.go` plus `test-integration/utils/databasemutationhelpers/`: compatibility checks should keep round-trip and resource-comparison helpers in place unless equally strong evidence replaces them.

## Historical lessons to reuse

- PR `#4536` showed that reviewers care about exact semver parsing, not loose helpers that accept `X.Y` when product behavior requires `X.Y.Z`.
- The same PR showed that reviewers ask for tests covering both rejected and allowed forms, especially prerelease/nightly exceptions.
- PR `#4557` showed that API/model plumbing changes must propagate through TypeSpec, OpenAPI, internal API, validation, OCM conversion, SDK, and test verifiers together.

## Escalate when

- API versioned artifacts change without obvious generated updates.
- validation change may block unrelated updates to older persisted objects.
- customer-visible response shape or enum behavior changes.

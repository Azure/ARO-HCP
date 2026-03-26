# High-Risk Helpers and Patterns

Use this asset when the diff touches controller, database, API, or test-helper code. These helpers encode real product and rollout contracts; replacing them with local shortcuts should trigger review scrutiny.

## 1. Operation status and condition lifecycle

Anchors:

- `internal/api/arm/resource.go`: `ProvisioningState.IsTerminal()`
- `internal/database/operation_status.go`: `UpdateOperationStatus()`, `PatchOperationDocument()`
- `backend/pkg/controllers/controllerutils/util.go`: `SetCondition()`, `ReportSyncError()`

Review when:

- terminal transitions stop clearing `ActiveOperationID`
- `LastTransitionTime` is rewritten without a real status transition
- controller error paths can leave stale degraded or active state behind
- a shortcut updates the operation document but not the resource, or vice versa

## 2. Cooldown and active-operation prioritization

Anchors:

- `backend/pkg/controllers/controllerutils/cooldown.go`: `DefaultActiveOperationPrioritizingCooldown()`, `ActiveOperationBasedChecker`, `TimeBasedCooldownChecker`

Review when:

- active-operation-aware cooldowns are replaced with fixed timers
- new controllers skip active-operation lister input
- cooldown changes can thrash hot controllers or hide stuck work

## 3. Resource ID derivation and identity safety

Anchors:

- `internal/api/types_cosmosdata.go`: `ToClusterResourceID*`, `ToNodePoolResourceID*`, `ToExternalAuthResourceIDString()`, `ToOperationResourceIDString()`
- `internal/database/convert_generic.go`: `InternalToCosmosGeneric()`, `CosmosGenericToInternal()`
- `backend/pkg/informers/informers.go`: `resourceGroupIndexFunc()`, `clusterResourceIDIndexFunc()`
- controller key types in `backend/pkg/controllers/controllerutils/`

Review when:

- code hand-builds ARM or Cosmos IDs with string joins or slices
- parent and child resource IDs might be mixed up
- tenant, subscription, resource-group, or cluster identity becomes caller-supplied instead of derived
- shared read or index paths start backfilling missing `ResourceID` data without focused regression tests for both the failure mode and the recovered identity

## 4. Database error classification and optimistic concurrency

Anchors:

- `internal/database/database.go`: `IsResponseError()`
- `internal/database/operation_status.go`: `UpdateOperationStatus()`, `PatchOperationDocument()`

Review when:

- `404`, `409`, `412`, and `429` are handled as the same class of error
- replace or patch flows skip precondition or etag semantics
- retry logic turns a correctness issue into transient noise

## 5. Version graph and semver fallback helpers

Anchors:

- `backend/pkg/controllers/upgradecontrollers/utils.go`: `isGatewayToNextMinor()`
- `backend/pkg/controllers/upgradecontrollers/control_plane_desired_version_controller.go`
- `backend/pkg/controllers/upgradecontrollers/control_plane_active_version_controller.go`

Review when:

- fallback logic does not distinguish missing next-minor channels from missing seed versions in that graph
- active or desired version state can leak across reconciles
- semver sorting or filtering changes without clear bounds or evidence

## 6. API error shaping and customer-visible error contracts

Anchors:

- `internal/api/arm/error.go`: `CloudErrorFromFieldErrors()`, `NewCloudError()`, `NewConflictError()`, `NewResourceNotFoundError()`
- frontend handlers under `frontend/pkg/frontend/` that call those helpers

Review when:

- field errors collapse into opaque generic failures
- conflict or not-found behavior changes without resource-target context
- new error paths bypass the standard CloudError helpers

## 7. Controller and round-trip test helper patterns

Anchors:

- `test-integration/utils/controllertesthelpers/basic_controller.go`: `BasicControllerTest`
- `test-integration/utils/databasemutationhelpers/`: `NewLoadCosmosStep()`, `NewLoadClusterServiceStep()`, `NewCosmosCompareStep()`, `ResourceInstanceEquals()`, `NewVersionedHTTPTestAccessor()`

Review when:

- tests stop loading realistic initial state or stop comparing persisted end state
- controller tests verify only in-memory state after `SyncOnce()`
- API compatibility tests remove round-trip or resource-comparison helpers without equivalent coverage
- panic or nil-recovery fixes in shared controller or conversion helpers land without a focused regression test for the reported failure path

## 8. Maestro bundle creation vs content persistence

Anchors:

- `backend/pkg/controllers/create_cluster_scoped_maestro_readonly_bundles_controller.go`
- `backend/pkg/controllers/read_and_persist_cluster_scoped_maestro_readonly_bundles_content_controller.go`

Review when:

- bundle reference creation and bundle-content persistence are mixed or assumed to be atomic
- partial persistence can leave `ServiceProviderCluster` or `ManagementClusterContent` stale
- readonly bundle ownership labels or recognized bundle-name lists drift

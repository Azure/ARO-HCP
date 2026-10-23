# OpenShift Version Catalog

The regional catalog connects backend version resolution to public OpenShift
version discovery. It advertises minor/channel pairs that the backend can resolve.

## Source Contract

The backend `OpenShiftVersionCatalog` controller projects Fleet
`ControlPlaneVersionRollout` documents into the singleton
`/providers/Microsoft.RedHatOpenShift/openShiftVersionCatalogs/default` in the
regional Cosmos `Resources` container. Its partition key is
`microsoft.redhatopenshift`.

- Each entry carries `Spec.Version` (minor ID and channel group) and an
  `Available` boolean equal to `Spec.BestExactVersion != nil`. Availability means
  an exact version has been resolved, not that rollout has completed.
- Entries meet `MinimumPublicVersion`; exact versions and rollout progress remain
  backend-owned.
- A missing minor ID or channel group defers the whole projection, preserving the
  previous snapshot until every rollout has a profile. An empty rollout set
  publishes an empty catalog; unchanged entries avoid a write.
- The publisher uses the generic watching controller with one singleton queue
  key, rollout add/update/delete events, and five-minute informer resync. An
  initial enqueue publishes even from an empty informer after cache sync. This
  publisher has no separate interval timer.

The frontend point-reads this snapshot through
`ResourcesDBClient.OpenShiftVersionCatalogs().Get(..., "default")`. Regional
storage and the frontend's configured Azure location define its scope;
subscription feature registration determines visibility within that snapshot.

## Public API

LIST and GET use
`/subscriptions/{subscriptionId}/providers/Microsoft.RedHatOpenShift/locations/{location}/hcpOpenShiftVersions[/{name}]`.
The contract is shared by all five registered API versions:
`2024-06-10-preview`, `2025-12-23-preview`, `2026-06-30-preview`,
`2026-09-01-preview`, and `2026-10-01`.

Stable names are the bare minor, such as `4.21`; other channel groups use
`{minor}-{channelGroup}`, such as `4.21-fast`. GET matches these names
case-insensitively. Exact-version URL aliases are not supported. Responses have
`enabled: true`, the channel group, and an omitted unknown end-of-life timestamp;
LIST sorts by public name and GET returns the same resource.

Ordinary subscriptions see `stable` and `fast`, including eligible 5.x and later
major versions. The existing `ExperimentalReleaseFeatures` AFEC requirement for
`candidate` and `nightly` remains unchanged. Visibility and the public floor are
applied before availability is evaluated.

| State | Result |
| --- | --- |
| Requested location differs from the frontend's region (case-insensitive) | 404 before catalog access |
| Catalog absent | LIST and GET return 503 |
| Some eligible entries available | LIST returns 200 with the available subset |
| Eligible entries exist, all unavailable | LIST returns 503 |
| No eligible entries in an existing catalog | LIST returns 200 with `value: []` |
| Requested eligible entry unavailable | GET returns 503 |
| Requested entry absent, hidden, or below the floor | GET returns 404 |

Catalog-availability 503 responses include `Retry-After: 59` (seconds).

## Operations

`MinimumPublicVersion` and `MinimumBackendVersion` are currently `4.20`.
Retire a minor by raising the public floor first, retaining backend support while
its clusters drain, then raising the backend floor to stop prospective seeding
and permit obsolete rollout retirement. The public floor governs discovery and
cluster requests, including PUT updates to existing active clusters even when
the requested version is unchanged.

The multi-version install E2E resolves upstream availability first. For
non-nightly channels it then waits up to 15 minutes for matching regional LIST
and GET results before allocating pooled identities or customer resources.
Nightly retains its exact-build resolution path.

## Sources

- [Publisher](../../backend/pkg/controllers/cluster/version/rollout/version_catalog_controller.go)
- [Frontend projection](../../frontend/pkg/frontend/openshift_versions.go)
- [Version floors](../../internal/api/coreapi/types_openshiftversion.go) and [cluster validation](../../internal/validation/validate_cluster.go)
- [E2E availability wait](../../test/util/framework/version_availability_helper.go)

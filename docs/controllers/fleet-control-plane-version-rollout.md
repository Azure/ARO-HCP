
## Definitions

| Field            | Field meaning                                                                                 | Examples                                                     | Long description                                                                                              |
|------------------|-----------------------------------------------------------------------------------------------|--------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| Minor version    | A major/minor OpenShift version without a patch or prerelease component.                      | `4.21`, `5.0`                                                | Used when a customer selects a supported y-stream but does not choose a specific z-stream version.            |
| Exact version    | A fully specified OpenShift version, including patch, prerelease, EC, or nightly information. | `4.21.6`, `4.22.0-ec.4`, `5.0.0-0.nightly-2026-08-06-014706` | Used when the desired version must resolve to one specific release artifact rather than a general y-stream.   |
| Y-stream channel | A release channel scoped to a specific minor version.                                         | `stable-4.21`, `fast-5.0`                                    | Used to determine available upgrade targets within a particular minor release stream.                         |
| Z-stream offset  | The number of z-stream versions behind the latest to select.                                  | `zStreamOffset=2`                                            | If the latest z-stream is 4.21.8 and zStreamOffset is 2, the selected version is 4.21.6. This prevents selecting versions that are too new according to rollout safety policy. |

# Notable API fields

## Control Plane Versions

| Field                                                                                                                     | Field meaning                                                                                          | Examples                                        | Long description                                                                                                                                                                                                 |
|---------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|-------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Cluster.customerProperties.version.id`                                                                                   | Customer desired minor version.                                                                        | `4.21`, `5.0`                                   | A standard customer may select a desired minor version only. They cannot specify a z-stream or exact patch version.                                                                                              |
| `Cluster.customerProperties.version.id`                                                                                   | CI-selected exact version for testing.                                                                 | `4.21.6`, `5.0.0-0.nightly-2026-08-06-014706`   | CI workflows may request a full exact version, including micro, prerelease, EC, or nightly builds.                                                                                                               |
| `Code.ControlPlaneUpgradeController.minimumVersions[y-stream channel]=exactVersion`                                       | SRE specified minimum allowed z-stream for a y-stream channel.                                         | `stable-4.21 >= 4.21.6`, `fast-4.21 >= 4.21.12` | Logically per-region, per-y-stream-channel.  SRE can define a floor for each y-stream channel so clusters do not select versions below the approved minimum.                                                     |
| `Code.ControlPlaneUpgradeController.maxUpgradeDuration[minor version]=duration`                                           | SRE specified maximum time to wait for a control plane upgrade to a particular version before failing. | `stable-4.21 >= 4.21.6`, `fast-4.21 >= 4.21.12` |                                                                                                                                                                                                                  |
| `Code.ControlPlaneUpgradeController.minVersionReadyDuration`                                                              | SRE specified minimum time a control plane must be at the desiredVersion before being successful.      | `stable-4.21 >= 4.21.6`, `fast-4.21 >= 4.21.12` |                                                                                                                                                                                                                  |
| `Code.ControlPlaneUpgradeController.canaryPercentage`                                                                     | SRE specified percent of clusters to upgrade first as canaries. 5% suggested.                          | `stable-4.21 >= 4.21.6`, `fast-4.21 >= 4.21.12` |                                                                                                                                                                                                                  |
| `Code.ControlPlaneUpgradeController.rollingPercentage`                                                                    | SRE specified percent of clusters to upgrade at the same time. 5% suggested.                           | `stable-4.21 >= 4.21.6`, `fast-4.21 >= 4.21.12` |                                                                                                                                                                                                                  |
| `ServiceProviderCluster.spec.pinnedVersion.exactVersion`<br/>`ServiceProviderCluster.spec.pinnedVersion.untilExactVersion` | SRE specified, cluster-specific exact version override until another version becomes available.        | `4.21.12` until `4.21.32` is present            | Set on an exactly cluster SRE can pin a particular cluster to an exact z-stream version regardless of its previous version. Once the specified future version is present, normal upgrade selection may continue. |
| `ServiceProviderCluster.status.control_plane_version.active_versions`                                                     | The exact versions present in the control plane                                                        |                                                 |                                                                                                                                                                                                                  |
| `Cincinnati.[y-stream].bestVersion`                                                                                       | Upgrade graph-selected exact version for a y-stream channel.                                           | `4.21.6` for `stable-4.21` with `zStreamOffset=2` | The upgrade graph determines the exact desired version for a channel after applying channel rules and z-stream offset.                                                                                  |
| `HostedCluster.spec.controlPlaneRelease.image`(!?)                                                                        | Exact version equivalent that the HostedCluster will try to achieve                                    | `4.21.6` for `stable-4.21` with `zStreamOffset=2`    |                                                                                                                                                                                                                  |
| `HostedCluster.status.controlPlaneVersion.history`                                                                        | Exact versions that the HostedCluster is currently or has run in the past                              | `4.21.6` for `stable-4.21` with `zStreamOffset=2`    |                                                                                                                                                                                                                  |
| `HostedCluster.status.version.desired.channels`                                                                           | y-stream channels that the current control plane version has upgrade edges into                        |                                                 |                                                                                                                                                                                                                  |
| `ServiceProviderCluster.spec.control_plane_version.desired_version`                                                        | OUTPUT: Exact version to actually set on the HostedCluster, determined by all the other inputs         | `4.21.6` for `stable-4.21` with `zStreamOffset=2`    | The desired version is the exact version that will be set on the HostedCluster.                                                                                                                                  |
| `ControlPlaneVersionRollout.spec.bestExactVersion`                                                                        | ControlPlaneVersionRollout is per y-stream channel, the best exact version of this 4.y for the fleet.  |                                                 | most recent z-stream without a platform+controlplane risk in the y-stream channel, offset by zStreamOffset from the latest.                                                                                              |

## New region-wide structs
```go
type ControlPlaneVersionRollout struct {
    // the name is y-stream channel it is associated with.
    CosmosMetadata `json:cosmosMetadata`

    Spec ControlPlaneVersionRolloutSpec `json:spec`

    Status ControlPlaneVersionRolloutStatus `json:status`
}

type ControlPlaneVersionRolloutSpec struct {
    // BestExactVersion is the most recent z-stream without a platform+controlplane risk in the y-stream channel, offset by zStreamOffset from the latest.
    BestExactVersion *SemVer `json:bestExactVersion`
}

type ControlPlaneVersionRolloutStatus struct {
    // ClusterCountByDesiredExactVersion is a map from desired exact version to the number of clusters that
    // have exact version as their ServiceProviderCluster.spec.control_plane_version.desired_version
    ClusterCountByDesiredExactVersion map[string]int64

    // MismatchedClusterCountByDesiredExactVersion is a map from desired exact version to the number of clusters that
    // do not have exact version as the earliest version in their activeVersions
    MismatchedClusterCountByDesiredExactVersion map[string]int64

    // FailedClusterCountByDesiredExactVersion is a map from desired exact version to the number of clusters that
    // have been mismatched (see above) for more than the maxUpgradeDuration for the desired minor version.
    FailedClusterCountByDesiredExactVersion map[string]int64

    // ClusterCountByAchievedExactVersion is a map from desired exact version to the number of clusters that
    // do have that exact version as the earliest version in their activeVersions
    ClusterCountByAchievedExactVersion map[string]int64

    // SuccessfulClusterCountByAchievedExactVersion is a map from desired exact version to the number of clusters that
    // do have that exact version as the earliest version in their activeVersions and have been at that level more than
    // minVersionReadyDuration.
    SuccessfulClusterCountByAchievedExactVersion map[string]int64
}
```

ControlPlaneVersionRollout will be added to the fleet API and cosmos container.

# Controllers
All controllers run in the `backend` binary.

## Cluster service update/install controller
Per-cluster controller.

### Inputs
1. `ServiceProviderCluster.spec.control_plane_version.desired_version`

### Outputs
1. `HostedCluster.spec.controlPlaneRelease.image` -- this is logical. Probably cluster-service at the moment.


## Forced Cluster Desired Version Assignment
Per-cluster controller that fires
1. when clusters update and when
2. when `ControlPlaneVersionRollout.spec.bestExactVersion` changes
3. on an interval

### Inputs
1. `ControlPlaneVersionRollout.spec.bestExactVersion`
2. `ServiceProviderCluster.spec.pinnedVersion.exactVersion`
3. `ServiceProviderCluster.spec.pinnedVersion.untilExactVersion`

### NeedsWork
1. `ServiceProviderCluster.spec.pinnedVersion.exactVersion` must be specified

### Output
1. `ServiceProviderCluster.spec.control_plane_version.desired_version`

### Sync
If `ControlPlaneVersionRollout.spec.bestExactVersion` is greater or equal to `ServiceProviderCluster.spec.pinnedVersion.untilExactVersion`,
1. set `ServiceProviderCluster.spec.control_plane_version.desired_version` to `ControlPlaneVersionRollout.spec.bestExactVersion`
2. clear `ServiceProviderCluster.spec.pinnedVersion.exactVersion`
3. clear `ServiceProviderCluster.spec.pinnedVersion.untilExactVersion`
4. return

If `ServiceProviderCluster.spec.control_plane_version.desired_version` does not equal `ServiceProviderCluster.spec.pinnedVersion.exactVersion`,
1. set `ServiceProviderCluster.spec.control_plane_version.desired_version` to `ControlPlaneVersionRollout.spec.bestExactVersion`
2. return


## Control Plane Version Status Collector
Per-ControlPlaneVersionRollout controller that fires
1. when `ServiceProviderCluster.status.control_plane_version.active_versions` changes
2. when `ServiceProviderCluster.spec.control_plane_version.desired_version` changes
3. when `ControlPlaneVersionRollout.spec.bestExactVersion` changes

### Inputs

### Outputs
1. `ControlPlaneVersionRollout.status.ClusterCountByDesiredExactVersion`
2. `ControlPlaneVersionRollout.status.MismatchedClusterCountByDesiredExactVersion`
3. `ControlPlaneVersionRollout.status.ClusterCountByAchievedExactVersion`

### Sync
Recalculate the output counts.


## Control Plane Version Best Version Selection
Per-ControlPlaneVersionRollout controller that fires on an interval

### Inputs
1. `zStreamOffset` - hardcoded integer (e.g. 2 means select the version 2 z-streams behind the latest)
2. `Cincinnati.[y-stream].bestVersion`
3. `Code.ControlPlaneUpgradeController.minimumVersions[y-stream channel]=exactVersion`

### Output
1. `ControlPlaneVersionRollout.spec.bestExactVersion`

### Sync
For the y-stream channel, find the most recent z-stream without a platform+controlplane risk in the y-stream channel, offset by zStreamOffset from the latest available z-stream.
If the cincinnati version is less than `Code.ControlPlaneUpgradeController.minimumVersions[y-stream channel]`,
1. set `ControlPlaneVersionRollout.spec.bestExactVersion` to `Code.ControlPlaneUpgradeController.minimumVersions[y-stream channel]`
2. return

Otherwise,
1. set `ControlPlaneVersionRollout.spec.bestExactVersion` to `Cincinnati.[y-stream].bestVersion`
2. return


## Normal Cluster Desired Version Assignment
Per-ControlPlaneVersionRollout controller that fires
1. when `ControlPlaneVersionRollout` changes
2. on an interval

### Inputs
1. `ControlPlaneVersionRollout.spec.bestExactVersion`
2. `ServiceProviderCluster.spec.control_plane_version.desired_version`
3. `ServiceProviderCluster.status.control_plane_version.active_versions`

### NeedsWork
1. It has been more than 60s since we last rolled out. We do this so that our caches are likely to have updated.

### Output
1. `ServiceProviderCluster.spec.control_plane_version.desired_version`

### Sync
If `ControlPlaneVersionRollout.status.FailedClusterCountByDesiredExactVersion` for the `ControlPlaneVersionRollout.spec.bestExactVersion`
has an absolute count greater than 2 or greater than 5% of the total clusters at that exact version.
1. produce failure message
2. produce failure condition
3. return

Eligible cluster calculation:
1. list all clusters with `ServiceProviderCluster.spec.control_plane_version.desired_version` that match the minor version and are below the `ControlPlaneVersionRollout.spec.bestExactVersion`
2. if `ServiceProviderCluster.spec.pinnedVersion.exactVersion` is nil, add to eligible.
3. if `ServiceProviderCluster.spec.pinnedVersion.exactVersion` is not nil and `ServiceProviderCluster.spec.pinnedVersion.untilExactVersion` is less than or equal the `ControlPlaneVersionRollout.spec.bestExactVersion`, add to eligible.
4. Call the list `EligibleClusters`.

If there are zero `EligibleClusters`,
1.produce a stable message
2.produce a stable condition
3.return

If `ControlPlaneVersionRollout.status.MismatchedClusterCountByDesiredExactVersion`+`ClusterCountByAchievedExactVersion` for the `ControlPlaneVersionRollout.spec.bestExactVersion`
is less than the `Code.ControlPlaneUpgradeController.canaryPercentage`+2, we need to select more N clusters for the canary.
2. select the best N from `EligibleClusters`. For now, this will be random. In the future, we can make criteria.
3. for each of the selected clusters, set `ServiceProviderCluster.spec.control_plane_version.desired_version` to `ControlPlaneVersionRollout.spec.bestExactVersion`
4. return

If `ControlPlaneVersionRollout.status.SuccessfulClusterCountByAchievedExactVersion` for the `ControlPlaneVersionRollout.spec.bestExactVersion`
is less than the `Code.ControlPlaneUpgradeController.canaryPercentage`
1. produce a progressing message
2. produce a progressing condition
3. return

If `ControlPlaneVersionRollout.status.MismatchedClusterCountByDesiredExactVersion`+`ClusterCountByAchievedExactVersion` for the `ControlPlaneVersionRollout.spec.bestExactVersion`
is less than the `Code.ControlPlaneUpgradeController.rollingPercentage`, we need to select more N clusters to rollout.
2. select the best N of `EligibleClusters`. For now, this will be random. In the future, we can make criteria.
3. for each of the selected clusters, set `ServiceProviderCluster.spec.control_plane_version.desired_version` to `ControlPlaneVersionRollout.spec.bestExactVersion`
4. return

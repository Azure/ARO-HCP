// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package upgradecontrollers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/go-version"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneUpgradeSyncer is a Cluster syncer that manages control plane version upgrades.
// It handles automated (managed) z-stream (patch) upgrades and assists with y-stream (minor)
// version upgrades by selecting the appropriate z-stream within the user-desired minor version.
type controlPlaneUpgradeSyncer struct {
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*controlPlaneUpgradeSyncer)(nil)

const (
	// TracerName is the name used for tracing OCM API calls
	TracerName = "github.com/Azure/ARO-HCP/backend"
)

// NewControlPlaneUpgradeController creates a new controller that manages control plane upgrades.
// It periodically checks each cluster and determines if an upgrade is needed based on the
// OCPVersion logic documented in the ServiceProviderCluster type.
func NewControlPlaneUpgradeController(
	cosmosClient database.DBClient,
	ocmConnection *ocmsdk.Connection,
	subscriptionLister listers.SubscriptionLister,
) controllerutils.Controller {

	clusterServiceClient := ocm.NewClusterServiceClientWithTracing(
		ocm.NewClusterServiceClient(
			ocmConnection,
			"",
			false,
			false,
		),
		TracerName,
	)

	syncer := &controlPlaneUpgradeSyncer{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ControlPlaneUpgrade",
		cosmosClient,
		subscriptionLister,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

// SyncOnce performs a single reconciliation of the control plane upgrade for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Initialize HCPClusterVersion if needed
//  3. Update the active versions array by querying version service to get current cluster version(s)
//  4. Compute the desired z-stream version based on upgrade logic (initial/z-stream/y-stream)
//  5. If the computed desired version differs from the previously stored desired version:
//     - Update the DesiredVersion field
//     - Trigger the upgrade via version service
//  6. Save the updated service provider cluster state
//
// TODO: Add comprehensive logging throughout the reconciliation process for:
//   - Version resolution decisions (which case, why this version was selected)
//   - Upgrade triggers (old version -> new version)
//   - Active version updates (from version service)
//
// TODO: Complete the upgrade logic implementation for all three cases:
//   - Initial version selection (Case 1)
//   - Z-stream managed upgrades (Case 2)
//   - Y-stream user-initiated upgrades (Case 3)
func (c *controlPlaneUpgradeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	// Get the customer's desired cluster configuration
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// Get or create the service provider cluster state
	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// Initialize the HCPClusterVersion if it doesn't exist
	if existingServiceProviderCluster.Version == nil {
		existingServiceProviderCluster.Version = &api.HCPClusterVersion{}
	}

	// Update the actual version in the status array by querying the version service
	err = c.updateActualVersionInStatus(ctx, existingCluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update actual version in status: %w", err))
	}

	// Determine the desired z-stream version
	desiredVersion, err := c.desiredControlPlaneZVersion(ctx, existingCluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to determine desired control plane version: %w", err))
	}

	// Check if we need to trigger an upgrade
	// We trigger an upgrade if the computed desired version differs from the previously stored desired version
	previousDesiredVersion := existingServiceProviderCluster.Version.DesiredVersion
	if len(desiredVersion) > 0 && desiredVersion != previousDesiredVersion {
		// TODO: Make API call to version service to trigger the upgrade
		// The version service API is idempotent:
		// - If desiredVersion == current cluster version: NOOP
		// - Otherwise: Initiate the upgrade to desiredVersion

		// Update the desired version to reflect the new decision
		existingServiceProviderCluster.Version.DesiredVersion = desiredVersion

		// For now, just log that we would trigger an upgrade
		logger := utils.LoggerFromContext(ctx)
		logger.Info("Would trigger control plane upgrade",
			"cluster", key.HCPClusterName,
			"desiredVersion", desiredVersion,
			"previousDesiredVersion", previousDesiredVersion)
	}

	// Save the updated service provider cluster state
	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// updateActualVersionInStatus queries version service to get cluster's actual version
// and updates version.active_versions with the retrieved actual version.
func (c *controlPlaneUpgradeSyncer) updateActualVersionInStatus(ctx context.Context, cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) error {
	// TODO: Query version service to get the cluster's actual version, then add to version.active_versions
	//       (only insert if not already present in the array, prepend to keep most recent first)

	return nil
}

// desiredControlPlaneZVersion determines the desired z-stream version for the control plane.
//
// The desired version selection logic is executed on each controller sync.
// NOTE: Rollback to a previous z-stream is not currently supported (future enhancement).
//
// It dispatches to one of three resolution methods based on the current cluster state:
// - Case 1: Initial version selection (no active versions yet)
// - Case 2: Z-stream managed upgrade (customer desired minor == actual minor)
// - Case 3: Next Y-stream user-initiated upgrade (customer desired minor == actual minor + 1)
func (c *controlPlaneUpgradeSyncer) desiredControlPlaneZVersion(ctx context.Context, customerDesired *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) (string, error) {
	// Step 1: Read user's desired_minor x.y version
	customerDesiredMinor := customerDesired.CustomerProperties.Version.ID
	_ = customerDesired.CustomerProperties.Version.ChannelGroup // TODO: Use channelGroup in version queries

	// Step 2: Retrieve the actual x.y.z version from the first element in the active versions array
	actualFullVersion := c.getLatestActiveVersion(serviceProviderCluster)

	// Step 3: Determine what case we're in
	if len(actualFullVersion) == 0 {
		// Case 1: Initial cluster creation (no actual version yet)
		return c.resolveInitialDesiredVersion(ctx, customerDesiredMinor, serviceProviderCluster)
	}

	// Extract minor version from actual (x.y from x.y.z)
	actualMinor := ocm.NewOpenShiftVersionXY(actualFullVersion)

	if actualMinor == customerDesiredMinor {
		// Case 2: Z-stream managed upgrade (actual minor == desired minor)
		return c.resolveNextZStream(ctx, customerDesiredMinor, actualFullVersion)
	}

	// Case 3: Next Y-stream upgrade (actual minor != desired minor)
	// User-initiated upgrade to the next minor version (e.g., 4.19 -> 4.20)
	// Validate that desired minor is exactly one minor ahead (actualMinor + 1)
	// We don't allow downgrades or skipping minor versions
	if !c.isValidNextYStreamUpgradePath(actualMinor, customerDesiredMinor) {
		return "", fmt.Errorf("invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no downgrades or skipping minor versions", actualMinor, customerDesiredMinor)
	}

	return c.resolveNextYStream(ctx, customerDesiredMinor, actualMinor, actualFullVersion)
}

// resolveInitialDesiredVersion handles Case 1: Initial cluster creation (no actual version yet).
//
// Version selection logic:
//  1. Find all candidate versions 4.y.z in the customer's desired minor (4.y)
//  2. Check if 4.y is the latest available minor:
//     - If NOT the latest, filter candidates to only those with an edge to 4.(y+1).something
//     - If IS the latest, all candidates are eligible
//  3. Pick the biggest (latest) eligible 4.y.z
//
// This ensures new clusters start on a version that can be upgraded to the next minor when available.
func (c *controlPlaneUpgradeSyncer) resolveInitialDesiredVersion(ctx context.Context, customerDesiredMinor string, serviceProviderCluster *api.ServiceProviderCluster) (string, error) {
	// TODO: Find all candidate versions in customerDesiredMinor
	// TODO: Filter candidates: if next minor available, keep only versions with edge to next minor
	// TODO: Pick the biggest (latest) eligible version
	return "", fmt.Errorf("initial version selection not yet implemented")
}

// resolveNextZStream handles Case 2: Z-stream managed upgrade (actual minor == desired minor).
//
// This is an automated upgrade where the controller decides to move to a newer z-stream
// within the current minor version (e.g., 4.19.10 → 4.19.22) without user intervention.
//
// Version selection logic:
//  1. Find all candidate versions 4.y.z in the upgrade graph where:
//     - 4.y.z has an upgrade edge from ALL currently active versions (not just the latest)
//     - 4.y.z > latest active 4.y.z
//  2. Check if 4.y is the latest available minor:
//     - If NOT the latest, filter candidates to only those with an edge to 4.(y+1).something
//     - If IS the latest, all candidates are eligible
//  3. Pick the biggest (latest) eligible 4.y.z
//
// The controller always tries to stay on the latest z-stream that maintains an upgrade
// path to the next minor version (when available).
func (c *controlPlaneUpgradeSyncer) resolveNextZStream(ctx context.Context, customerDesiredMinor string, actualFullVersion string) (string, error) {
	// TODO: Find all candidates with upgrade edge from ALL active versions and > latest active version
	// TODO: Filter candidates: if next minor available, keep only versions with edge to next minor
	// TODO: Pick the biggest (latest) eligible version
	return "", fmt.Errorf("z-stream upgrade not yet implemented")
}

// resolveNextYStream handles Case 3: Next Y-stream upgrade (user-initiated minor version upgrade).
//
// This handles user-initiated upgrades to the next minor version (e.g., 4.19.x → 4.20.y).
// The validation that desired minor is exactly one ahead (actualMinor + 1) has already been performed by the caller.
//
// Version selection logic:
//  1. Find all candidate versions 4.(y+1).z in the upgrade graph where:
//     - 4.(y+1).z has an upgrade edge from ALL currently active versions (not just the latest)
//     - 4.(y+1).z > latest active 4.y.z
//  2. Check if 4.(y+1) is the latest available minor:
//     - If NOT the latest, filter candidates to only those with an edge to 4.(y+2).something
//     - If IS the latest, all candidates are eligible
//  3. Pick the biggest (latest) eligible 4.(y+1).z
func (c *controlPlaneUpgradeSyncer) resolveNextYStream(ctx context.Context, customerDesiredMinor string, actualMinor string, actualFullVersion string) (string, error) {
	// TODO: Find all candidates in next minor with upgrade edge from ALL active versions and > latest active version
	// TODO: Filter candidates: if next minor+1 available, keep only versions with edge to next minor+1
	// TODO: Pick the biggest (latest) eligible version
	return "", fmt.Errorf("next y-stream upgrade not yet implemented")
}

// isValidNextYStreamUpgradePath validates that a next Y-stream upgrade path is valid.
// It ensures the desired minor is exactly one ahead of the actual minor (actualMinor + 1) and prevents downgrades.
// Returns true if the upgrade path is valid, false otherwise.
func (c *controlPlaneUpgradeSyncer) isValidNextYStreamUpgradePath(actualMinor string, desiredMinor string) bool {
	actualVersion, err := version.NewVersion(actualMinor + ".0")
	if err != nil {
		return false
	}
	desiredVersion, err := version.NewVersion(desiredMinor + ".0")
	if err != nil {
		return false
	}

	// Check for downgrade (desired < actual)
	if desiredVersion.LessThan(actualVersion) {
		return false
	}

	// Check if desired minor is exactly one ahead
	actualSegments := actualVersion.Segments()
	desiredSegments := desiredVersion.Segments()

	actualMajor := actualSegments[0]
	actualMinorNum := actualSegments[1]
	desiredMajor := desiredSegments[0]
	desiredMinorNum := desiredSegments[1]

	// Ensure desired is exactly one minor ahead (same major, minor + 1)
	if desiredMajor != actualMajor || desiredMinorNum != actualMinorNum+1 {
		return false
	}

	return true
}

// getLatestActiveVersion retrieves the most recent active version from the service provider cluster.
// Returns an empty string if no active version is recorded yet (e.g., for newly created clusters).
func (c *controlPlaneUpgradeSyncer) getLatestActiveVersion(serviceProviderCluster *api.ServiceProviderCluster) string {
	if len(serviceProviderCluster.Version.ActiveVersions) > 0 {
		return serviceProviderCluster.Version.ActiveVersions[0].Version
	}
	return ""
}

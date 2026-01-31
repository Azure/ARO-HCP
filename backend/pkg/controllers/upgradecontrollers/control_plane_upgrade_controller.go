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
//  2. Initialize/update the HCPClusterVersion with the customer's desired minor and channel group
//  3. Update the actual version in the status array by querying CS upgrade operation history
//  4. Determine the desired z-stream version based on upgrade logic (initial/z-stream/y-stream)
//  5. If the desired version changed, add a new status entry and trigger the upgrade via CS API
//  6. Save the updated service provider cluster state
//
// TODO: Add comprehensive logging throughout the reconciliation process for:
//   - Version resolution decisions (which case, why this version was selected)
//   - Upgrade triggers (old version -> new version)
//   - Status updates (actual version changes)
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

	// Initialize or update the HCPClusterVersion from customer's desired configuration
	if existingServiceProviderCluster.Version == nil {
		existingServiceProviderCluster.Version = &api.HCPClusterVersion{}
	}
	// Update the desired minor and channel group from customer's desired configuration
	existingServiceProviderCluster.Version.DesiredMinor = existingCluster.CustomerProperties.Version.ID
	existingServiceProviderCluster.Version.ChannelGroup = existingCluster.CustomerProperties.Version.ChannelGroup

	// Update the actual version in the status array by querying the Cluster Service
	err = c.updateActualVersionInStatus(ctx, existingCluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update actual version in status: %w", err))
	}

	// Determine the desired z-stream version
	desiredVersion, err := c.desiredControlPlaneZVersion(existingCluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to determine desired control plane version: %w", err))
	}

	// Get actual version from status
	var actualVersion string
	if len(existingServiceProviderCluster.Version.Status) > 0 {
		actualVersion = existingServiceProviderCluster.Version.Status[0].Actual
	}

	// Update service provider cluster if the selected version differs from current
	currentDesiredFullVersion := existingServiceProviderCluster.Version.DesiredFullVersion
	if desiredVersion != currentDesiredFullVersion && desiredVersion != "" {
		// Set desired_full_version to the selected x.y.z version
		existingServiceProviderCluster.Version.DesiredFullVersion = desiredVersion

		// Add an entry to the status array as the first element (most recent)
		newStatus := api.HCPClusterVersionStatus{
			Desired:            desiredVersion,
			Actual:             actualVersion,
			LastTransitionTime: time.Now().UTC().Format(time.RFC3339),
		}

		// Prepend to status slice
		existingServiceProviderCluster.Version.Status = append([]api.HCPClusterVersionStatus{newStatus}, existingServiceProviderCluster.Version.Status...)
	}

	// Check if we need to trigger an upgrade
	if c.shouldTriggerUpgrade(existingServiceProviderCluster, desiredVersion) {
		// TODO: Make API call to Cluster Service to trigger the upgrade
		// The CS API is idempotent:
		// - If desiredVersion == current cluster version: NOOP
		// - Otherwise: Initiate the upgrade to desiredVersion

		// For now, just log that we would trigger an upgrade
		logger := utils.LoggerFromContext(ctx)
		logger.Info("Would trigger control plane upgrade",
			"cluster", key.HCPClusterName,
			"desiredVersion", desiredVersion,
			"currentVersion", existingServiceProviderCluster.Version.DesiredFullVersion)
	}

	// Save the updated service provider cluster state
	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// shouldTriggerUpgrade determines if we need to trigger an upgrade to the Cluster Service.
// Returns true if the desired version differs from the current desired_full_version,
// indicating that a new upgrade decision has been made.
func (c *controlPlaneUpgradeSyncer) shouldTriggerUpgrade(serviceProviderCluster *api.ServiceProviderCluster, desiredVersion string) bool {
	if serviceProviderCluster.Version == nil {
		return false
	}

	// If we have a new desired version, we should trigger the upgrade
	return serviceProviderCluster.Version.DesiredFullVersion != desiredVersion
}

// updateActualVersionInStatus queries the Cluster Service API for the upgrade operation history
// and updates the "actual" field in all status entries based on the upgrade operation state.
func (c *controlPlaneUpgradeSyncer) updateActualVersionInStatus(ctx context.Context, cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) error {
	if serviceProviderCluster.Version == nil || len(serviceProviderCluster.Version.Status) == 0 {
		return nil // No status entries to update
	}

	// TODO: Query CS API to get the cluster's upgrade operation history
	// TODO: For each entry in HCPClusterVersion.Status, check the upgrade operation state:
	//       - If actual == desired, nothing to do (checked first)
	//       - If CS marked the upgrade operation of a given desired as completed, the corresponding actual
	//         moves to the desired and we update the last transition timestamp
	//       - Otherwise, we leave the field untouched

	return nil
}

// desiredControlPlaneZVersion determines the desired z-stream version for the control plane.
//
// The desired version selection logic is executed on each controller sync.
// NOTE: Rollback to a previous z-stream is not currently supported (future enhancement).
//
// 1. Read user's desired_minor x.y version from customer configuration
// 2. Retrieve the actual x.y.z version from the first element in the status array
//
// Case 1: Initial version selection (x.y.z is not set)
//   - Query CS API for all versions in desired minor
//   - If next minor is available, pick the latest version that has an upgrade path to next minor
//   - Otherwise, pick the latest version in desired minor
//
// Case 2: Z-stream managed upgrade (actual minor == desired minor)
//   - Z-stream managed upgrades: automated upgrades within the same minor (e.g., 4.19.10 → 4.19.22)
//   - Get available upgrades from the actual version
//   - If next minor is available, pick the latest available upgrade that has a path to next minor
//   - Otherwise, pick the latest available upgrade
//
// Case 3: Y-stream user-initiated upgrade (actual minor != desired minor)
//   - User has changed desired_minor (e.g., 4.19 → 4.20)
//   - Get available upgrades from the actual version
//   - Filter for versions in the desired minor and pick the latest
//   - If no direct path, find the latest intermediate z-stream in actual minor that leads to desired minor
//
// The function takes the customer's desired cluster configuration and the current service provider cluster state,
// and returns the full x.y.z version that should be reconciled.
func (c *controlPlaneUpgradeSyncer) desiredControlPlaneZVersion(customerDesired *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) (string, error) {
	// Step 1: Read user's desired_minor x.y version
	if customerDesired == nil || customerDesired.CustomerProperties.Version.ID == "" {
		return "", fmt.Errorf("customer desired version is not set")
	}
	desiredMinor := customerDesired.CustomerProperties.Version.ID
	_ = customerDesired.CustomerProperties.Version.ChannelGroup // TODO: Use channelGroup in version queries

	// Step 2: Retrieve the actual x.y.z version from the first element in the status array
	var actualFullVersion string
	if len(serviceProviderCluster.Version.Status) > 0 {
		actualFullVersion = serviceProviderCluster.Version.Status[0].Actual
	}

	// Step 3: Determine what case we're in
	if actualFullVersion == "" {
		// Case 1: Initial cluster creation (no actual version yet)
		// TODO: Query CS API for versions in desiredMinor
		// TODO: Check if next minor is available
		// TODO: If next minor available, pick latest z with upgrade path to next minor
		// TODO: Otherwise, pick latest z in desiredMinor
		return "", fmt.Errorf("initial version selection not yet implemented")
	}

	// Extract minor version from actual (x.y from x.y.z)
	actualMinor := ocm.NewOpenShiftVersionXY(actualFullVersion)
	if actualMinor == "" {
		return "", fmt.Errorf("failed to extract minor version from actual version %s", actualFullVersion)
	}

	if actualMinor == desiredMinor {
		// Case 2: Z-stream managed upgrade (actual minor == desired minor)
		// This is an automated upgrade where the controller decides to move to a newer z-stream
		// within the current minor version (e.g., 4.19.10 -> 4.19.22) without user intervention.
		// The controller always tries to stay on the latest z-stream that maintains an upgrade
		// path to the next minor version (when available).
		//
		// TODO: Get available upgrades from the actual version
		// TODO: If next minor is available, pick the latest available upgrade that has a path to next minor
		// TODO: Otherwise, pick the latest available upgrade
		return "", fmt.Errorf("z-stream upgrade not yet implemented")
	}

	// Case 3: Y-stream upgrade (actual minor != desired minor)
	// User-initiated upgrade to a different minor version
	// TODO: Query CS API for the actual version to get its available_upgrades
	// TODO: Filter available_upgrades for versions in desiredMinor
	// TODO: Pick the latest version in desiredMinor from available_upgrades
	// TODO: If no direct path, fall back to finding the latest intermediate z-stream in actualMinor that we can upgrade to and that leads to desiredMinor
	return "", fmt.Errorf("y-stream upgrade not yet implemented")
}

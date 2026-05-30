// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package backupcontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"open-cluster-management.io/api/utils/work/v1/workapplier"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// backupAction represents a single mutating action to be taken by the controller.
// At most one field may be set per action.
type backupAction struct {
	createManifestWork *workv1.ManifestWork
	patchManifestWork  *workv1.ManifestWork
	updateSPC          *api.ServiceProviderCluster
}

func (a *backupAction) validate() error {
	var set int
	if a.createManifestWork != nil {
		set++
	}
	if a.patchManifestWork != nil {
		set++
	}
	if a.updateSPC != nil {
		set++
	}
	if set > 1 {
		return errors.New("programmer error: more than one action set")
	}
	return nil
}

// backupSyncState holds resolved data shared across steps.
type backupSyncState struct {
	key                 controllerutils.HCPClusterKey
	spc                 *api.ServiceProviderCluster
	maestroClient       maestro.Client
	desiredManifestWork *workv1.ManifestWork
	manifestWorkName    string
}

// backupStep is a step in the backup schedule reconciliation process.
// Returns:
//   - done: whether the current reconciliation loop should stop with the current step result
//   - action: the action to take (nil if no action needed)
//   - error: an error that occurred
type backupStep func(ctx context.Context, state *backupSyncState) (bool, *backupAction, error)

// backupScheduleSyncer is a controller that creates an owned Maestro ManifestWork
// containing a Velero Schedule for each cluster that has reached Succeeded state.
// The ManifestWork includes FeedbackRules to propagate backup status from the
// management cluster back through Maestro.
//
// Each SyncOnce invocation performs at most one mutating action,
// following the sessiongate step-chain pattern.
type backupScheduleSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	cosmosClient database.ResourcesDBClient

	fleetDBClient database.FleetDBClient

	maestroSourceEnvironmentIdentifier string

	maestroClientBuilder maestro.MaestroClientBuilder

	backupConfig *BackupConfig
}

var _ controllerutils.ClusterSyncer = (*backupScheduleSyncer)(nil)

func NewBackupScheduleController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.ResourcesDBClient,
	fleetDBClient database.FleetDBClient,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
	backupConfig *BackupConfig,
) controllerutils.Controller {

	syncer := &backupScheduleSyncer{
		cooldownChecker:                    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:                       cosmosClient,
		fleetDBClient:                      fleetDBClient,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
		maestroClientBuilder:               maestroClientBuilder,
		backupConfig:                       backupConfig,
	}

	controller := controllerutils.NewClusterWatchingController(
		"BackupSchedule",
		cosmosClient,
		informers,
		nil,
		5*time.Minute,
		syncer,
	)

	return controller
}

func (c *backupScheduleSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	if !clusterNeedsBackup(existingCluster.ServiceProviderProperties.ProvisioningState) {
		return nil
	}

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if spc.Status.ManagementClusterResourceID == nil {
		return nil
	}

	stampID := spc.Status.ManagementClusterResourceID.Parent.Name
	mc, err := c.fleetDBClient.Stamps().ManagementClusters(stampID).Get(ctx, fleet.ManagementClusterResourceName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get management cluster for stamp %s: %w", stampID, err))
	}

	maestroClient, err := createMaestroClientFromManagementCluster(ctx, mc, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	clusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()
	clusterName := existingCluster.CustomerProperties.DNS.BaseDomainPrefix
	hcNamespace := c.getHostedClusterNamespace(c.maestroSourceEnvironmentIdentifier, clusterID)
	hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, clusterName)
	manifestWorkName := fmt.Sprintf("%s-dr", clusterID)

	maestroBundleNamespacedName := types.NamespacedName{
		Name:      manifestWorkName,
		Namespace: mc.Status.MaestroConsumerName,
	}

	clusterPaused := spc.Status.BackupState == api.BackupScheduleStatePaused

	schedules := make([]*velerov1api.Schedule, 0, len(c.backupConfig.Schedules))
	for _, sc := range c.backupConfig.Schedules {
		paused := sc.Paused || clusterPaused
		schedule := NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace, sc.Name, sc.Schedule, sc.TTLDuration(), paused)
		schedules = append(schedules, schedule)
	}
	desiredMW, err := buildScheduleManifestWork(maestroBundleNamespacedName, schedules)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build ManifestWork: %w", err))
	}

	state := backupSyncState{
		key:                 key,
		spc:                 spc,
		maestroClient:       maestroClient,
		desiredManifestWork: desiredMW,
		manifestWorkName:    manifestWorkName,
	}

	action, err := c.processBackup(ctx, &state)
	if err != nil {
		return err
	}

	if action == nil {
		return nil
	}

	if err := action.validate(); err != nil {
		return utils.TrackError(fmt.Errorf("invalid backup action: %w", err))
	}

	switch {
	case action.createManifestWork != nil:
		_, err = maestroClient.Create(ctx, action.createManifestWork, metav1.CreateOptions{})
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return utils.TrackError(fmt.Errorf("failed to create ManifestWork: %w", err))
		}

	case action.patchManifestWork != nil:
		patchData, err := json.Marshal(map[string]any{
			"spec": action.patchManifestWork.Spec,
		})
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to marshal patch data: %w", err))
		}
		_, err = maestroClient.Patch(ctx, action.patchManifestWork.Name, types.MergePatchType, patchData, metav1.PatchOptions{})
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to patch ManifestWork: %w", err))
		}

	case action.updateSPC != nil:
		spcCRUD := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		_, err = spcCRUD.Replace(ctx, action.updateSPC, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to update ServiceProviderCluster status: %w", err))
		}

	}

	return nil
}

// processBackup chains through backup steps. Each step either handles the current
// state and returns an action, or passes through to the next step.
func (c *backupScheduleSyncer) processBackup(ctx context.Context, state *backupSyncState) (*backupAction, error) {
	for _, step := range []backupStep{
		c.ensureManifestWorkCreated,
		c.ensureManifestWorkPatched,
		c.recordManifestWorkInStatus,
		c.updateBackupProfileFromFeedback,
	} {
		done, action, err := step(ctx, state)
		if done {
			return action, err
		}
	}
	return nil, nil
}

// ensureManifestWorkCreated checks if the ManifestWork exists in Maestro.
// If not found, returns an action to create it. If found, passes through.
func (c *backupScheduleSyncer) ensureManifestWorkCreated(ctx context.Context, state *backupSyncState) (bool, *backupAction, error) {
	_, err := state.maestroClient.Get(ctx, state.manifestWorkName, metav1.GetOptions{})
	if err == nil {
		return false, nil, nil // MW exists, continue to next step
	}
	if !k8serrors.IsNotFound(err) {
		return true, nil, utils.TrackError(fmt.Errorf("failed to get ManifestWork: %w", err))
	}

	return true, &backupAction{createManifestWork: state.desiredManifestWork}, nil
}

// ensureManifestWorkPatched checks if the ManifestWork that exists in Maestro is up to date.
// This step only runs after ensureManifestWorkCreated passed through, so the MW is known to exist.
func (c *backupScheduleSyncer) ensureManifestWorkPatched(ctx context.Context, state *backupSyncState) (bool, *backupAction, error) {
	mfw, err := state.maestroClient.Get(ctx, state.manifestWorkName, metav1.GetOptions{})
	if err != nil {
		return true, nil, utils.TrackError(fmt.Errorf("failed to get ManifestWork: %w", err))
	}

	if workapplier.ManifestWorkEqual(state.desiredManifestWork, mfw) {
		return false, nil, nil // up to date, continue to next step
	}
	return true, &backupAction{patchManifestWork: state.desiredManifestWork}, nil
}

// recordManifestWorkInStatus checks if the SPC status has the ManifestWork name recorded.
// If not, returns an action to update the SPC. If already recorded, passes through to the next step.
func (c *backupScheduleSyncer) recordManifestWorkInStatus(_ context.Context, state *backupSyncState) (bool, *backupAction, error) {
	if state.spc.Status.BackupScheduleManifestWorkName == state.manifestWorkName {
		return false, nil, nil // already recorded, continue to next step
	}

	state.spc.Status.BackupScheduleManifestWorkName = state.manifestWorkName
	return true, &backupAction{updateSPC: state.spc}, nil
}

// updateBackupProfileFromFeedback reads the ManifestWork status feedback from Maestro
// and updates the BackupProfile with the last backup time and status.
func (c *backupScheduleSyncer) updateBackupProfileFromFeedback(ctx context.Context, state *backupSyncState) (bool, *backupAction, error) {
	mfw, err := state.maestroClient.Get(ctx, state.manifestWorkName, metav1.GetOptions{})
	if err != nil {
		return true, nil, utils.TrackError(fmt.Errorf("failed to get ManifestWork for feedback: %w", err))
	}

	lastBackupTime, lastBackupPhase := extractScheduleFeedback(mfw)

	needsUpdate := lastBackupPhase != "" && lastBackupPhase != state.spc.Status.LastBackupStatus
	if lastBackupTime != nil {
		if state.spc.Status.LastBackupTime == nil || !lastBackupTime.Equal(state.spc.Status.LastBackupTime) {
			needsUpdate = true
		}
	}

	if !needsUpdate {
		return true, nil, nil // nothing to update
	}

	state.spc.Status.LastBackupTime = lastBackupTime
	state.spc.Status.LastBackupStatus = lastBackupPhase
	return true, &backupAction{updateSPC: state.spc}, nil
}

// extractScheduleFeedback extracts the most recent backup time and phase across
// all schedules from ManifestWork status feedback.
func extractScheduleFeedback(mfw *workv1.ManifestWork) (*metav1.Time, string) {
	if mfw == nil {
		return nil, ""
	}

	var latestTime *metav1.Time
	var latestPhase string

	for _, manifest := range mfw.Status.ResourceStatus.Manifests {
		for _, feedback := range manifest.StatusFeedbacks.Values {
			if feedback.Name != "status" || feedback.Value.Type != workv1.JsonRaw || feedback.Value.JsonRaw == nil {
				continue
			}

			var scheduleStatus struct {
				LastBackup *metav1.Time `json:"lastBackup,omitempty"`
				Phase      string       `json:"phase,omitempty"`
			}
			if err := json.Unmarshal([]byte(*feedback.Value.JsonRaw), &scheduleStatus); err != nil {
				continue
			}
			if scheduleStatus.LastBackup != nil {
				if latestTime == nil || scheduleStatus.LastBackup.After(latestTime.Time) {
					latestTime = scheduleStatus.LastBackup
					latestPhase = scheduleStatus.Phase
				}
			}
		}
	}
	return latestTime, latestPhase
}

// clusterNeedsBackup returns true for provisioning states where the cluster
// is or was operational and should have backup schedules.
// Clusters that are still installing or being deleted don't need backups.
func clusterNeedsBackup(state arm.ProvisioningState) bool {
	switch state {
	case arm.ProvisioningStateSucceeded,
		arm.ProvisioningStateFailed,
		arm.ProvisioningStateUpdating:
		return true
	default:
		return false
	}
}

func (c *backupScheduleSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// getHostedClusterNamespace returns the namespace for the hosted cluster.
// Format: ocm-<envName>-<csClusterID>
func (c *backupScheduleSyncer) getHostedClusterNamespace(envName string, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envName, csClusterID)
}

func createMaestroClientFromManagementCluster(
	ctx context.Context,
	mc *fleet.ManagementCluster,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
) (maestro.Client, error) {
	provisionShardID := mc.Status.ClusterServiceProvisionShardID.ID()
	maestroSourceID := maestro.GenerateMaestroSourceID(maestroSourceEnvironmentIdentifier, provisionShardID)

	return maestroClientBuilder.NewClient(ctx, mc.Status.MaestroRESTAPIURL, mc.Status.MaestroGRPCTarget, mc.Status.MaestroConsumerName, maestroSourceID)
}

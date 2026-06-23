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
	"fmt"
	"strings"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// backupScheduleSyncer is a controller that creates ApplyDesires and ReadDesires
// in the kube-applier Cosmos container for each cluster that has reached Succeeded state.
// Each ApplyDesire contains a single Velero Schedule; each matching ReadDesire observes
// the Schedule's status on the management cluster.
type backupScheduleSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	cosmosClient database.ResourcesDBClient

	kubeApplierDBClients database.KubeApplierDBClients

	hostedClusterNamespaceEnvIdentifier string

	backupConfig *BackupConfig
}

var _ controllerutils.ClusterSyncer = (*backupScheduleSyncer)(nil)

func NewBackupScheduleController(
	cosmosClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	informers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
	backupConfig *BackupConfig,
) controllerutils.Controller {

	syncer := &backupScheduleSyncer{
		cooldownChecker:                     controllerutil.NewTimeBasedCooldownChecker(5 * time.Minute),
		cosmosClient:                        cosmosClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
		backupConfig:                        backupConfig,
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

	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}
	if existingCluster.CustomerProperties.DNS.BaseDomainPrefix == "" {
		return nil
	}

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}

	adCrud, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	rdCrud, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	clusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()
	clusterName := existingCluster.CustomerProperties.DNS.BaseDomainPrefix
	hcNamespace := controllers.HostedClusterNamespace(c.hostedClusterNamespaceEnvIdentifier, clusterID)
	hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, clusterName)

	clusterPaused := spc.Spec.BackupState == api.BackupScheduleStatePaused

	configSchedules := c.backupConfig.Schedules()
	schedules := make([]*velerov1api.Schedule, 0, len(configSchedules))
	for _, sc := range configSchedules {
		paused := c.backupConfig.Paused || clusterPaused
		schedule := NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace, sc.Name, sc.Schedule, sc.TTL, paused)
		schedules = append(schedules, schedule)
	}

	desiredADs, err := buildApplyDesiresFromSchedules(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, mcResourceID, schedules)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build ApplyDesires: %w", err))
	}
	desiredRDs, err := buildReadDesiresFromSchedules(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, mcResourceID, schedules)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build ReadDesires: %w", err))
	}

	if err := c.ensureDesireCreated(ctx, adCrud, rdCrud, desiredADs, desiredRDs); err != nil {
		return err
	}

	if err := c.ensureDesireUpdated(ctx, adCrud, desiredADs); err != nil {
		return err
	}

	if err := c.deleteStaleDesires(ctx, adCrud, rdCrud, mcResourceID, desiredADs); err != nil {
		return err
	}

	if err := c.cleanupCompletedOnDemandBackupDesires(ctx, adCrud); err != nil {
		return err
	}

	return nil
}

func (c *backupScheduleSyncer) ensureDesireCreated(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	desiredADs []*kubeapplier.ApplyDesire,
	desiredRDs []*kubeapplier.ReadDesire,
) error {
	for i, desired := range desiredADs {
		_, err := adCrud.Get(ctx, desired.ResourceID.Name)
		if err != nil {
			if !database.IsNotFoundError(err) {
				return utils.TrackError(fmt.Errorf("failed to get ApplyDesire %s: %w", desired.ResourceID.Name, err))
			}
			if _, err := adCrud.Create(ctx, desired, nil); err != nil {
				return utils.TrackError(fmt.Errorf("failed to create ApplyDesire %s: %w", desired.ResourceID.Name, err))
			}
		}
		if i < len(desiredRDs) {
			_, err := rdCrud.Get(ctx, desiredRDs[i].ResourceID.Name)
			if err != nil {
				if !database.IsNotFoundError(err) {
					return utils.TrackError(fmt.Errorf("failed to get ReadDesire %s: %w", desiredRDs[i].ResourceID.Name, err))
				}
				if _, err := rdCrud.Create(ctx, desiredRDs[i], nil); err != nil {
					return utils.TrackError(fmt.Errorf("failed to create ReadDesire %s: %w", desiredRDs[i].ResourceID.Name, err))
				}
			}
		}
	}
	return nil
}

func (c *backupScheduleSyncer) ensureDesireUpdated(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	desiredADs []*kubeapplier.ApplyDesire,
) error {
	for _, desired := range desiredADs {
		existing, err := adCrud.Get(ctx, desired.ResourceID.Name)
		if err != nil {
			if database.IsNotFoundError(err) {
				continue
			}
			return utils.TrackError(fmt.Errorf("failed to get ApplyDesire %s: %w", desired.ResourceID.Name, err))
		}
		if !applyDesireNeedsUpdate(existing, desired) {
			continue
		}
		desired.CosmosMetadata = *existing.CosmosMetadata.DeepCopy()
		desired.Status = *existing.Status.DeepCopy()
		if _, err := adCrud.Replace(ctx, desired, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ApplyDesire %s: %w", desired.ResourceID.Name, err))
		}
	}
	return nil
}

func (c *backupScheduleSyncer) deleteStaleDesires(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	mcResourceID *azcorearm.ResourceID,
	desiredADs []*kubeapplier.ApplyDesire,
) error {
	desiredSet := make(map[string]bool, len(desiredADs))
	for _, ad := range desiredADs {
		desiredSet[ad.ResourceID.Name] = true
	}

	iterator, err := adCrud.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ApplyDesires: %w", err))
	}
	for _, ad := range iterator.Items(ctx) {
		name := ad.ResourceID.Name
		if !strings.HasPrefix(name, backup.BackupDesireNamePrefix) {
			continue
		}
		if desiredSet[name] {
			continue
		}

		deleteAD := buildDeleteApplyDesireFromApplyDesire(ad, mcResourceID)
		deleteAD.CosmosMetadata = *ad.CosmosMetadata.DeepCopy()
		deleteAD.Status = *ad.Status.DeepCopy()
		if _, err := adCrud.Replace(ctx, deleteAD, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace stale ApplyDesire %s with Delete type: %w", name, err))
		}

		if err := rdCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete stale ReadDesire %s: %w", name, err))
		}
	}
	if err := iterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed iterating ApplyDesires: %w", err))
	}
	return nil
}

func applyDesireNeedsUpdate(existing, desired *kubeapplier.ApplyDesire) bool {
	if existing == nil {
		return true
	}

	var existingRaw, desiredRaw []byte
	if existing.Spec.ServerSideApply != nil && existing.Spec.ServerSideApply.KubeContent != nil {
		existingRaw = existing.Spec.ServerSideApply.KubeContent.Raw
	}
	if desired.Spec.ServerSideApply != nil && desired.Spec.ServerSideApply.KubeContent != nil {
		desiredRaw = desired.Spec.ServerSideApply.KubeContent.Raw
	}
	if existingRaw == nil && desiredRaw == nil {
		return false
	}
	if existingRaw == nil || desiredRaw == nil {
		return true
	}

	var existingObj, desiredObj map[string]any
	if err := json.Unmarshal(existingRaw, &existingObj); err != nil {
		return true
	}
	if err := json.Unmarshal(desiredRaw, &desiredObj); err != nil {
		return true
	}

	existingNormalized, _ := json.Marshal(existingObj)
	desiredNormalized, _ := json.Marshal(desiredObj)
	return string(existingNormalized) != string(desiredNormalized)
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

// cleanupCompletedOnDemandBackupDesires removes ApplyDesires for on-demand
// backups that have been successfully applied. This prevents kube-applier from
// re-creating the Backup object after Velero deletes it when its TTL expires.
func (c *backupScheduleSyncer) cleanupCompletedOnDemandBackupDesires(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
) error {
	iterator, err := adCrud.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ApplyDesires for on-demand cleanup: %w", err))
	}
	for _, ad := range iterator.Items(ctx) {
		name := ad.ResourceID.Name
		if !strings.HasPrefix(name, backup.OndemandDesireNamePrefix) {
			continue
		}
		if !isDesireSuccessful(ad.Status.Conditions) {
			continue
		}
		if err := adCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete on-demand ApplyDesire %s: %w", name, err))
		}
	}
	if err := iterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed iterating ApplyDesires for on-demand cleanup: %w", err))
	}
	return nil
}

func isDesireSuccessful(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == kubeapplier.ConditionTypeSuccessful && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

func (c *backupScheduleSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

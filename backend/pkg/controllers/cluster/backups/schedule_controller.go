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
package backups

import (
	"context"
	"fmt"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// backupScheduleSyncer creates Velero backup schedule ApplyDesires and ReadDesires
// for active clusters, and tears down those desires when the cluster is being deleted.
// Each ApplyDesire contains a single Velero Schedule; each matching ReadDesire observes
// the Schedule's status on the management cluster.
type backupScheduleSyncer struct {
	cosmosClient                 corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister

	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister    kubeapplierlisters.ApplyDesireLister
	readDesireLister     kubeapplierlisters.ReadDesireLister

	backupConfig *BackupConfig
}

var _ controllerutils.ClusterSyncer = (*backupScheduleSyncer)(nil)

const BackupScheduleControllerName = "BackupSchedule"

func NewBackupScheduleController(
	cosmosClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	backupConfig *BackupConfig,
) controllerutils.Controller {

	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	applyDesireInformer, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()

	syncer := &backupScheduleSyncer{
		cosmosClient:                 cosmosClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		kubeApplierDBClients:         kubeApplierDBClients,
		applyDesireLister:            applyDesireLister,
		readDesireLister:             readDesireLister,
		backupConfig:                 backupConfig,
	}

	controller := controllerutils.NewClusterWatchingController(
		BackupScheduleControllerName,
		cosmosClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)

	if kubeApplierInformers != nil {
		// React to ApplyDesire creation so that a missing ReadDesire can be
		// created promptly rather than waiting for the next resync cycle.
		// NewClusterWatchingController only wires ReadDesires (which carry
		// kube-applier status), so we add the ApplyDesire informer here.
		if err := controller.QueueForInformers(5*time.Minute, applyDesireInformer); err != nil {
			panic(err)
		}
	}

	return controller
}

// needsDeletionWork returns true when the cluster is being deleted and the
// schedule controller should tear down backup desires. Only
// ManagementClusterResourceID is required (to reach the kube-applier).
func needsDeletionWork(existingCluster coreapi.HCPOpenShiftCluster, serviceProviderCluster coreapi.ServiceProviderCluster) bool {
	if existingCluster.ServiceProviderProperties.DeletionTimestamp == nil {
		return false
	}
	return serviceProviderCluster.Status.ManagementClusterResourceID != nil
}

func (c *backupScheduleSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached Cluster: %w", err))
	}
	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached ServiceProviderCluster: %w", err))
	}

	if needsDeletionWork(*cachedCluster, *cachedServiceProviderCluster) {
		return c.syncDeletion(ctx, key, cachedServiceProviderCluster)
	}

	if !needsWork(*cachedCluster, *cachedServiceProviderCluster) {
		return nil
	}

	resourceID := cachedCluster.ResourceID.String()
	hostedClusterNamespace := cachedServiceProviderCluster.Status.HostedClusterNamespace
	controlPlaneNamespace := cachedServiceProviderCluster.Status.ControlPlaneNamespace
	managementClusterResourceID := cachedServiceProviderCluster.Status.ManagementClusterResourceID
	clusterPaused := cachedServiceProviderCluster.Spec.BackupScheduleState == coreapi.BackupScheduleStateDisabled

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, cachedServiceProviderCluster.Status.ManagementClusterResourceID)
	if kubeApplierClient == nil {
		// Registry doesn't have an entry yet for this MC (e.g. the fleet
		// lister hasn't caught up). Skip and rely on retrigger. When the MC
		// document is registered but misconfigured (e.g. missing its
		// kube-applier container name), For() surfaces that loudly.
		return nil
	}

	applyDesireCRUD, err := kubeApplierClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	readDesireCRUD, err := kubeApplierClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	configSchedules := c.backupConfig.Schedules()
	schedules := make([]*velerov1.Schedule, 0, len(configSchedules))
	for _, scheduleConfig := range configSchedules {
		paused := c.backupConfig.BackupScheduleState == coreapi.BackupScheduleStateDisabled || clusterPaused
		schedule := NewScheduledBackup(resourceID, hostedClusterNamespace, controlPlaneNamespace, scheduleConfig, paused)
		schedules = append(schedules, schedule)
	}

	// Build every ApplyDesire up front, then hand the built desires to the
	// create-or-update pass. Building and create-or-update stay separate steps.
	applyDesires, err := buildApplyDesiresFromSchedules(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, managementClusterResourceID, schedules)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build ApplyDesires: %w", err))
	}

	if err := c.createOrUpdateDesires(ctx, key, applyDesires, applyDesireCRUD, readDesireCRUD); err != nil {
		return err
	}

	applyDesireNames := make(map[string]bool, len(applyDesires))
	for _, applyDesire := range applyDesires {
		applyDesireNames[applyDesire.ResourceID.Name] = true
	}

	if requeue, err := c.deleteStaleApplyDesires(ctx, key, applyDesireCRUD, applyDesireNames); requeue || err != nil {
		return err
	}

	if requeue, err := c.deleteStaleReadDesires(ctx, key, readDesireCRUD); requeue || err != nil {
		return err
	}

	return nil
}

// syncDeletion tears down all backup schedule desires for a cluster that is
// being deleted. It iterates desires directly and drives each through the
// Apply→Delete→Wait→Purge lifecycle.
func (c *backupScheduleSyncer) syncDeletion(ctx context.Context, key controllerutils.HCPClusterKey, serviceProviderCluster *coreapi.ServiceProviderCluster) error {
	managementClusterResourceID := serviceProviderCluster.Status.ManagementClusterResourceID

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if kubeApplierClient == nil {
		// Registry doesn't have an entry yet for this MC (e.g. the fleet
		// lister hasn't caught up). Skip and rely on retrigger. When the MC
		// document is registered but misconfigured (e.g. missing its
		// kube-applier container name), For() surfaces that loudly.
		return nil
	}

	applyDesireCRUD, err := kubeApplierClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	readDesireCRUD, err := kubeApplierClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	applyDesires, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return err
	}
	for _, applyDesire := range applyDesires {
		if _, ok := applyDesire.Tags[backup.DesireTagKeySchedule]; !ok {
			continue
		}
		if _, err := kubeapplierhelpers.EnsureApplyDesireRemoved(ctx, applyDesire.ResourceID.Name, applyDesireCRUD); err != nil {
			return err
		}
		return nil
	}

	readDesires, err := c.readDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}
	for _, readDesire := range readDesires {
		if _, ok := readDesire.Tags[backup.DesireTagKeySchedule]; !ok {
			continue
		}
		if err := readDesireCRUD.Delete(ctx, readDesire.ResourceID.Name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete ReadDesire %s: %w", readDesire.ResourceID.Name, err))
		}
		// requeue for next sync
		return nil
	}
	return nil
}

// createOrUpdateDesires reconciles the backup-schedule ApplyDesires (and their
// paired ReadDesires). The backups package builds its own desires (see
// buildApplyDesiresFromSchedules / buildReadDesireFromApplyDesire): the
// ApplyDesires are built by the caller, this method builds every paired
// ReadDesire up front, and only then hands the fully-formed desires to the
// shared kubeapplierhelpers ensure helpers, which consult the informer listers,
// compare with JSON-aware equality, and write only on drift. Keeping the build
// and create-or-update work in separate passes (rather than one combined loop)
// mirrors the original structure. All desires are cluster-scoped and carry the
// schedule tag so the stale-cleanup and deletion paths can find them.
func (c *backupScheduleSyncer) createOrUpdateDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	applyDesires []*kubeapplierapi.ApplyDesire,
	applyDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	readDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
) error {
	// Build phase: construct the paired ReadDesire for every ApplyDesire before
	// touching the store.
	readDesires := make([]*kubeapplierapi.ReadDesire, 0, len(applyDesires))
	for _, applyDesire := range applyDesires {
		readDesire, err := buildReadDesireFromApplyDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, applyDesire)
		if err != nil {
			return err
		}
		readDesires = append(readDesires, readDesire)
	}

	// Ensure phase: create-or-update the built desires. ReadDesires first so a
	// schedule's status is observed as soon as its ApplyDesire lands.
	for _, readDesire := range readDesires {
		if err := kubeapplierhelpers.EnsureReadDesire(ctx, readDesireCRUD, c.readDesireLister, readDesire); err != nil {
			return err
		}
	}
	for _, applyDesire := range applyDesires {
		if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyDesireCRUD, c.applyDesireLister, applyDesire); err != nil {
			return err
		}
	}
	return nil
}

func (c *backupScheduleSyncer) deleteStaleApplyDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	applyDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	applyDesireNames map[string]bool,
) (bool, error) {
	applyDesires, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, err
	}
	for _, applyDesire := range applyDesires {
		if applyDesireNames[applyDesire.ResourceID.Name] {
			continue
		}
		if _, ok := applyDesire.Tags[backup.DesireTagKeySchedule]; !ok {
			continue
		}
		removed, err := kubeapplierhelpers.EnsureApplyDesireRemoved(ctx, applyDesire.ResourceID.Name, applyDesireCRUD)
		if err != nil {
			return false, err
		}
		if removed {
			return true, nil
		}
	}

	return false, nil
}

func (c *backupScheduleSyncer) deleteStaleReadDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	readDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
) (bool, error) {
	readDesires, err := c.readDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}
	for _, readDesire := range readDesires {
		if _, ok := readDesire.Tags[backup.DesireTagKeySchedule]; !ok {
			continue
		}
		name := readDesire.ResourceID.Name
		_, err := c.applyDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, name)
		if err == nil {
			continue
		}
		if !cosmosstorageutils.IsNotFoundError(err) {
			return false, err
		}
		if err := readDesireCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("failed to delete stale ReadDesire %s: %w", name, err))
		}
		return true, nil
	}
	return false, nil
}

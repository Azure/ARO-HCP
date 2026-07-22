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
	"fmt"
	"strings"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	internalcontrollerutils "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// backupScheduleSyncer creates Velero backup schedule ApplyDesires and ReadDesires
// for clusters that are not being deleted.
// Each ApplyDesire contains a single Velero Schedule; each matching ReadDesire observes
// the Schedule's status on the management cluster.
type backupScheduleSyncer struct {
	cosmosClient                 database.ResourcesDBClient
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister

	kubeApplierDBClients database.KubeApplierDBClients

	hostedClusterNamespaceEnvIdentifier string

	backupConfig *BackupConfig
}

var _ controllerutils.ClusterSyncer = (*backupScheduleSyncer)(nil)

const BackupScheduleControllerName = "BackupSchedule"

func NewBackupScheduleController(
	cosmosClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	hostedClusterNamespaceEnvIdentifier string,
	backupConfig *BackupConfig,
) controllerutils.Controller {

	_, clusterLister := informers.Clusters()
	_, spcLister := informers.ServiceProviderClusters()
	applyDesireInformer, _ := kubeApplierInformers.ApplyDesires()

	syncer := &backupScheduleSyncer{
		cosmosClient:                        cosmosClient,
		clusterLister:                       clusterLister,
		serviceProviderClusterLister:        spcLister,
		kubeApplierDBClients:                kubeApplierDBClients,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
		backupConfig:                        backupConfig,
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

// needsWork returns true when backup desires should be reconciled for the cluster.
// Clusters being deleted or that have never reached Succeeded state are skipped.
func needsWork(existingCluster api.HCPOpenShiftCluster) bool {
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	if existingCluster.ServiceProviderProperties.BillingDocumentCosmosID == "" {
		return false
	}
	return true
}

func (c *backupScheduleSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached Cluster: %w", err))
	}
	if !needsWork(*cachedCluster) {
		return nil
	}

	cachedSPC, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached ServiceProviderCluster: %w", err))
	}

	mcResourceID := cachedSPC.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return utils.TrackError(fmt.Errorf("failed to get KubeApplier Client for %s", mcResourceID))
	}

	adCrud, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	rdCrud, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	if cachedCluster.ServiceProviderProperties.ClusterServiceID == nil {
		return utils.TrackError(fmt.Errorf("expected ClusterServiceID to not be nil"))
	}
	clusterName := cachedCluster.CustomerProperties.DNS.BaseDomainPrefix
	if clusterName == "" {
		return utils.TrackError(fmt.Errorf("expected BaseDomainPrefix to not be empty"))
	}
	clusterID := cachedCluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcNamespace := controllers.HostedClusterNamespace(c.hostedClusterNamespaceEnvIdentifier, clusterID)
	hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, clusterName)

	clusterPaused := cachedSPC.Spec.BackupState == api.BackupScheduleStatePaused

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

	if done, err := c.ensureADsCreated(ctx, adCrud, desiredADs); done || err != nil {
		return err
	}

	if done, err := c.ensureRDsCreated(ctx, rdCrud, desiredRDs); done || err != nil {
		return err
	}

	if done, err := c.ensureDesireUpdated(ctx, adCrud, desiredADs); done || err != nil {
		return err
	}

	if done, err := c.deleteStaleApplyDesires(ctx, adCrud, mcResourceID, desiredADs); done || err != nil {
		return err
	}

	if done, err := c.deleteStaleReadDesires(ctx, rdCrud, desiredRDs); done || err != nil {
		return err
	}

	return nil
}

func (c *backupScheduleSyncer) ensureADsCreated(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	desiredADs []*kubeapplier.ApplyDesire,
) (bool, error) {
	for _, desired := range desiredADs {
		_, err := adCrud.Get(ctx, desired.ResourceID.Name)
		if err == nil {
			continue
		}
		if !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("failed to get ApplyDesire %s: %w", desired.ResourceID.Name, err))
		}
		if _, err := adCrud.Create(ctx, desired, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to create ApplyDesire %s: %w", desired.ResourceID.Name, err))
		}
		return true, nil
	}
	return false, nil
}

func (c *backupScheduleSyncer) ensureRDsCreated(
	ctx context.Context,
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	desiredRDs []*kubeapplier.ReadDesire,
) (bool, error) {
	for _, desired := range desiredRDs {
		_, err := rdCrud.Get(ctx, desired.ResourceID.Name)
		if err == nil {
			continue
		}
		if !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("failed to get ReadDesire %s: %w", desired.ResourceID.Name, err))
		}
		if _, err := rdCrud.Create(ctx, desired, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to create ReadDesire %s: %w", desired.ResourceID.Name, err))
		}
		return true, nil
	}
	return false, nil
}

func (c *backupScheduleSyncer) ensureDesireUpdated(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	desiredADs []*kubeapplier.ApplyDesire,
) (bool, error) {
	for _, desired := range desiredADs {
		existing, err := adCrud.Get(ctx, desired.ResourceID.Name)
		if err != nil {
			if database.IsNotFoundError(err) {
				continue
			}
			return false, utils.TrackError(fmt.Errorf("failed to get ApplyDesire %s: %w", desired.ResourceID.Name, err))
		}
		if !internalcontrollerutils.NeedsUpdate(existing.Spec.ServerSideApply.KubeContent, desired.Spec.ServerSideApply.KubeContent) {
			continue
		}
		desired.CosmosMetadata = *existing.CosmosMetadata.DeepCopy()
		if _, err := adCrud.Replace(ctx, desired, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to replace ApplyDesire %s: %w", desired.ResourceID.Name, err))
		}
		return true, nil
	}
	return false, nil
}

func (c *backupScheduleSyncer) deleteStaleApplyDesires(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	mcResourceID *azcorearm.ResourceID,
	desiredADs []*kubeapplier.ApplyDesire,
) (bool, error) {
	desiredSet := make(map[string]bool, len(desiredADs))
	for _, ad := range desiredADs {
		desiredSet[ad.ResourceID.Name] = true
	}

	iterator, err := adCrud.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list ApplyDesires: %w", err))
	}
	for _, ad := range iterator.Items(ctx) {
		name := ad.ResourceID.Name
		if !strings.HasPrefix(name, backup.BackupScheduleDesireNamePrefix) || desiredSet[name] {
			continue
		}

		// ensure the delete is successful before purging the object from cosmos
		if ad.Spec.Type == kubeapplier.ApplyDesireTypeDelete {
			if !isDesireSuccessful(ad.Status.Conditions) {
				continue
			}
			if err := adCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("failed to delete successful stale ApplyDesire %s: %w", name, err))
			}
			return true, nil
		}

		// First time this AD is seen as stale — replace it with a Delete-type desire
		// to signal kube-applier to remove the Schedule from the management cluster.
		deleteAD := buildDeleteApplyDesireFromApplyDesire(ad, mcResourceID)
		if _, err := adCrud.Replace(ctx, deleteAD, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to replace stale ApplyDesire %s with Delete type: %w", name, err))
		}
		return true, nil
	}
	if err := iterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("failed iterating ApplyDesires: %w", err))
	}
	return false, nil
}

func (c *backupScheduleSyncer) deleteStaleReadDesires(
	ctx context.Context,
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	desiredRDs []*kubeapplier.ReadDesire,
) (bool, error) {
	desiredSet := make(map[string]bool, len(desiredRDs))
	for _, rd := range desiredRDs {
		desiredSet[rd.ResourceID.Name] = true
	}

	iterator, err := rdCrud.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}
	for _, rd := range iterator.Items(ctx) {
		name := rd.ResourceID.Name
		if !strings.HasPrefix(name, backup.BackupScheduleDesireNamePrefix) || desiredSet[name] {
			continue
		}
		if err := rdCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("failed to delete stale ReadDesire %s: %w", name, err))
		}
		return true, nil
	}
	if err := iterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("failed iterating ReadDesires: %w", err))
	}
	return false, nil
}

func isDesireSuccessful(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == kubeapplier.ConditionTypeSuccessful && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

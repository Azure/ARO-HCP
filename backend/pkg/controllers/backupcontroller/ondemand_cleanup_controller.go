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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// onDemandBackupCleanupSyncer removes ApplyDesires and ReadDesires for on-demand
// backups.  Once the ApplyDesire creates a backup on the management velero needs
// to manage the resource so backups are cleaned up on TTL expiration.  When velero
// has deleted the backup when TTL expires the read desire can be removed.
type onDemandBackupCleanupSyncer struct {
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	kubeApplierDBClients         database.KubeApplierDBClients
}

var _ controllerutils.ClusterSyncer = (*onDemandBackupCleanupSyncer)(nil)

const OnDemandBackupCleanupControllerName = "OnDemandBackupCleanup"

func NewOnDemandBackupCleanupController(
	cosmosClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	inf informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {

	_, clusterLister := inf.Clusters()
	_, spcLister := inf.ServiceProviderClusters()

	syncer := &onDemandBackupCleanupSyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: spcLister,
		kubeApplierDBClients:         kubeApplierDBClients,
	}

	return controllerutils.NewClusterWatchingController(
		OnDemandBackupCleanupControllerName,
		cosmosClient,
		inf,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *onDemandBackupCleanupSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	err = cleanupCompletedOnDemandBackupDesires(ctx, adCrud, rdCrud)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to clean up demand backup Read/Apply Desires: %w", err))
	}
	return nil
}

// cleanupCompletedOnDemandBackupDesires removes ApplyDesires and ReadDesires
// for on-demand backups. It iterates ReadDesires and point-Gets the paired
// ApplyDesire to decide what to clean up.
func cleanupCompletedOnDemandBackupDesires(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
) error {
	rdIterator, err := rdCrud.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ReadDesires for on-demand cleanup: %w", err))
	}
	for _, rd := range rdIterator.Items(ctx) {
		name := rd.ResourceID.Name
		if !strings.HasPrefix(name, backup.OndemandBackupDesireNamePrefix) {
			continue
		}

		ad, err := adCrud.Get(ctx, name)
		if err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to get on-demand ApplyDesire %s: %w", name, err))
		}

		if ad != nil {
			// Require both desires to be Successful AND the ReadDesire to have
			// observed a non-nil KubeContent before removing the ApplyDesire.
			// This ensures the read informer has confirmed the Backup exists on
			// the cluster, so a later nil KubeContent unambiguously means Velero
			// deleted it rather than the informer not having seen it yet.
			if !isDesireSuccessful(ad.Status.Conditions) || !isDesireSuccessful(rd.Status.Conditions) {
				continue
			}
			if rd.Status.KubeContent == nil || rd.Status.KubeContent.Raw == nil {
				continue
			}
			if err := adCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
				return utils.TrackError(fmt.Errorf("failed to delete on-demand ApplyDesire %s: %w", name, err))
			}
			return nil
		}

		// ApplyDesire is gone. Remove the ReadDesire once the read informer
		// confirms the Backup is absent (Successful=True, nil KubeContent).
		if !isDesireSuccessful(rd.Status.Conditions) {
			continue
		}
		if rd.Status.KubeContent != nil && rd.Status.KubeContent.Raw != nil {
			continue
		}
		if err := rdCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete on-demand ReadDesire %s: %w", name, err))
		}
		return nil
	}
	if err := rdIterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed iterating ReadDesires for on-demand cleanup: %w", err))
	}
	return nil
}

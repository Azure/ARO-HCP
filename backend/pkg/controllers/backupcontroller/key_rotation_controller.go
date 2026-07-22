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

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const KeyRotationBackupControllerName = "KeyRotationBackup"

type keyRotationBackupSyncer struct {
	cosmosClient                        database.ResourcesDBClient
	clusterLister                       listers.ClusterLister
	serviceProviderClusterLister        listers.ServiceProviderClusterLister
	readDesireLister                    dblisters.ReadDesireLister
	kubeApplierDBClients                database.KubeApplierDBClients
	hostedClusterNamespaceEnvIdentifier string
	backupConfig                        *BackupConfig
}

var _ controllerutils.ClusterSyncer = (*keyRotationBackupSyncer)(nil)

func NewKeyRotationBackupController(
	cosmosClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	inf informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	hostedClusterNamespaceEnvIdentifier string,
	backupConfig *BackupConfig,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {

	_, clusterLister := inf.Clusters()
	_, spcLister := inf.ServiceProviderClusters()

	syncer := &keyRotationBackupSyncer{
		cosmosClient:                        cosmosClient,
		clusterLister:                       clusterLister,
		serviceProviderClusterLister:        spcLister,
		readDesireLister:                    readDesireLister,
		kubeApplierDBClients:                kubeApplierDBClients,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
		backupConfig:                        backupConfig,
	}

	return controllerutils.NewClusterWatchingController(
		KeyRotationBackupControllerName,
		cosmosClient,
		inf,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *keyRotationBackupSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster: %w", err))
	}
	if hostedCluster == nil {
		return nil
	}

	if hostedCluster.Status.SecretEncryption.ActiveKey == hostedCluster.Status.SecretEncryption.TargetKey {
		return nil
	}
	// exit early for clusters pre-4.22
	kmsKeyVersion := hostedCluster.Status.SecretEncryption.ActiveKey.Azure.KeyVersion
	if kmsKeyVersion == "" {
		return nil
	}

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

	if cachedCluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}
	clusterName := cachedCluster.CustomerProperties.DNS.BaseDomainPrefix
	if clusterName == "" {
		return nil
	}
	clusterID := cachedCluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcNamespace := controllers.HostedClusterNamespace(c.hostedClusterNamespaceEnvIdentifier, clusterID)
	hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, clusterName)

	if !rotationComplete(hostedCluster) {
		return nil
	}

	backupName := keyRotationBackupName(clusterID, kmsKeyVersion)
	desireName := keyRotationDesireName(backupName)

	hcpBackup := backup.NewBackup(backupName, clusterID, kmsKeyVersion, c.backupConfig.KeyRotationBackupTTL(), hcNamespace, hcpNamespace)
	ad, rd, err := buildOnDemandBackupDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName, mcResourceID, hcpBackup)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build key rotation backup desires: %w", err))
	}

	if done, err := c.ensureDesireCreated(ctx, adCrud, rdCrud, desireName, ad, rd, kmsKeyVersion); done || err != nil {
		return err
	}

	if done, err := c.deleteStaleKeyRotationDesires(ctx, rdCrud, desireName); done || err != nil {
		return err
	}

	return nil
}

func (c *keyRotationBackupSyncer) ensureDesireCreated(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	desireName string,
	ad *kubeapplier.ApplyDesire,
	rd *kubeapplier.ReadDesire,
	kmsKeyVersion string,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	_, adErr := adCrud.Get(ctx, desireName)
	if adErr != nil && !database.IsNotFoundError(adErr) {
		return false, utils.TrackError(fmt.Errorf("failed to get ApplyDesire %s: %w", desireName, adErr))
	}
	_, rdErr := rdCrud.Get(ctx, desireName)
	if rdErr != nil && !database.IsNotFoundError(rdErr) {
		return false, utils.TrackError(fmt.Errorf("failed to get ReadDesire %s: %w", desireName, rdErr))
	}
	if !database.IsNotFoundError(adErr) && !database.IsNotFoundError(rdErr) {
		return false, nil
	}

	if database.IsNotFoundError(adErr) {
		if _, err := adCrud.Create(ctx, ad, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to create ApplyDesire %s: %w", desireName, err))
		}
		logger.Info("created key rotation backup ApplyDesire", "desireName", desireName, "keyVersion", kmsKeyVersion)
	}
	if database.IsNotFoundError(rdErr) {
		if _, err := rdCrud.Create(ctx, rd, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to create ReadDesire %s: %w", desireName, err))
		}
		logger.Info("created key rotation backup ReadDesire", "desireName", desireName, "keyVersion", kmsKeyVersion)
	}
	return true, nil
}

// deleteStaleKeyRotationDesires removes ReadDesires for old key versions.
// Unlike schedule_controller's deleteStaleDesires (which converts ADs to Delete
// type), this does a Cosmos-only delete — the Velero Backup on the management
// cluster is left for Velero to manage via its built-in TTL.
// Stale ApplyDesires for old key rotations are cleaned up by
// OnDemandBackupCleanupController, which handles all on-demand backup AD lifecycle.
func (c *keyRotationBackupSyncer) deleteStaleKeyRotationDesires(
	ctx context.Context,
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	currentDesireName string,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	iterator, err := rdCrud.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}
	for _, rd := range iterator.Items(ctx) {
		name := rd.ResourceID.Name
		if !strings.HasPrefix(name, backup.OndemandBackupDesireNamePrefix) {
			continue
		}
		if !strings.Contains(name, keyRotationBackupNameSeparator) {
			continue
		}
		if name == currentDesireName {
			continue
		}
		if err := rdCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("failed to delete stale key rotation ReadDesire %s: %w", name, err))
		}
		logger.Info("deleted stale key rotation ReadDesire", "desireName", name)
		return true, nil
	}
	if err := iterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("failed iterating ReadDesires: %w", err))
	}
	return false, nil
}

// rotationComplete returns true only when a rotation has finished:
//   - History empty --> no rotation ever happened
//   - TargetKey set and differs from ActiveKey --> key change pending
//   - History[0] != Completed --> re-encryption running
func rotationComplete(hc *v1beta1.HostedCluster) bool {
	status := hc.Status.SecretEncryption
	if len(status.History) == 0 {
		return false
	}
	if status.TargetKey.Azure.KeyVersion != "" &&
		status.TargetKey.Azure.KeyVersion != status.ActiveKey.Azure.KeyVersion {
		return false
	}
	if status.History[0].State != v1beta1.EncryptionMigrationStateCompleted {
		return false
	}
	return true
}

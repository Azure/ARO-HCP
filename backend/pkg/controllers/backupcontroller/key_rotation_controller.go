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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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

	if done, err := c.deleteStaleKeyRotationDesires(ctx, adCrud, rdCrud, mcResourceID, key, desireName, hostedCluster); done || err != nil {
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

// deleteStaleKeyRotationDesires deletes on-demand Velero Backups from the
// management cluster for key versions older than N-1. Scheduled backups with
// stale key versions are not handled here — the schedule controller updates
// the template with the new key version so no new stale backups are created,
// and old ones expire via Velero TTL. Active deletion of scheduled backups
// requires label-selector-based ReadDesires in kube-applier (not yet available).
//
// The lifecycle follows the schedule controller's deleteStaleDesires pattern:
// AD-driven iteration → convert to Delete type → wait for success → purge AD+RD.
// Orphaned RDs (where the ondemand cleanup controller already deleted the AD)
// are cleaned up in a second pass.
func (c *keyRotationBackupSyncer) deleteStaleKeyRotationDesires(
	ctx context.Context,
	adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	mcResourceID *azcorearm.ResourceID,
	key controllerutils.HCPClusterKey,
	currentDesireName string,
	hostedCluster *v1beta1.HostedCluster,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)
	status := hostedCluster.Status.SecretEncryption

	if len(status.History) < 1 || status.History[0].State != v1beta1.EncryptionMigrationStateCompleted {
		return false, nil
	}
	nMinus1Fingerprint := status.History[0].From.Fingerprint
	keyVaultName := status.ActiveKey.Azure.KeyVaultName
	keyName := status.ActiveKey.Azure.KeyName
	if keyVaultName == "" || keyName == "" {
		return false, nil
	}

	// Pass 1: iterate ADs (like schedule controller's deleteStaleDesires).
	adIterator, err := adCrud.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list ApplyDesires: %w", err))
	}
	for _, ad := range adIterator.Items(ctx) {
		name := ad.ResourceID.Name
		if !strings.HasPrefix(name, backup.OndemandBackupDesireNamePrefix) {
			continue
		}
		if !strings.Contains(name, keyRotationBackupNameSeparator) {
			continue
		}
		if name == currentDesireName {
			continue
		}
		keyVersion, ok := extractKeyVersionFromDesireName(name)
		if !ok {
			continue
		}
		if azureKMSKeyFingerprint(keyVaultName, keyName, keyVersion) == nMinus1Fingerprint {
			continue
		}

		if ad.Spec.Type == kubeapplier.ApplyDesireTypeDelete {
			if !isDesireSuccessful(ad.Status.Conditions) {
				continue
			}
			if err := rdCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("failed to delete stale ReadDesire %s: %w", name, err))
			}
			if err := adCrud.Delete(ctx, name); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("failed to delete successful Delete-type ApplyDesire %s: %w", name, err))
			}
			logger.Info("cleaned up stale key rotation backup desires", "desireName", name, "keyVersion", keyVersion)
			return true, nil
		}

		deleteAD := buildDeleteApplyDesireFromApplyDesire(ad, mcResourceID)
		if _, err := adCrud.Replace(ctx, deleteAD, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to convert ApplyDesire %s to Delete type: %w", name, err))
		}
		logger.Info("converted stale key rotation ApplyDesire to Delete type", "desireName", name, "keyVersion", keyVersion)
		return true, nil
	}
	if err := adIterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("failed iterating ApplyDesires: %w", err))
	}

	// Pass 2: clean up orphaned RDs whose AD was already deleted (e.g., by
	// the ondemand cleanup controller) but whose backup still needs deletion
	// from the management cluster.
	rdIterator, err := rdCrud.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}
	for _, rd := range rdIterator.Items(ctx) {
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
		keyVersion, ok := extractKeyVersionFromDesireName(name)
		if !ok {
			continue
		}
		if azureKMSKeyFingerprint(keyVaultName, keyName, keyVersion) == nMinus1Fingerprint {
			continue
		}
		if _, err := adCrud.Get(ctx, name); err == nil {
			continue
		}

		backupName := strings.TrimPrefix(name, backup.OndemandBackupDesireNamePrefix)
		deleteAD, err := buildDeleteApplyDesireForBackup(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, name, mcResourceID, backupName)
		if err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to build Delete ApplyDesire for %s: %w", name, err))
		}
		if _, err := adCrud.Create(ctx, deleteAD, nil); err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to create Delete ApplyDesire %s: %w", name, err))
		}
		logger.Info("created Delete-type ApplyDesire for orphaned key rotation backup", "desireName", name, "keyVersion", keyVersion)
		return true, nil
	}
	if err := rdIterator.GetError(); err != nil {
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

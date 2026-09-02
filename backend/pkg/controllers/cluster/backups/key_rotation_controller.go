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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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

const keyRotationBackupNameSeparator = "-keyrotation-"

// rotationComplete returns true only when KMS key rotation has fully completed and the
// cluster is safe to snapshot. The HostedClusterConfigOperator derives the rotation phase
// from the live SecretEncryption state and clears TargetKey once the final rotation update
// has completed; History[0].State is recorded for observability, not as an input to the
// decision. We therefore require both TargetKey=="" and History[0].State==Completed as
// a conservative fail-safe: if those fields are ever patched separately, we skip the
// backup instead of snapshotting mid-rotation. This also covers an existing AESCBC
// cluster's spec-driven migration to KMS after the first KMS key is active; clusters
// created directly with KMS never populate History and therefore never trigger this path.
// This function does not deduplicate: SyncOnce gates actual backup creation on
// Status.KeyRotationBackupFingerprint, so a completed backup for the active key
// is never recreated.
func rotationComplete(hc *hyperv1beta1.HostedCluster) bool {
	status := hc.Status.SecretEncryption
	if len(status.History) == 0 {
		return false
	}
	return status.TargetKey.Azure.KeyVersion == "" &&
		status.History[0].State == hyperv1beta1.EncryptionMigrationStateCompleted
}

const KeyRotationBackupControllerName = "KeyRotationBackup"

type keyRotationBackupSyncer struct {
	cosmosClient                 corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister

	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister    kubeapplierlisters.ApplyDesireLister
	readDesireLister     kubeapplierlisters.ReadDesireLister

	backupConfig *BackupConfig
}

var _ controllerutils.ClusterSyncer = (*keyRotationBackupSyncer)(nil)

func NewKeyRotationBackupController(
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

	syncer := &keyRotationBackupSyncer{
		cosmosClient:                 cosmosClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		kubeApplierDBClients:         kubeApplierDBClients,
		applyDesireLister:            applyDesireLister,
		readDesireLister:             readDesireLister,
		backupConfig:                 backupConfig,
	}

	controller := controllerutils.NewClusterWatchingController(
		KeyRotationBackupControllerName,
		cosmosClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)

	if kubeApplierInformers != nil {
		if err := controller.QueueForInformers(5*time.Minute, applyDesireInformer); err != nil {
			panic(err)
		}
	}

	return controller
}

func (c *keyRotationBackupSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	cm := cachedCluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged
	if cm == nil || cm.Kms == nil || cm.Kms.ActiveKey.Version == "" {
		return nil
	}

	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached HostedCluster: %w", err))
	}
	if hostedCluster == nil {
		return nil
	}

	if !rotationComplete(hostedCluster) {
		return nil
	}

	managementClusterResourceID := cachedServiceProviderCluster.Status.ManagementClusterResourceID
	kubeApplierClient := c.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if kubeApplierClient == nil {
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

	activeKey := hostedCluster.Status.SecretEncryption.ActiveKey.Azure
	kmsKeyFingerprint := backup.AzureKMSKeyFingerprint(activeKey.KeyVaultName, activeKey.KeyName, activeKey.KeyVersion)
	resourceID := cachedCluster.ResourceID.String()
	hostedClusterNamespace := cachedServiceProviderCluster.Status.HostedClusterNamespace
	controlPlaneNamespace := cachedServiceProviderCluster.Status.ControlPlaneNamespace

	currentDesireName := keyRotationDesireName(keyRotationBackupName(hostedClusterNamespace, kmsKeyFingerprint))

	// Once this rotation's on-demand backup has already completed successfully,
	// there is nothing left to create: skip straight to the cleanup sweep below.
	if cachedServiceProviderCluster.Status.KeyRotationBackupFingerprint != kmsKeyFingerprint {
		// On-demand backups use the first (shortest-lived) schedule's TTL, just long
		// enough to survive until the next scheduled backup captures the post-rotation state.
		if c.backupConfig == nil || len(c.backupConfig.Schedules()) == 0 {
			return utils.TrackError(fmt.Errorf("no backup schedules configured to reuse TTL for on-demand backup (backupConfig=%v)", c.backupConfig))
		}
		ttl := c.backupConfig.Schedules()[0].TTL
		veleroBackup := backup.NewBackup(keyRotationBackupName(hostedClusterNamespace, kmsKeyFingerprint), resourceID, kmsKeyFingerprint, hostedClusterNamespace, controlPlaneNamespace, ttl)

		// No explicit in-flight check: Velero queues backups natively, ensuring
		// on-demand backups run after scheduled backups and provide a valid backup
		// as soon as possible, avoiding longer waits for the next scheduled backup.
		desiredApplyDesire, err := buildOnDemandBackupApplyDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, managementClusterResourceID, veleroBackup)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to build key rotation backup desire: %w", err))
		}

		// Build the paired ReadDesire, then create-or-update both through the
		// shared kubeapplierhelpers ensure helpers. The ReadDesire is ensured
		// first so the on-demand backup's status is observed as soon as its
		// ApplyDesire lands.
		desiredReadDesire, err := buildReadDesireFromApplyDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desiredApplyDesire)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to build key rotation read desire: %w", err))
		}

		if err := kubeapplierhelpers.EnsureReadDesire(ctx, readDesireCRUD, c.readDesireLister, desiredReadDesire); err != nil {
			return err
		}

		if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyDesireCRUD, c.applyDesireLister, desiredApplyDesire); err != nil {
			return err
		}
	}

	// Retire on-demand desires once their work is done. The completed backup itself is left
	// alone to expire at its own Velero TTL, so it stays a valid restore point (and stays
	// listable via the admin API) until then; scheduled backups provide ongoing coverage.
	if requeue, err := c.purgeCompletedOnDemandApplyDesires(ctx, key, applyDesireCRUD, cachedServiceProviderCluster, kmsKeyFingerprint, currentDesireName); requeue || err != nil {
		return err
	}
	if requeue, err := c.deleteStaleOnDemandReadDesires(ctx, key, readDesireCRUD, cachedServiceProviderCluster, kmsKeyFingerprint, currentDesireName); requeue || err != nil {
		return err
	}

	return nil
}

// syncDeletion purges on-demand key-rotation desires for a deleted cluster directly
// from Cosmos. Unlike Schedule desires, these target Backup CRs and have no in-flight
// check, so setting Type=Delete could race a running Velero backup. Direct purging
// leaves the Backup CR and its data intact until its TTL expires.
func (c *keyRotationBackupSyncer) syncDeletion(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	cachedServiceProviderCluster *coreapi.ServiceProviderCluster,
) error {
	managementClusterResourceID := cachedServiceProviderCluster.Status.ManagementClusterResourceID
	kubeApplierClient := c.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if kubeApplierClient == nil {
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

	logger := utils.LoggerFromContext(ctx)

	applyDesires, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return err
	}
	for _, applyDesire := range applyDesires {
		if _, ok := applyDesire.Tags[backup.DesireTagKeyOndemandBackup]; !ok {
			continue
		}
		logger.Info("purging on-demand backup ApplyDesire for deleted cluster", "desire", applyDesire.ResourceID.Name)
		if err := purgeApplyDesire(ctx, *applyDesire, applyDesireCRUD); err != nil {
			return err
		}
	}

	readDesires, err := c.readDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}
	for _, readDesire := range readDesires {
		if _, ok := readDesire.Tags[backup.DesireTagKeyOndemandBackup]; !ok {
			continue
		}
		name := readDesire.ResourceID.Name
		logger.Info("deleting on-demand backup ReadDesire for deleted cluster", "desire", name)
		if err := readDesireCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete ReadDesire %s: %w", name, err))
		}
	}

	return nil
}

// observedVeleroBackupPhase returns the phase of the Backup observed by kube-applier.
func observedVeleroBackupPhase(kubeContent *runtime.RawExtension) (velerov1.BackupPhase, error) {
	if kubeContent == nil || kubeContent.Raw == nil {
		// Successful=True only confirms kube-applier is alive, not that it observed a Backup.
		return "", nil
	}
	var veleroBackup velerov1.Backup
	if err := json.Unmarshal(kubeContent.Raw, &veleroBackup); err != nil {
		return "", fmt.Errorf("failed to unmarshal observed Backup: %w", err)
	}
	return veleroBackup.Status.Phase, nil
}

// purgeCompletedOnDemandApplyDesires retires superseded or completed on-demand desires.
func (c *keyRotationBackupSyncer) purgeCompletedOnDemandApplyDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	applyDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	cachedServiceProviderCluster *coreapi.ServiceProviderCluster,
	kmsKeyFingerprint string,
	currentDesireName string,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	applyDesires, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, err
	}
	for _, applyDesire := range applyDesires {
		if _, ok := applyDesire.Tags[backup.DesireTagKeyOndemandBackup]; !ok {
			continue
		}

		isCurrent := applyDesire.ResourceID.Name == currentDesireName
		// Desires from older rotations can be retired regardless of backup status.
		reason := "superseded by newer rotation"
		if isCurrent {
			if cachedServiceProviderCluster.Status.KeyRotationBackupFingerprint == kmsKeyFingerprint {
				// Completion was already durably recorded on a prior sync: purge
				// unconditionally. Do not re-derive phase from the ReadDesire here,
				// since its KubeContent may have already gone nil (Velero TTL GC
				// observed by kube-applier) or the ReadDesire may already be gone
				// (deleteStaleOnDemandReadDesires runs after this); either would
				// otherwise orphan this ApplyDesire forever.
				reason = "backup completed successfully; Velero Backup retained until its TTL"
			} else {
				// Fingerprint not yet recorded: only retire the current rotation's
				// ApplyDesire once its backup has succeeded.
				readDesire, err := c.readDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, applyDesire.ResourceID.Name)
				if err != nil {
					if cosmosstorageutils.IsNotFoundError(err) {
						continue
					}
					return false, err
				}
				phase, err := observedVeleroBackupPhase(readDesire.Status.KubeContent)
				if err != nil {
					return false, utils.TrackError(fmt.Errorf("failed to determine observed Backup phase for %s: %w", applyDesire.ResourceID.Name, err))
				}
				if phase != velerov1.BackupPhaseCompleted {
					// Keep unsuccessful backups until a future rotation supersedes them.
					continue
				}
				// Record completion before purging so a crash cannot cause the desire to be recreated.
				logger.Info("recording key rotation backup fingerprint",
					"desire", applyDesire.ResourceID.Name, "fingerprint", kmsKeyFingerprint, "phase", phase)
				return c.recordKeyRotationBackupFingerprint(ctx, key, cachedServiceProviderCluster, kmsKeyFingerprint)
			}
		}

		logger.Info("purging on-demand backup ApplyDesire", "desire", applyDesire.ResourceID.Name, "reason", reason)
		// Purge the Cosmos document directly, leaving the Velero Backup to expire at its TTL.
		if err := purgeApplyDesire(ctx, *applyDesire, applyDesireCRUD); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func (c *keyRotationBackupSyncer) recordKeyRotationBackupFingerprint(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	cachedServiceProviderCluster *coreapi.ServiceProviderCluster,
	kmsKeyFingerprint string,
) (bool, error) {
	replacement := cachedServiceProviderCluster.DeepCopy()
	replacement.Status.KeyRotationBackupFingerprint = kmsKeyFingerprint

	serviceProviderClusterCRUD := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if _, err := serviceProviderClusterCRUD.Replace(ctx, replacement, nil); err != nil {
		// another writer already updated the document; the informer will retrigger a sync with the fresh cache.
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return false, nil
		}
		return false, utils.TrackError(fmt.Errorf("failed to record key rotation backup fingerprint: %w", err))
	}
	return true, nil
}

// deleteStaleOnDemandReadDesires removes on-demand key-rotation ReadDesires after
// their Velero Backups have been garbage-collected.
func (c *keyRotationBackupSyncer) deleteStaleOnDemandReadDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	readDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
	cachedServiceProviderCluster *coreapi.ServiceProviderCluster,
	kmsKeyFingerprint string,
	currentDesireName string,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	readDesires, err := c.readDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}
	for _, readDesire := range readDesires {
		if _, ok := readDesire.Tags[backup.DesireTagKeyOndemandBackup]; !ok {
			continue
		}

		// ApplyDesires are removed when backups complete, before Velero's TTL cleanup.
		// A successful empty read confirms that the Backup is now gone.
		backupGarbageCollected := readDesire.Status.KubeContent == nil && isDesireSuccessful(readDesire.Status.Conditions)

		isCurrent := readDesire.ResourceID.Name == currentDesireName
		reason := "stale: superseded rotation, Backup GC'd by Velero at TTL"
		if isCurrent {
			reason = "current: Backup GC'd by Velero at TTL"
			// Keep the current desire until its completion is durably recorded.
			fingerprintRecorded := cachedServiceProviderCluster.Status.KeyRotationBackupFingerprint == kmsKeyFingerprint
			if !fingerprintRecorded || !backupGarbageCollected {
				continue
			}
		} else if !backupGarbageCollected {
			continue
		}

		name := readDesire.ResourceID.Name
		logger.Info("deleting stale on-demand backup ReadDesire", "desire", name, "reason", reason)
		if err := readDesireCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("failed to delete stale ReadDesire %s: %w", name, err))
		}
		return true, nil
	}

	return false, nil
}

func keyRotationBackupName(hostedClusterNamespace, kmsKeyFingerprint string) string {
	return hostedClusterNamespace + keyRotationBackupNameSeparator + kmsKeyFingerprint
}

func keyRotationDesireName(backupName string) string {
	return backup.OndemandBackupDesireNamePrefix + backupName
}

func buildOnDemandBackupApplyDesire(
	subscriptionID, resourceGroupName, clusterName string,
	managementClusterResourceID *azcorearm.ResourceID,
	veleroBackup *velerov1.Backup,
) (*kubeapplierapi.ApplyDesire, error) {
	desireName := keyRotationDesireName(veleroBackup.Name)
	resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ApplyDesire resource ID for backup %s: %w", veleroBackup.Name, err)
	}

	raw, err := json.Marshal(veleroBackup)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal backup %s: %w", veleroBackup.Name, err)
	}

	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementClusterResourceID.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementClusterResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplierapi.ResourceReference{
				Group:     backup.VeleroGroup,
				Version:   backup.VeleroVersion,
				Resource:  backup.VeleroBackupResource,
				Namespace: backup.VeleroNamespace,
				Name:      veleroBackup.Name,
			},
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: raw},
			},
		},
		Tags: map[string]string{
			backup.DesireTagKeyOndemandBackup: "",
			kubeapplierapi.TagControllerName:  KeyRotationBackupControllerName,
		},
	}, nil
}

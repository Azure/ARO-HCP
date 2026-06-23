// Copyright 2025 Microsoft Corporation
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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// ConditionTypeSystemAdminCredentialContentDeleted is the condition
	// type set on ServiceProviderCluster when all credential-related
	// content has been cleaned up during cluster deletion.
	ConditionTypeSystemAdminCredentialContentDeleted = "SystemAdminCredentialContentDeleted"
)

// clusterDeletionCleanupSyncer ensures all credential-related
// ApplyDesires, ReadDesires, and DeleteDesires are torn down from the
// management cluster during cluster deletion. Once all content is
// gone, it deletes the SystemAdminCredential docs and sets the
// SystemAdminCredentialContentDeleted condition on ServiceProviderCluster.
type clusterDeletionCleanupSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
}

var _ controllerutils.ClusterSyncer = (*clusterDeletionCleanupSyncer)(nil)

// NewClusterDeletionCleanupController wires the cluster-deletion
// cleanup gate as a cluster-watching controller.
func NewClusterDeletionCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &clusterDeletionCleanupSyncer{
		cooldownChecker:      controllerutil.NewTimeBasedCooldownChecker(30 * time.Second),
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialClusterDeletionCleanup",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *clusterDeletionCleanupSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterDeletionCleanupSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}

	// Only act when cluster deletion is in progress
	if cluster.ServiceProviderProperties.DeletionTimestamp == nil {
		return nil
	}
	if cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil {
		return nil
	}

	// Check if already done
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	for _, cond := range spc.Status.Conditions {
		if cond.Type == ConditionTypeSystemAdminCredentialContentDeleted && cond.Status == metav1.ConditionTrue {
			return nil // already done
		}
	}

	mcResourceID := spc.Status.ManagementClusterResourceID

	// List all credentials and drive teardown of outstanding desires
	credCRUD := c.resourcesDBClient.SystemAdminCredentials(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials: %w", err))
	}

	allDesiresDone := true
	for _, cred := range iter.Items(ctx) {
		if len(cred.Status.OutstandingDesires) == 0 {
			continue
		}
		if mcResourceID == nil {
			// Can't tear down MC content without an MC
			allDesiresDone = false
			continue
		}

		kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
		if kaClient == nil {
			allDesiresDone = false
			continue
		}

		replacement := cred.DeepCopy()
		err := driveDesireTeardown(ctx, kaClient, key, replacement)
		if err != nil {
			return utils.TrackError(err)
		}

		if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace SystemAdminCredential: %w", err))
		}

		if len(replacement.Status.OutstandingDesires) > 0 {
			allDesiresDone = false
		}
	}

	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating SystemAdminCredentials: %w", err))
	}

	if !allDesiresDone {
		return nil // wait for next reconcile
	}

	// All desires are gone. Delete all SystemAdminCredential docs.
	deleteIter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials for deletion: %w", err))
	}

	for _, cred := range deleteIter.Items(ctx) {
		credName := cred.GetResourceID().Name
		logger.Info("deleting SystemAdminCredential during cluster deletion", "credentialName", credName)
		if err := credCRUD.Delete(ctx, credName); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete SystemAdminCredential %q: %w", credName, err))
		}
	}
	if err := deleteIter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating SystemAdminCredentials for deletion: %w", err))
	}

	// Also clean up the serving CA ReadDesire
	if mcResourceID != nil {
		kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
		if kaClient != nil {
			readDesireCRUD, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
			if err == nil {
				_ = deleteReadDesireIfExists(ctx, readDesireCRUD, "systemAdminCredentialServingCA")
			}
		}
	}

	// Set the condition
	logger.Info("all SystemAdminCredential content deleted, setting condition")
	spcReplacement := spc.DeepCopy()
	setCondition(&spcReplacement.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeSystemAdminCredentialContentDeleted,
		Status:             metav1.ConditionTrue,
		Reason:             "ContentDeleted",
		Message:            "All SystemAdminCredential content has been cleaned up",
		LastTransitionTime: metav1.Now(),
	})

	spcCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if _, err := spcCRUD.Replace(ctx, spcReplacement, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to set condition on ServiceProviderCluster: %w", err))
	}

	return nil
}

// setCondition sets a condition in the conditions slice, replacing
// an existing condition of the same type if present.
func setCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	for i, existing := range *conditions {
		if existing.Type == condition.Type {
			(*conditions)[i] = condition
			return
		}
	}
	*conditions = append(*conditions, condition)
}

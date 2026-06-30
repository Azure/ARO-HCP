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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterDeletionCleanup struct {
	cooldownChecker              controllerutil.CooldownChecker
	clusterLister                listers.ClusterLister
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.CredentialRequestSyncer = (*clusterDeletionCleanup)(nil)

// NewClusterDeletionCleanupController returns a CredentialRequestWatchingController
// that is the precondition gate for cluster deletion. When a cluster is
// being deleted, it:
//  1. Walks credential-related desires in the kube-applier DB and issues
//     DeleteDesires for each ApplyDesire (so the kube-applier removes
//     MC-side objects).
//  2. Waits for every DeleteDesire to succeed.
//  3. Removes all credential-related Cosmos docs (ApplyDesires, ReadDesires,
//     DeleteDesires, and the SystemAdminCredentialRequest docs themselves).
//  4. Sets SystemAdminCredentialContentDeleted=True on ServiceProviderCluster
//     so the cluster-deletion finalizer can advance.
//
// This controller fires on credential request events. When the cluster is
// being deleted, each credential request event triggers a full cluster-wide
// cleanup pass, which is idempotent.
func NewClusterDeletionCleanupController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &clusterDeletionCleanup{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:                clusterLister,
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewCredentialRequestWatchingController(
		"SystemAdminCredentialClusterDeletionCleanup",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *clusterDeletionCleanup) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterDeletionCleanup) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Only run during cluster deletion — both deletion and CS-side
	// deletion must be confirmed.
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if cachedCluster.ServiceProviderProperties.DeletionTimestamp == nil ||
		cachedCluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil {
		return nil
	}

	// Check if we already set the condition.
	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// Already done — no-op.
	for _, cond := range spc.Status.Validations {
		if cond.Type == "SystemAdminCredentialContentDeleted" && cond.Status == "True" {
			return nil
		}
	}

	mcResourceID := spc.Status.ManagementClusterResourceID

	// Step 1: Drive desire teardown via kube-applier DB.
	var hasOutstanding bool
	if mcResourceID != nil {
		kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
		if kaClient != nil {
			outstanding, err := c.driveDesireTeardown(ctx, key, kaClient)
			if err != nil {
				return err
			}
			if outstanding {
				hasOutstanding = true
			}
		} else {
			hasOutstanding = true
		}
	}

	if hasOutstanding {
		logger.Info("credential desires still outstanding, waiting")
		return nil
	}

	// Step 2: Delete all credential docs.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentialRequests for deletion: %w", err))
	}
	for _, cred := range iter.Items(ctx) {
		credName := cred.ResourceID.Name
		if err := credCRUD.Delete(ctx, credName); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete credential %s: %w", credName, err))
		}
		logger.Info("deleted credential during cluster deletion", "credential", credName)
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate SystemAdminCredentialRequests for deletion: %w", err))
	}

	// Step 3: Set the condition on ServiceProviderCluster.
	// Re-read SPC to avoid stale writes.
	spc, err = c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	replacement := spc.DeepCopy()
	conditionSet := false
	for i := range replacement.Status.Validations {
		if replacement.Status.Validations[i].Type == "SystemAdminCredentialContentDeleted" {
			replacement.Status.Validations[i].Status = "True"
			conditionSet = true
			break
		}
	}
	if !conditionSet {
		replacement.Status.Validations = append(replacement.Status.Validations, api.SystemAdminCredentialContentDeletedCondition())
	}

	if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); err != nil {
		if database.IsPreconditionFailedError(err) {
			// Will be retriggered by the informer.
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to set SystemAdminCredentialContentDeleted condition: %w", err))
	}

	logger.Info("SystemAdminCredentialContentDeleted condition set")
	return nil
}

// driveDesireTeardown walks credential-related desires in the kube-applier DB:
// - For Apply desires: creates a DeleteDesire, waits for success, then removes both.
// - For Read desires: deletes directly.
// Returns true if there are still outstanding desires.
func (c *clusterDeletionCleanup) driveDesireTeardown(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	kaClient database.KubeApplierDBClient,
) (bool, error) {
	applyCRUD, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, utils.TrackError(err)
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, utils.TrackError(err)
	}
	deleteCRUD, err := kaClient.DeleteDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, utils.TrackError(err)
	}

	hasOutstanding := false

	// Process apply desires matching credential prefix.
	applyIter, err := applyCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list ApplyDesires: %w", err))
	}
	for _, desire := range applyIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !strings.HasPrefix(strings.ToLower(desireName), strings.ToLower(credentialDesirePrefix)) {
			continue
		}
		removed, err := c.removeApplyDesireDuringDeletion(ctx, key, desireName, applyCRUD, deleteCRUD)
		if err != nil {
			return false, err
		}
		if !removed {
			hasOutstanding = true
		}
	}
	if err := applyIter.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("iterate ApplyDesires: %w", err))
	}

	// Process read desires matching credential prefix.
	readIter, err := readCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list ReadDesires: %w", err))
	}
	for _, desire := range readIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !strings.HasPrefix(strings.ToLower(desireName), strings.ToLower(credentialDesirePrefix)) {
			continue
		}
		if err := readCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("delete ReadDesire %s: %w", desireName, err))
		}
	}
	if err := readIter.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("iterate ReadDesires: %w", err))
	}

	// Clean up any remaining delete desires that have completed.
	deleteIter, err := deleteCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list DeleteDesires: %w", err))
	}
	for _, desire := range deleteIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !strings.HasPrefix(strings.ToLower(desireName), strings.ToLower(credentialDesirePrefix)) {
			continue
		}
		if err := deleteCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("delete DeleteDesire %s: %w", desireName, err))
		}
	}
	if err := deleteIter.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("iterate DeleteDesires: %w", err))
	}

	return hasOutstanding, nil
}

func (c *clusterDeletionCleanup) removeApplyDesireDuringDeletion(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	desireName string,
	applyCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	deleteCRUD database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
) (bool, error) {
	// Get the ApplyDesire to extract TargetItem for DeleteDesire creation.
	applyDesire, err := applyCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		// Already gone.
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", desireName, err))
	}

	// Issue a DeleteDesire.
	deleteResourceIDStr := kubeapplier.ToClusterScopedDeleteDesireResourceIDString(
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName)
	deleteResourceID, _ := azcorearm.ParseResourceID(deleteResourceIDStr)

	deleteDesire := &kubeapplier.DeleteDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   deleteResourceID,
			PartitionKey: applyDesire.PartitionKey,
		},
		Spec: kubeapplier.DeleteDesireSpec{
			ManagementCluster: applyDesire.Spec.ManagementCluster,
			TargetItem:        applyDesire.Spec.TargetItem,
		},
	}
	if _, err := deleteCRUD.Create(ctx, deleteDesire, nil); err != nil && !database.IsConflictError(err) {
		return false, utils.TrackError(fmt.Errorf("create DeleteDesire %s: %w", desireName, err))
	}

	// Check if DeleteDesire succeeded.
	existingDelete, err := deleteCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(err)
	}

	for _, cond := range existingDelete.Status.Conditions {
		if cond.Type == "Successful" && cond.Status == "True" {
			// Clean up both.
			if err := applyCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desireName, err))
			}
			if err := deleteCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete DeleteDesire %s: %w", desireName, err))
			}
			return true, nil
		}
	}
	// Not yet successful.
	return false, nil
}

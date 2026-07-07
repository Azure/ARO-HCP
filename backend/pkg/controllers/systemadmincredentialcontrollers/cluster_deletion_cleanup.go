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
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.CredentialRequestSyncer = (*clusterDeletionCleanup)(nil)

// NewClusterDeletionCleanupController returns a CredentialRequestWatchingController
// that deletes SystemAdminCredentialRequest resources. When
// SystemAdminCredentialRequest.Status.DeleteTimestamp is set, this controller:
//
//  1. Deletes the ApplyDesires it created for this credential request.
//  2. Creates DeleteDesires for all the ApplyDesires it created before.
//  3. Checks all DeleteDesires for them to have successfully deleted.
//  4. Deletes all the DeleteDesires and ReadDesires.
//
// Once all desires are cleaned up, the controller deletes the credential
// request document itself and sets SystemAdminCredentialContentDeleted=True
// on ServiceProviderCluster so the cluster-deletion finalizer can advance.
func NewClusterDeletionCleanupController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &clusterDeletionCleanup{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
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

	// Only run when the SystemAdminCredentialRequest has a DeleteTimestamp set.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}
	if cred.Status.DeleteTimestamp == nil {
		return nil
	}

	// Check if we already set the condition on ServiceProviderCluster.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID

	// Drive desire teardown via kube-applier DB.
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

	// All desires cleaned up — delete the credential request document.
	if err := credCRUD.Delete(ctx, key.CredentialName); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete credential %s: %w", key.CredentialName, err))
	}
	logger.Info("deleted credential during cleanup", "credential", key.CredentialName)

	// Check if there are any remaining credential requests for this cluster.
	// Only set the condition when all are gone.
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentialRequests: %w", err))
	}
	hasRemaining := false
	for range iter.Items(ctx) {
		hasRemaining = true
		break
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate SystemAdminCredentialRequests: %w", err))
	}
	if hasRemaining {
		// Other credential requests still exist; don't set the cluster-level condition yet.
		return nil
	}

	// All credential requests deleted — set condition on ServiceProviderCluster.
	// Re-read SPC to avoid stale writes.
	serviceProviderCluster, err = c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// Already done — no-op.
	for _, cond := range serviceProviderCluster.Status.Validations {
		if cond.Type == "SystemAdminCredentialContentDeleted" && cond.Status == "True" {
			return nil
		}
	}

	replacement := serviceProviderCluster.DeepCopy()
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

// driveDesireTeardown implements the 4-step teardown for a single credential request:
//  1. For Apply desires matching the credential prefix: create a DeleteDesire.
//  2. For Apply desires with a completed DeleteDesire: delete the ApplyDesire.
//  3. For Delete desires that have succeeded: delete them.
//  4. For Read desires matching the credential prefix: delete directly.
//
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

	// Step 1 & 2: Process apply desires matching credential prefix.
	// For each ApplyDesire: create a DeleteDesire, wait for it to succeed, then delete both.
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

	// Step 3: Clean up any remaining delete desires that have completed.
	deleteIter, err := deleteCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list DeleteDesires: %w", err))
	}
	for _, desire := range deleteIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !strings.HasPrefix(strings.ToLower(desireName), strings.ToLower(credentialDesirePrefix)) {
			continue
		}
		// Only delete completed DeleteDesires.
		isSuccessful := false
		for _, cond := range desire.Status.Conditions {
			if cond.Type == "Successful" && cond.Status == "True" {
				isSuccessful = true
				break
			}
		}
		if isSuccessful {
			if err := deleteCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete DeleteDesire %s: %w", desireName, err))
			}
		} else {
			hasOutstanding = true
		}
	}
	if err := deleteIter.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("iterate DeleteDesires: %w", err))
	}

	// Step 4: Delete read desires matching credential prefix.
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

	// Step 1: Create a DeleteDesire for this ApplyDesire.
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

	// Step 2: Check if DeleteDesire succeeded.
	existingDelete, err := deleteCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(err)
	}

	for _, cond := range existingDelete.Status.Conditions {
		if cond.Type == "Successful" && cond.Status == "True" {
			// Delete succeeded — remove the ApplyDesire.
			if err := applyCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desireName, err))
			}
			return true, nil
		}
	}
	// Not yet successful.
	return false, nil
}

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

type postIssuanceCleanup struct {
	cooldownChecker              controllerutil.CooldownChecker
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.CredentialRequestSyncer = (*postIssuanceCleanup)(nil)

// NewPostIssuanceCleanupController returns a CredentialRequestWatchingController
// that eagerly tears down per-credential CSR/CSRA/RBAC ApplyDesires and
// ReadDesires once an individual credential reaches Issued or Failed condition,
// freeing MC resources.
func NewPostIssuanceCleanupController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &postIssuanceCleanup{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewCredentialRequestWatchingController(
		"SystemAdminCredentialPostIssuanceCleanup",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *postIssuanceCleanup) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *postIssuanceCleanup) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Get the management cluster resource ID.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}

	// Get the specific credential request.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}

	// Only process credentials that are Issued or Failed.
	if !cred.Status.IsIssued() && !cred.Status.IsFailed() {
		return nil
	}

	if err := c.cleanupDesires(ctx, key, cred, kaClient); err != nil {
		return err
	}

	logger.Info("post-issuance cleanup processed", "credential", key.CredentialName)
	return nil
}

func (c *postIssuanceCleanup) cleanupDesires(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	cred *api.SystemAdminCredentialRequest,
	kaClient database.KubeApplierDBClient,
) error {
	logger := utils.LoggerFromContext(ctx)
	credName := cred.ResourceID.Name

	// List all apply desires for this cluster and filter by credential name prefix.
	applyCRUD, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	deleteCRUD, err := kaClient.DeleteDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}

	// Find apply desires matching this credential by name pattern.
	applyIter, err := applyCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires: %w", err))
	}
	var hasOutstanding bool
	for _, desire := range applyIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isCredentialDesire(desireName, credName) {
			continue
		}
		removed, err := c.removeApplyDesire(ctx, key, desireName, applyCRUD, deleteCRUD)
		if err != nil {
			return err
		}
		if !removed {
			hasOutstanding = true
		}
	}
	if err := applyIter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("iterate ApplyDesires: %w", err))
	}

	// Find read desires matching this credential.
	readIter, err := readCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ReadDesires: %w", err))
	}
	for _, desire := range readIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isCredentialDesire(desireName, credName) {
			continue
		}
		if err := readCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete ReadDesire %s: %w", desireName, err))
		}
	}
	if err := readIter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("iterate ReadDesires: %w", err))
	}

	if !hasOutstanding {
		logger.Info("post-issuance cleanup complete", "credential", credName)
	}

	return nil
}

func (c *postIssuanceCleanup) removeApplyDesire(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	desireName string,
	applyCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	deleteCRUD database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
) (bool, error) {
	// For ApplyDesires, issue a DeleteDesire to remove the MC-side object,
	// then wait for it to confirm, then remove both.
	if err := c.ensureDeleteDesire(ctx, key, desireName, applyCRUD, deleteCRUD); err != nil {
		return false, err
	}

	// Check if the DeleteDesire has completed.
	deleteDesire, err := deleteCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		// DeleteDesire doesn't exist yet; we'll create it next time.
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(err)
	}

	// Check if the DeleteDesire has succeeded.
	for _, cond := range deleteDesire.Status.Conditions {
		if cond.Type == "Successful" && cond.Status == "True" {
			// Clean up: delete both the ApplyDesire and DeleteDesire.
			if err := applyCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desireName, err))
			}
			if err := deleteCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete DeleteDesire %s: %w", desireName, err))
			}
			return true, nil
		}
	}
	// DeleteDesire not yet successful; wait.
	return false, nil
}

func (c *postIssuanceCleanup) ensureDeleteDesire(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	applyDesireName string,
	applyCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	deleteCRUD database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
) error {
	// Get the ApplyDesire to copy its TargetItem.
	applyDesire, err := applyCRUD.Get(ctx, strings.ToLower(applyDesireName))
	if database.IsNotFoundError(err) {
		// ApplyDesire already gone.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", applyDesireName, err))
	}

	deleteResourceIDStr := kubeapplier.ToClusterScopedDeleteDesireResourceIDString(
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, applyDesireName)
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
		return utils.TrackError(fmt.Errorf("create DeleteDesire %s: %w", applyDesireName, err))
	}
	return nil
}

// isCredentialDesire returns true if the desire name contains the credential
// name as a suffix component (e.g. "systemAdminCredentialCSR-<credName>").
func isCredentialDesire(desireName, credName string) bool {
	return strings.Contains(strings.ToLower(desireName), strings.ToLower(credName))
}

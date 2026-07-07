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

type credentialRequestDeletion struct {
	cooldownChecker              controllerutil.CooldownChecker
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.CredentialRequestSyncer = (*credentialRequestDeletion)(nil)

// NewClusterDeletionCleanupController returns a CredentialRequestWatchingController
// that deletes a single SystemAdminCredentialRequest resource. It fires on every
// SystemAdminCredentialRequest change and only does work once that request's
// Status.DeleteTimestamp is set. For that one request it:
//
//  1. Flips each ApplyDesire it created before to Type=Delete so the
//     kube-applier tears down the applied object.
//  2. Waits for those Delete desires to report success, then removes them.
//  3. Deletes the ReadDesires belonging to this credential request.
//  4. Deletes the SystemAdminCredentialRequest document itself.
//
// The controller is deliberately scoped to the single credential request named
// by the key — it never touches desires or documents belonging to other
// credential requests on the same cluster.
func NewClusterDeletionCleanupController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &credentialRequestDeletion{
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

func (c *credentialRequestDeletion) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// needsWork reports whether deletion has been requested for this credential
// request. The controller only acts once Status.DeleteTimestamp is set.
func (c *credentialRequestDeletion) needsWork(cred *api.SystemAdminCredentialRequest) bool {
	return cred.Status.DeleteTimestamp != nil
}

func (c *credentialRequestDeletion) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}
	if !c.needsWork(cred) {
		return nil
	}

	// The management cluster resource ID tells us which kube-applier partition
	// holds this credential's desires.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		logger.Info("waiting to tear down credential desires: ServiceProviderCluster has no ManagementClusterResourceID yet",
			"credential", key.CredentialName)
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		logger.Info("waiting to tear down credential desires: no kube-applier client for management cluster",
			"credential", key.CredentialName, "managementCluster", mcResourceID.String())
		return nil
	}

	hasOutstanding, err := c.driveDesireTeardown(ctx, key, kaClient)
	if err != nil {
		return err
	}
	if hasOutstanding {
		logger.Info("waiting for management-cluster teardown of this credential's desires (ApplyDesires flipped to Delete, then removed along with ReadDesires)",
			"credential", key.CredentialName, "managementCluster", mcResourceID.String())
		return nil
	}

	// All of this credential request's desires are gone — delete the document.
	if err := credCRUD.Delete(ctx, key.CredentialName); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete credential %s: %w", key.CredentialName, err))
	}
	logger.Info("deleted credential request after tearing down its desires", "credential", key.CredentialName)
	return nil
}

// driveDesireTeardown implements the teardown for the single credential
// request named by key:
//  1. For this credential's ApplyDesires: flip each to Type=Delete so the
//     kube-applier tears down the applied object, then remove the desire once
//     the delete has succeeded.
//  2. For this credential's ReadDesires: delete directly.
//
// Only desires that belong to this credential request (matched by name) are
// touched. Returns true if there are still outstanding desires.
func (c *credentialRequestDeletion) driveDesireTeardown(
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

	hasOutstanding := false

	// Step 1: process only this credential request's apply desires. Each
	// ApplyDesire is flipped to Type=Delete, and once the delete succeeds the
	// desire document is removed.
	applyIter, err := applyCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list ApplyDesires: %w", err))
	}
	for _, desire := range applyIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isCredentialDesire(desireName, key.CredentialName) {
			continue
		}
		removed, err := c.removeApplyDesireDuringDeletion(ctx, desireName, applyCRUD)
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

	// Step 2: delete this credential request's read desires.
	readIter, err := readCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list ReadDesires: %w", err))
	}
	for _, desire := range readIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isCredentialDesire(desireName, key.CredentialName) {
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

// removeApplyDesireDuringDeletion tears down a single ApplyDesire by converting
// it to a Type=Delete desire (so the kube-applier deletes spec.targetItem from
// the management cluster) and, once that delete reports success, removing the
// desire document. It returns true once the ApplyDesire is gone.
func (c *credentialRequestDeletion) removeApplyDesireDuringDeletion(
	ctx context.Context,
	desireName string,
	applyCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
) (bool, error) {
	applyDesire, err := applyCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		// Already gone.
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", desireName, err))
	}

	// Step 1: if the desire is still a ServerSideApply, flip it to a Delete so
	// the kube-applier tears down the applied object. TargetItem already names
	// what to delete; the ServerSideApply payload is cleared.
	if applyDesire.Spec.Type != kubeapplier.ApplyDesireTypeDelete {
		applyDesire.Spec.Type = kubeapplier.ApplyDesireTypeDelete
		applyDesire.Spec.ServerSideApply = nil
		if _, err := applyCRUD.Replace(ctx, applyDesire, nil); err != nil && !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("convert ApplyDesire %s to Delete: %w", desireName, err))
		}
		return false, nil
	}

	// Step 2: the desire is a Delete — once it reports success, remove the document.
	for _, cond := range applyDesire.Status.Conditions {
		if cond.Type == "Successful" && cond.Status == "True" {
			if err := applyCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desireName, err))
			}
			return true, nil
		}
	}
	// Delete not yet successful; wait.
	return false, nil
}

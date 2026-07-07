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
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type revocationDeletion struct {
	cooldownChecker              controllerutil.CooldownChecker
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.RevocationSyncer = (*revocationDeletion)(nil)

// NewRevocationDeletionController returns a RevocationWatchingController that runs
// once a revocation has been marked for deletion (Status.DeleteTimestamp set). It
// tears down the revocation's desires (CRR ApplyDesire/ReadDesire and the CRR
// RBAC) by flipping each ApplyDesire to Type=Delete and, when they are all gone,
// deletes the SystemAdminCredentialRevocation document. The RevokeCredentials
// operation completes once the document no longer exists.
func NewRevocationDeletionController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &revocationDeletion{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewRevocationWatchingController(
		"SystemAdminCredentialRevocationDeletion",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *revocationDeletion) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *revocationDeletion) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.SystemAdminCredentialRevocations(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.RevocationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// Only tear down once the revocation has been marked for deletion.
	if revocation.Status.DeleteTimestamp == nil {
		return nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(err)
	}

	// If the ServiceProviderCluster (and thus the management cluster mapping) is
	// still present, tear the revocation's desires down before removing the doc.
	if err == nil && serviceProviderCluster.Status.ManagementClusterResourceID != nil {
		mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
		kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
		if kaClient == nil {
			logger.Info("waiting for kube-applier client for management cluster before tearing down revocation desires",
				"managementCluster", mcResourceID.String())
			return nil
		}
		hasOutstanding, err := c.driveDesireTeardown(ctx, key, revocation.Spec.RevokeOpSuffix, kaClient)
		if err != nil {
			return err
		}
		if hasOutstanding {
			logger.Info("waiting for management-cluster teardown of the revocation's desires (CRR and RBAC ApplyDesires flipped to Delete, then removed along with ReadDesires)",
				"revokeOpSuffix", revocation.Spec.RevokeOpSuffix, "managementCluster", mcResourceID.String())
			return nil
		}
	}

	// All revocation desires are gone (or the cluster is already gone) — delete
	// the revocation document. Its disappearance completes the operation.
	if err := revocationCRUD.Delete(ctx, key.RevocationName); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete SystemAdminCredentialRevocation: %w", err))
	}
	logger.Info("deleted revocation after tearing down its desires", "revokeOpSuffix", revocation.Spec.RevokeOpSuffix)
	return nil
}

// driveDesireTeardown removes every desire that belongs to this revocation
// (matched by the revocation suffix): each ApplyDesire is flipped to Type=Delete
// so the kube-applier tears down the applied object and, once the delete
// succeeds, the desire is removed; the ReadDesires are then deleted. It returns
// true while any of the revocation's desires remain.
func (c *revocationDeletion) driveDesireTeardown(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRevocationKey,
	suffix string,
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

	// Step 1: for each of the revocation's apply desires, flip it to Type=Delete
	// and, once the delete succeeds, remove the desire document.
	applyIter, err := applyCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list ApplyDesires: %w", err))
	}
	for _, desire := range applyIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isRevocationDesire(desireName, suffix) {
			continue
		}
		removed, err := c.removeApplyDesire(ctx, desireName, applyCRUD)
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

	// Step 2: delete the revocation's read desires.
	readIter, err := readCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list ReadDesires: %w", err))
	}
	for _, desire := range readIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isRevocationDesire(desireName, suffix) {
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

// removeApplyDesire tears down a single ApplyDesire by converting it to a
// Type=Delete desire (so the kube-applier deletes spec.targetItem from the
// management cluster) and, once that delete reports success, removing the
// desire document. It returns true once the ApplyDesire is gone.
func (c *revocationDeletion) removeApplyDesire(
	ctx context.Context,
	desireName string,
	applyCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
) (bool, error) {
	applyDesire, err := applyCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", desireName, err))
	}

	// If the desire is still a ServerSideApply, flip it to a Delete so the
	// kube-applier tears down the applied object.
	if applyDesire.Spec.Type != kubeapplier.ApplyDesireTypeDelete {
		applyDesire.Spec.Type = kubeapplier.ApplyDesireTypeDelete
		applyDesire.Spec.ServerSideApply = nil
		if _, err := applyCRUD.Replace(ctx, applyDesire, nil); err != nil && !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("convert ApplyDesire %s to Delete: %w", desireName, err))
		}
		return false, nil
	}

	// The desire is a Delete — remove the document once the delete has succeeded.
	for _, cond := range applyDesire.Status.Conditions {
		if cond.Type == "Successful" && cond.Status == "True" {
			if err := applyCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desireName, err))
			}
			return true, nil
		}
	}
	return false, nil
}

// isRevocationDesire reports whether a desire name belongs to the revocation
// identified by suffix. Every revocation desire (CRR ApplyDesire/ReadDesire and
// the CRR RBAC ApplyDesires) is named with the revocation's unique suffix.
func isRevocationDesire(desireName, suffix string) bool {
	return strings.Contains(strings.ToLower(desireName), strings.ToLower(suffix))
}

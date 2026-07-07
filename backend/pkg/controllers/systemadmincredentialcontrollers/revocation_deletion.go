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
// RBAC) using DeleteDesires and, when they are all gone, deletes the
// SystemAdminCredentialRevocation document. The RevokeCredentials operation
// completes once the document no longer exists.
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
			logger.Info("waiting for management-cluster teardown of the revocation's desires (CRR and RBAC ApplyDesires deleted via DeleteDesires, then DeleteDesires and ReadDesires removed)",
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
// (matched by the revocation suffix): each ApplyDesire is deleted via a
// DeleteDesire, then the DeleteDesires and ReadDesires are removed. It returns
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
	deleteCRUD, err := kaClient.DeleteDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, utils.TrackError(err)
	}

	hasOutstanding := false

	// Step 1 & 2: for each of the revocation's apply desires, create a
	// DeleteDesire, wait for it to succeed, then delete the ApplyDesire.
	applyIter, err := applyCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list ApplyDesires: %w", err))
	}
	for _, desire := range applyIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isRevocationDesire(desireName, suffix) {
			continue
		}
		removed, err := c.removeApplyDesire(ctx, key, desireName, applyCRUD, deleteCRUD)
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

	// Step 3: clean up the revocation's completed delete desires.
	deleteIter, err := deleteCRUD.List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("list DeleteDesires: %w", err))
	}
	for _, desire := range deleteIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !isRevocationDesire(desireName, suffix) {
			continue
		}
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

	// Step 4: delete the revocation's read desires.
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

func (c *revocationDeletion) removeApplyDesire(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRevocationKey,
	desireName string,
	applyCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	deleteCRUD database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire],
) (bool, error) {
	applyDesire, err := applyCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", desireName, err))
	}

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

	existingDelete, err := deleteCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(err)
	}
	for _, cond := range existingDelete.Status.Conditions {
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

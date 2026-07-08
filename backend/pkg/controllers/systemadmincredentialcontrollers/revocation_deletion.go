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
// RBAC) via the shared deleteDesires helper and, when they are all gone, deletes
// the SystemAdminCredentialRevocation document. The RevokeCredentials operation
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
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return utils.TrackError(fmt.Errorf("ServiceProviderCluster for cluster %q has no ManagementClusterResourceID", key.HCPClusterName))
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		logger.Info("waiting for kube-applier client for management cluster before tearing down revocation desires",
			"managementCluster", mcResourceID.String())
		return nil
	}

	suffix := revocation.Spec.RevokeOpSuffix
	waitingFor, err := deleteDesires(ctx, kubeApplierClient, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		func(desireName string) bool { return isRevocationDesire(desireName, suffix) })
	if err != nil {
		return err
	}
	if len(waitingFor) > 0 {
		logger.Info("waiting for management-cluster teardown of the revocation's desires",
			"revokeOpSuffix", suffix, "managementCluster", mcResourceID.String(),
			"waitingFor", strings.Join(waitingFor, "; "))
		return nil
	}

	// All revocation desires are gone — delete the revocation document. Its
	// disappearance completes the operation.
	if err := revocationCRUD.Delete(ctx, key.RevocationName); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete SystemAdminCredentialRevocation: %w", err))
	}
	logger.Info("deleted revocation after tearing down its desires", "revokeOpSuffix", suffix)
	return nil
}

// isRevocationDesire reports whether a desire name belongs to the revocation
// identified by suffix. Every revocation desire (CRR ApplyDesire/ReadDesire and
// the CRR RBAC ApplyDesires) is named with the revocation's unique suffix.
func isRevocationDesire(desireName, suffix string) bool {
	return strings.Contains(strings.ToLower(desireName), strings.ToLower(suffix))
}

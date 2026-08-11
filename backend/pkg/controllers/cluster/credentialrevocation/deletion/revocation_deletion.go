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

package deletion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type revocationDeletion struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.SystemAdminCredentialRevocationSyncer = (*revocationDeletion)(nil)

// NewRevocationDeletionController returns a RevocationWatchingController that runs
// once a revocation has been marked for deletion (Status.DeletionTimestamp set). It
// tears down the revocation's desires (CRR ApplyDesire/ReadDesire and the CRR
// RBAC) via the shared deleteDesires helper and, when they are all gone, deletes
// the SystemAdminCredentialRevocation document. The RevokeCredentials operation
// completes once the document no longer exists.
func NewRevocationDeletionController(
	activeOperationLister corelisters.ActiveOperationLister,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	backendInformers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &revocationDeletion{
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewSystemAdminCredentialRevocationWatchingController(
		"SystemAdminCredentialRevocationDeletion",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *revocationDeletion) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRevocations(key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.RevocationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// Only tear down once the revocation has been marked for deletion.
	if revocation.Status.DeletionTimestamp == nil {
		return nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
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

	waitingFor, err := kubeapplierhelpers.DeleteAllChildDesires(ctx, kubeApplierClient, kubeapplierhelpers.RevocationDesireParent(key.RevocationName),
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return err
	}
	if len(waitingFor) > 0 {
		logger.Info("waiting for management-cluster teardown of the revocation's desires",
			"managementCluster", mcResourceID.String(),
			"waitingFor", strings.Join(waitingFor, "; "))
		return nil
	}

	// All revocation desires are gone — delete the revocation document. Its
	// disappearance completes the operation.
	if err := revocationCRUD.Delete(ctx, key.RevocationName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete SystemAdminCredentialRevocation: %w", err))
	}
	logger.Info("deleted revocation after tearing down its desires")
	return nil
}

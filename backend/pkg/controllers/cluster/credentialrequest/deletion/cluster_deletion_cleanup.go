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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type credentialRequestDeletion struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.SystemAdminCredentialRequestSyncer = (*credentialRequestDeletion)(nil)

// NewClusterDeletionCleanupController returns a CredentialRequestWatchingController
// that deletes a SystemAdminCredentialRequest resource. It fires on every
// SystemAdminCredentialRequest change and only does work once that request's
// Status.DeletionTimestamp is set. It then:
//
//  1. Flips every credential ApplyDesire to Type=Delete so the kube-applier tears
//     down the applied object.
//  2. Waits for those Delete desires to report success, then removes them.
//  3. Deletes the credential ReadDesires.
//  4. Deletes the SystemAdminCredentialRequest document itself.
//
// The desire teardown is performed by the shared deleteDesires helper, which is
// also used by the post-issuance-cleanup and revocation-deletion controllers.
func NewClusterDeletionCleanupController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	backendInformers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &credentialRequestDeletion{
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewSystemAdminCredentialRequestWatchingController(
		"SystemAdminCredentialClusterDeletionCleanup",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

// needsWork reports whether deletion has been requested for this credential
// request. The controller only acts once Status.DeletionTimestamp is set.
func (c *credentialRequestDeletion) needsWork(cred *coreapi.SystemAdminCredentialRequest) bool {
	return cred.Status.DeletionTimestamp != nil
}

func (c *credentialRequestDeletion) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	credCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRequests(key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}
	if !c.needsWork(cred) {
		return nil
	}

	// The management cluster resource ID tells us which kube-applier partition
	// holds this credential's kubeapplierhelpers.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
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

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		logger.Info("waiting to tear down credential desires: no kube-applier client for management cluster",
			"credential", key.CredentialName, "managementCluster", mcResourceID.String())
		return nil
	}

	waitingFor, err := kubeapplierhelpers.DeleteAllChildDesires(ctx, kubeApplierClient, kubeapplierhelpers.CredentialRequestDesireParent(key.CredentialName),
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return err
	}
	if len(waitingFor) > 0 {
		logger.Info("waiting for management-cluster teardown of credential desires",
			"credential", key.CredentialName, "managementCluster", mcResourceID.String(),
			"waitingFor", strings.Join(waitingFor, "; "))
		return nil
	}

	// All of this credential request's desires are gone — delete the document.
	if err := credCRUD.Delete(ctx, key.CredentialName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete credential %s: %w", key.CredentialName, err))
	}
	logger.Info("deleted credential request after tearing down its desires", "credential", key.CredentialName)
	return nil
}

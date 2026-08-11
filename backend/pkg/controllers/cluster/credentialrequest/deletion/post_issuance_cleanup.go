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

	"k8s.io/apimachinery/pkg/api/meta"

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

type postIssuanceCleanup struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.SystemAdminCredentialRequestSyncer = (*postIssuanceCleanup)(nil)

// NewPostIssuanceCleanupController returns a CredentialRequestWatchingController
// that eagerly tears down per-credential CSR/CSRApproval/RBAC ApplyDesires and
// ReadDesires once an individual credential reaches Issued or Failed condition,
// freeing MC resources. The teardown reuses the shared deleteDesires helper.
func NewPostIssuanceCleanupController(
	activeOperationLister corelisters.ActiveOperationLister,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	backendInformers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &postIssuanceCleanup{
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewSystemAdminCredentialRequestWatchingController(
		"SystemAdminCredentialPostIssuanceCleanup",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

// needsWork reports whether this credential request is ready for post-issuance
// cleanup: its desires may be torn down only once issuance has reached a terminal
// outcome (Issued or Failed).
func (c *postIssuanceCleanup) needsWork(cred *coreapi.SystemAdminCredentialRequest) bool {
	return meta.IsStatusConditionTrue(cred.Status.Conditions, coreapi.SystemAdminCredentialRequestConditionIssued) ||
		meta.IsStatusConditionTrue(cred.Status.Conditions, coreapi.SystemAdminCredentialRequestConditionFailed)
}

func (c *postIssuanceCleanup) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Get the specific credential request.
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

	// Resolve the management cluster / kube-applier client. These are readiness
	// checks: if the mapping is not available yet we wait to be retriggered.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		return nil
	}

	credName := cred.ResourceID.Name
	waitingFor, err := kubeapplierhelpers.DeleteAllChildDesires(ctx, kubeApplierClient, kubeapplierhelpers.CredentialRequestDesireParent(credName),
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return err
	}
	if len(waitingFor) > 0 {
		logger.Info("post-issuance cleanup waiting for desire teardown",
			"credential", credName, "waitingFor", strings.Join(waitingFor, "; "))
		return nil
	}

	logger.Info("post-issuance cleanup complete", "credential", credName)
	return nil
}

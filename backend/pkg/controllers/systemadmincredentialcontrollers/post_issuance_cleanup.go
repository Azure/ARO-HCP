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
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// postIssuanceCleanup is controller #7. Eagerly tears down a
// credential's per-credential ApplyDesires/ReadDesires once Phase
// reaches Issued or Failed — the kubeconfig assembly no longer needs
// MC content and we want to free the partition.
//
// AwaitingRevocation/Revoked credentials are handled by controller #5
// (revoke poller); this controller stays out of those.
type postIssuanceCleanup struct {
	cooldownChecker      controllerutil.CooldownChecker
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
}

func NewPostIssuanceCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &postIssuanceCleanup{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}
	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialPostIssuanceCleanup",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *postIssuanceCleanup) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *postIssuanceCleanup) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	clusterRID, err := api.ToClusterResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("derive cluster RID: %w", err))
	}
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterRID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get/create SPC: %w", err))
	}
	if spc.Status.ManagementClusterResourceID == nil {
		return nil
	}
	kaClient := c.kubeApplierDBClients.For(ctx, spc.Status.ManagementClusterResourceID)
	if kaClient == nil {
		return nil
	}

	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminCredentials(key.HCPClusterName)
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list credentials: %w", err))
	}
	for _, cred := range iter.Items(ctx) {
		if cred == nil {
			continue
		}
		phase := cred.Status.Phase
		if phase != api.SystemAdminCredentialPhaseIssued && phase != api.SystemAdminCredentialPhaseFailed {
			continue
		}
		if len(cred.Status.OutstandingDesires) == 0 {
			continue
		}
		remaining, err := teardownCredentialOutstandingDesires(ctx, kaClient, c.resourcesDBClient, cred)
		if err != nil {
			return utils.TrackError(fmt.Errorf("teardown credential %q: %w", cred.GetResourceID().Name, err))
		}
		if _, err := credentialsCRUD.Replace(ctx, cred, nil); err != nil {
			return utils.TrackError(fmt.Errorf("persist credential teardown: %w", err))
		}
		if remaining == 0 {
			logger.Info("credential MC content cleaned up", "credential", cred.GetResourceID().Name)
		}
	}
	return iter.GetError()
}

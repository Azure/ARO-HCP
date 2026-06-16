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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// SystemAdminCredentialContentDeletedConditionType is the
// ServiceProviderCluster.Status condition this controller flips to True
// once every credential-related ApplyDesire/ReadDesire under the
// cluster has been torn down. The cluster-deletion finalizer treats
// True as a precondition.
const SystemAdminCredentialContentDeletedConditionType = "SystemAdminCredentialContentDeleted"

// clusterDeletionCleanup is controller #6. The precondition gate for
// cluster deletion: it walks every credential's OutstandingDesires
// (and any straggler CRR) under the cluster, drives them to gone via
// the shared teardown helper, deletes the credential docs, and flips
// the SPC condition.
type clusterDeletionCleanup struct {
	cooldownChecker      controllerutil.CooldownChecker
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
}

func NewClusterDeletionCleanupController(
	clusterLister listers.ClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &clusterDeletionCleanup{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}
	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialClusterDeletionCleanup",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *clusterDeletionCleanup) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterDeletionCleanup) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get cluster: %w", err))
	}

	// Gate: only run once the cluster is actively being deleted by the
	// existing finalizer pipeline.
	if cluster.ServiceProviderProperties.DeletionTimestamp == nil ||
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil {
		return nil
	}

	clusterRID := cluster.GetResourceID()
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterRID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get/create SPC: %w", err))
	}
	// If we've already finished, this is a no-op.
	if isSPCConditionTrue(spc, SystemAdminCredentialContentDeletedConditionType) {
		return nil
	}
	if spc.Status.ManagementClusterResourceID == nil {
		// No MC was ever assigned; mark done.
		return c.markDone(ctx, spc)
	}
	kaClient := c.kubeApplierDBClients.For(ctx, spc.Status.ManagementClusterResourceID)
	if kaClient == nil {
		// MC gone or not yet resolvable; defer.
		return nil
	}

	// Sweep every credential's OutstandingDesires + the credential
	// docs themselves once empty.
	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminCredentials(key.HCPClusterName)
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list credentials: %w", err))
	}
	stillDraining := false
	for _, cred := range iter.Items(ctx) {
		if cred == nil {
			continue
		}
		remaining, err := teardownCredentialOutstandingDesires(ctx, kaClient, c.resourcesDBClient, cred)
		if err != nil {
			return utils.TrackError(fmt.Errorf("teardown credential %q: %w", cred.GetResourceID().Name, err))
		}
		if remaining > 0 {
			if _, err := credentialsCRUD.Replace(ctx, cred, nil); err != nil {
				return utils.TrackError(fmt.Errorf("persist credential teardown: %w", err))
			}
			stillDraining = true
			continue
		}
		// Credential has no live MC content. Delete the doc itself —
		// no kubeconfig will ever be served again for a cluster mid-
		// delete.
		if err := credentialsCRUD.Delete(ctx, cred.GetResourceID().Name); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete credential %q: %w", cred.GetResourceID().Name, err))
		}
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("iterate credentials: %w", err))
	}

	if stillDraining {
		return nil
	}

	logger.Info("SystemAdminCredential cleanup complete; clearing finalizer precondition")
	return c.markDone(ctx, spc)
}

func (c *clusterDeletionCleanup) markDone(ctx context.Context, spc *api.ServiceProviderCluster) error {
	apimeta.SetStatusCondition(&spc.Status.Conditions, metav1.Condition{
		Type:    SystemAdminCredentialContentDeletedConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "AllCredentialsTornDown",
		Message: "Every SystemAdminCredential ApplyDesire/ReadDesire has been torn down on the management cluster.",
	})
	clusterRID := spc.GetResourceID().Parent
	_, err := c.resourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Replace(ctx, spc, nil)
	return err
}

func isSPCConditionTrue(spc *api.ServiceProviderCluster, conditionType string) bool {
	cond := apimeta.FindStatusCondition(spc.Status.Conditions, conditionType)
	if cond == nil {
		return false
	}
	return cond.Status == metav1.ConditionTrue
}

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
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// servingCASecretName is the HyperShift-managed Secret in the HCP
// namespace whose data["ca.crt"] holds the kube-apiserver serving CA
// bundle. Pinned against the HyperShift version we target — see
// PLAN.md "Serving CA bundle".
const servingCASecretName = "kube-apiserver-server-ca"

// servingCAReadDesireCreator is controller #10. Companion to controller
// #8 (CABundleSync): #10 ensures the per-cluster ReadDesire exists in
// Cosmos so the kube-applier sidecar on the management cluster mirrors
// the serving CA Secret into the doc's KubeContent; #8 then extracts
// the CA bytes and writes them onto ServiceProviderCluster.Status.ServingCABundle.
//
// Without #10 the consumer (#8) has nothing to read. Only this repo
// can create the Cosmos doc — the kube-applier sidecar only writes
// status; it never creates the desire itself.
type servingCAReadDesireCreator struct {
	cooldownChecker      controllerutil.CooldownChecker
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
	hostedClusterNSEnvID string
}

func NewServingCAReadDesireCreatorController(
	activeOperationLister listers.ActiveOperationLister,
	clusterLister listers.ClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &servingCAReadDesireCreator{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
		hostedClusterNSEnvID: hostedClusterNamespaceEnvIdentifier,
	}
	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialServingCAReadDesireCreator",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *servingCAReadDesireCreator) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *servingCAReadDesireCreator) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get cluster: %w", err))
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		// No CS reference yet; cluster_service_id_sync will retrigger us.
		return nil
	}

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("get/create SPC: %w", err))
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	hcpNamespace := hostedClusterNamespace(c.hostedClusterNSEnvID, cluster.ServiceProviderProperties.ClusterServiceID.ID())
	target := kubeapplier.ResourceReference{
		Group:     "",
		Version:   "v1",
		Resource:  "secrets",
		Namespace: hcpNamespace,
		Name:      servingCASecretName,
	}
	clusterRID := key.GetResourceID()
	desired := &kubeapplier.ReadDesire{
		CosmosMetadata: buildScopedDesireMetadata(clusterRID, ServingCAReadDesireName, kubeapplier.ReadDesireResourceTypeName, mcResourceID),
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcResourceID,
			TargetItem:        target,
		},
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}
	crud, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesires CRUD: %w", err))
	}

	existing, err := crud.Get(ctx, ServingCAReadDesireName)
	if database.IsNotFoundError(err) {
		if _, err := crud.Create(ctx, desired, nil); err != nil {
			return utils.TrackError(fmt.Errorf("create serving CA ReadDesire: %w", err))
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get serving CA ReadDesire: %w", err))
	}
	if controllerutil.ResourceIDsEqual(existing.Spec.ManagementCluster, mcResourceID) && existing.Spec.TargetItem == target {
		return nil
	}
	desired.CosmosMetadata = *existing.CosmosMetadata.DeepCopy()
	desired.Status = *existing.Status.DeepCopy()
	if _, err := crud.Replace(ctx, desired, nil); err != nil {
		return utils.TrackError(fmt.Errorf("replace serving CA ReadDesire: %w", err))
	}
	return nil
}

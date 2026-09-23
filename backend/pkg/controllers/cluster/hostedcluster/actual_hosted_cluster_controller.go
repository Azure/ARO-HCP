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

package hostedcluster

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ActualHostedClusterControllerName is the single source of truth for this
// controller's identity: the workqueue name (and therefore the Prometheus
// label), the controller name on the context, and the log field.
const ActualHostedClusterControllerName = "ActualHostedCluster"

// actualHostedClusterSyncer copies the observed HostedCluster from the
// per-cluster ReadDesire (the kube-applier's mirror of the management cluster)
// onto ServiceProviderCluster.Status.ActualHostedCluster.
//
// It exists so the frontend has a way to reason about real management-cluster
// state. The frontend has no access to kube-applier's Cosmos containers by
// design — that isolation is what stops a compromised frontend from creating
// arbitrary resources on management clusters — so content it needs must be
// placed on a resource in the "resources" container. See
// internal/admission/CLAUDE.md.
//
// Backend controllers are not consumers of this field: they read the ReadDesire
// directly, which is closer to the source and does not wait on this copy.
type actualHostedClusterSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*actualHostedClusterSyncer)(nil)

// NewActualHostedClusterController creates a controller that mirrors the
// observed HostedCluster onto ServiceProviderCluster.Status.ActualHostedCluster.
func NewActualHostedClusterController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) controllerutils.Controller {
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &actualHostedClusterSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		readDesireLister:             readDesireLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewClusterWatchingController(
		ActualHostedClusterControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

// SyncOnce mirrors the observed HostedCluster onto the ServiceProviderCluster,
// writing only when the observed object differs from what is already stored.
func (c *actualHostedClusterSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	// A cluster on its way out will not converge on anything worth mirroring, and
	// writing to it races the deletion controllers.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster from ReadDesire: %w", err))
	}
	if hostedCluster == nil {
		// No observed HostedCluster right now. Leave the stored value as it is:
		// nil if we have never observed this cluster, otherwise the last value
		// we mirrored.
		//
		// Clearing it here would be wrong. Nil also covers "the union ReadDesire
		// informer has not filled in yet" — workers start before the
		// per-management-cluster sub-informers register, and an empty union
		// reports itself synced — so every backend restart would wipe and then
		// rewrite every mirror. A HostedCluster that is genuinely gone belongs to
		// a cluster that is itself being deleted, which we skipped above, and the
		// whole ServiceProviderCluster document is removed along with it.
		//
		// We are re-enqueued when the kube-applier writes status.
		return nil
	}

	existing, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will create it; the ServiceProviderCluster
		// informer re-enqueues us once it exists.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	replacement := existing.DeepCopy()
	replacement.Status.ActualHostedCluster = hostedCluster

	// Every Replace costs RUs and wakes every controller watching the
	// ServiceProviderCluster changefeed, so only write when the observed
	// HostedCluster actually differs from what is already stored.
	if equality.Semantic.DeepEqual(existing.Status.ActualHostedCluster, replacement.Status.ActualHostedCluster) {
		return nil
	}

	_, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// Someone else wrote the document first; we re-sync off the changefeed.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	utils.LoggerFromContext(ctx).Info("mirrored actual HostedCluster onto ServiceProviderCluster",
		"hostedClusterNamespace", hostedCluster.Namespace, "hostedClusterName", hostedCluster.Name)
	return nil
}

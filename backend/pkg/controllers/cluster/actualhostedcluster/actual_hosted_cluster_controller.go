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

package actualhostedcluster

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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

// lastAppliedConfigurationAnnotation is kubectl's copy of the whole object,
// stored as a JSON string on the object itself. Mirroring it would roughly
// double the stored size for no benefit to any consumer.
const lastAppliedConfigurationAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

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
	readDesireLister             kubeapplierlisters.ReadDesireLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*actualHostedClusterSyncer)(nil)

// NewActualHostedClusterController creates a controller that mirrors the
// observed HostedCluster onto ServiceProviderCluster.Status.ActualHostedCluster.
func NewActualHostedClusterController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) controllerutils.Controller {
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &actualHostedClusterSyncer{
		resourcesDBClient:            resourcesDBClient,
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
// writing only when the sanitized object differs from what is already stored.
func (c *actualHostedClusterSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
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
		// ReadDesire absent, or the kube-applier has not observed the
		// HostedCluster yet. We are re-enqueued when it writes status, and the
		// stored value stays nil ("unknown") until then.
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
	replacement.Status.ActualHostedCluster = sanitizeHostedCluster(hostedCluster)

	// The HostedCluster status changes far more often than anything a consumer
	// of this mirror cares about, and every Replace here costs RUs and wakes
	// every controller watching the ServiceProviderCluster changefeed. Only
	// write when the sanitized object actually differs.
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

// sanitizeHostedCluster returns a copy of the observed HostedCluster with
// server-side bookkeeping removed. None of what it strips means anything to a
// consumer of the mirror, and all of it changes on writes that are otherwise
// no-ops for us — keeping it would turn every observed revision into a Cosmos
// write and a changefeed event.
func sanitizeHostedCluster(hostedCluster *hsv1beta1.HostedCluster) *hsv1beta1.HostedCluster {
	sanitized := hostedCluster.DeepCopy()
	sanitized.ManagedFields = nil
	sanitized.ResourceVersion = ""
	delete(sanitized.Annotations, lastAppliedConfigurationAnnotation)
	if len(sanitized.Annotations) == 0 {
		sanitized.Annotations = nil
	}
	return sanitized
}

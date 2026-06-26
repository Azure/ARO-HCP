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

package properties

import (
	"context"
	"fmt"
	"time"

	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// desiredControlPlaneSizeSyncer forwards the SRE-selected
// DesiredHostedClusterControlPlaneSize from the ServiceProviderCluster into
// cluster-service via the cluster's properties bag (CSPropertySizeOverride).
//
// It reads the live cluster-service cluster, computes the desired property
// value via ocm.DesiredHostedClusterSizeOverride (the same helper the frontend
// update path uses), and only issues an UpdateCluster when the value actually
// differs — avoiding the cost (and the CS "updating" state churn) of pushing
// on every resync.
//
// After a successful reconcile the syncer writes the applied size back to
// ServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize so that
// NeedsWork can detect the unset transition (Spec nil, Status non-nil)
// without round-tripping to CS — the only signal that the previously-applied
// property still needs to be cleared from CS.
type desiredControlPlaneSizeSyncer struct {
	cooldownChecker              controllerutil.CooldownChecker
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	clusterLister                listers.ClusterLister
	resourcesDBClient            database.ResourcesDBClient
	clusterServiceClient         ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*desiredControlPlaneSizeSyncer)(nil)

// NewDesiredControlPlaneSizeController creates a controller that reads
// ServiceProviderClusterSpec.DesiredHostedClusterControlPlaneSize and writes
// the corresponding cluster-service property when it diverges.
func NewDesiredControlPlaneSizeController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, clusterLister := informers.Clusters()

	syncer := &desiredControlPlaneSizeSyncer{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		resourcesDBClient:            resourcesDBClient,
		clusterServiceClient:         clusterServiceClient,
	}

	return controllerutils.NewClusterWatchingController(
		"DesiredControlPlaneSize",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *desiredControlPlaneSizeSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether ServiceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize
// diverges from what we last wrote (ServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize).
// The comparison covers all three transitions we care about:
//   - set → changed:  Spec=A, Status=B
//   - unset → set:    Spec=A, Status=nil
//   - set → unset:    Spec=nil, Status=B  (the case Spec alone cannot signal)
func (c *desiredControlPlaneSizeSyncer) NeedsWork(serviceProviderCluster *api.ServiceProviderCluster) bool {
	if serviceProviderCluster == nil {
		return false
	}
	return !ptrStringEqual(
		serviceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize,
		serviceProviderCluster.Status.DesiredHostedClusterControlPlaneSize,
	)
}

func ptrStringEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (c *desiredControlPlaneSizeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if !c.NeedsWork(cachedServiceProviderCluster) {
		return nil
	}

	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		logger.V(1).Info("Cluster not found in cache, skipping")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if cachedCluster.ServiceProviderProperties.ClusterServiceID == nil || len(cachedCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		logger.V(1).Info("Cluster has no ClusterServiceID, skipping")
		return nil
	}

	clusterServiceID := *cachedCluster.ServiceProviderProperties.ClusterServiceID
	clusterServiceCluster, err := c.clusterServiceClient.GetCluster(ctx, clusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// Compute the desired CSPropertySizeOverride value using the same logic
	// BuildCSCluster applies on the frontend update path — the helper is the
	// single source of truth so the two writers cannot disagree.
	desired, desiredPresent := ocm.DesiredHostedClusterSizeOverride(cachedServiceProviderCluster, cachedCluster)
	current, currentPresent := clusterServiceCluster.Properties()[ocm.CSPropertySizeOverride]

	if desiredPresent != currentPresent || desired != current {
		// Overlay onto a copy of the existing properties so any other entries
		// CS already holds (SingleReplica, provision shard, etc.) stay intact.
		properties := map[string]string{}
		for k, v := range clusterServiceCluster.Properties() {
			properties[k] = v
		}
		if desiredPresent {
			properties[ocm.CSPropertySizeOverride] = desired
		} else {
			delete(properties, ocm.CSPropertySizeOverride)
		}

		clusterBuilder := arohcpv1alpha1.NewCluster().Properties(properties)
		if _, err := c.clusterServiceClient.UpdateCluster(ctx, clusterServiceID, clusterBuilder); err != nil {
			return utils.TrackError(fmt.Errorf("failed to update cluster in Cluster Service: %w", err))
		}
	}

	// Record what we just applied. Status mirrors Spec so NeedsWork can
	// trivially detect the next divergence — including the unset case.
	replacement := cachedServiceProviderCluster.DeepCopy()
	if cachedServiceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize == nil {
		replacement.Status.DesiredHostedClusterControlPlaneSize = nil
	} else {
		replacement.Status.DesiredHostedClusterControlPlaneSize = ptr.To(*cachedServiceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize)
	}
	serviceProviderClusterCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClusterCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		// Another writer beat us to the SPC; the informer will deliver the
		// updated document and re-enqueue us, so treat the conflict as a no-op.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update ServiceProviderCluster status: %w", err))
	}

	logger.Info("reconciled DesiredHostedClusterControlPlaneSize with Cluster Service",
		"size", desired,
		"present", desiredPresent,
	)
	return nil
}

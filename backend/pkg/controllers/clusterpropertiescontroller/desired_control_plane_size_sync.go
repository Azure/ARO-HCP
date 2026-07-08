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

package clusterpropertiescontroller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/utils/ptr"

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

// desiredControlPlaneSizeSyncer records ServiceProviderCluster.Status
// DesiredHostedClusterControlPlaneSize once cluster-service reflects the
// effective size override implied by SPC Spec and the cluster experimental
// features (via ocm.ConvertHostedClusterSizeOverrideToCS).
//
// It does not PATCH cluster-service. The cluster update dispatch controller
// is the sole writer for CSPropertySizeOverride. This syncer only observes
// the live CS cluster and, when the property already matches the merged
// desired value, mirrors SPC Spec into Status so NeedsWork can detect the
// unset transition (Spec nil, Status non-nil) without round-tripping to CS.
//
// Status records the SPC tier field only, not the merged CS property value.
// For example, when SPC Spec is cleared and experimental Minimal applies,
// Status is set to nil once CS holds e2e_minimal, not e2e_minimal itself.
type desiredControlPlaneSizeSyncer struct {
	cooldownChecker              controllerutil.CooldownChecker
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	clusterLister                listers.ClusterLister
	resourcesDBClient            database.ResourcesDBClient
	clusterServiceClient         ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*desiredControlPlaneSizeSyncer)(nil)

// NewDesiredControlPlaneSizeController creates a controller that reconciles
// ServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize against
// SPC Spec once the cluster update dispatch controller has applied the
// effective size override to cluster-service.
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
// diverges from what we last recorded (ServiceProviderCluster.Status.DesiredHostedClusterControlPlaneSize).
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

	// Compute the effective CSPropertySizeOverride using the same logic the
	// cluster update dispatch controller applies. The helper is the single
	// source of truth so dispatch and this status reconciler cannot disagree.
	effectiveDesired, effectiveDesiredPresent := ocm.ConvertHostedClusterSizeOverrideToCS(cachedCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing, cachedServiceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize)
	current, currentPresent := clusterServiceCluster.Properties()[ocm.CSPropertySizeOverride]

	if effectiveDesiredPresent != currentPresent || effectiveDesired != current {
		logger.V(1).Info("effective cluster-service size override not yet applied, waiting for update dispatch", "effectiveDesired", effectiveDesired, "effectiveDesiredPresent", effectiveDesiredPresent, "current", current, "currentPresent", currentPresent)
		return nil
	}

	// Cluster-service reflects the merged desired value. Mirror SPC Spec into
	// Status so NeedsWork can trivially detect the next divergence.
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

	logger.Info("recorded DesiredHostedClusterControlPlaneSize status after cluster-service confirmed", "size", effectiveDesired, "present", effectiveDesiredPresent)
	return nil
}

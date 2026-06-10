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

package upgradecontrollers

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolActiveVersionSyncer is a NodePool syncer that updates
// ServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions from the
// per-node-pool ReadDesire kubeContent (the kube-applier's mirror of the
// management cluster's Hypershift NodePool object). Reading from the cached
// NodePool CR replaces the previous round-trip through Cluster Service.
type nodePoolActiveVersionSyncer struct {
	cooldownChecker               controllerutil.CooldownChecker
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister
	resourcesDBClient             database.ResourcesDBClient
	readDesireLister              dblisters.ReadDesireLister
}

var _ controllerutils.NodePoolSyncer = (*nodePoolActiveVersionSyncer)(nil)

// NewNodePoolActiveVersionController creates a new controller that updates
// Status.NodePoolVersion.ActiveVersions on the ServiceProviderNodePool from the
// per-node-pool ReadDesire's observed Hypershift NodePool.
func NewNodePoolActiveVersionController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	_, serviceProviderNodePoolLister := informers.ServiceProviderNodePools()
	syncer := &nodePoolActiveVersionSyncer{
		cooldownChecker:               controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		resourcesDBClient:             resourcesDBClient,
		readDesireLister:              readDesireLister,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolActiveVersions",
		resourcesDBClient,
		informers,
		5*time.Minute,
		syncer,
	)
}

func (c *nodePoolActiveVersionSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether the controller has a ServiceProviderNodePool to
// update. The actual decision of whether the active versions need rewriting
// depends on the ReadDesire NodePool's Status.Version (compared to the SPNP's
// current tip), which can only be evaluated after the ReadDesire fetch — so
// here we only verify that an SPNP exists to write back to.
func (c *nodePoolActiveVersionSyncer) NeedsWork(spnp *api.ServiceProviderNodePool) bool {
	return spnp != nil
}

// SyncOnce updates ServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions
// from the per-node-pool ReadDesire's observed Hypershift NodePool's
// Status.Version. The new version is prepended to the existing list when it
// differs from the current tip, and the slice is capped at two entries (newest
// first, previous one second) so we don't grow without bound.
func (c *nodePoolActiveVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Cheap cache check: skip when the ServiceProviderNodePool hasn't been
	// created yet. Once a sibling controller seeds it, the informer will
	// retrigger us.
	cachedServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool from cache: %w", err))
	}
	if !c.NeedsWork(cachedServiceProviderNodePool) {
		return nil
	}

	hsNodePool, err := maestrohelpers.GetCachedNodePoolForNodePool(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool from ReadDesire: %w", err))
	}
	if hsNodePool == nil {
		// ReadDesire absent or kubeContent not yet observed; retrigger
		// once the kube-applier writes status.
		return nil
	}
	if len(hsNodePool.Status.Version) == 0 {
		// Mirror exists but the Hypershift NodePool hasn't reported a
		// version yet; nothing to record.
		return nil
	}

	actualVersion, err := semver.ParseTolerant(hsNodePool.Status.Version)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse NodePool Status.Version %q: %w", hsNodePool.Status.Version, err))
	}

	oldActiveVersions := cachedServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions
	newActiveVersions := prependActiveVersionIfChanged(oldActiveVersions, actualVersion)
	if slices.Equal(oldActiveVersions, newActiveVersions) {
		return nil
	}

	logger.Info("Active versions changed", "oldActiveVersions", oldActiveVersions, "newActiveVersions", newActiveVersions)
	replacement := cachedServiceProviderNodePool.DeepCopy()
	replacement.Status.NodePoolVersion.ActiveVersions = newActiveVersions
	_, err = c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName).Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		// the cache will update eventually since we're out of date and we'll enter this controller again. No need to fail.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
	}
	return nil
}

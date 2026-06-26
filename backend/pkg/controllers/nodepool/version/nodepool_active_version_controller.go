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

package version

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/blang/semver/v4"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	internalcontrollerutils "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const NodePoolActiveVersionsControllerName = "NodePoolActiveVersions"

// nodePoolActiveVersionSyncer is a NodePool syncer that updates
// ServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions from the
// per-node-pool ReadDesire kubeContent (the kube-applier's mirror of the
// management cluster's Hypershift NodePool object). Reading from the cached
// NodePool CR replaces the previous round-trip through Cluster Service.
type nodePoolActiveVersionSyncer struct {
	cooldownChecker               internalcontrollerutils.CooldownChecker
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
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
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
		NodePoolActiveVersionsControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *nodePoolActiveVersionSyncer) CooldownChecker() internalcontrollerutils.CooldownChecker {
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
// to the distinct set of OCPVersions reported by the Hypershift NodePool's
// Status.NodesInfo.NodeVersions. During an in-progress upgrade NodeVersions
// will hold one entry per (OCPVersion, KubeletVersion) tuple that any node is
// running; we dedupe on OCPVersion and order newest-semver first so the SPNP
// reflects exactly what is live on the management cluster.
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
	if len(hsNodePool.Status.NodesInfo.NodeVersions) == 0 {
		// Mirror exists but the Hypershift NodePool hasn't reported any
		// node versions yet; nothing to record.
		return nil
	}

	newActiveVersions, err := activeVersionsFromNodeVersions(ctx, hsNodePool.Status.NodesInfo.NodeVersions)
	if err != nil {
		return utils.TrackError(err)
	}
	if len(newActiveVersions) == 0 {
		// Every NodeVersions entry had an empty/unparseable OCPVersion;
		// nothing usable to record, wait for the next reconcile.
		return nil
	}

	if !internalcontrollerutils.NeedsUpdate(cachedServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions, newActiveVersions) {
		return nil
	}

	logger.Info("Active versions changed",
		"oldActiveVersions", cachedServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions,
		"newActiveVersions", newActiveVersions)
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

// activeVersionsFromNodeVersions reduces a list of Hypershift NodeVersion entries
// to the distinct, semver-sorted (newest first) set of OCPVersions. Multiple
// NodeVersion entries may share the same OCPVersion (they differ on
// KubeletVersion / readiness counts), so we dedupe; entries whose OCPVersion is
// empty or unparseable are skipped with an Info log so a single malformed row
// doesn't poison the rest of the list.
func activeVersionsFromNodeVersions(ctx context.Context, nodeVersions []hsv1beta1.NodeVersion) ([]api.HCPNodePoolActiveVersion, error) {
	logger := utils.LoggerFromContext(ctx)
	seen := map[string]struct{}{}
	parsed := []semver.Version{}
	for _, nv := range nodeVersions {
		if len(nv.OCPVersion) == 0 {
			continue
		}
		if _, ok := seen[nv.OCPVersion]; ok {
			continue
		}
		v, err := semver.ParseTolerant(nv.OCPVersion)
		if err != nil {
			logger.Info("skipping NodeVersions entry with unparseable OCPVersion", "ocpVersion", nv.OCPVersion, "err", err.Error())
			continue
		}
		seen[nv.OCPVersion] = struct{}{}
		parsed = append(parsed, v)
	}
	// Newest first.
	semver.Sort(parsed)
	slices.Reverse(parsed)

	out := make([]api.HCPNodePoolActiveVersion, 0, len(parsed))
	for i := range parsed {
		out = append(out, api.HCPNodePoolActiveVersion{Version: &parsed[i]})
	}
	return out, nil
}

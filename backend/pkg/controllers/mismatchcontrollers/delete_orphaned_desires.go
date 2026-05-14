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

package mismatchcontrollers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

// deleteOrphanedDesires walks the kube-applier container cross-partition
// and deletes *Desire documents whose parent (cluster, or nodepool-under-
// cluster) no longer exists in the resources container.
//
// Sibling of deleteOrphanedCosmosResources. The latter iterates per
// subscription against the resources container; this one iterates across
// every kube-applier partition because *Desires are partitioned by
// management cluster, not subscription.
type deleteOrphanedDesires struct {
	name string

	resourcesDBClient   database.ResourcesDBClient
	kubeApplierDBClient database.KubeApplierDBClient

	queue workqueue.TypedRateLimitingInterface[string]
}

// NewDeleteOrphanedDesiresController periodically deletes *Desires whose
// parent cluster/nodepool is gone from the resources container.
func NewDeleteOrphanedDesiresController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClient database.KubeApplierDBClient,
) controllerutils.Controller {
	return &deleteOrphanedDesires{
		name:                "DeleteOrphanedDesires",
		resourcesDBClient:   resourcesDBClient,
		kubeApplierDBClient: kubeApplierDBClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "DeleteOrphanedDesires",
			},
		),
	}
}

func (c *deleteOrphanedDesires) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Sweeping orphaned *Desires from kube-applier container")

	// parentExists caches the resources-container lookups so a cluster
	// with many desires only costs one Get per sync.
	parentExists := newParentExistsCache(c.resourcesDBClient)

	var syncErrors []error

	applyDesires, err := database.ListAll(ctx, 500, c.kubeApplierDBClient.GlobalListers().ApplyDesires().List)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires: %w", err))
	}
	for _, d := range applyDesires {
		orphaned, err := c.isOrphanedDesire(ctx, d.ResourceID, parentExists)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(err))
			continue
		}
		if !orphaned {
			continue
		}
		parent, err := desireResourceParent(d.ResourceID)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(err))
			continue
		}
		crud, err := c.kubeApplierDBClient.KubeApplier(d.Spec.ManagementCluster).ApplyDesires(parent)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("ApplyDesires CRUD: %w", err)))
			continue
		}
		logger.Info("deleting orphaned ApplyDesire", "resourceID", d.ResourceID.String())
		if err := crud.Delete(ctx, d.ResourceID.Name); err != nil && !database.IsNotFoundError(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("delete orphaned ApplyDesire %s: %w", d.ResourceID.String(), err)))
		}
	}

	deleteDesires, err := database.ListAll(ctx, 500, c.kubeApplierDBClient.GlobalListers().DeleteDesires().List)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list DeleteDesires: %w", err))
	}
	for _, d := range deleteDesires {
		orphaned, err := c.isOrphanedDesire(ctx, d.ResourceID, parentExists)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(err))
			continue
		}
		if !orphaned {
			continue
		}
		parent, err := desireResourceParent(d.ResourceID)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(err))
			continue
		}
		crud, err := c.kubeApplierDBClient.KubeApplier(d.Spec.ManagementCluster).DeleteDesires(parent)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("DeleteDesires CRUD: %w", err)))
			continue
		}
		logger.Info("deleting orphaned DeleteDesire", "resourceID", d.ResourceID.String())
		if err := crud.Delete(ctx, d.ResourceID.Name); err != nil && !database.IsNotFoundError(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("delete orphaned DeleteDesire %s: %w", d.ResourceID.String(), err)))
		}
	}

	readDesires, err := database.ListAll(ctx, 500, c.kubeApplierDBClient.GlobalListers().ReadDesires().List)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ReadDesires: %w", err))
	}
	for _, d := range readDesires {
		orphaned, err := c.isOrphanedDesire(ctx, d.ResourceID, parentExists)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(err))
			continue
		}
		if !orphaned {
			continue
		}
		parent, err := desireResourceParent(d.ResourceID)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(err))
			continue
		}
		crud, err := c.kubeApplierDBClient.KubeApplier(d.Spec.ManagementCluster).ReadDesires(parent)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("ReadDesires CRUD: %w", err)))
			continue
		}
		logger.Info("deleting orphaned ReadDesire", "resourceID", d.ResourceID.String())
		if err := crud.Delete(ctx, d.ResourceID.Name); err != nil && !database.IsNotFoundError(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("delete orphaned ReadDesire %s: %w", d.ResourceID.String(), err)))
		}
	}

	return errors.Join(syncErrors...)
}

// isOrphanedDesire returns true when the desire's parent (cluster, or
// nodepool whose parent cluster is gone) is missing from the resources
// container. A missing intermediate (cluster gone but nodepool was never
// recorded) is also treated as orphaned: it cannot be reattached.
func (c *deleteOrphanedDesires) isOrphanedDesire(ctx context.Context, desireResourceID *azcorearm.ResourceID, cache *parentExistsCache) (bool, error) {
	if desireResourceID == nil || desireResourceID.Parent == nil {
		return false, fmt.Errorf("desire resource ID has no parent: %v", desireResourceID)
	}
	parentRID := desireResourceID.Parent

	switch {
	case armhelpers.ResourceTypeEqual(parentRID.ResourceType, api.ClusterResourceType):
		exists, err := cache.clusterExists(ctx, parentRID)
		if err != nil {
			return false, err
		}
		return !exists, nil

	case armhelpers.ResourceTypeEqual(parentRID.ResourceType, api.NodePoolResourceType):
		exists, err := cache.nodePoolExists(ctx, parentRID)
		if err != nil {
			return false, err
		}
		return !exists, nil
	}

	// Unknown parent type: don't delete. Surface so we notice in logs.
	return false, fmt.Errorf("desire %s has unexpected parent resource type %s", desireResourceID.String(), parentRID.ResourceType.String())
}

// desireResourceParent extracts the database.ResourceParent for a
// *Desire's CRUD lookup, from the desire's parent (cluster or nodepool).
func desireResourceParent(desireResourceID *azcorearm.ResourceID) (database.ResourceParent, error) {
	if desireResourceID == nil || desireResourceID.Parent == nil {
		return database.ResourceParent{}, fmt.Errorf("desire resource ID has no parent: %v", desireResourceID)
	}
	parentRID := desireResourceID.Parent
	switch {
	case armhelpers.ResourceTypeEqual(parentRID.ResourceType, api.ClusterResourceType):
		return database.ResourceParent{
			SubscriptionID:    parentRID.SubscriptionID,
			ResourceGroupName: parentRID.ResourceGroupName,
			ClusterName:       parentRID.Name,
		}, nil
	case armhelpers.ResourceTypeEqual(parentRID.ResourceType, api.NodePoolResourceType):
		if parentRID.Parent == nil {
			return database.ResourceParent{}, fmt.Errorf("nodepool-scoped desire %s has no grandparent cluster", desireResourceID.String())
		}
		return database.ResourceParent{
			SubscriptionID:    parentRID.SubscriptionID,
			ResourceGroupName: parentRID.ResourceGroupName,
			ClusterName:       parentRID.Parent.Name,
			NodePoolName:      parentRID.Name,
		}, nil
	}
	return database.ResourceParent{}, fmt.Errorf("unexpected parent resource type %s", parentRID.ResourceType.String())
}

// parentExistsCache memoizes resources-container lookups across a single
// sync pass. Lower-cased keys throughout so we don't double-look-up
// because of case differences in stored resource IDs.
type parentExistsCache struct {
	resourcesDBClient database.ResourcesDBClient
	clusters          map[string]bool // lowercased cluster resource ID → exists
	nodePools         map[string]bool // lowercased nodepool resource ID → exists
}

func newParentExistsCache(resourcesDBClient database.ResourcesDBClient) *parentExistsCache {
	return &parentExistsCache{
		resourcesDBClient: resourcesDBClient,
		clusters:          map[string]bool{},
		nodePools:         map[string]bool{},
	}
}

func (p *parentExistsCache) clusterExists(ctx context.Context, clusterRID *azcorearm.ResourceID) (bool, error) {
	key := lowerResourceID(clusterRID)
	if exists, ok := p.clusters[key]; ok {
		return exists, nil
	}
	_, err := p.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Get(ctx, clusterRID.Name)
	if database.IsNotFoundError(err) {
		p.clusters[key] = false
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get cluster %s: %w", clusterRID.String(), err)
	}
	p.clusters[key] = true
	return true, nil
}

func (p *parentExistsCache) nodePoolExists(ctx context.Context, nodePoolRID *azcorearm.ResourceID) (bool, error) {
	key := lowerResourceID(nodePoolRID)
	if exists, ok := p.nodePools[key]; ok {
		return exists, nil
	}
	if nodePoolRID.Parent == nil {
		return false, fmt.Errorf("nodepool %s has no parent cluster", nodePoolRID.String())
	}
	clusterRID := nodePoolRID.Parent
	clusterExists, err := p.clusterExists(ctx, clusterRID)
	if err != nil {
		return false, err
	}
	if !clusterExists {
		// Parent cluster gone implies the nodepool is gone too.
		p.nodePools[key] = false
		return false, nil
	}
	_, err = p.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).
		NodePools(clusterRID.Name).Get(ctx, nodePoolRID.Name)
	if database.IsNotFoundError(err) {
		p.nodePools[key] = false
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get nodepool %s: %w", nodePoolRID.String(), err)
	}
	p.nodePools[key] = true
	return true, nil
}

func lowerResourceID(rid *azcorearm.ResourceID) string {
	return strings.ToLower(rid.String())
}

func (c *deleteOrphanedDesires) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	// Run on a 60-minute jitter, matching deleteOrphanedCosmosResources;
	// orphan cleanup is best-effort and does not need to be tight.
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) {
		c.queue.Add("sweep")
	}, 60*time.Minute, 0.1, true)

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *deleteOrphanedDesires) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *deleteOrphanedDesires) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)
	return true
}

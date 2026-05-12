// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// cleanupLegacyMaestroReadonlyBundles is a migration-only controller that
// drains the Maestro readonly bundles created by the now-deleted
// createClusterScopedMaestroReadonlyBundlesSyncer /
// createNodePoolScopedMaestroReadonlyBundlesSyncer controllers. With the
// switch to ReadDesire-sourced kube-content mirroring, these bundles are
// dead weight in every provision shard's Maestro API.
//
// On each sync (jittered every 10 minutes) this controller walks every
// ServiceProviderCluster / ServiceProviderNodePool that still has a
// non-empty Status.MaestroReadonlyBundles, deletes each referenced
// Maestro bundle, and clears the field. Once every ServiceProvider* has
// drained, this controller is a no-op forever; it can be retired and the
// MaestroReadonlyBundles API field deleted in a follow-up.
//
// The existing deleteOrphanedMaestroReadonlyBundles controller stays in
// place as a safety net for bundles whose cosmos reference was already
// gone before this controller ran.
type cleanupLegacyMaestroReadonlyBundles struct {
	name string

	resourcesDBClient database.ResourcesDBClient

	clusterServiceClient ocm.ClusterServiceClientSpec
	maestroClientBuilder maestro.MaestroClientBuilder

	maestroSourceEnvironmentIdentifier string

	queue workqueue.TypedRateLimitingInterface[string]
}

// NewCleanupLegacyMaestroReadonlyBundlesController constructs the
// migration-cleanup controller. Wire it alongside the existing
// delete-orphaned controller; both can run concurrently.
func NewCleanupLegacyMaestroReadonlyBundlesController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	maestroClientBuilder maestro.MaestroClientBuilder,
	maestroSourceEnvironmentIdentifier string,
) controllerutils.Controller {
	return &cleanupLegacyMaestroReadonlyBundles{
		name:                               "CleanupLegacyMaestroReadonlyBundles",
		resourcesDBClient:                  resourcesDBClient,
		clusterServiceClient:               clusterServiceClient,
		maestroClientBuilder:               maestroClientBuilder,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "CleanupLegacyMaestroReadonlyBundles",
			},
		),
	}
}

// SyncOnce drains MaestroReadonlyBundles from every ServiceProvider* doc.
// Failures on individual bundles or cosmos replaces are aggregated and
// returned; the workqueue will requeue this sentinel key and retry on the
// next pass.
func (c *cleanupLegacyMaestroReadonlyBundles) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Cleaning up legacy Maestro Readonly Bundles")

	// Reuse the same maestro-client-per-shard pattern the orphan controller
	// uses so we open one connection per shard regardless of how many
	// ServiceProvider* docs reference it.
	maestroClientsByShard, err := c.buildMaestroClientsByProvisionShard(ctx)
	defer cancelMaestroClientsByProvisionShard(maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build Maestro clients by provision shard: %w", err))
	}

	var syncErrors []error

	spcs, err := database.ListAll(ctx, 500, c.resourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters().List)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ServiceProviderClusters: %w", err))
	}
	for _, spc := range spcs {
		if len(spc.Status.MaestroReadonlyBundles) == 0 {
			continue
		}
		if err := c.cleanupServiceProviderCluster(ctx, spc, maestroClientsByShard); err != nil {
			syncErrors = append(syncErrors, utils.TrackError(
				fmt.Errorf("cleanup ServiceProviderCluster %s: %w", spc.ResourceID.String(), err),
			))
		}
	}

	spnps, err := database.ListAll(ctx, 500, c.resourcesDBClient.ResourcesGlobalListers().ServiceProviderNodePools().List)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ServiceProviderNodePools: %w", err))
	}
	for _, spnp := range spnps {
		if len(spnp.Status.MaestroReadonlyBundles) == 0 {
			continue
		}
		if err := c.cleanupServiceProviderNodePool(ctx, spnp, maestroClientsByShard); err != nil {
			syncErrors = append(syncErrors, utils.TrackError(
				fmt.Errorf("cleanup ServiceProviderNodePool %s: %w", spnp.ResourceID.String(), err),
			))
		}
	}

	return errors.Join(syncErrors...)
}

func (c *cleanupLegacyMaestroReadonlyBundles) cleanupServiceProviderCluster(
	ctx context.Context,
	spc *api.ServiceProviderCluster,
	maestroClientsByShard map[string]*shardMaestroClient,
) error {
	maestroClient, skip, err := c.maestroClientForServiceProviderCluster(ctx, spc, maestroClientsByShard)
	if err != nil {
		return err
	}
	// If the parent cluster is gone we still want to clear the field so the
	// ServiceProviderCluster is consistent. The orphan-cleanup safety net
	// will remove the dangling Maestro bundles on its own pass.
	if !skip {
		if err := c.deleteAllBundles(ctx, maestroClient, spc.Status.MaestroReadonlyBundles); err != nil {
			return err
		}
	}

	clusterParent := spc.ResourceID.Parent
	if clusterParent == nil {
		return utils.TrackError(fmt.Errorf("ServiceProviderCluster %s has no parent resource ID", spc.ResourceID.String()))
	}
	spcCRUD := c.resourcesDBClient.ServiceProviderClusters(
		clusterParent.SubscriptionID, clusterParent.ResourceGroupName, clusterParent.Name,
	)
	updated := spc.DeepCopy()
	updated.Status.MaestroReadonlyBundles = nil
	if _, err := spcCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to clear MaestroReadonlyBundles on ServiceProviderCluster: %w", err))
	}
	return nil
}

func (c *cleanupLegacyMaestroReadonlyBundles) cleanupServiceProviderNodePool(
	ctx context.Context,
	spnp *api.ServiceProviderNodePool,
	maestroClientsByShard map[string]*shardMaestroClient,
) error {
	maestroClient, skip, err := c.maestroClientForServiceProviderNodePool(ctx, spnp, maestroClientsByShard)
	if err != nil {
		return err
	}
	if !skip {
		if err := c.deleteAllBundles(ctx, maestroClient, spnp.Status.MaestroReadonlyBundles); err != nil {
			return err
		}
	}

	nodePoolParent := spnp.ResourceID.Parent
	if nodePoolParent == nil {
		return utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no parent resource ID", spnp.ResourceID.String()))
	}
	clusterParent := nodePoolParent.Parent
	if clusterParent == nil {
		return utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no grandparent cluster resource ID", spnp.ResourceID.String()))
	}
	spnpCRUD := c.resourcesDBClient.ServiceProviderNodePools(
		clusterParent.SubscriptionID, clusterParent.ResourceGroupName, clusterParent.Name, nodePoolParent.Name,
	)
	updated := spnp.DeepCopy()
	updated.Status.MaestroReadonlyBundles = nil
	if _, err := spnpCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to clear MaestroReadonlyBundles on ServiceProviderNodePool: %w", err))
	}
	return nil
}

// deleteAllBundles iterates over a MaestroReadonlyBundles list and best-effort
// deletes each bundle from Maestro. A NotFound is treated as success
// (the orphan-cleanup controller may have already removed it).
func (c *cleanupLegacyMaestroReadonlyBundles) deleteAllBundles(
	ctx context.Context,
	maestroClient maestro.Client,
	bundles api.MaestroBundleReferenceList,
) error {
	var errs []error
	for _, ref := range bundles {
		if len(ref.MaestroAPIMaestroBundleName) == 0 {
			continue
		}
		err := maestroClient.Delete(ctx, ref.MaestroAPIMaestroBundleName, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			errs = append(errs, utils.TrackError(
				fmt.Errorf("delete Maestro bundle %q: %w", ref.MaestroAPIMaestroBundleName, err),
			))
		}
	}
	return errors.Join(errs...)
}

// maestroClientForServiceProviderCluster resolves the Maestro client for
// the provision shard the SPC's parent cluster runs on. skip==true means
// the parent cluster is gone or unregistered with CS; the caller should
// only clear the cosmos field in that case.
func (c *cleanupLegacyMaestroReadonlyBundles) maestroClientForServiceProviderCluster(
	ctx context.Context,
	spc *api.ServiceProviderCluster,
	maestroClientsByShard map[string]*shardMaestroClient,
) (maestro.Client, bool, error) {
	clusterParent := spc.ResourceID.Parent
	if clusterParent == nil {
		return nil, false, utils.TrackError(fmt.Errorf("ServiceProviderCluster %s has no parent resource ID", spc.ResourceID.String()))
	}
	cluster, err := c.resourcesDBClient.HCPClusters(clusterParent.SubscriptionID, clusterParent.ResourceGroupName).Get(ctx, clusterParent.Name)
	if database.IsNotFoundError(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get parent Cluster: %w", err))
	}
	return c.maestroClientForCluster(cluster, maestroClientsByShard)
}

func (c *cleanupLegacyMaestroReadonlyBundles) maestroClientForServiceProviderNodePool(
	ctx context.Context,
	spnp *api.ServiceProviderNodePool,
	maestroClientsByShard map[string]*shardMaestroClient,
) (maestro.Client, bool, error) {
	nodePoolParent := spnp.ResourceID.Parent
	if nodePoolParent == nil {
		return nil, false, utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no parent resource ID", spnp.ResourceID.String()))
	}
	clusterParent := nodePoolParent.Parent
	if clusterParent == nil {
		return nil, false, utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no grandparent cluster resource ID", spnp.ResourceID.String()))
	}
	cluster, err := c.resourcesDBClient.HCPClusters(clusterParent.SubscriptionID, clusterParent.ResourceGroupName).Get(ctx, clusterParent.Name)
	if database.IsNotFoundError(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get parent Cluster: %w", err))
	}
	return c.maestroClientForCluster(cluster, maestroClientsByShard)
}

func (c *cleanupLegacyMaestroReadonlyBundles) maestroClientForCluster(
	cluster *api.HCPOpenShiftCluster,
	maestroClientsByShard map[string]*shardMaestroClient,
) (maestro.Client, bool, error) {
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil, true, nil
	}
	shard, err := c.clusterServiceClient.GetClusterProvisionShard(context.Background(), *cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard: %w", err))
	}
	entry, ok := maestroClientsByShard[shard.ID()]
	if !ok {
		return nil, false, utils.TrackError(fmt.Errorf("provision shard %s for cluster %s is not present in maestro client map", shard.ID(), cluster.ID.String()))
	}
	return entry.maestroClient, false, nil
}

// buildMaestroClientsByProvisionShard mirrors the orphan-cleanup
// controller's helper of the same name. Defined here so the cleanup
// controller is self-contained; the orphan controller has its own copy
// because the two will be removed on different timelines (this one
// retires when MaestroReadonlyBundles is empty everywhere; the orphan
// controller stays as long as any bundles can drift orphaned).
func (c *cleanupLegacyMaestroReadonlyBundles) buildMaestroClientsByProvisionShard(ctx context.Context) (map[string]*shardMaestroClient, error) {
	out := map[string]*shardMaestroClient{}
	iter := c.clusterServiceClient.ListProvisionShards()
	for shard := range iter.Items(ctx) {
		maestroClientCtx, cancel := context.WithCancel(ctx)
		maestroClient, err := createMaestroClientFromCSProvisionShard(maestroClientCtx, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder, shard)
		if err != nil {
			cancel()
			return out, utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
		}
		out[shard.ID()] = &shardMaestroClient{maestroClient: maestroClient, maestroClientCancelFunc: cancel}
	}
	if err := iter.GetError(); err != nil {
		return out, utils.TrackError(fmt.Errorf("failed to list Cluster Service provision shards: %w", err))
	}
	return out, nil
}

func (c *cleanupLegacyMaestroReadonlyBundles) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	// Kick the first pass shortly after startup and then on a long jitter
	// so this migration controller doesn't add steady load. Sentinel key;
	// the controller takes no per-key argument.
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) {
		c.queue.Add("cleanup")
	}, 10*time.Minute, 0.1, true)

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *cleanupLegacyMaestroReadonlyBundles) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *cleanupLegacyMaestroReadonlyBundles) processNextWorkItem(ctx context.Context) bool {
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

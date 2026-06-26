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

package maestro

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// shardMaestroClient holds a Maestro API client scoped to a single
// management cluster and its teardown cancel func. This is intentionally a
// package-local type (not the ShardMaestroClient in shared/maestro) because
// the cleanup-legacy controller keys by management-cluster resource-ID, not
// by Cluster Service provision-shard ID.
type shardMaestroClient struct {
	maestroClient           maestro.Client
	maestroClientCancelFunc context.CancelFunc
}

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

	resourcesDBClient       database.ResourcesDBClient
	managementClusterLister dblisters.ManagementClusterLister

	maestroClientBuilder maestro.MaestroClientBuilder

	maestroSourceEnvironmentIdentifier string

	queue workqueue.TypedRateLimitingInterface[string]
}

// NewCleanupLegacyMaestroReadonlyBundlesController constructs the
// migration-cleanup controller. Wire it alongside the existing
// delete-orphaned controller; both can run concurrently.
func NewCleanupLegacyMaestroReadonlyBundlesController(
	resourcesDBClient database.ResourcesDBClient,
	managementClusterLister dblisters.ManagementClusterLister,
	maestroClientBuilder maestro.MaestroClientBuilder,
	maestroSourceEnvironmentIdentifier string,
) controllerutils.Controller {
	return &cleanupLegacyMaestroReadonlyBundles{
		name:                               "CleanupLegacyMaestroReadonlyBundles",
		resourcesDBClient:                  resourcesDBClient,
		managementClusterLister:            managementClusterLister,
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

	// Build one maestro client per ManagementCluster cosmos doc. Keying by
	// MC resourceID lets us look the client up directly from
	// ServiceProviderCluster.Status.ManagementClusterResourceID without ever
	// calling Cluster Service.
	maestroClientsByMC, err := c.buildMaestroClientsByManagementCluster(ctx)
	defer cancelMaestroClientsByManagementCluster(maestroClientsByMC)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build Maestro clients by management cluster: %w", err))
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
		if err := c.cleanupServiceProviderCluster(ctx, spc, maestroClientsByMC); err != nil {
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
		if err := c.cleanupServiceProviderNodePool(ctx, spnp, maestroClientsByMC); err != nil {
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
	maestroClientsByMC map[string]*shardMaestroClient,
) error {
	maestroClient, skip, err := c.maestroClientForServiceProviderCluster(spc, maestroClientsByMC)
	if err != nil {
		return err
	}
	// If the SPC has no management cluster wired up we still want to clear
	// the field so it is consistent. The orphan-cleanup safety net will
	// remove any dangling Maestro bundles on its own pass.
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
	maestroClientsByMC map[string]*shardMaestroClient,
) error {
	maestroClient, skip, err := c.maestroClientForServiceProviderNodePool(ctx, spnp, maestroClientsByMC)
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

// maestroClientForServiceProviderCluster resolves the Maestro client for the
// management cluster that hosts the SPC's HostedCluster. skip==true means
// the SPC has not yet been wired to a management cluster (or the MC is
// no longer registered); the caller should only clear the cosmos field
// in that case.
func (c *cleanupLegacyMaestroReadonlyBundles) maestroClientForServiceProviderCluster(
	spc *api.ServiceProviderCluster,
	maestroClientsByMC map[string]*shardMaestroClient,
) (maestro.Client, bool, error) {
	return maestroClientForMC(spc.Status.ManagementClusterResourceID, maestroClientsByMC)
}

// maestroClientForServiceProviderNodePool looks up the parent cluster's SPC
// (its ManagementClusterResourceID is the source of truth) and returns the
// maestro client for that MC. skip handling matches the cluster-scoped path.
func (c *cleanupLegacyMaestroReadonlyBundles) maestroClientForServiceProviderNodePool(
	ctx context.Context,
	spnp *api.ServiceProviderNodePool,
	maestroClientsByMC map[string]*shardMaestroClient,
) (maestro.Client, bool, error) {
	nodePoolParent := spnp.ResourceID.Parent
	if nodePoolParent == nil {
		return nil, false, utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no parent resource ID", spnp.ResourceID.String()))
	}
	clusterParent := nodePoolParent.Parent
	if clusterParent == nil {
		return nil, false, utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no grandparent cluster resource ID", spnp.ResourceID.String()))
	}
	spc, err := c.resourcesDBClient.ServiceProviderClusters(
		clusterParent.SubscriptionID, clusterParent.ResourceGroupName, clusterParent.Name,
	).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get parent ServiceProviderCluster: %w", err))
	}
	return maestroClientForMC(spc.Status.ManagementClusterResourceID, maestroClientsByMC)
}

// maestroClientForMC looks the maestro client up in the per-MC map. A nil
// resourceID means the placement-sync controller has not assigned the
// management cluster yet (skip=true); a non-nil resourceID without a map
// entry means the MC has been unregistered between map build and lookup —
// surface that as an error so we retry rather than silently swallow data.
func maestroClientForMC(
	mcResourceID *azcorearm.ResourceID,
	maestroClientsByMC map[string]*shardMaestroClient,
) (maestro.Client, bool, error) {
	if mcResourceID == nil {
		return nil, true, nil
	}
	key := strings.ToLower(mcResourceID.String())
	entry, ok := maestroClientsByMC[key]
	if !ok {
		return nil, false, utils.TrackError(fmt.Errorf("management cluster %s is not present in maestro client map", mcResourceID.String()))
	}
	return entry.maestroClient, false, nil
}

// buildMaestroClientsByManagementCluster lists ManagementCluster cosmos docs
// and builds a maestro client per MC straight from the doc's status fields
// (MaestroRESTAPIURL, MaestroGRPCTarget, MaestroConsumerName,
// ClusterServiceProvisionShardID for the source ID). The returned map is
// keyed by lowercased MC resourceID so callers can look up by
// ServiceProviderCluster.Status.ManagementClusterResourceID. On error the
// returned map may be partial (clients created before the error); the caller
// must defer cancelMaestroClientsByManagementCluster unconditionally.
func (c *cleanupLegacyMaestroReadonlyBundles) buildMaestroClientsByManagementCluster(ctx context.Context) (map[string]*shardMaestroClient, error) {
	out := map[string]*shardMaestroClient{}
	mcs, err := c.managementClusterLister.List(ctx)
	if err != nil {
		return out, utils.TrackError(fmt.Errorf("failed to list ManagementClusters: %w", err))
	}
	for _, mc := range mcs {
		if mc.ResourceID == nil {
			continue
		}
		maestroClientCtx, cancel := context.WithCancel(ctx)
		maestroClient, err := createMaestroClientFromManagementCluster(maestroClientCtx, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder, mc)
		if err != nil {
			cancel()
			return out, utils.TrackError(fmt.Errorf("failed to create Maestro client for management cluster %s: %w", mc.ResourceID.String(), err))
		}
		out[strings.ToLower(mc.ResourceID.String())] = &shardMaestroClient{maestroClient: maestroClient, maestroClientCancelFunc: cancel}
	}
	return out, nil
}

// cancelMaestroClientsByManagementCluster runs the cancel function for each
// maestro client built by buildMaestroClientsByManagementCluster.
func cancelMaestroClientsByManagementCluster(maestroClientsByMC map[string]*shardMaestroClient) {
	for _, entry := range maestroClientsByMC {
		entry.maestroClientCancelFunc()
	}
}

// createMaestroClientFromManagementCluster builds a maestro client from a
// ManagementCluster cosmos doc's status fields. The source ID is derived
// from envIdentifier + the MC's recorded CS provision shard so that
// bundles owned by the same shard remain visible across the cleanup runs.
func createMaestroClientFromManagementCluster(
	ctx context.Context,
	envIdentifier string,
	builder maestro.MaestroClientBuilder,
	mc *fleet.ManagementCluster,
) (maestro.Client, error) {
	if mc.Status.ClusterServiceProvisionShardID == nil {
		return nil, fmt.Errorf("management cluster %s has no ClusterServiceProvisionShardID", mc.ResourceID.String())
	}
	sourceID := maestro.GenerateMaestroSourceID(envIdentifier, mc.Status.ClusterServiceProvisionShardID.ID())
	return builder.NewClient(ctx, mc.Status.MaestroRESTAPIURL, mc.Status.MaestroGRPCTarget, mc.Status.MaestroConsumerName, sourceID)
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

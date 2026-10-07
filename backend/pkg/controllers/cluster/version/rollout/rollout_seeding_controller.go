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

package rollout

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

const RolloutSeedingControllerName = "ControlPlaneVersionRolloutSeeding"

type rolloutSeedingController struct {
	*controllerutil.GenericWatchingController[controllerutils.ControlPlaneVersionRolloutKey]
	discover func(context.Context) error
	clock    clock.WithTicker
}

type rolloutSeedingSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	controllerLister             corelisters.ControllerLister
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient                fleetcosmosstorage.FleetDBClient
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	queue                        controllerutil.Enqueuer
	discovery                    graphDataClient
}

// NewControlPlaneVersionRolloutSeedingController establishes version state for
// prospective offerings before their first cluster exists. It also restores state
// needed by existing clusters through upgrades, pin release and deletion, including
// referenced minor version and channel group pairs below the backend support floor.
//
// Discovery and cluster observations enqueue canonical channel keys.
// Reconciliation creates missing rollouts without rewriting existing state.
func NewControlPlaneVersionRolloutSeedingController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	informers coreinformers.BackendInformers,
	fleetInformers fleetinformers.FleetInformers,
) controllerregistry.Runnable {
	clusterInformer, clusterLister := informers.Clusters()
	spcInformer, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, controllerLister := informers.Controllers()
	rolloutInformer, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &rolloutSeedingSyncer{
		clusterLister: clusterLister, serviceProviderClusterLister: serviceProviderClusterLister,
		controllerLister: controllerLister,
		rolloutLister:    rolloutLister, fleetDBClient: fleetDBClient, resourcesDBClient: resourcesDBClient,
		discovery: cincinnati.NewGraphDataClient(),
	}
	controller := &rolloutSeedingController{
		GenericWatchingController: controllerutils.NewControlPlaneVersionRolloutWatchingController(RolloutSeedingControllerName, fleetInformers, 5*time.Minute, syncer),
		discover:                  syncer.discover,
		clock:                     clock.RealClock{},
	}
	syncer.queue = controller
	if err := watchRolloutReferences(clusterInformer, spcInformer, clusterLister, serviceProviderClusterLister, controller, RolloutSeedingControllerName, 5*time.Minute); err != nil {
		panic(err)
	}
	// The generic watcher handles adds and updates; deletion must also repair
	// supported channels and channels that remain referenced.
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(RolloutSeedingControllerName)...)
	_, err := rolloutInformer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{DeleteFunc: func(obj any) {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		if rollout, ok := obj.(*fleetapi.ControlPlaneVersionRollout); ok && rollout.ResourceID != nil {
			controller.Enqueue(controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: rollout.ResourceID.Name})
		}
	}}, cache.HandlerOptions{Logger: &logger})
	if err != nil {
		panic(err)
	}
	return controller
}

func (c *rolloutSeedingController) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	ctx, cancel := context.WithCancel(ctx)
	var discovery sync.WaitGroup
	defer discovery.Wait()
	defer cancel()
	if c.WaitForCacheSync(ctx) {
		discoveryCtx := utils.ContextWithControllerName(ctx, RolloutSeedingControllerName)
		discoveryCtx = utils.ContextWithLogger(discoveryCtx, utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(RolloutSeedingControllerName)...))
		discovery.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer discovery.Done()
			// Fixed cadence without overlapping slow Cincinnati requests.
			ticker := c.clock.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for ctx.Err() == nil {
				if err := c.discover(discoveryCtx); err != nil {
					utilruntime.HandleErrorWithContext(discoveryCtx, err, "Rollout discovery failed; retrying on next tick")
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C():
				}
			}
		}()
	}
	c.GenericWatchingController.Run(ctx, workers)
}

func (c *rolloutSeedingSyncer) CooldownChecker() controllerutil.CooldownChecker { return nil }

func (c *rolloutSeedingSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	profile, err := versionpolicy.ProfileForChannel(key.YStreamChannel)
	if err != nil {
		return err
	}
	crud := c.fleetDBClient.ControlPlaneVersionRollouts()
	existing, err := crud.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		existing = nil
	} else if err != nil {
		return err
	}
	// Event payloads are only hints. Current cached references authorize creation
	// below the floor and determine when historical controller status is obsolete.
	clusters, clusterErr := c.clusterLister.List(ctx)
	spcs, spcErr := c.serviceProviderClusterLister.List(ctx)
	refs, referenceErr := collectRolloutReferences(clusters, spcs)
	if existing == nil && !versionpolicy.AtLeast(profile.ID, versionpolicy.MinimumBackendVersion) {
		if !refs.Has(profile) {
			return errors.Join(clusterErr, spcErr, referenceErr)
		}
	}
	desired, err := reconcileSeeding(profile, existing)
	if err != nil {
		return err
	}
	if desired != nil {
		_, err = crud.Create(ctx, desired, nil)
		if err != nil {
			return fmt.Errorf("create rollout %s: %w", key.YStreamChannel, err)
		}
	}
	if err := errors.Join(clusterErr, spcErr, referenceErr); err != nil {
		return err
	}
	return c.cleanupLegacyStatus(ctx, profile, clusters, spcs)
}

// reconcileSeeding receives a normalized profile from discovery or reference
// collection and returns a new rollout only when it does not already exist.
func reconcileSeeding(profile coreapi.VersionProfile, existing *fleetapi.ControlPlaneVersionRollout) (*fleetapi.ControlPlaneVersionRollout, error) {
	if existing != nil {
		return nil, nil
	}
	id, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID(profile.ChannelGroup + "-" + profile.ID)
	if err != nil {
		return nil, err
	}
	return &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: strings.ToLower(coreapi.ProviderNamespace)},
		Spec:           fleetapi.ControlPlaneVersionRolloutSpec{Version: profile},
	}, nil
}

// Cleanup runs in workers for cached legacy status on clusters referencing this
// channel. Missing rollout cache entries cause a retry before deleting status.
func (c *rolloutSeedingSyncer) cleanupLegacyStatus(ctx context.Context, profile coreapi.VersionProfile, clusters []*coreapi.Cluster, spcs []*coreapi.ServiceProviderCluster) error {
	var errs []error
	byCluster := map[string][]*coreapi.ServiceProviderCluster{}
	for _, spc := range spcs {
		parent := strings.ToLower(spc.ResourceID.Parent.String())
		byCluster[parent] = append(byCluster[parent], spc)
	}
	for _, cluster := range clusters {
		refs, err := collectRolloutReferences([]*coreapi.Cluster{cluster}, byCluster[strings.ToLower(cluster.ID.String())])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !refs.Has(profile) {
			continue
		}
		id := cluster.ID
		controllers, err := c.controllerLister.ListForCluster(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("list cached controller status for %s: %w", id, err))
			continue
		}
		legacyStatusExists := false
		for _, controller := range controllers {
			resourceID := controller.ResourceID
			if resourceID != nil && resourceID.Parent != nil && strings.EqualFold(resourceID.Parent.String(), id.String()) && strings.EqualFold(resourceID.Name, RolloutSeedingControllerName) {
				legacyStatusExists = true
				break
			}
		}
		if !legacyStatusExists {
			continue
		}
		ready := true
		for profile := range refs {
			_, err := c.rolloutLister.Get(ctx, profile.ChannelGroup+"-"+profile.ID)
			if err != nil {
				ready = false
				errs = append(errs, err)
			}
		}
		if ready {
			err := c.resourcesDBClient.HCPClusters(id.SubscriptionID, id.ResourceGroupName).Controllers(id.Name).Delete(ctx, RolloutSeedingControllerName)
			if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
				errs = append(errs, fmt.Errorf("delete obsolete rollout seeding controller status: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

func clusterYStreamChannel(cluster *coreapi.Cluster) (string, bool) {
	channel, err := versionpolicy.ChannelForProfile(cluster.CustomerProperties.Version)
	return channel, err == nil
}

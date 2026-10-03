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
	"reflect"
	"strings"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
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

type rolloutSeedKey struct {
	Version coreapi.VersionProfile
}

func (k rolloutSeedKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("version", k.Version.ID, "channelGroup", k.Version.ChannelGroup)
}

type rolloutSeedingSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient                fleetcosmosstorage.FleetDBClient
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	queue                        interface{ Enqueue(rolloutSeedKey) }
	discovery                    graphDataClient
}

// NewControlPlaneVersionRolloutSeedingController establishes version state for
// prospective offerings before their first cluster exists. It also restores state
// needed by existing clusters through upgrades, pin release and deletion, including
// referenced minor version and channel group pairs below the backend support floor.
//
// Discovery and cluster observations enqueue normalized version profiles.
// Reconciliation preserves selection and rollout progress while establishing the
// structured identity required by the fleet's selection and assignment controllers.
func NewControlPlaneVersionRolloutSeedingController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	informers coreinformers.BackendInformers,
	fleetInformers fleetinformers.FleetInformers,
) controllerregistry.Runnable {
	clusterInformer, clusterLister := informers.Clusters()
	spcInformer, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &rolloutSeedingSyncer{
		clusterLister: clusterLister, serviceProviderClusterLister: serviceProviderClusterLister,
		rolloutLister: rolloutLister, fleetDBClient: fleetDBClient, resourcesDBClient: resourcesDBClient,
		discovery: cincinnati.NewGraphDataClient(),
	}
	controller := newPeriodicRolloutController(RolloutSeedingControllerName, syncer.SyncOnce, syncer.discover, syncer.repairReferences)
	syncer.queue = controller
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(RolloutSeedingControllerName)...)
	for _, informer := range []cache.SharedIndexInformer{clusterInformer, spcInformer} {
		_, err := informer.AddEventHandlerWithOptions(syncer.eventHandler(), cache.HandlerOptions{Logger: &logger})
		if err != nil {
			panic(err)
		}
	}
	return controller
}

func (c *rolloutSeedingSyncer) SyncOnce(ctx context.Context, key rolloutSeedKey) error {
	channel, err := versionpolicy.ChannelForProfile(key.Version)
	if err != nil {
		return err
	}
	crud := c.fleetDBClient.ControlPlaneVersionRollouts()
	existing, err := crud.Get(ctx, channel)
	if cosmosstorageutils.IsNotFoundError(err) {
		existing = nil
	} else if err != nil {
		return err
	}
	// Migration keys carry no lasting permission to recreate retired documents.
	// Only missing below-floor rollouts require a fresh cached reference inventory.
	if existing == nil && !versionpolicy.AtLeast(key.Version.ID, versionpolicy.MinimumBackendVersion) {
		clusters, clusterErr := c.clusterLister.List(ctx)
		spcs, spcErr := c.serviceProviderClusterLister.List(ctx)
		refs, referenceErr := collectRolloutReferences(clusters, spcs)
		if !refs.Has(key.Version) {
			return errors.Join(clusterErr, spcErr, referenceErr)
		}
	}
	desired, err := reconcileSeeding(key.Version, existing)
	if err != nil || desired == nil {
		return err
	}
	if existing != nil {
		_, err = crud.Replace(ctx, desired, existing, nil)
	} else {
		_, err = crud.Create(ctx, desired, nil)
		if cosmosstorageutils.IsConflictError(err) {
			// Retry the live read, including any legacy document created concurrently.
			return fmt.Errorf("rollout %s created concurrently: %w", channel, err)
		}
	}
	return err
}

// reconcileSeeding is additive: selection, status and metadata survive backfill.
func reconcileSeeding(profile coreapi.VersionProfile, existing *fleetapi.ControlPlaneVersionRollout) (*fleetapi.ControlPlaneVersionRollout, error) {
	normalized, err := versionpolicy.NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	if normalized != profile {
		return nil, fmt.Errorf("seeding requires a canonical minor version and channel group: %v", profile)
	}
	if existing != nil {
		if existing.Spec.Version == (coreapi.VersionProfile{}) {
			desired := existing.DeepCopy()
			desired.Spec.Version = profile
			if err := fleetapihelpers.ValidateRolloutVersion(desired); err != nil {
				return nil, err
			}
			return desired, nil
		}
		if existing.Spec.Version != profile {
			return nil, fmt.Errorf("rollout version %v does not match queued profile %v", existing.Spec.Version, profile)
		}
		return nil, fleetapihelpers.ValidateRolloutVersion(existing)
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

// repairReferences also inventories every legacy rollout, including unused ones
// below the floor. Cleanup waits until a later cache observation proves repair.
func (c *rolloutSeedingSyncer) repairReferences(ctx context.Context) error {
	clusters, clusterErr := c.clusterLister.List(ctx)
	spcs, spcErr := c.serviceProviderClusterLister.List(ctx)
	refs, referenceErr := collectRolloutReferences(clusters, spcs)
	for profile := range refs {
		c.queue.Enqueue(rolloutSeedKey{Version: profile})
	}
	errs := []error{clusterErr, spcErr, referenceErr}
	rollouts, err := c.rolloutLister.List(ctx)
	errs = append(errs, err)
	for _, rollout := range rollouts {
		desired, err := fleetapihelpers.NormalizeRolloutVersion(rollout)
		errs = append(errs, err)
		if desired != nil {
			c.queue.Enqueue(rolloutSeedKey{Version: desired.Spec.Version})
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
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
		ready := true
		for profile := range refs {
			rollout, err := c.rolloutLister.Get(ctx, profile.ChannelGroup+"-"+profile.ID)
			if err == nil {
				err = fleetapihelpers.ValidateRolloutVersion(rollout)
			}
			if err != nil {
				ready = false
				errs = append(errs, err)
			}
		}
		if ready {
			id := cluster.ID
			err := c.resourcesDBClient.HCPClusters(id.SubscriptionID, id.ResourceGroupName).Controllers(id.Name).Delete(ctx, RolloutSeedingControllerName)
			if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
				errs = append(errs, fmt.Errorf("delete obsolete rollout seeding controller status: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

func (c *rolloutSeedingSyncer) eventHandler() cache.ResourceEventHandlerFuncs {
	enqueue := func(obj any) {
		ctx := utils.ContextWithControllerName(context.Background(), RolloutSeedingControllerName)
		ctx = utils.ContextWithLogger(ctx, utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(RolloutSeedingControllerName)...))
		if err := c.enqueueReferences(ctx, obj); err != nil {
			utilruntime.HandleErrorWithContext(ctx, err, "Cannot enqueue all rollout references; periodic repair will retry")
		}
	}
	return cache.ResourceEventHandlerFuncs{
		AddFunc: enqueue,
		UpdateFunc: func(oldObj, newObj any) {
			if !reflect.DeepEqual(seedingEventInputs(oldObj), seedingEventInputs(newObj)) {
				enqueue(newObj)
			}
		},
	}
}

// Event callbacks derive profile keys using targeted cache reads.
func (c *rolloutSeedingSyncer) enqueueReferences(ctx context.Context, obj any) error {
	var cluster *coreapi.Cluster
	var spc *coreapi.ServiceProviderCluster
	var err error
	switch obj := obj.(type) {
	case *coreapi.Cluster:
		cluster = obj
		if cluster.ID == nil {
			return fmt.Errorf("cluster without resource ID")
		}
		id := cluster.ID
		spc, err = c.serviceProviderClusterLister.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
		if cosmosstorageutils.IsNotFoundError(err) {
			err = nil
		}
	case *coreapi.ServiceProviderCluster:
		spc = obj
		if spc.ResourceID == nil || spc.ResourceID.Parent == nil {
			return fmt.Errorf("service provider cluster without parent")
		}
		id := spc.ResourceID.Parent
		cluster, err = c.clusterLister.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
	default:
		return nil
	}
	var clusters []*coreapi.Cluster
	var spcs []*coreapi.ServiceProviderCluster
	if cluster != nil {
		clusters = append(clusters, cluster)
	}
	if spc != nil {
		spcs = append(spcs, spc)
	}
	refs, referenceErr := collectRolloutReferences(clusters, spcs)
	for profile := range refs {
		c.queue.Enqueue(rolloutSeedKey{Version: profile})
	}
	return errors.Join(err, referenceErr)
}

type seedingEventInput struct {
	ID       string
	Version  coreapi.VersionProfile
	Versions []string
}

func seedingEventInputs(obj any) seedingEventInput {
	var id *azcorearm.ResourceID
	var input seedingEventInput
	switch obj := obj.(type) {
	case *coreapi.Cluster:
		id = obj.ID
		input.Version = obj.CustomerProperties.Version
		input.Versions = []string{versionString(obj.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion)}
		for _, active := range obj.Status.ActiveVersions {
			input.Versions = append(input.Versions, active.Version)
		}
	case *coreapi.ServiceProviderCluster:
		if obj.ResourceID != nil {
			id = obj.ResourceID.Parent
		}
		input.Versions = []string{versionString(obj.Spec.ControlPlaneVersion.DesiredVersion), versionString(obj.Spec.PinnedVersion.ExactVersion)}
		for _, active := range obj.Status.ControlPlaneVersion.ActiveVersions {
			input.Versions = append(input.Versions, versionString(active.Version))
		}
	}
	if id != nil {
		input.ID = strings.ToLower(id.String())
	}
	return input
}

func clusterYStreamChannel(cluster *coreapi.Cluster) (string, bool) {
	channel, err := versionpolicy.ChannelForProfile(cluster.CustomerProperties.Version)
	return channel, err == nil
}

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

package versionrollout

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// RolloutSeedingControllerName is the single source of the controller name.
const RolloutSeedingControllerName = "ControlPlaneVersionRolloutSeeding"

// rolloutSeedingSyncer is a per-cluster syncer that ensures a
// ControlPlaneVersionRollout document exists for every cluster's y-stream channel
// (design open question §8.4: who creates a ControlPlaneVersionRollout per active
// channel). It never mutates an existing rollout — the Best Version Selection,
// Status Collector, and Desired Version Assignment controllers own that — it only
// creates the empty rollout so those controllers have something to reconcile.
type rolloutSeedingSyncer struct {
	clusterLister corelisters.ClusterLister
	rolloutLister fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

var _ controllerutils.ClusterSyncer = (*rolloutSeedingSyncer)(nil)

// NewControlPlaneVersionRolloutSeedingController wires the seeding syncer into a
// cluster watching controller, so every cluster create/update (and the periodic
// resync) ensures its y-stream channel's rollout exists.
func NewControlPlaneVersionRolloutSeedingController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	informers coreinformers.BackendInformers,
	fleetInformers fleetinformers.FleetInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &rolloutSeedingSyncer{
		clusterLister: clusterLister,
		rolloutLister: rolloutLister,
		fleetDBClient: fleetDBClient,
	}
	// kubeApplierInformers is intentionally omitted: seeding depends only on the
	// cluster's channel group and version, not on any management-cluster state.
	return controllerutils.NewClusterWatchingController(
		RolloutSeedingControllerName,
		resourcesDBClient,
		informers,
		nil,
		5*time.Minute,
		syncer,
	)
}

// SyncOnce ensures the rollout for the triggering cluster's y-stream channel exists.
func (c *rolloutSeedingSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	// A cluster on its way out does not need its channel seeded; any other
	// (non-deleting) cluster in the same channel will seed it on its own sync.
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	channel, ok := clusterYStreamChannel(cluster)
	if !ok {
		// No channel group or unparseable version — nothing to seed. version.id
		// and version.channelGroup are required by static validation, so this only
		// happens for malformed documents.
		return nil
	}

	return c.ensureRollout(ctx, channel)
}

// clusterYStreamChannel returns the y-stream channel a cluster belongs to,
// derived from its channel group and the minor of its customer-requested version
// (e.g. channelGroup "stable" + version "4.21.5" -> "stable-4.21"). The boolean
// is false when the channel cannot be determined.
func clusterYStreamChannel(cluster *coreapi.HCPOpenShiftCluster) (string, bool) {
	channelGroup := cluster.CustomerProperties.Version.ChannelGroup
	if channelGroup == "" {
		return "", false
	}
	// ParseTolerant handles both "4.21" and full "4.21.5" version strings.
	parsed, err := semver.ParseTolerant(cluster.CustomerProperties.Version.ID)
	if err != nil {
		return "", false
	}
	return yStreamChannel(channelGroup, minorString(parsed)), true
}

// ensureRollout creates an empty ControlPlaneVersionRollout for the channel when
// one does not already exist. The lister is consulted first so steady-state runs
// avoid a Cosmos round-trip; a create that races another seeder (or a stale
// lister) surfaces as a 409 conflict, which is treated as success.
func (c *rolloutSeedingSyncer) ensureRollout(ctx context.Context, channel string) error {
	_, err := c.rolloutLister.Get(ctx, channel)
	if err == nil {
		return nil // already exists
	}
	if !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", channel, err))
	}

	rollout, err := newControlPlaneVersionRollout(channel)
	if err != nil {
		return utils.TrackError(err)
	}

	utils.LoggerFromContext(ctx).Info("creating ControlPlaneVersionRollout", "ystreamChannel", channel)
	if _, err := c.fleetDBClient.ControlPlaneVersionRollouts().Create(ctx, rollout, nil); cosmosstorageutils.IsConflictError(err) {
		return nil // another seeder won the race; the rollout now exists
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create ControlPlaneVersionRollout %q: %w", channel, err))
	}
	return nil
}

// newControlPlaneVersionRollout builds an empty rollout for the given y-stream
// channel: the channel is the top-level resource name, and every rollout shares
// the provider-namespace partition key (see ProviderNamespacePartitionKeyDeriver).
func newControlPlaneVersionRollout(channel string) (*fleetapi.ControlPlaneVersionRollout, error) {
	id, err := fleetapi.ToControlPlaneVersionRolloutResourceID(channel)
	if err != nil {
		return nil, fmt.Errorf("failed to build resource ID for ControlPlaneVersionRollout %q: %w", channel, err)
	}
	return &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   id,
			PartitionKey: strings.ToLower(coreapi.ProviderNamespace),
		},
	}, nil
}

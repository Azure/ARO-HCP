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
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

const RolloutRetirementControllerName = "ControlPlaneVersionRolloutRetirement"

type rolloutRetirementSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient                fleetcosmosstorage.FleetDBClient
}

// NewControlPlaneVersionRolloutRetirementController reclaims obsolete internal
// version state after a supported minor version has been retired and its clusters
// have drained. Supported channels remain available for future cluster creation,
// including those with zero membership.
//
// Retirement requires an error-free reference inventory confirming that a rollout
// is below the backend support floor and all cluster references have drained,
// including in-flight versions, pins and deleting clusters.
func NewControlPlaneVersionRolloutRetirementController(fleetDBClient fleetcosmosstorage.FleetDBClient, informers coreinformers.BackendInformers, fleetInformers fleetinformers.FleetInformers) controllerregistry.Runnable {
	clusterInformer, clusters := informers.Clusters()
	spcInformer, spcs := informers.ServiceProviderClusters()
	_, rollouts := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &rolloutRetirementSyncer{clusterLister: clusters, serviceProviderClusterLister: spcs, rolloutLister: rollouts, fleetDBClient: fleetDBClient}
	controller := controllerutils.NewControlPlaneVersionRolloutWatchingController(
		RolloutRetirementControllerName, fleetInformers, 5*time.Minute, syncer)
	if err := watchRolloutReferences(clusterInformer, spcInformer, clusters, spcs, controller, RolloutRetirementControllerName, 5*time.Minute); err != nil {
		panic(err) // coding error
	}
	return controller
}

func (c *rolloutRetirementSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

func (c *rolloutRetirementSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	rollout, err := c.rolloutLister.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get rollout %q for retirement: %w", key.YStreamChannel, err)
	}
	if rollout.Spec.Version == (coreapi.VersionProfile{}) {
		// Wait for a rollout notification with a complete identity.
		return nil
	}
	clusters, err := c.clusterLister.List(ctx)
	if err != nil {
		return fmt.Errorf("list clusters for retirement: %w", err)
	}
	spcs, err := c.serviceProviderClusterLister.List(ctx)
	if err != nil {
		return fmt.Errorf("list service provider clusters for retirement: %w", err)
	}
	retired, err := reconcileRolloutRetirement(clusters, spcs, rollout)
	if err != nil {
		return err
	}
	if retired {
		if err := c.fleetDBClient.ControlPlaneVersionRollouts().Delete(ctx, key.YStreamChannel); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return fmt.Errorf("retire rollout %q: %w", key.YStreamChannel, err)
		}
	}
	return nil
}

// reconcileRolloutRetirement retires only an unreferenced target below the backend
// support floor. Any uncertain reference inventory prevents retirement.
func reconcileRolloutRetirement(clusters []*coreapi.Cluster, spcs []*coreapi.ServiceProviderCluster, rollout *fleetapi.ControlPlaneVersionRollout) (bool, error) {
	profile := rollout.Spec.Version
	if profile == (coreapi.VersionProfile{}) {
		return false, nil
	}
	refs, err := collectRolloutReferences(clusters, spcs)
	if err != nil {
		return false, err
	}
	return !versionpolicy.AtLeast(profile.ID, versionpolicy.MinimumBackendVersion) && !refs.Has(profile), nil
}

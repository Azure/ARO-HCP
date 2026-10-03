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
	"slices"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

const RolloutRetirementControllerName = "ControlPlaneVersionRolloutRetirement"

type rolloutRetirementKey struct{}

func (rolloutRetirementKey) AddLoggerValues(logger logr.Logger) logr.Logger { return logger }

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
	_, clusters := informers.Clusters()
	_, spcs := informers.ServiceProviderClusters()
	_, rollouts := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &rolloutRetirementSyncer{clusterLister: clusters, serviceProviderClusterLister: spcs, rolloutLister: rollouts, fleetDBClient: fleetDBClient}
	controller := newPeriodicRolloutController(RolloutRetirementControllerName, syncer.SyncOnce)
	controller.producers = []func(context.Context) error{func(context.Context) error {
		controller.Enqueue(rolloutRetirementKey{})
		return nil
	}}
	return controller
}

func (c *rolloutRetirementSyncer) SyncOnce(ctx context.Context, _ rolloutRetirementKey) error {
	clusters, err := c.clusterLister.List(ctx)
	if err != nil {
		return fmt.Errorf("list clusters for retirement: %w", err)
	}
	spcs, err := c.serviceProviderClusterLister.List(ctx)
	if err != nil {
		return fmt.Errorf("list service provider clusters for retirement: %w", err)
	}
	rollouts, err := c.rolloutLister.List(ctx)
	if err != nil {
		return fmt.Errorf("list rollouts for retirement: %w", err)
	}
	rollouts = slices.Clone(rollouts)
	for i, rollout := range rollouts {
		desired, err := fleetapihelpers.NormalizeRolloutVersion(rollout)
		if err != nil {
			return err
		}
		if desired != nil {
			rollouts[i] = desired
		}
	}
	retired, err := reconcileRolloutRetirement(clusters, spcs, rollouts)
	if err != nil {
		return err
	}
	for _, channel := range retired {
		if err := c.fleetDBClient.ControlPlaneVersionRollouts().Delete(ctx, channel); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return fmt.Errorf("retire rollout %q: %w", channel, err)
		}
	}
	return nil
}

// reconcileRolloutRetirement returns sorted, unreferenced channels below the
// backend support floor. Any uncertain input prevents all retirement.
func reconcileRolloutRetirement(clusters []*coreapi.Cluster, spcs []*coreapi.ServiceProviderCluster, rollouts []*fleetapi.ControlPlaneVersionRollout) ([]string, error) {
	refs, err := collectRolloutReferences(clusters, spcs)
	if err != nil {
		return nil, err
	}
	// Validate the complete inventory before authorizing any retirement.
	var retired []string
	for _, rollout := range rollouts {
		if err := fleetapihelpers.ValidateRolloutVersion(rollout); err != nil {
			return nil, err
		}
		channel := rollout.ResourceID.Name
		profile := rollout.Spec.Version
		if !versionpolicy.AtLeast(profile.ID, versionpolicy.MinimumBackendVersion) && !refs.Has(rollout.Spec.Version) {
			retired = append(retired, channel)
		}
	}
	slices.Sort(retired)
	return retired, nil
}

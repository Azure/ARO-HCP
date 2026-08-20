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

package controllerutils

import (
	"context"
	"time"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ControlPlaneVersionRolloutKey identifies a ControlPlaneVersionRollout by its
// y-stream channel (e.g. "stable-4.21"), which is the rollout's top-level
// resource name / partition key.
type ControlPlaneVersionRolloutKey struct {
	YStreamChannel string `json:"ystreamChannel"`
}

func (k ControlPlaneVersionRolloutKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToControlPlaneVersionRolloutResourceID(k.YStreamChannel))
}

func (k ControlPlaneVersionRolloutKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

// ControlPlaneVersionRolloutSyncer is the domain syncer driven by the
// ControlPlaneVersionRollout watching controller.
type ControlPlaneVersionRolloutSyncer interface {
	SyncOnce(ctx context.Context, key ControlPlaneVersionRolloutKey) error
	CooldownChecker() controllerutil.CooldownChecker
}

type controlPlaneVersionRolloutWatchingController struct {
	name   string
	syncer ControlPlaneVersionRolloutSyncer
}

// NewControlPlaneVersionRolloutWatchingController watches ControlPlaneVersionRollout
// documents (and re-enqueues them on the resync interval) and drives the given
// syncer per rollout channel.
//
// Unlike the cluster/management-cluster watching controllers, this one does not
// write a per-key Controller status document: ControlPlaneVersionRollout has no
// nested Controllers sub-resource, and rollout health is reported on the
// rollout's own Status.Conditions by the Normal assignment syncer.
func NewControlPlaneVersionRolloutWatchingController(
	name string,
	fleetInformers fleetinformers.FleetInformers,
	resyncDuration time.Duration,
	syncer ControlPlaneVersionRolloutSyncer,
) Controller {
	wrapper := &controlPlaneVersionRolloutWatchingController{
		name:   name,
		syncer: syncer,
	}
	controller := newGenericWatchingController(name, fleetapi.ControlPlaneVersionRolloutResourceType, wrapper)

	// Nil informers is the "no triggering" mode used by unit tests.
	if fleetInformers != nil {
		rolloutInformer, _ := fleetInformers.ControlPlaneVersionRollouts()
		if err := controller.QueueForInformers(resyncDuration, rolloutInformer); err != nil {
			panic(err) // coding error
		}
	}

	return controller
}

func (c *controlPlaneVersionRolloutWatchingController) SyncOnce(ctx context.Context, key ControlPlaneVersionRolloutKey) error {
	return c.syncer.SyncOnce(ctx, key)
}

func (c *controlPlaneVersionRolloutWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *controlPlaneVersionRolloutWatchingController) MakeKey(resourceID *azcorearm.ResourceID) ControlPlaneVersionRolloutKey {
	return ControlPlaneVersionRolloutKey{
		YStreamChannel: resourceID.Name,
	}
}

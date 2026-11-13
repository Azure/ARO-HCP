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
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NamespaceDeletionKey identifies one NamespaceDeletion by its parent stamp
// and its own name. Unlike the singleton documents under a management cluster,
// cleanups are a collection, so the name is part of the key.
type NamespaceDeletionKey struct {
	StampIdentifier string `json:"stampIdentifier"`
	Name            string `json:"name"`
}

func (k NamespaceDeletionKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapihelpers.ToNamespaceDeletionResourceID(k.StampIdentifier, k.Name))
}

func (k NamespaceDeletionKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

// NamespaceDeletionSyncer is the domain syncer driven by the
// NamespaceDeletion watching controller.
type NamespaceDeletionSyncer interface {
	SyncOnce(ctx context.Context, key NamespaceDeletionKey) error
}

type stuckClusterCleanupWatchingController struct {
	name   string
	syncer NamespaceDeletionSyncer
}

// NewNamespaceDeletionWatchingController watches NamespaceDeletion documents
// (and re-enqueues them on the resync interval) and drives the given syncer per
// cleanup request.
//
// Like the ControlPlaneVersionRollout controller and unlike the cluster and
// management-cluster watching controllers, this one writes no per-key Controller
// status document: NamespaceDeletion has no nested Controllers sub-resource,
// and per-request progress is reported on the cleanup's own Status.
func NewNamespaceDeletionWatchingController(
	name string,
	fleetInformers fleetinformers.FleetInformers,
	resyncDuration time.Duration,
	syncer NamespaceDeletionSyncer,
) *controllerutil.GenericWatchingController[NamespaceDeletionKey] {
	wrapper := &stuckClusterCleanupWatchingController{
		name:   name,
		syncer: syncer,
	}
	controller := newGenericWatchingController(name, fleetapi.ManagementClusterNamespaceDeletionResourceType, wrapper)

	// Nil informers is the "no triggering" mode used by unit tests.
	if fleetInformers != nil {
		cleanupInformer, _ := fleetInformers.NamespaceDeletions()
		if err := controller.QueueForInformers(resyncDuration, cleanupInformer); err != nil {
			panic(err) // coding error
		}
	}

	return controller
}

func (c *stuckClusterCleanupWatchingController) SyncOnce(ctx context.Context, key NamespaceDeletionKey) error {
	return c.syncer.SyncOnce(ctx, key)
}

func (c *stuckClusterCleanupWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	if syncer, ok := c.syncer.(interface {
		CooldownChecker() controllerutil.CooldownChecker
	}); ok {
		return syncer.CooldownChecker()
	}
	return nil
}

func (c *stuckClusterCleanupWatchingController) MakeKey(resourceID *azcorearm.ResourceID) NamespaceDeletionKey {
	return NamespaceDeletionKey{
		StampIdentifier: stampIdentifierFromResourceID(resourceID),
		Name:            resourceID.Name,
	}
}

// stampIdentifierFromResourceID walks up from a stamp-rooted resource ID to the
// stamp name. For a cleanup the ID is
// /providers/.../stamps/{stamp}/managementClusters/default/namespaceDeletions/{name},
// so the stamp is two parents up.
func stampIdentifierFromResourceID(resourceID *azcorearm.ResourceID) string {
	for current := resourceID; current != nil; current = current.Parent {
		if metadataapi.ResourceTypeEqual(current.ResourceType, fleetapi.StampResourceType) {
			return current.Name
		}
	}
	return ""
}

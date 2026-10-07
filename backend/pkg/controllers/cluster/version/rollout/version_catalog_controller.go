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
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

const VersionCatalogControllerName = "OpenShiftVersionCatalog"

// Every rollout change projects into the same regional Resources document.
type versionCatalogKey struct{}

func (versionCatalogKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(
		metadataapi.Must(coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)))...)
}

type versionCatalogSyncer struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	rolloutLister     fleetlisters.ControlPlaneVersionRolloutLister
}

var _ controllerutil.GenericSyncer[versionCatalogKey] = (*versionCatalogSyncer)(nil)

func (c *versionCatalogSyncer) MakeKey(_ *azcorearm.ResourceID) versionCatalogKey {
	// All rollout resource IDs contribute to the same regional catalog.
	return versionCatalogKey{}
}

func (c *versionCatalogSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

// NewOpenShiftVersionCatalogController publishes the customer-facing view of
// internal version rollouts in Resources so the frontend can answer discovery with
// a local snapshot read. This projection keeps fleet rollout details backend-owned
// and decouples public request latency from upstream discovery and selection.
// It advertises minor version and channel group pairs; a pair becomes available
// as soon as its rollout has a selected exact version.
//
// The Resources snapshot is an exact projection subject to the public support
// floor. Withdrawal follows source state or policy, and writes occur only when
// public content changes. The frontend applies subscription visibility to this
// shared regional snapshot.
func NewOpenShiftVersionCatalogController(resourcesDBClient corecosmosstorage.ResourcesDBClient, fleetInformers fleetinformers.FleetInformers) controllerregistry.Runnable {
	informer, lister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &versionCatalogSyncer{resourcesDBClient: resourcesDBClient, rolloutLister: lister}
	controller := controllerutil.NewGenericWatchingController(
		VersionCatalogControllerName, fleetapi.ControlPlaneVersionRolloutResourceType, syncer, controllerutils.ReconcileTotal)
	controller.AddCacheSyncs(informer.HasSynced)
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(VersionCatalogControllerName)...)
	enqueueRollout := func(obj any) {
		controller.EnqueueResourceIDAdd(obj.(*fleetapi.ControlPlaneVersionRollout).ResourceID, true)
	}
	_, err := informer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc:    enqueueRollout,
		UpdateFunc: func(_, obj any) { enqueueRollout(obj) },
		// Every deletion, including a tombstone, triggers a full catalog projection.
		DeleteFunc: func(any) { controller.Enqueue(versionCatalogKey{}) },
	}, cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(5 * time.Minute)})
	if err != nil {
		panic(err)
	}
	// Seed even an empty informer; Run waits for its initial list before publishing.
	controller.Enqueue(versionCatalogKey{})
	return controller
}

func (c *versionCatalogSyncer) SyncOnce(ctx context.Context, _ versionCatalogKey) error {
	rollouts, err := c.rolloutLister.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list version rollouts: %w", err)
	}
	crud := c.resourcesDBClient.OpenShiftVersionCatalogs()
	existing, err := crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	if cosmosstorageutils.IsNotFoundError(err) {
		existing = nil
	} else if err != nil {
		return fmt.Errorf("failed to get version catalog: %w", err)
	}
	desired := reconcileVersionCatalog(rollouts, existing)
	if desired == nil {
		return nil
	}
	if existing == nil {
		_, err = crud.Create(ctx, desired, nil)
	} else {
		_, err = crud.Replace(ctx, desired, nil)
	}
	if err != nil {
		return fmt.Errorf("failed to publish version catalog: %w", err)
	}
	return nil
}

// reconcileVersionCatalog returns the exact public projection, or nil when the
// existing catalog is unchanged or a profile is absent. It preserves inputs,
// including existing metadata.
func reconcileVersionCatalog(rollouts []*fleetapi.ControlPlaneVersionRollout, existing *coreapi.OpenShiftVersionCatalog) *coreapi.OpenShiftVersionCatalog {
	entries := make([]coreapi.OpenShiftVersionCatalogEntry, 0, len(rollouts))
	for _, rollout := range rollouts {
		profile := rollout.Spec.Version
		if profile == (coreapi.VersionProfile{}) {
			// Do not publish a partial snapshot; rollout updates and periodic sync retry.
			return nil
		}
		if !versionpolicy.AtLeast(profile.ID, versionpolicy.MinimumPublicVersion) {
			continue
		}
		entries = append(entries, coreapi.OpenShiftVersionCatalogEntry{
			Version: profile, Available: rollout.Spec.BestExactVersion != nil,
		})
	}
	slices.SortFunc(entries, func(a, b coreapi.OpenShiftVersionCatalogEntry) int {
		if n := strings.Compare(a.Version.ID, b.Version.ID); n != 0 {
			return n
		}
		return strings.Compare(a.Version.ChannelGroup, b.Version.ChannelGroup)
	})

	if existing == nil {
		return &coreapi.OpenShiftVersionCatalog{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)),
				PartitionKey: strings.ToLower(coreapi.ProviderNamespace),
			},
			Entries: entries,
		}
	}
	if slices.Equal(existing.Entries, entries) {
		return nil
	}
	desired := existing.DeepCopy()
	desired.Entries = entries
	return desired
}
